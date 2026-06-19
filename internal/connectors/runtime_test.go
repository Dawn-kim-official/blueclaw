package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/agentruntime"
	"blueclaw/internal/agenttest"
	"blueclaw/internal/capability"
	"blueclaw/internal/config"
	"blueclaw/internal/identity"
	"blueclaw/internal/llm"
	"blueclaw/internal/mcp"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
	"blueclaw/internal/task"
)

func TestConnectorRuntimeProcessesInvitedMessageAndDeduplicates(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "안녕하세요"})
	event := testInboundEvent("message-1")

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected first event to process: %v", errorValue)
	}
	duplicateResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected duplicate event to process: %v", errorValue)
	}

	if result.TaskRunID == "" {
		t.Fatal("expected task run id")
	}
	if result.ReplyDispatchID != "dispatch-1" {
		t.Fatalf("expected first dispatch id, got %q", result.ReplyDispatchID)
	}
	if !duplicateResult.Duplicate {
		t.Fatal("expected duplicate result")
	}
	if len(adapter.sentReplies) != 1 {
		t.Fatalf("expected one reply, got %d", len(adapter.sentReplies))
	}
	if len(adapter.progressStarts) != 1 {
		t.Fatalf("expected one progress start, got %d", len(adapter.progressStarts))
	}
	if len(adapter.progressStops) != 1 {
		t.Fatalf("expected one progress stop, got %d", len(adapter.progressStops))
	}
	if !connectorTaskEventsContain(connectorRuntime, result.TaskRunID, "blueclaw.task.execution_duration", "durationMs") {
		t.Fatal("expected task execution duration event")
	}
}

func TestConnectorRuntimeSuppressesStaleRetryWhileOriginalTaskIsRunning(t *testing.T) {
	languageModel := &blockingTestLanguageModel{
		reply:   "done",
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	event := testInboundEvent("message-stale-retry")
	firstResultChannel := make(chan ConnectorRuntimeResult, 1)
	firstErrorChannel := make(chan error, 1)

	go func() {
		result, errorValue := connectorRuntime.processInboundEventWithReplySender(context.Background(), adapter, event, adapter.SendReply)
		firstResultChannel <- result
		firstErrorChannel <- errorValue
	}()

	select {
	case <-languageModel.started:
	case <-time.After(time.Second):
		t.Fatal("expected original task to start")
	}

	retryContext, cancelRetry := context.WithTimeout(context.Background(), time.Second)
	defer cancelRetry()
	duplicateResult, errorValue := connectorRuntime.processInboundEventWithReplySender(retryContext, adapter, event, adapter.SendReply)
	if errorValue != nil {
		t.Fatalf("expected stale retry to suppress cleanly: %v", errorValue)
	}
	if !duplicateResult.Duplicate || duplicateResult.Reason != "duplicate_source_reference" {
		t.Fatalf("expected duplicate source suppression, got %+v", duplicateResult)
	}
	if duplicateResult.TaskRunID == "" {
		t.Fatal("expected duplicate result to point at original task")
	}
	if len(connectorRuntime.agentKernel.ListTaskRunByPersonID("person-1")) != 1 {
		t.Fatalf("expected one task run, got %+v", connectorRuntime.agentKernel.ListTaskRunByPersonID("person-1"))
	}
	if !connectorTaskEventsContain(connectorRuntime, duplicateResult.TaskRunID, "connector.duplicate_source_suppressed", event.MessageID) {
		t.Fatal("expected duplicate suppression event")
	}

	close(languageModel.release)
	select {
	case errorValue := <-firstErrorChannel:
		if errorValue != nil {
			t.Fatalf("expected original task to finish: %v", errorValue)
		}
	case <-time.After(time.Second):
		t.Fatal("expected original task to finish")
	}
	firstResult := <-firstResultChannel
	if firstResult.TaskRunID != duplicateResult.TaskRunID {
		t.Fatalf("expected duplicate to reuse %s, got %+v", firstResult.TaskRunID, duplicateResult)
	}
	if len(adapter.sentReplies) != 1 {
		t.Fatalf("expected only original reply, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeDefersNewTaskLaunchWhenQuiesced(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "should not run"})
	repository := &testConnectorQueueRepository{}
	connectorRuntime.UseEventRepository(repository)
	connectorRuntime.UseTaskIntakeGate(testTaskIntakeGate{isQuiesced: true})
	event := testInboundEvent("message-quiesced")
	adapter.httpParseResult = HTTPParseResult{HasEvent: true, Event: event}
	request, errorValue := http.NewRequest(http.MethodPost, "/connectors/test/events", strings.NewReader(`{}`))
	if errorValue != nil {
		t.Fatalf("expected request: %v", errorValue)
	}

	result, _, errorValue := connectorRuntime.HandleHTTPEvent(context.Background(), adapter.Name(), request)
	if errorValue != nil {
		t.Fatalf("expected http event to queue: %v", errorValue)
	}
	if result.Reason != "queued" {
		t.Fatalf("expected queued result, got %+v", result)
	}
	if !connectorRuntime.processNextQueuedConnectorEvent(context.Background()) {
		t.Fatal("expected queued connector event to be claimed")
	}
	if len(repository.succeededEvents) != 0 {
		t.Fatalf("quiesced new task must not be marked succeeded, got %+v", repository.succeededEvents)
	}
	if len(repository.pendingReplies) != 0 || len(adapter.sentReplies) != 0 {
		t.Fatalf("quiesced new task must not reply, pending=%+v sent=%+v", repository.pendingReplies, adapter.sentReplies)
	}
}

func TestConnectorRuntimeAllowsWaitingTaskContinuationWhenQuiesced(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"blueclaw_turn_router": {
				`{"route":"continue_task","classification":"bounded_task","taskShape":"maintenance_task","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"input reply","userFacingReply":""}`,
			},
		},
		ActionResponses: []string{connectorFinishMessage("continued while quiesced")},
	})
	connectorRuntime, adapter, taskRunService, taskWaitRepository := newWaitRoutingTestConnectorRuntime(t, languageModel)
	connectorRuntime.UseTaskIntakeGate(testTaskIntakeGate{isQuiesced: true})
	waitingTaskRun := createWaitingInputTaskRun(t, taskRunService, "single prompt", "single-interaction")
	if errorValue := taskWaitRepository.InsertTaskWaitToken(waitRoutingTaskWaitToken(waitingTaskRun, "single-dispatch", "single-interaction")); errorValue != nil {
		t.Fatal(errorValue)
	}
	event := testInboundEvent("message-continuation")
	event.ReplyTargetID = "single-dispatch"
	event.Prompt = "answer"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected continuation to process: %v", errorValue)
	}

	if result.TaskRunID != waitingTaskRun.TaskRunID {
		t.Fatalf("expected waiting task %s, got %+v", waitingTaskRun.TaskRunID, result)
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "continued while quiesced" {
		t.Fatalf("expected continuation reply, got %+v", adapter.sentReplies)
	}
}

func TestOnlyExactStopCommandsBypassConversationLock(t *testing.T) {
	stopEvent := testInboundEvent("message-stop")
	stopEvent.Prompt = "/stop"
	koreanStopEvent := testInboundEvent("message-stop-ko")
	koreanStopEvent.Prompt = "/중단"
	stopUnderscoreEvent := testInboundEvent("message-stop-underscore")
	stopUnderscoreEvent.Prompt = "/stop_all"
	askEvent := testInboundEvent("message-ask")
	askEvent.LegacyFields = map[string]interface{}{"askAction": "confirm"}
	askEvent.Prompt = "approved"

	if !shouldProcessBeforeConversationLock(stopEvent) {
		t.Fatal("expected exact stop command to bypass conversation lock")
	}
	if shouldProcessBeforeConversationLock(koreanStopEvent) {
		t.Fatal("korean stop alias should not bypass conversation lock")
	}
	if shouldProcessBeforeConversationLock(stopUnderscoreEvent) {
		t.Fatal("underscore stop alias should not bypass conversation lock")
	}
	if shouldProcessBeforeConversationLock(askEvent) {
		t.Fatal("ask interaction should keep conversation lock ordering")
	}
}

func TestConnectorRuntimeReplyTargetWaitResolvesOlderWaitingTask(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"blueclaw_turn_router": {
				`{"route":"continue_task","classification":"bounded_task","taskShape":"maintenance_task","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"input reply","userFacingReply":""}`,
			},
		},
		ActionResponses: []string{connectorFinishMessage("older continued")},
	})
	connectorRuntime, adapter, taskRunService, taskWaitRepository := newWaitRoutingTestConnectorRuntime(t, languageModel)
	olderTaskRun := createWaitingInputTaskRun(t, taskRunService, "older prompt", "old-interaction")
	if errorValue := taskWaitRepository.InsertTaskWaitToken(waitRoutingTaskWaitToken(olderTaskRun, "old-dispatch", "old-interaction")); errorValue != nil {
		t.Fatal(errorValue)
	}
	time.Sleep(time.Millisecond)
	newerTaskRun := createWaitingInputTaskRun(t, taskRunService, "newer prompt", "new-interaction")
	if errorValue := taskWaitRepository.InsertTaskWaitToken(waitRoutingTaskWaitToken(newerTaskRun, "new-dispatch", "new-interaction")); errorValue != nil {
		t.Fatal(errorValue)
	}
	event := testInboundEvent("message-old-reply")
	event.ReplyTargetID = "old-dispatch"
	event.Prompt = "older answer"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected old wait reply to process: %v", errorValue)
	}

	if result.TaskRunID != olderTaskRun.TaskRunID {
		t.Fatalf("expected older task run %s, got %+v", olderTaskRun.TaskRunID, result)
	}
	olderTaskRun, _ = connectorRuntime.agentKernel.FindTaskRun(olderTaskRun.TaskRunID)
	newerTaskRun, _ = connectorRuntime.agentKernel.FindTaskRun(newerTaskRun.TaskRunID)
	if olderTaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected older task completed, got %+v", olderTaskRun)
	}
	if newerTaskRun.Status != task.TaskStatusWaitingUserInput {
		t.Fatalf("expected newer task to remain waiting, got %+v", newerTaskRun)
	}
	openWaits, errorValue := taskWaitRepository.FindOpenByPersonAndConversation("person-1", "test", "direct-1")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(openWaits) != 1 || openWaits[0].TaskRunID != newerTaskRun.TaskRunID {
		t.Fatalf("expected only newer wait open, got %+v", openWaits)
	}
}

func TestConnectorRuntimeAmbiguousWaitDoesNotSelectNewest(t *testing.T) {
	connectorRuntime, adapter, taskRunService, taskWaitRepository := newWaitRoutingTestConnectorRuntime(t, testLanguageModel{reply: "어느 작업에 답하셨나요?"})
	olderTaskRun := createWaitingInputTaskRun(t, taskRunService, "older prompt", "old-interaction")
	newerTaskRun := createWaitingInputTaskRun(t, taskRunService, "newer prompt", "new-interaction")
	for _, taskWaitToken := range []task.TaskWaitToken{
		waitRoutingTaskWaitToken(olderTaskRun, "old-dispatch", "old-interaction"),
		waitRoutingTaskWaitToken(newerTaskRun, "new-dispatch", "new-interaction"),
	} {
		if errorValue := taskWaitRepository.InsertTaskWaitToken(taskWaitToken); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	event := testInboundEvent("message-ambiguous")
	event.ReplyTargetID = "unmatched-reply-target"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected ambiguous wait to process: %v", errorValue)
	}

	if result.TaskRunID == olderTaskRun.TaskRunID || result.TaskRunID == newerTaskRun.TaskRunID {
		t.Fatalf("expected disambiguation task, got %+v", result)
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].taskRunID != result.TaskRunID {
		t.Fatalf("expected disambiguation reply, got %+v result=%+v", adapter.sentReplies, result)
	}
	olderTaskRun, _ = connectorRuntime.agentKernel.FindTaskRun(olderTaskRun.TaskRunID)
	newerTaskRun, _ = connectorRuntime.agentKernel.FindTaskRun(newerTaskRun.TaskRunID)
	if olderTaskRun.Status != task.TaskStatusWaitingUserInput || newerTaskRun.Status != task.TaskStatusWaitingUserInput {
		t.Fatalf("ambiguous reply must not continue waits, older=%s newer=%s", olderTaskRun.Status, newerTaskRun.Status)
	}
	if !connectorTaskEventsContain(connectorRuntime, result.TaskRunID, "ask.requested", `"choice_single"`) {
		t.Fatalf("expected disambiguation ask.choice, taskRunID=%s", result.TaskRunID)
	}
}

func TestConnectorRuntimeSingleOpenWaitFallbackContinuesTask(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"blueclaw_turn_router": {
				`{"route":"continue_task","classification":"bounded_task","taskShape":"maintenance_task","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"input reply","userFacingReply":""}`,
			},
		},
		ActionResponses: []string{connectorFinishMessage("single continued")},
	})
	connectorRuntime, adapter, taskRunService, taskWaitRepository := newWaitRoutingTestConnectorRuntime(t, languageModel)
	waitingTaskRun := createWaitingInputTaskRun(t, taskRunService, "single prompt", "single-interaction")
	if errorValue := taskWaitRepository.InsertTaskWaitToken(waitRoutingTaskWaitToken(waitingTaskRun, "single-dispatch", "single-interaction")); errorValue != nil {
		t.Fatal(errorValue)
	}
	event := testInboundEvent("message-single")
	event.ReplyTargetID = "unmatched-reply-target"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected single wait fallback to process: %v", errorValue)
	}

	if result.TaskRunID != waitingTaskRun.TaskRunID {
		t.Fatalf("expected waiting task %s, got %+v", waitingTaskRun.TaskRunID, result)
	}
	waitingTaskRun, _ = connectorRuntime.agentKernel.FindTaskRun(waitingTaskRun.TaskRunID)
	if waitingTaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected waiting task completed, got %+v", waitingTaskRun)
	}
}

func TestConnectorRuntimeWritesResolvesAndExpiresTaskWaitRecord(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		ActionResponses: []string{
			`{"action":"continue","toolName":"ask.input","toolInput":{"question":"추가 정보가 필요합니다."},"nextStepPlan":{"objective":"wait","expectedTools":[],"expectedNextResults":["user replies"],"doneCriteria":["reply received"],"risk":"none","workingSetReason":"ask.input waits for the user"}}`,
		},
	})
	connectorRuntime, adapter, taskRunService, taskWaitRepository := newWaitRoutingTestConnectorRuntime(t, languageModel)
	event := testInboundEvent("message-send-wait")

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected ask input send to process: %v", errorValue)
	}
	openWaits, errorValue := taskWaitRepository.FindOpenByPersonAndConversation("person-1", "test", "direct-1")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(openWaits) != 1 {
		t.Fatalf("expected one open wait, got %+v", openWaits)
	}
	if openWaits[0].TaskRunID != result.TaskRunID || openWaits[0].ReplyTargetID != "dispatch-1" || openWaits[0].DispatchID != "dispatch-1" || openWaits[0].Kind != "input" {
		t.Fatalf("unexpected persisted wait: %+v result=%+v", openWaits[0], result)
	}
	if errorValue := taskWaitRepository.ResolveTaskWait(openWaits[0].WaitID, time.Now().UTC()); errorValue != nil {
		t.Fatal(errorValue)
	}
	openWaits, errorValue = taskWaitRepository.FindOpenByPersonAndConversation("person-1", "test", "direct-1")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(openWaits) != 0 {
		t.Fatalf("expected resolved wait closed, got %+v", openWaits)
	}
	expiringTaskRun := createWaitingInputTaskRun(t, taskRunService, "expire prompt", "expire-interaction")
	expiringWait := waitRoutingTaskWaitToken(expiringTaskRun, "expire-dispatch", "expire-interaction")
	expiringWait.ExpiresAt = time.Now().Add(-time.Minute)
	if errorValue := taskWaitRepository.InsertTaskWaitToken(expiringWait); errorValue != nil {
		t.Fatal(errorValue)
	}
	expiredTaskRunIDs, errorValue := taskWaitRepository.ExpireOldTaskWaits(time.Now().UTC())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(expiredTaskRunIDs) != 1 || expiredTaskRunIDs[0] != expiringTaskRun.TaskRunID {
		t.Fatalf("expected expired task run id, got %+v", expiredTaskRunIDs)
	}
	openWaits, errorValue = taskWaitRepository.FindOpenByPersonAndConversation("person-1", "test", "direct-1")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(openWaits) != 0 {
		t.Fatalf("expected expired wait closed, got %+v", openWaits)
	}
}

func TestConnectorRuntimeStopCommandCancelsCurrentConversationTask(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "ignored"})
	taskRun, errorValue := connectorRuntime.agentKernel.RunTask("person-1", "direct-1", "long task")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	event := testInboundEvent("message-stop")
	event.Prompt = "/stop"
	event.ReplyTargetID = event.MessageID

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil {
		t.Fatalf("expected stop event to process: %v", errorValue)
	}
	if result.Reason != "task_control" {
		t.Fatalf("reason = %q, want task_control", result.Reason)
	}
	cancelledTaskRun, isFound := connectorRuntime.agentKernel.FindTaskRun(taskRun.TaskRunID)
	if !isFound || cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected cancelled task run, got found=%v run=%+v", isFound, cancelledTaskRun)
	}
	if len(adapter.sentReplies) != 1 || !strings.Contains(adapter.sentReplies[0].message, "1") {
		t.Fatalf("expected stop reply, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeStopCommandCancelsLatestThreadScopedTask(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "ignored"})
	topLevelTaskRun, errorValue := connectorRuntime.agentKernel.RunTaskWithOrigin("person-1", task.TaskRunOrigin{
		ConversationID: "channel-1",
		ReplyTargetID:  "root-1",
		IsThread:       false,
	}, "top level task")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	time.Sleep(time.Millisecond)
	threadTaskRun, errorValue := connectorRuntime.agentKernel.RunTaskWithOrigin("person-1", task.TaskRunOrigin{
		ConversationID: "channel-1",
		ReplyTargetID:  "root-1",
		IsThread:       true,
	}, "thread task")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	time.Sleep(time.Millisecond)
	otherThreadTaskRun, errorValue := connectorRuntime.agentKernel.RunTaskWithOrigin("person-1", task.TaskRunOrigin{
		ConversationID: "channel-1",
		ReplyTargetID:  "root-2",
		IsThread:       true,
	}, "other thread task")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	event := testChannelInboundEvent("message-stop")
	event.Prompt = "/stop"
	event.ReplyTargetID = "root-1"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil {
		t.Fatalf("expected stop event to process: %v", errorValue)
	}
	if result.Reason != "task_control" {
		t.Fatalf("reason = %q, want task_control", result.Reason)
	}
	cancelledTaskRun, _ := connectorRuntime.agentKernel.FindTaskRun(threadTaskRun.TaskRunID)
	topLevelTaskRun, _ = connectorRuntime.agentKernel.FindTaskRun(topLevelTaskRun.TaskRunID)
	otherThreadTaskRun, _ = connectorRuntime.agentKernel.FindTaskRun(otherThreadTaskRun.TaskRunID)
	if cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected thread task cancelled, got %+v", cancelledTaskRun)
	}
	if topLevelTaskRun.Status == task.TaskStatusCancelled || otherThreadTaskRun.Status == task.TaskStatusCancelled {
		t.Fatalf("expected only matching thread task cancelled, top=%s other=%s", topLevelTaskRun.Status, otherThreadTaskRun.Status)
	}
}

func TestConnectorRuntimeStopCommandAtChannelRootCancelsLatestRootScopedTask(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "ignored"})
	oldRootTaskRun, errorValue := connectorRuntime.agentKernel.RunTaskWithOrigin("person-1", task.TaskRunOrigin{
		ConversationID: "channel-1",
		ReplyTargetID:  "root-old",
		IsThread:       false,
	}, "old root task")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	time.Sleep(time.Millisecond)
	latestRootTaskRun, errorValue := connectorRuntime.agentKernel.RunTaskWithOrigin("person-1", task.TaskRunOrigin{
		ConversationID: "channel-1",
		ReplyTargetID:  "root-latest",
		IsThread:       false,
	}, "latest root task")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	time.Sleep(time.Millisecond)
	threadTaskRun, errorValue := connectorRuntime.agentKernel.RunTaskWithOrigin("person-1", task.TaskRunOrigin{
		ConversationID: "channel-1",
		ReplyTargetID:  "thread-root",
		IsThread:       true,
	}, "thread task")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	event := testChannelInboundEvent("message-stop")
	event.Prompt = "/stop"
	event.ReplyTargetID = event.MessageID

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil {
		t.Fatalf("expected stop event to process: %v", errorValue)
	}
	if result.Reason != "task_control" {
		t.Fatalf("reason = %q, want task_control", result.Reason)
	}
	oldRootTaskRun, _ = connectorRuntime.agentKernel.FindTaskRun(oldRootTaskRun.TaskRunID)
	latestRootTaskRun, _ = connectorRuntime.agentKernel.FindTaskRun(latestRootTaskRun.TaskRunID)
	threadTaskRun, _ = connectorRuntime.agentKernel.FindTaskRun(threadTaskRun.TaskRunID)
	if latestRootTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected latest root task cancelled, got %+v", latestRootTaskRun)
	}
	if oldRootTaskRun.Status == task.TaskStatusCancelled || threadTaskRun.Status == task.TaskStatusCancelled {
		t.Fatalf("expected only latest root task cancelled, old=%s thread=%s", oldRootTaskRun.Status, threadTaskRun.Status)
	}
}

func TestConnectorRuntimeBusyStatusDoesNotCreateNewTask(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"blueclaw_turn_router": {
				`{"route":"answer_question","classification":"quick_reply","taskShape":"immediate_reply","effortLevel":"quick","requestedOutputFormats":null,"responseLanguage":"ko","reason":"user asked for progress","userFacingReply":"","busyRoute":"status","busyInstruction":""}`,
			},
			"blueclaw_reply": {
				`{"reply":"지금 처리 중입니다."}`,
			},
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntime.agentKernel.UseIntakeLanguageModelProvider(languageModel)
	connectorRuntime.agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true})
	activeTaskRun, errorValue := connectorRuntime.agentKernel.RunTask("person-1", "direct-1", "보고서 작성")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, isFound := connectorRuntime.latestCurrentConversationActiveTask("person-1", "direct-1"); !isFound {
		t.Fatal("expected active task before busy status event")
	}
	event := testInboundEvent("message-busy-status")
	event.Prompt = "하고 있어?"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil {
		t.Fatalf("expected busy status event to process: %v", errorValue)
	}
	if result.Reason != "busy_status" || result.TaskRunID != activeTaskRun.TaskRunID {
		t.Fatalf("expected busy status for active task, got %+v", result)
	}
	if len(connectorRuntime.agentKernel.ListTaskRunByPersonID("person-1")) != 1 {
		t.Fatalf("expected no new task run, got %+v", connectorRuntime.agentKernel.ListTaskRunByPersonID("person-1"))
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "지금 처리 중입니다." {
		t.Fatalf("expected status reply, got %+v", adapter.sentReplies)
	}
	if !connectorTaskEventsContain(connectorRuntime, activeTaskRun.TaskRunID, "task.status.requested", "message-busy-status") {
		t.Fatal("expected status request event")
	}
}

func TestConnectorRuntimeInterruptsInactiveRunningTaskAndStartsNewTask(t *testing.T) {
	now := time.Now()
	taskRunRepository := newTestTaskRunRepository()
	orphanedTaskRun := task.TaskRun{
		TaskRunID:            "task-orphaned",
		RequesterPersonID:    "person-1",
		OriginConversationID: "direct-1",
		CurrentAttemptID:     "attempt-orphaned",
		Status:               task.TaskStatusRunning,
		Prompt:               "멈춘 작업",
		CreatedAt:            now.Add(-time.Minute),
		UpdatedAt:            now.Add(-time.Minute),
	}
	taskRunRepository.taskRuns[orphanedTaskRun.TaskRunID] = orphanedTaskRun
	taskRunRepository.taskAttempts[orphanedTaskRun.CurrentAttemptID] = task.TaskAttempt{
		TaskAttemptID: orphanedTaskRun.CurrentAttemptID,
		TaskRunID:     orphanedTaskRun.TaskRunID,
		RunnerID:      "previous-runner",
		Status:        task.TaskAttemptStatusRunning,
		StartedAt:     now.Add(-time.Minute),
	}
	connectorRuntime, adapter, taskEventService := newRepositoryBackedTestConnectorRuntime(t, testLanguageModel{reply: "새 작업으로 처리했습니다."}, taskRunRepository)
	taskEventService.AppendTaskEvent(orphanedTaskRun.TaskRunID, "tool.site.app.build.requested", `{"observationID":"observation-1","toolName":"site.app.build"}`)
	event := testInboundEvent("message-after-stale-task")
	event.Prompt = "다시 해줘"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil {
		t.Fatalf("expected event after inactive running task to process: %v", errorValue)
	}
	if result.TaskRunID == "" || result.TaskRunID == orphanedTaskRun.TaskRunID {
		t.Fatalf("expected new task after inactive task interruption, got %+v", result)
	}
	interruptedTaskRun, isFound := connectorRuntime.agentKernel.FindTaskRun(orphanedTaskRun.TaskRunID)
	if !isFound || interruptedTaskRun.Status != task.TaskStatusInterrupted {
		t.Fatalf("expected inactive task interrupted, got found=%v task=%+v", isFound, interruptedTaskRun)
	}
	taskAttempt := taskRunRepository.taskAttempts[orphanedTaskRun.CurrentAttemptID]
	if taskAttempt.Status != task.TaskAttemptStatusInterrupted {
		t.Fatalf("attempt status = %s, want interrupted", taskAttempt.Status)
	}
	if !connectorTaskEventsContain(connectorRuntime, orphanedTaskRun.TaskRunID, "tool.site.app.build.cancelled", "cancelled_by_attempt_end") {
		t.Fatal("expected orphaned tool request to be cancelled")
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "새 작업으로 처리했습니다." {
		t.Fatalf("expected new task reply, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeBusySteerAppendsInstructionWithoutNewTask(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"blueclaw_turn_router": {
				`{"route":"revise_task","classification":"bounded_task","taskShape":"maintenance_task","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"user corrected active task","userFacingReply":"","busyRoute":"steer","busyInstruction":"PDF 대신 HTML로 작성한다."}`,
			},
			"blueclaw_reply": {
				`{"reply":"방향 수정 내용을 현재 작업에 반영하겠습니다."}`,
			},
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntime.agentKernel.UseIntakeLanguageModelProvider(languageModel)
	connectorRuntime.agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true})
	activeTaskRun, errorValue := connectorRuntime.agentKernel.RunTask("person-1", "direct-1", "PDF 보고서 작성")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, isFound := connectorRuntime.latestCurrentConversationActiveTask("person-1", "direct-1"); !isFound {
		t.Fatal("expected active task before busy steer event")
	}
	event := testInboundEvent("message-busy-steer")
	event.Prompt = "아니 PDF 말고 HTML로 해"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil {
		t.Fatalf("expected busy steer event to process: %v", errorValue)
	}
	if result.Reason != "busy_steer" || result.TaskRunID != activeTaskRun.TaskRunID {
		t.Fatalf("expected busy steer for active task, got %+v", result)
	}
	if len(connectorRuntime.agentKernel.ListTaskRunByPersonID("person-1")) != 1 {
		t.Fatalf("expected no new task run, got %+v", connectorRuntime.agentKernel.ListTaskRunByPersonID("person-1"))
	}
	if !connectorTaskEventsContain(connectorRuntime, activeTaskRun.TaskRunID, "task.steer.requested", "PDF 대신 HTML") {
		t.Fatal("expected steer request event")
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "방향 수정 내용을 현재 작업에 반영하겠습니다." {
		t.Fatalf("expected steer acknowledgement, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeBusyCancelStopsActiveTaskWithoutNewTask(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"blueclaw_turn_router": {
				`{"route":"consume","classification":"quick_reply","taskShape":"immediate_reply","effortLevel":"quick","requestedOutputFormats":null,"responseLanguage":"ko","reason":"user asked to cancel active task","userFacingReply":"","busyRoute":"cancel","busyInstruction":""}`,
			},
			"blueclaw_reply": {
				`{"reply":"진행 중인 작업을 중단했습니다."}`,
			},
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntime.agentKernel.UseIntakeLanguageModelProvider(languageModel)
	connectorRuntime.agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true})
	activeTaskRun, errorValue := connectorRuntime.agentKernel.RunTask("person-1", "direct-1", "긴 작업")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	event := testInboundEvent("message-busy-cancel")
	event.Prompt = "중단해"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil {
		t.Fatalf("expected busy cancel event to process: %v", errorValue)
	}
	cancelledTaskRun, isFound := connectorRuntime.agentKernel.FindTaskRun(activeTaskRun.TaskRunID)
	if !isFound || cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected active task cancelled, got found=%v task=%+v", isFound, cancelledTaskRun)
	}
	if result.Reason != "busy_cancel" || result.TaskRunID != activeTaskRun.TaskRunID {
		t.Fatalf("expected busy cancel result, got %+v", result)
	}
	if len(connectorRuntime.agentKernel.ListTaskRunByPersonID("person-1")) != 1 {
		t.Fatalf("expected no new task run, got %+v", connectorRuntime.agentKernel.ListTaskRunByPersonID("person-1"))
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "진행 중인 작업을 중단했습니다." {
		t.Fatalf("expected cancel reply, got %+v", adapter.sentReplies)
	}
	if !connectorTaskEventsContain(connectorRuntime, activeTaskRun.TaskRunID, "task.cancel.requested", "message-busy-cancel") {
		t.Fatal("expected cancel request event")
	}
}

func TestConnectorRuntimeBusyReplaceCancelsActiveTaskAndStartsNewTask(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"blueclaw_turn_router": {
				`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"user replaced active task","userFacingReply":"","busyRoute":"replace","busyInstruction":"새 지시로 교체한다."}`,
				`{"route":"start_task","classification":"quick_reply","taskShape":"immediate_reply","effortLevel":"quick","requestedOutputFormats":null,"responseLanguage":"ko","reason":"replacement task","userFacingReply":""}`,
			},
		},
		ActionResponses: []string{
			connectorFinishMessage("새 작업으로 진행했습니다."),
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntime.agentKernel.UseIntakeLanguageModelProvider(languageModel)
	connectorRuntime.agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true})
	activeTaskRun, errorValue := connectorRuntime.agentKernel.RunTask("person-1", "direct-1", "기존 작업")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	event := testInboundEvent("message-busy-replace")
	event.Prompt = "아니 그거 취소하고 새 작업 해"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil {
		t.Fatalf("expected busy replace event to process: %v", errorValue)
	}
	cancelledTaskRun, isFound := connectorRuntime.agentKernel.FindTaskRun(activeTaskRun.TaskRunID)
	if !isFound || cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected active task cancelled, got found=%v task=%+v", isFound, cancelledTaskRun)
	}
	if result.TaskRunID == "" || result.TaskRunID == activeTaskRun.TaskRunID {
		t.Fatalf("expected replacement task result, got %+v", result)
	}
	if !connectorTaskEventsContain(connectorRuntime, activeTaskRun.TaskRunID, "task.replaced", "message-busy-replace") {
		t.Fatal("expected replaced event")
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "새 작업으로 진행했습니다." {
		t.Fatalf("expected replacement reply, got %+v", adapter.sentReplies)
	}
}

func TestOutboundReplyJSONPreservesInlineAttachmentPayload(t *testing.T) {
	reply := OutboundReply{
		Message:   "attached",
		TaskRunID: "task-1",
		ReplyKind: connectorReplyKindSuccess,
		Attachments: []agent.FileAttachment{{
			DevicePath:    "/workspace/deck.pptx",
			Filename:      "deck.pptx",
			ContentType:   "application/vnd.openxmlformats-officedocument.presentationml.presentation",
			SizeBytes:     4,
			ContentBase64: "cHB0eA==",
		}},
	}

	document, errorValue := json.Marshal(reply)
	if errorValue != nil {
		t.Fatalf("expected reply to marshal: %v", errorValue)
	}
	var decodedReply OutboundReply
	if errorValue := json.Unmarshal(document, &decodedReply); errorValue != nil {
		t.Fatalf("expected reply to unmarshal: %v", errorValue)
	}

	if len(decodedReply.Attachments) != 1 || decodedReply.Attachments[0].ContentBase64 != "cHB0eA==" {
		t.Fatalf("expected inline payload to survive outbox json, got %+v", decodedReply.Attachments)
	}
	if decodedReply.TaskRunID != "task-1" || decodedReply.ReplyKind != connectorReplyKindSuccess {
		t.Fatalf("expected delivery metadata to survive outbox json, got %+v", decodedReply)
	}
}

func TestOutboundReplyJSONPreservesAskInteraction(t *testing.T) {
	reply := OutboundReply{
		Message:         "확인해 주세요.",
		EphemeralUserID: "requester-1",
		Interaction: &AskInteraction{
			InteractionID:        "interaction-1",
			TaskRunID:            "task-1",
			Kind:                 "ask_confirm",
			Message:              "진행할까요?",
			TargetPlatformUserID: "user-1",
		},
	}

	document, errorValue := json.Marshal(reply)
	if errorValue != nil {
		t.Fatalf("expected reply to marshal: %v", errorValue)
	}
	var decodedReply OutboundReply
	if errorValue := json.Unmarshal(document, &decodedReply); errorValue != nil {
		t.Fatalf("expected reply to unmarshal: %v", errorValue)
	}

	if decodedReply.Interaction == nil || decodedReply.Interaction.Kind != "ask_confirm" || decodedReply.Interaction.Message != "진행할까요?" || decodedReply.Interaction.TargetPlatformUserID != "user-1" {
		t.Fatalf("expected ask interaction to survive outbox json, got %+v", decodedReply.Interaction)
	}
	if decodedReply.EphemeralUserID != "requester-1" {
		t.Fatalf("expected ephemeral target to survive outbox json, got %+v", decodedReply)
	}
}

func TestOutboundReplyJSONPreservesFailureNotice(t *testing.T) {
	reply := OutboundReply{
		Message:   "작업을 완료하지 못했습니다. 접근 권한을 확인한 뒤 다시 시도해 주세요.",
		TaskRunID: "task-1",
		ReplyKind: connectorReplyKindUserNotice,
		FailureNotice: agent.FailureNotice{
			Message:           "작업을 완료하지 못했습니다. 접근 권한을 확인한 뒤 다시 시도해 주세요.",
			Source:            "generated",
			Language:          "ko",
			DiagnosticEventID: "task-1:failed",
			IsSendable:        true,
		},
	}

	document, errorValue := json.Marshal(reply)
	if errorValue != nil {
		t.Fatalf("expected reply to marshal: %v", errorValue)
	}
	var decodedReply OutboundReply
	if errorValue := json.Unmarshal(document, &decodedReply); errorValue != nil {
		t.Fatalf("expected reply to unmarshal: %v", errorValue)
	}

	if decodedReply.FailureNotice.DiagnosticEventID != "task-1:failed" || !decodedReply.FailureNotice.IsSendable {
		t.Fatalf("expected failure notice to survive outbox json, got %+v", decodedReply.FailureNotice)
	}
}

func TestConnectorRuntimeStopsProgressAfterRequestContextCancellation(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "ignored"})
	ctx, cancel := context.WithCancel(context.Background())
	stopProgress := connectorRuntime.startProgress(ctx, adapter, ReplyTarget{
		ConversationID: "conversation-1",
		ReplyTargetID:  "reply-target-1",
	})

	cancel()
	stopProgress()

	if len(adapter.progressStops) != 1 {
		t.Fatalf("expected one progress stop, got %d", len(adapter.progressStops))
	}
	if adapter.progressStopErrors[0] != nil {
		t.Fatalf("expected stop progress context not to inherit cancellation, got %v", adapter.progressStopErrors[0])
	}
}

func TestConnectorRuntimeRejectsUninvitedUserWithoutTask(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "ignored"})
	adapter.senderEmail = "outside@example.com"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testInboundEvent("message-1"))
	if errorValue != nil {
		t.Fatalf("expected uninvited user to receive rejection: %v", errorValue)
	}

	if result.TaskRunID != "" {
		t.Fatalf("expected no task run, got %q", result.TaskRunID)
	}
	if result.ReplyDispatchID != "dispatch-1" {
		t.Fatalf("expected rejection dispatch id, got %q", result.ReplyDispatchID)
	}
	if adapter.sentReplies[0].message != NotInvitedReply {
		t.Fatalf("expected not invited reply, got %q", adapter.sentReplies[0].message)
	}
}

func TestConnectorRuntimeRequesterEmailFallsBackToVisibleSenderEmail(t *testing.T) {
	identityService := identity.NewIdentityService(policy.PolicyProjection{
		PersonAccessByPersonID: map[string]policy.PersonAccess{
			"person-1": {PersonID: "person-1"},
		},
	})
	taskEventService := task.NewTaskEventService()
	agentKernel := agent.NewAgentKernel(task.NewTaskRunService(taskEventService), task.NewTaskStepService())
	connectorRuntime := NewConnectorRuntime(identityService, agentKernel, nil)
	event := testInboundEvent("message-1")
	event.Context.Sender.Email = "Sender@Example.com"

	email := connectorRuntime.requesterEmailForEvent("person-1", event)

	if email != "sender@example.com" {
		t.Fatalf("email = %q", email)
	}
}

func TestConnectorRuntimeRequesterEmailPrefersPolicyPrimaryEmail(t *testing.T) {
	identityService := identity.NewIdentityService(policy.PolicyProjection{
		PersonIDByEmail: map[string]string{"primary@example.com": "person-1"},
		PersonAccessByPersonID: map[string]policy.PersonAccess{
			"person-1": {PersonID: "person-1"},
		},
	})
	taskEventService := task.NewTaskEventService()
	agentKernel := agent.NewAgentKernel(task.NewTaskRunService(taskEventService), task.NewTaskStepService())
	connectorRuntime := NewConnectorRuntime(identityService, agentKernel, nil)
	event := testInboundEvent("message-1")
	event.Context.Sender.Email = "sender@example.com"

	email := connectorRuntime.requesterEmailForEvent("person-1", event)

	if email != "primary@example.com" {
		t.Fatalf("email = %q", email)
	}
}

func TestConnectorRuntimeSkipsAddressingClassifierForDirectMessage(t *testing.T) {
	languageModel := &addressingTestLanguageModel{addressingTarget: string(agent.AddressingTargetHuman), reply: "ok"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	event := testInboundEvent("message-1")
	event.Context.ConversationType = "D"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected direct message to process: %v", errorValue)
	}

	if result.TaskRunID == "" || len(adapter.sentReplies) != 1 {
		t.Fatalf("expected direct message task and reply, got result=%+v replies=%d", result, len(adapter.sentReplies))
	}
	if connectorContainsSchemaName(languageModel.requests, "blueclaw_addressing_classification") {
		t.Fatalf("expected direct message to skip addressing classifier, got schemas %+v", connectorRequestSchemaNames(languageModel.requests))
	}
}

func TestConnectorRuntimeReactsToConsumedAddressedMessageWithoutReply(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"blueclaw_turn_router": {
				`{"route":"consume","classification":"quick_reply","taskShape":"immediate_reply","effortLevel":"quick","requestedOutputFormats":null,"responseLanguage":"ko","reason":"acknowledgement","userFacingReply":"","reactionEmojiName":"tada"}`,
			},
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntime.agentKernel.UseIntakeLanguageModelProvider(languageModel)
	connectorRuntime.agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true})
	event := testInboundEvent("message-consume")

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected consume event to process: %v", errorValue)
	}

	if result.Reason != "consume_reacted" || result.TaskRunID == "" {
		t.Fatalf("expected consume reaction result, got %+v", result)
	}
	if len(adapter.sentReplies) != 0 {
		t.Fatalf("expected no reply for consume, got %+v", adapter.sentReplies)
	}
	if len(adapter.reactions) != 1 {
		t.Fatalf("expected one reaction, got %+v", adapter.reactions)
	}
	reaction := adapter.reactions[0]
	if reaction.MessageID != event.MessageID || reaction.EmojiName != "tada" || reaction.Reason != "consume" {
		t.Fatalf("unexpected consume reaction: %+v", reaction)
	}
	if !connectorTaskEventsContain(connectorRuntime, result.TaskRunID, "connector.reaction.sent", "tada") {
		t.Fatal("expected reaction event")
	}
}

func TestConnectorRuntimeConsumeWithoutReactionAdapterDoesNotReply(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"blueclaw_turn_router": {
				`{"route":"consume","classification":"quick_reply","taskShape":"immediate_reply","effortLevel":"quick","requestedOutputFormats":null,"responseLanguage":"ko","reason":"acknowledgement","userFacingReply":""}`,
			},
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntime.agentKernel.UseIntakeLanguageModelProvider(languageModel)
	connectorRuntime.agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true})
	noReactionAdapter := testAdapterWithoutReaction{adapter: adapter}

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), noReactionAdapter, testInboundEvent("message-consume"))
	if errorValue != nil {
		t.Fatalf("expected consume event to process: %v", errorValue)
	}

	if result.Reason != "consume_no_reaction_adapter" {
		t.Fatalf("expected no-adapter consume result, got %+v", result)
	}
	if len(adapter.sentReplies) != 0 || len(adapter.reactions) != 0 {
		t.Fatalf("expected no reply or reaction, replies=%+v reactions=%+v", adapter.sentReplies, adapter.reactions)
	}
}

func TestConnectorRuntimeReactionFailureDoesNotSendFallbackReply(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"blueclaw_turn_router": {
				`{"route":"consume","classification":"quick_reply","taskShape":"immediate_reply","effortLevel":"quick","requestedOutputFormats":null,"responseLanguage":"ko","reason":"acknowledgement","userFacingReply":""}`,
			},
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntime.agentKernel.UseIntakeLanguageModelProvider(languageModel)
	connectorRuntime.agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true})
	adapter.reactionError = errors.New("reaction failed")

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testInboundEvent("message-consume"))
	if errorValue != nil {
		t.Fatalf("expected consume event to process: %v", errorValue)
	}

	if result.Reason != "consume_reaction_failed" {
		t.Fatalf("expected reaction failure result, got %+v", result)
	}
	if len(adapter.sentReplies) != 0 {
		t.Fatalf("expected no fallback reply, got %+v", adapter.sentReplies)
	}
	if !connectorTaskEventsContain(connectorRuntime, result.TaskRunID, "connector.reaction.failed", "reaction failed") {
		t.Fatal("expected reaction failure event")
	}
}

func TestLatestApprovalQuestionUsesOnlyUserFacingMessage(t *testing.T) {
	taskEvents := []task.TaskEvent{{
		Name: "confirmation.requested",
		Body: `{"reason":"Direct messages are external sends and require approval before immediate delivery.","reasonCode":"external_send"}`,
	}, {
		Name: "confirmation.requested",
		Body: `{"userFacingMessage":"동하 님에게 다음 DM을 보내도 될까요?\n\n테스트","reasonDetail":"internal only"}`,
	}}

	question := latestApprovalQuestion(taskEvents)

	if question != "동하 님에게 다음 DM을 보내도 될까요?\n\n테스트" {
		t.Fatalf("expected user-facing approval question, got %q", question)
	}
}

func TestLatestApprovalQuestionDoesNotFallBackToReason(t *testing.T) {
	taskEvents := []task.TaskEvent{{
		Name: "confirmation.requested",
		Body: `{"reason":"Direct messages are external sends and require approval before immediate delivery.","reasonCode":"external_send"}`,
	}}

	question := latestApprovalQuestion(taskEvents)

	if question != "" {
		t.Fatalf("expected no question from internal reason, got %q", question)
	}
}

func TestLatestAskInteractionSkipsResolvedInteraction(t *testing.T) {
	taskEvents := []task.TaskEvent{{
		TaskEventID: "ask-1",
		Name:        "ask.requested",
		Body:        `{"kind":"choice_single","question":"배포할 사이트를 선택해 주세요.","options":[{"key":"A","label":"첫 번째"},{"key":"B","label":"두 번째"}]}`,
	}, {
		TaskEventID: "resolved-1",
		Name:        "ask.resolved",
		Body:        `{"interactionID":"ask-1","kind":"ask_choice_single","choices":["B"]}`,
	}}

	interaction, isFound := latestAskInteraction("task-1", taskEvents)

	if isFound {
		t.Fatalf("expected resolved ask interaction to be hidden, got %+v", interaction)
	}
}

func TestLatestAskInteractionReturnsNewAskAfterEarlierResolution(t *testing.T) {
	taskEvents := []task.TaskEvent{{
		TaskEventID: "ask-1",
		Name:        "ask.requested",
		Body:        `{"kind":"choice_single","question":"배포할 사이트를 선택해 주세요.","options":[{"key":"A","label":"첫 번째"},{"key":"B","label":"두 번째"}]}`,
	}, {
		TaskEventID: "resolved-1",
		Name:        "ask.resolved",
		Body:        `{"interactionID":"ask-1","kind":"ask_choice_single","choices":["B"]}`,
	}, {
		TaskEventID: "ask-2",
		Name:        "ask.requested",
		Body:        `{"kind":"confirm","message":"복구를 진행할까요?"}`,
	}}

	interaction, isFound := latestAskInteraction("task-1", taskEvents)

	if !isFound || interaction.InteractionID != "ask-2" || interaction.Kind != "ask_confirm" {
		t.Fatalf("expected latest unresolved ask interaction, got found=%v interaction=%+v", isFound, interaction)
	}
}

func TestConnectorRuntimeProcessesBotMentionWithoutAddressingClassifier(t *testing.T) {
	languageModel := &addressingTestLanguageModel{addressingTarget: string(agent.AddressingTargetHuman), reply: "ok"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	event := testChannelInboundEvent("message-1")
	event.Context.Addressing.BotMentioned = true

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected bot mention to process: %v", errorValue)
	}

	if result.TaskRunID == "" || len(adapter.sentReplies) != 1 {
		t.Fatalf("expected bot mention task and reply, got result=%+v replies=%d", result, len(adapter.sentReplies))
	}
	if connectorContainsSchemaName(languageModel.requests, "blueclaw_addressing_classification") {
		t.Fatalf("expected bot mention to skip addressing classifier, got schemas %+v", connectorRequestSchemaNames(languageModel.requests))
	}
}

func TestConnectorRuntimeIgnoresOtherPersonMentionWithoutClassifying(t *testing.T) {
	languageModel := &addressingTestLanguageModel{addressingTarget: string(agent.AddressingTargetHuman), reply: "unused"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	event := testChannelInboundEvent("message-1")
	event.Context.Addressing.OtherPersonMentioned = true

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected other mention to be ignored: %v", errorValue)
	}

	if !result.Ignored || result.Reason != "addressed_to_other_person" {
		t.Fatalf("expected other-person mention ignore, got %+v", result)
	}
	if result.TaskRunID != "" || len(adapter.sentReplies) != 0 || len(adapter.progressStarts) != 0 {
		t.Fatalf("expected no task/reply/progress, got result=%+v replies=%d progress=%d", result, len(adapter.sentReplies), len(adapter.progressStarts))
	}
	if len(adapter.reactions) != 0 {
		t.Fatalf("expected addressing ignored message not to receive reaction, got %+v", adapter.reactions)
	}
	if connectorContainsSchemaName(languageModel.requests, "blueclaw_addressing_classification") {
		t.Fatalf("expected other-person mention to skip addressing classifier, got schemas %+v", connectorRequestSchemaNames(languageModel.requests))
	}
}

func TestConnectorRuntimeProcessesAssistantRequestedAmbiguousChannelMessage(t *testing.T) {
	languageModel := &addressingTestLanguageModel{addressingTarget: string(agent.AddressingTargetBot), reply: "ok"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testChannelInboundEvent("message-1"))
	if errorValue != nil {
		t.Fatalf("expected assistant-requested message to process: %v", errorValue)
	}

	if result.TaskRunID == "" || len(adapter.sentReplies) != 1 {
		t.Fatalf("expected assistant-requested task and reply, got result=%+v replies=%d", result, len(adapter.sentReplies))
	}
	if !connectorContainsSchemaName(languageModel.requests, "blueclaw_addressing_classification") {
		t.Fatalf("expected addressing classifier request, got schemas %+v", connectorRequestSchemaNames(languageModel.requests))
	}
}

func TestConnectorRuntimeUsesIntakeLanguageModelForAddressingClassifier(t *testing.T) {
	replyLanguageModel := &addressingTestLanguageModel{addressingTarget: string(agent.AddressingTargetHuman), reply: "ok"}
	intakeLanguageModel := &addressingTestLanguageModel{addressingTarget: string(agent.AddressingTargetBot), reply: "unused"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, replyLanguageModel)
	connectorRuntime.agentKernel.UseIntakeLanguageModelProvider(intakeLanguageModel)

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testChannelInboundEvent("message-1"))
	if errorValue != nil {
		t.Fatalf("expected intake classifier to process: %v", errorValue)
	}

	if result.TaskRunID == "" || len(adapter.sentReplies) != 1 {
		t.Fatalf("expected intake classifier result to launch task, got result=%+v replies=%d", result, len(adapter.sentReplies))
	}
	if !connectorContainsSchemaName(intakeLanguageModel.requests, "blueclaw_addressing_classification") {
		t.Fatalf("expected intake language model to classify addressing, got schemas %+v", connectorRequestSchemaNames(intakeLanguageModel.requests))
	}
	if connectorContainsSchemaName(replyLanguageModel.requests, "blueclaw_addressing_classification") {
		t.Fatalf("expected reply language model not to classify addressing, got schemas %+v", connectorRequestSchemaNames(replyLanguageModel.requests))
	}
}

func TestConnectorRuntimeIgnoresNonAssistantAddressingClasses(t *testing.T) {
	tests := []struct {
		name             string
		addressingTarget string
		reason           string
	}{
		{name: "human", addressingTarget: string(agent.AddressingTargetHuman), reason: "addressing_human dutyMatch=false"},
		{name: "anyone", addressingTarget: string(agent.AddressingTargetAnyone), reason: "addressing_anyone dutyMatch=false"},
		{name: "none", addressingTarget: string(agent.AddressingTargetNone), reason: "addressing_none dutyMatch=false"},
		{name: "unclear", addressingTarget: string(agent.AddressingTargetUnclear), reason: "addressing_unclear dutyMatch=false"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			languageModel := &addressingTestLanguageModel{addressingTarget: test.addressingTarget, reply: "unused"}
			connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)

			result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testChannelInboundEvent("message-1"))
			if errorValue != nil {
				t.Fatalf("expected message to be ignored: %v", errorValue)
			}

			if !result.Ignored || result.Reason != test.reason {
				t.Fatalf("expected %s ignore, got %+v", test.reason, result)
			}
			if result.TaskRunID != "" || len(adapter.sentReplies) != 0 || len(adapter.progressStarts) != 0 {
				t.Fatalf("expected no task/reply/progress, got result=%+v replies=%d progress=%d", result, len(adapter.sentReplies), len(adapter.progressStarts))
			}
		})
	}
}

func TestConnectorRuntimeIgnoresUninvitedAmbiguousChannelMessageWithoutReply(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "unused"})
	adapter.senderEmail = "outside@example.com"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testChannelInboundEvent("message-1"))
	if errorValue != nil {
		t.Fatalf("expected uninvited ambiguous message to be ignored: %v", errorValue)
	}

	if !result.Ignored || result.Reason != "not_addressed_to_bot" {
		t.Fatalf("expected not_addressed_to_bot ignore, got %+v", result)
	}
	if result.TaskRunID != "" || len(adapter.sentReplies) != 0 {
		t.Fatalf("expected no task or not-invited reply, got result=%+v replies=%d", result, len(adapter.sentReplies))
	}
}

func TestConnectorRuntimeIgnoresWhenAddressingClassifierFails(t *testing.T) {
	languageModel := &addressingTestLanguageModel{addressingError: errors.New("classifier unavailable"), reply: "unused"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testChannelInboundEvent("message-1"))
	if errorValue != nil {
		t.Fatalf("expected classifier failure to close the gate: %v", errorValue)
	}

	if !result.Ignored || result.Reason != "addressing_classifier_failed dutyMatch=false" {
		t.Fatalf("expected addressing_classifier_failed ignore, got %+v", result)
	}
	if result.TaskRunID != "" || len(adapter.sentReplies) != 0 || len(adapter.progressStarts) != 0 {
		t.Fatalf("expected no task/reply/progress, got result=%+v replies=%d progress=%d", result, len(adapter.sentReplies), len(adapter.progressStarts))
	}
}

func TestConnectorRuntimeSkipsUnsafeUserNoticeAttachmentClaims(t *testing.T) {
	connectorRuntime, _ := newTestConnectorRuntime(t, testLanguageModel{reply: "unused"})
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")
	event.Prompt = "파일 만들어줘"
	dispatchID, isSent := connectorRuntime.sendUserNoticeReply(
		context.Background(),
		"test",
		event,
		"task-1",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agent.AgentTurnResult{UserNotice: "파일을 생성해 첨부했습니다."},
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if isSent || dispatchID != "" {
		t.Fatalf("expected skipped incomplete reply, got dispatchID=%q sent=%v", dispatchID, isSent)
	}
	if len(sentReplies) != 0 {
		t.Fatalf("expected no recovered reply, got %+v", sentReplies)
	}
}

func TestConnectorRuntimeSkipsUnsafeUserNoticeUnattachedFilenames(t *testing.T) {
	connectorRuntime, _ := newTestConnectorRuntime(t, testLanguageModel{reply: "unused"})
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")
	event.Prompt = "html 파일 만들어줘"
	dispatchID, isSent := connectorRuntime.sendUserNoticeReply(
		context.Background(),
		"test",
		event,
		"task-1",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agent.AgentTurnResult{UserNotice: "아래 파일을 확인해 주세요.\n[Hermes_Agent_Slide_Part1.html]"},
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if isSent || dispatchID != "" {
		t.Fatalf("expected skipped incomplete reply, got dispatchID=%q sent=%v", dispatchID, isSent)
	}
	if len(sentReplies) != 0 {
		t.Fatalf("expected no recovered reply, got %+v", sentReplies)
	}
}

func TestConnectorRuntimeSendsSafeUserNoticeForBlockedTask(t *testing.T) {
	connectorRuntime, _ := newTestConnectorRuntime(t, testLanguageModel{reply: "unused"})
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")

	dispatchID, isSent := connectorRuntime.sendUserNoticeReply(
		context.Background(),
		"test",
		event,
		"task-1",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agent.AgentTurnResult{UserNotice: "PPTX를 만들지 못했습니다. 다시 시도해 주세요."},
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if !isSent || dispatchID != "dispatch-1" {
		t.Fatalf("expected user notice to send, got dispatchID=%q sent=%v", dispatchID, isSent)
	}
	if len(sentReplies) != 1 || sentReplies[0].ReplyKind != connectorReplyKindUserNotice || sentReplies[0].TaskRunID != "task-1" {
		t.Fatalf("expected user notice reply metadata, got %+v", sentReplies)
	}
	if !connectorTaskEventsContain(connectorRuntime, "task-1", "connector.reply.sent", "user_notice") {
		t.Fatal("expected sent event for user notice")
	}
}

func TestConnectorRuntimeSendsFailureNoticeForBlockedTask(t *testing.T) {
	connectorRuntime, _ := newTestConnectorRuntime(t, testLanguageModel{reply: "unused"})
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")

	dispatchID, isSent := connectorRuntime.sendUserNoticeReply(
		context.Background(),
		"test",
		event,
		"task-1",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agent.AgentTurnResult{
			TaskRun:    task.TaskRun{Status: task.TaskStatusBlocked},
			UserNotice: "replyStatus: raw internal diagnostic",
			FailureNotice: agent.FailureNotice{
				Message:           "작업을 완료하지 못했습니다. 접근 권한을 확인한 뒤 다시 시도해 주세요.",
				Source:            "generated",
				Language:          "ko",
				DiagnosticEventID: "task-1:limit",
				IsSendable:        true,
			},
		},
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if !isSent || dispatchID != "dispatch-1" {
		t.Fatalf("expected failure notice to send, got dispatchID=%q sent=%v", dispatchID, isSent)
	}
	if len(sentReplies) != 1 || sentReplies[0].Message != "작업을 완료하지 못했습니다. 접근 권한을 확인한 뒤 다시 시도해 주세요.\n\n`task-1`" {
		t.Fatalf("expected failure notice reply with run footer, got %+v", sentReplies)
	}
	if sentReplies[0].FailureNotice.DiagnosticEventID != "task-1:limit" {
		t.Fatalf("expected diagnostic reference to be preserved, got %+v", sentReplies[0].FailureNotice)
	}
}

func TestConnectorRuntimeFailureFooterLinksAdminTaskWhenConfigured(t *testing.T) {
	connectorRuntime, _ := newTestConnectorRuntime(t, testLanguageModel{reply: "unused"})
	connectorRuntime.UseAdminTaskLinkBaseURL("https://demo.intern.kim/")
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")

	_, isSent := connectorRuntime.sendUserNoticeReply(
		context.Background(),
		"test",
		event,
		"a1b2c3d4e5f6",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agent.AgentTurnResult{
			TaskRun: task.TaskRun{Status: task.TaskStatusFailed},
			FailureNotice: agent.FailureNotice{
				Message:    "작업을 완료하지 못했습니다.",
				Source:     "generated",
				Language:   "ko",
				IsSendable: true,
			},
		},
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if !isSent || len(sentReplies) != 1 {
		t.Fatalf("expected failure notice to send, got %+v", sentReplies)
	}
	if !strings.HasSuffix(sentReplies[0].Message, "`a1b2c3` https://demo.intern.kim/tasks/a1b2c3d4e5f6") {
		t.Fatalf("expected admin task link footer, got %q", sentReplies[0].Message)
	}
}

func TestConnectorRuntimeWaitingNoticeHasNoRunFooter(t *testing.T) {
	connectorRuntime, _ := newTestConnectorRuntime(t, testLanguageModel{reply: "unused"})
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")

	_, isSent := connectorRuntime.sendUserNoticeReply(
		context.Background(),
		"test",
		event,
		"task-1",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agent.AgentTurnResult{
			TaskRun:    task.TaskRun{Status: task.TaskStatusWaitingUserInput},
			UserNotice: "범위를 알려주시면 진행할게요.",
		},
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if !isSent || len(sentReplies) != 1 {
		t.Fatalf("expected waiting notice to send, got %+v", sentReplies)
	}
	if sentReplies[0].Message != "범위를 알려주시면 진행할게요." {
		t.Fatalf("expected no run footer on waiting notice, got %q", sentReplies[0].Message)
	}
}

func TestConnectorRuntimeSendsSafeUserNoticeWhenFailureNoticeMissing(t *testing.T) {
	connectorRuntime, _ := newTestConnectorRuntime(t, testLanguageModel{reply: "unused"})
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")

	dispatchID, isSent := connectorRuntime.sendUserNoticeReply(
		context.Background(),
		"test",
		event,
		"task-1",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agent.AgentTurnResult{
			TaskRun:    task.TaskRun{Status: task.TaskStatusBlocked},
			UserNotice: "메시지 삭제 작업을 완료하지 못했습니다.",
		},
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if !isSent || dispatchID != "dispatch-1" {
		t.Fatalf("expected safe fallback notice to send, got dispatchID=%q sent=%v", dispatchID, isSent)
	}
	if len(sentReplies) != 1 || !strings.HasPrefix(sentReplies[0].Message, "메시지 삭제 작업을 완료하지 못했습니다.") || !strings.Contains(sentReplies[0].Message, "\n\n`") {
		t.Fatalf("expected fallback user notice reply with run footer, got %+v", sentReplies)
	}
}

func TestConnectorRuntimeSendsGenericFailureNoticeWhenFailureReplyMissing(t *testing.T) {
	connectorRuntime, _ := newTestConnectorRuntime(t, testLanguageModel{reply: "unused"})
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")

	dispatchID, isSent := connectorRuntime.sendUserNoticeReply(
		context.Background(),
		"test",
		event,
		"task-1",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agent.AgentTurnResult{
			TaskRun: task.TaskRun{Status: task.TaskStatusFailed},
		},
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if isSent || dispatchID != "" {
		t.Fatalf("expected missing generated failure notice to be suppressed, got dispatchID=%q sent=%v", dispatchID, isSent)
	}
	if len(sentReplies) != 0 {
		t.Fatalf("expected no generic failure reply, got %+v", sentReplies)
	}
}

func TestConnectorRuntimeAddsSenderToRecoveryActions(t *testing.T) {
	connectorRuntime, _ := newTestConnectorRuntime(t, testLanguageModel{reply: "unused"})
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")
	event.SenderID = "sender-user-1"

	_, isSent := connectorRuntime.sendUserNoticeReply(
		context.Background(),
		"test",
		event,
		"task-1",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agent.AgentTurnResult{
			UserNotice: "Companion 연결이 필요합니다.",
			RecoveryActions: []agent.RecoveryAction{{
				Kind:           "companion_connect",
				Delivery:       "dm_preferred",
				DownloadURL:    "https://example.com/companion.dmg",
				ConnectCommand: "/connect",
			}},
		},
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if !isSent || len(sentReplies) != 1 || len(sentReplies[0].RecoveryActions) != 1 {
		t.Fatalf("expected recovery reply, got sent=%v replies=%+v", isSent, sentReplies)
	}
	if sentReplies[0].RecoveryActions[0].PlatformUserID != "sender-user-1" {
		t.Fatalf("expected sender recovery target, got %+v", sentReplies[0].RecoveryActions[0])
	}
}

func TestConnectorRuntimeSendsFailureNoticeWhenTurnReturnsError(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{errorValue: errors.New("provider unavailable")})

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testInboundEvent("message-1"))
	if errorValue != nil {
		t.Fatalf("expected turn error to be reported to the user: %v", errorValue)
	}

	if result.Reason != "task_not_completed" || result.TaskRunID == "" {
		t.Fatalf("expected task failure result, got %+v", result)
	}
	if len(adapter.sentReplies) != 1 || !strings.Contains(adapter.sentReplies[0].message, "provider unavailable") {
		t.Fatalf("expected task failure notice, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeSendsNoticeWhenLaunchReturnsNoTask(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, nil)

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testInboundEvent("message-1"))
	if errorValue != nil {
		t.Fatalf("expected launch error to be reported to the user: %v", errorValue)
	}

	if result.Reason != "task_not_completed" || result.TaskRunID == "" {
		t.Fatalf("expected launch failure result, got %+v", result)
	}
	if len(adapter.sentReplies) != 1 || !strings.Contains(adapter.sentReplies[0].message, "language model provider is not configured") {
		t.Fatalf("expected launch failure notice, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeUsesOpaqueReplyTarget(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "reply"})
	event := testInboundEvent("message-1")
	event.ReplyTargetID = "opaque-reply-target"

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}

	if adapter.sentReplies[0].target.ReplyTargetID != "opaque-reply-target" {
		t.Fatalf("expected opaque reply target, got %q", adapter.sentReplies[0].target.ReplyTargetID)
	}
}

func TestConnectorRuntimeStartsDirectProgressBeforeInitialHistoryFetch(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "reply"})
	event := testInboundEvent("message-1")
	event.Context.HasMoreBefore = true
	event.Context.HistoryCursor = "history-cursor-1"

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}

	if len(adapter.operationNames) < 2 || adapter.operationNames[0] != "progress.start" || adapter.operationNames[1] != "history.fetch" {
		t.Fatalf("expected progress before history fetch, got %+v", adapter.operationNames)
	}
}

func TestConnectorRuntimeInjectsRequesterPinnedMemoryIntoLanguageModel(t *testing.T) {
	languageModel := &recordingLanguageModel{reply: "기억했습니다"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	pinnedMemoryStore := memory.NewMarkdownStore(t.TempDir(), 1200)
	if _, errorValue := pinnedMemoryStore.MergePersonMemory(context.Background(), "person-1", "사용자는 Graphiti 메모리 설계를 선택했다."); errorValue != nil {
		t.Fatalf("expected pinned memory setup to succeed: %v", errorValue)
	}
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UsePinnedMemoryStore(pinnedMemoryStore)
	connectorRuntime.UseTaskLauncher(agentruntime.NewTaskLauncher(connectorRuntime.agentKernel, toolCatalogBuilder))

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testInboundEvent("message-1"))
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}

	if len(languageModel.request.Messages) < 2 {
		t.Fatalf("expected memory context message, got %+v", languageModel.request.Messages)
	}
	if !structuredMessagesContain(languageModel.request.Messages, "Graphiti 메모리 설계") {
		t.Fatalf("expected requester memory in model context, got %+v", languageModel.request.Messages)
	}
}

func TestConnectorRuntimeInjectsVisibleContextBeforeMemory(t *testing.T) {
	languageModel := &recordingLanguageModel{reply: "맥락 확인"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	event := testInboundEvent("message-1")
	event.Context = VisibleContext{
		Messages: []VisibleContextMessage{
			{Speaker: "admin", Text: "이전 메시지"},
		},
		HasMoreBefore: true,
		HistoryCursor: "cursor-1",
	}
	pinnedMemoryStore := memory.NewMarkdownStore(t.TempDir(), 1200)
	if _, errorValue := pinnedMemoryStore.MergePersonMemory(context.Background(), "person-1", "사용자는 간결한 설계를 선호한다."); errorValue != nil {
		t.Fatalf("expected pinned memory setup to succeed: %v", errorValue)
	}
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UsePinnedMemoryStore(pinnedMemoryStore)
	connectorRuntime.UseTaskLauncher(agentruntime.NewTaskLauncher(connectorRuntime.agentKernel, toolCatalogBuilder))

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}

	toolContextIndex := messageIndex(languageModel.request.Messages, "Available tool catalog")
	visibleContextIndex := messageIndex(languageModel.request.Messages, "admin: 이전 메시지")
	memoryIndex := messageIndex(languageModel.request.Messages, "간결한 설계")
	promptIndex := userMessageIndex(languageModel.request.Messages, event.Prompt)
	if toolContextIndex < 0 || visibleContextIndex < 0 || memoryIndex < 0 || promptIndex < 0 {
		t.Fatalf("expected visible context, memory, and prompt messages, got %+v", languageModel.request.Messages)
	}
	contextBody := joinConnectorMessageContent(languageModel.request.Messages)
	toolContextTextIndex := strings.Index(contextBody, "Available tool catalog")
	visibleContextTextIndex := strings.Index(contextBody, "admin: 이전 메시지")
	memoryTextIndex := strings.Index(contextBody, "간결한 설계")
	promptTextIndex := strings.LastIndex(contextBody, event.Prompt)
	if !(toolContextTextIndex < visibleContextTextIndex && visibleContextTextIndex < memoryTextIndex && memoryTextIndex < promptTextIndex) {
		t.Fatalf("expected tool context before visible context before memory before prompt, got %q", contextBody)
	}
}

func TestVisibleContextSeparatesCurrentAndPreviousAttachments(t *testing.T) {
	visibleContext := VisibleContext{
		InputAttachments: []InputAttachment{{
			Platform:    "mattermost",
			FileID:      "current-file",
			MessageID:   "current-post",
			Path:        "home/inbox/mattermost/current.html",
			IsAvailable: true,
		}},
		Materials: []InputAttachment{{
			Platform:    "mattermost",
			FileID:      "current-file",
			MessageID:   "current-post",
			Path:        "home/inbox/mattermost/current.html",
			IsAvailable: true,
		}, {
			Platform:    "mattermost",
			FileID:      "previous-file",
			MessageID:   "previous-post",
			Path:        "home/inbox/mattermost/previous.html",
			IsAvailable: true,
		}},
	}

	agentContext := visibleContext.ToAgentVisibleContext()

	if len(agentContext.CurrentMaterials) != 1 || agentContext.CurrentMaterials[0].MaterialID != "mattermost:current-file" {
		t.Fatalf("expected current attachment to stay current, got %+v", agentContext.CurrentMaterials)
	}
	if len(agentContext.Materials) != 1 || agentContext.Materials[0].MaterialID != "mattermost:previous-file" {
		t.Fatalf("expected previous attachments to exclude current, got %+v", agentContext.Materials)
	}
}

func TestConnectorRuntimeAddsImportedImageAttachmentCatalog(t *testing.T) {
	languageModel := &recordingLanguageModel{reply: "이미지 확인"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	adapter.inputAttachmentImportResult = InputAttachmentImportResult{
		InputParts: []agent.AgentPart{{
			Type: agent.AgentPartTypeImage,
			Image: &agent.AgentImagePart{
				MimeType:   "image/png",
				DataBase64: "aW1hZ2U=",
				Filename:   "mascot.png",
			},
			Source: agent.AgentPartSource{
				Platform:  "mattermost",
				FileID:    "file-1",
				MessageID: "message-1",
			},
		}},
		InputAttachments: []InputAttachment{{
			Platform:    "mattermost",
			FileID:      "file-1",
			MessageID:   "message-1",
			Filename:    "mascot.png",
			ContentType: "image/png",
			Path:        "/workspace/private/people/person-1/inbox/mattermost/direct-1/message-1/mascot.png",
			IsAvailable: true,
		}},
	}
	event := testInboundEvent("message-1")
	event.Context.ConversationType = "D"
	event.Context.InputAttachments = []InputAttachment{{Platform: "mattermost", FileID: "file-1", MessageID: "message-1"}}

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}
	if len(adapter.inputAttachmentImportRequests) != 1 {
		t.Fatalf("expected one attachment import request, got %+v", adapter.inputAttachmentImportRequests)
	}
	body := joinConnectorMessageContent(languageModel.request.Messages)
	for _, expected := range []string{"Current attachments", "materialID=mattermost:file-1", "path=home/inbox/mattermost/direct-1/message-1/mascot.png", "availableTools=image.read"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected attachment catalog %q in model request, got %s", expected, body)
		}
	}
	for _, unexpected := range []string{"filename=mascot.png", "contentType=image/png"} {
		if strings.Contains(body, unexpected) {
			t.Fatalf("expected normal attachment catalog to omit %q, got %s", unexpected, body)
		}
	}
	if !connectorMessagesContainImagePart(languageModel.request.Messages, "image/png", "aW1hZ2U=") {
		t.Fatalf("expected current input image part, got %+v", languageModel.request.Messages)
	}
}

func TestConnectorRuntimeKeepsHistoryImageAttachmentCatalogOnly(t *testing.T) {
	languageModel := &recordingLanguageModel{reply: "이미지 확인"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	adapter.inputAttachmentImportResult = InputAttachmentImportResult{
		InputParts: []agent.AgentPart{{
			Type: agent.AgentPartTypeImage,
			Image: &agent.AgentImagePart{
				MimeType:   "image/png",
				DataBase64: "aW1hZ2U=",
				Filename:   "mascot.png",
			},
			Source: agent.AgentPartSource{
				Platform:  "mattermost",
				FileID:    "file-1",
				MessageID: "message-1",
			},
		}},
	}
	event := testInboundEvent("message-2")
	event.Context.ConversationType = "D"
	event.Context.Materials = []InputAttachment{{
		Platform:    "mattermost",
		FileID:      "file-1",
		MessageID:   "message-1",
		Filename:    "mascot.png",
		ContentType: "image/png",
		Path:        "/workspace/private/people/person-1/inbox/mattermost/direct-1/message-1/mascot.png",
		IsAvailable: true,
	}}

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}
	body := joinConnectorMessageContent(languageModel.request.Messages)
	if !strings.Contains(body, "materialID=mattermost:file-1") {
		t.Fatalf("expected history attachment material id in model request, got %s", body)
	}
	if connectorMessagesContainImagePart(languageModel.request.Messages, "image/png", "aW1hZ2U=") {
		t.Fatalf("expected history attachment catalog only, got %+v", languageModel.request.Messages)
	}
}

func TestConnectorRuntimeAddsDocumentAttachmentCatalog(t *testing.T) {
	languageModel := &recordingLanguageModel{reply: "파일 확인"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	adapter.inputAttachmentImportResult = InputAttachmentImportResult{
		InputAttachments: []InputAttachment{{
			Platform:    "mattermost",
			FileID:      "file-1",
			MessageID:   "message-1",
			Filename:    "report.pdf",
			ContentType: "application/pdf",
			Path:        "/workspace/circles/staff/inbox/mattermost/direct-1/message-1/report.pdf",
			IsAvailable: true,
		}},
	}
	event := testInboundEvent("message-1")
	event.Context.InputAttachments = []InputAttachment{{Platform: "mattermost", FileID: "file-1", MessageID: "message-1"}}

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}
	body := joinConnectorMessageContent(languageModel.request.Messages)
	for _, expected := range []string{"Current attachments", "materialID=mattermost:file-1", "path=/workspace/circles/staff/inbox/mattermost/direct-1/message-1/report.pdf", "availableTools=file.preview,file.read"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected attachment catalog %q in model request, got %s", expected, body)
		}
	}
	for _, unexpected := range []string{"filename=report.pdf", "contentType=application/pdf"} {
		if strings.Contains(body, unexpected) {
			t.Fatalf("expected normal attachment catalog to omit %q, got %s", unexpected, body)
		}
	}
	if strings.Contains(body, "Markdown preview:") || strings.Contains(body, "Converted content") {
		t.Fatalf("expected no automatic document rehydrate, got %s", body)
	}
}

func TestConnectorRuntimeAddsUnavailableAttachmentCatalog(t *testing.T) {
	languageModel := &recordingLanguageModel{reply: "파일 메타데이터 확인"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	adapter.inputAttachmentImportResult = InputAttachmentImportResult{
		InputAttachments: []InputAttachment{{
			Platform:    "mattermost",
			FileID:      "file-1",
			MessageID:   "message-1",
			Filename:    "archive.bin",
			ContentType: "application/octet-stream",
			Path:        "/workspace/circles/staff/inbox/mattermost/direct-1/message-1/archive.bin",
			IsAvailable: false,
			ErrorCode:   "download_failed",
			Message:     "unsupported format",
		}},
	}
	event := testInboundEvent("message-1")
	event.Context.InputAttachments = []InputAttachment{{Platform: "mattermost", FileID: "file-1", MessageID: "message-1"}}

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}
	body := joinConnectorMessageContent(languageModel.request.Messages)
	for _, expected := range []string{"archive.bin", "application/octet-stream", "available=false", "errorCode=download_failed", "unsupported format"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected unsupported file metadata %q in model request, got %s", expected, body)
		}
	}
}

func TestConnectorAttachmentMaterialResolverImportsHistoryMaterial(t *testing.T) {
	adapter := &testAdapter{
		historyContext: VisibleContext{
			Materials: []InputAttachment{{
				Platform:    "mattermost",
				FileID:      "file-1",
				MessageID:   "root-message",
				Filename:    "mascot.png",
				ContentType: "image/png",
				SizeBytes:   5,
			}},
		},
		inputAttachmentImportResult: InputAttachmentImportResult{
			InputAttachments: []InputAttachment{{
				Platform:    "mattermost",
				FileID:      "file-1",
				MessageID:   "root-message",
				Filename:    "mascot.png",
				ContentType: "image/png",
				SizeBytes:   5,
				Path:        "/workspace/circles/staff/inbox/mattermost/thread-1/mascot.png",
				IsAvailable: true,
			}},
		},
	}
	event := testInboundEvent("reply-message")
	event.Platform = "mattermost"
	event.ConversationID = "thread-1"
	event.Context.HistoryCursor = "history-cursor"
	event.Context.ConversationType = "O"
	event.Context.ChannelID = "town-square"
	resolver := connectorAttachmentMaterialResolver{adapter: adapter, personID: "person-1", event: event}

	material, errorValue := resolver.ResolveAttachmentMaterial(context.Background(), "mattermost:file-1")

	if errorValue != nil {
		t.Fatalf("expected history material to resolve: %v", errorValue)
	}
	if material.MaterialID != "mattermost:file-1" || material.Path != "/workspace/circles/staff/inbox/mattermost/thread-1/mascot.png" {
		t.Fatalf("expected imported history material, got %+v", material)
	}
	if len(adapter.historyCursors) != 1 || adapter.historyCursors[0] != "history-cursor" {
		t.Fatalf("expected resolver to fetch history, got %+v", adapter.historyCursors)
	}
	if len(adapter.inputAttachmentImportRequests) != 1 {
		t.Fatalf("expected one import request, got %+v", adapter.inputAttachmentImportRequests)
	}
	importRequest := adapter.inputAttachmentImportRequests[0]
	if importRequest.MessageID != "root-message" {
		t.Fatalf("expected import to carry the source message id, got %+v", importRequest)
	}
	if !strings.Contains(importRequest.TargetDirectoryPath, "/inbox/mattermost/thread-1") || strings.Contains(importRequest.TargetDirectoryPath, "/root-message") {
		t.Fatalf("expected import to use the conversation directory without a per-message folder, got %+v", importRequest)
	}
}

func TestConnectorAttachmentMaterialResolverRefreshesStaleMaterialPath(t *testing.T) {
	adapter := &testAdapter{
		inputAttachmentImportResult: InputAttachmentImportResult{
			InputAttachments: []InputAttachment{{
				Platform:    "mattermost",
				FileID:      "file-1",
				MessageID:   "message-1",
				Filename:    "report.html",
				ContentType: "text/html",
				Path:        "/workspace/private/people/person-1/inbox/mattermost/direct-1/message-1/report.html",
				IsAvailable: true,
			}},
		},
	}
	event := testInboundEvent("message-1")
	event.Platform = "mattermost"
	event.ConversationID = "direct-1"
	event.Context.InputAttachments = []InputAttachment{{
		Platform:    "mattermost",
		FileID:      "file-1",
		MessageID:   "message-1",
		Filename:    "report.html",
		ContentType: "text/html",
		Path:        "/workspace/private/people/person-1/inbox/mattermost/direct-1/old/report.html",
		IsAvailable: true,
	}}
	resolver := connectorAttachmentMaterialResolver{adapter: adapter, personID: "person-1", event: event}

	material, errorValue := resolver.ResolveAttachmentMaterial(context.Background(), "mattermost:file-1")

	if errorValue != nil {
		t.Fatalf("expected stale material to refresh: %v", errorValue)
	}
	if material.Path != "/workspace/private/people/person-1/inbox/mattermost/direct-1/message-1/report.html" {
		t.Fatalf("expected refreshed material path, got %+v", material)
	}
	if len(adapter.inputAttachmentImportRequests) != 1 {
		t.Fatalf("expected one refresh import request, got %+v", adapter.inputAttachmentImportRequests)
	}
}

func TestConnectorRuntimeFetchesInitialVisibleContextFromHistoryCursor(t *testing.T) {
	languageModel := &recordingLanguageModel{reply: "맥락 확인"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	event := testInboundEvent("message-1")
	event.Context.HasMoreBefore = true
	event.Context.HistoryCursor = "cursor-1"

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}

	if len(adapter.historyCursors) != 1 || adapter.historyCursors[0] != "cursor-1" {
		t.Fatalf("expected initial history fetch, got %+v", adapter.historyCursors)
	}
	if !structuredMessagesContain(languageModel.request.Messages, "admin: older message") {
		t.Fatalf("expected fetched visible context in model messages, got %+v", languageModel.request.Messages)
	}
}

func TestConnectorRuntimeRunsAgentHistoryToolAndSendsOneFinishMessage(t *testing.T) {
	languageModel := agenttest.NewActionScriptedLanguageModel(
		`{"action":"continue","toolName":"conversation.history","toolInput":{"limit":20}}`,
		connectorFinishMessageWithEvidence("이전 대화를 확인했습니다", "obs-001", "conversation.history", 0),
	)
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	event := testInboundEvent("message-1")
	event.Context.HasMoreBefore = true
	event.Context.HistoryCursor = "cursor-1"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}

	if result.TaskRunID == "" {
		t.Fatal("expected task run id")
	}
	if len(adapter.historyCursors) != 2 || adapter.historyCursors[0] != "cursor-1" || adapter.historyCursors[1] != "cursor-1" {
		t.Fatalf("expected history fetch with cursor, got %+v", adapter.historyCursors)
	}
	if len(adapter.sentReplies) != 1 {
		t.Fatalf("expected one final reply, got %d", len(adapter.sentReplies))
	}
	if adapter.sentReplies[0].message != "이전 대화를 확인했습니다" {
		t.Fatalf("expected final reply, got %q", adapter.sentReplies[0].message)
	}
}

func TestConnectorRuntimeCreatesScheduledTaskFromNaturalLanguagePrompt(t *testing.T) {
	languageModel := agenttest.NewActionScriptedLanguageModel(
		`{"action":"request_tools","toolNames":["schedule.create"],"skillNames":["scheduled-task"],"executionStateUpdate":{}}`,
		`{"action":"continue","toolName":"schedule.create","toolInput":{"name":"daily research brief","taskInstruction":"업계 뉴스를 조사해서 핵심만 보고해줘.","kind":"cron","cronExpression":"0 7 * * *","repeatPolicy":"unbounded","timeZone":"Asia/Seoul","platform":"spoofed","conversationID":"spoofed","replyTargetID":"spoofed"},"executionStateUpdate":{},"nextStepPlan":{"objective":"confirm schedule creation","expectedTools":[],"doneCriteria":["schedule is created"],"risk":"","workingSetReason":"schedule.create returns the created schedule"}}`,
		connectorFinishMessage("매일 아침 7시에 조사해서 알려드릴게요."),
	)
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	useTestConnectorSkill(connectorRuntime, connectorScheduledTaskSkill())
	repository := &connectorTaskScheduleRepository{}
	connectorRuntime.UseTaskScheduleRepository(repository)
	event := testInboundEvent("message-1")
	event.Prompt = "매일 업계 뉴스를 조사해서 아침 7시에 알려줘."

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil {
		t.Fatalf("expected schedule request to process: %v", errorValue)
	}
	if result.TaskRunID == "" {
		t.Fatal("expected task run id")
	}
	if len(repository.taskSchedules) != 1 {
		t.Fatalf("expected one task schedule, got %+v", repository.taskSchedules)
	}
	taskSchedule := repository.taskSchedules[0]
	if taskSchedule.Prompt != "업계 뉴스를 조사해서 핵심만 보고해줘." {
		t.Fatalf("expected stored task instruction without cadence, got %q", taskSchedule.Prompt)
	}
	if taskSchedule.CronExpression != "0 7 * * *" || taskSchedule.TimeZone != "Asia/Seoul" {
		t.Fatalf("expected cron schedule in Asia/Seoul, got %+v", taskSchedule)
	}
	if taskSchedule.Platform != event.Platform || taskSchedule.ConversationID != event.ConversationID || taskSchedule.ReplyTargetID != event.ReplyTargetID {
		t.Fatalf("expected connector context delivery target, got %+v", taskSchedule)
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "예약을 만들었습니다." {
		t.Fatalf("expected confirmation reply, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeClassifiesConfirmationReplyBeforeResumingPendingTask(t *testing.T) {
	invokedTools := []string{}
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"blueclaw_task_intake_effort": {
				`{"classification":"bounded_task","taskShape":"approval_gated_task","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"calendar delete needs approval first","userFacingReply":"","workKinds":["calendar","destructive_action"]}`,
				`{"classification":"bounded_task","taskShape":"maintenance_task","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"approved calendar tool work","userFacingReply":"","workKinds":["calendar"]}`,
			},
			"blueclaw_execution_plan": {
				`{"originalInstruction":"내일 휴가 일정을 캘린더에서 삭제해줘","summary":"내일 휴가 일정을 삭제합니다.","targets":["calendar event"],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":false,"thirdPartyExternalSend":false,"repeated":false,"highFrequency":false,"destructive":true,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":[],"continuationInstruction":"내일 휴가 일정을 캘린더에서 삭제합니다. 이미 사용자가 확인했습니다."}`,
			},
			"blueclaw_confirmation_message": {
				`{"reply":"내일 휴가 일정을 캘린더에서 삭제하는 것으로 이해했습니다. 승인하면 바로 진행하겠습니다."}`,
			},
			"blueclaw_confirmation_reply_decision": {
				`{"decision":"approved","reason":"user explicitly confirms the pending calendar deletion."}`,
			},
		},
		ActionResponses: []string{
			`{"action":"continue","toolName":"calendar.event.delete","toolInput":{"eventID":"event-1","userConfirmed":true}}`,
			connectorFinishMessageWithEvidence("내일 휴가 일정을 캘린더에서 삭제했습니다.", "obs-001", "calendar.event.delete", 0),
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntime.agentKernel.UseIntakeLanguageModelProvider(languageModel)
	connectorRuntime.agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true})
	useTestConnectorSkill(connectorRuntime, connectorCalendarSkill())
	connectorRuntime.UseAllowedToolNames([]string{"conversation.history", "memory.search", "ask.confirm", "calendar.event.add", "calendar.event.delete"})
	connectorRuntime.UseCapabilityTools(capability.Client{
		Endpoint: "http://capability.test",
		HTTPClient: testHTTPDoer(func(request *http.Request) (*http.Response, error) {
			invokedTools = append(invokedTools, strings.TrimPrefix(request.URL.Path, "/v1/tools/"))
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":"ok","content":"calendar event deleted","result":{"eventID":"event-1"}}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}),
	}, []string{"calendar.event.add", "calendar.event.delete"})
	firstEvent := testInboundEvent("message-1")
	firstEvent.Prompt = "내일 휴가 일정을 캘린더에서 삭제해줘"

	firstResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, firstEvent)
	if errorValue != nil {
		t.Fatalf("expected first event to process: %v", errorValue)
	}
	if firstResult.TaskRunID == "" {
		t.Fatal("expected first task run id")
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "내일 휴가 일정을 캘린더에서 삭제하는 것으로 이해했습니다. 승인하면 바로 진행하겠습니다." {
		t.Fatalf("expected confirmation reply, got %+v", adapter.sentReplies)
	}

	secondEvent := testInboundEvent("message-2")
	secondEvent.Prompt = "응 맞아 삭제해"
	secondResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, secondEvent)
	if errorValue != nil {
		t.Fatalf("expected approval reply to process: %v", errorValue)
	}

	if secondResult.TaskRunID != firstResult.TaskRunID || secondResult.TaskRunID == "" {
		t.Fatalf("expected approved continuation to reuse task, got first=%q second=%q", firstResult.TaskRunID, secondResult.TaskRunID)
	}
	requests := languageModel.Requests()
	approvalRouterIndex := connectorSchemaIndexAfter(requests, "blueclaw_turn_router", 1)
	if approvalRouterIndex < 0 {
		t.Fatalf("expected approval classification before continuation turn, got requests: %+v", connectorRequestSchemaNames(requests))
	}
	actionIndex := connectorSchemaIndexAfter(requests, "blueclaw_agent_turn_action", approvalRouterIndex)
	if actionIndex < 0 {
		t.Fatalf("expected continuation action after approval classification, got requests: %+v", connectorRequestSchemaNames(requests))
	}
	if !structuredMessagesContain(requests[actionIndex].Messages, "The user approved the pending action") {
		t.Fatalf("expected active goal context to carry approval context, got %+v", requests[actionIndex].Messages)
	}
	if len(invokedTools) != 1 || invokedTools[0] != "calendar.event.delete/invoke" {
		t.Fatalf("expected calendar delete tool invocation, got %+v", invokedTools)
	}
	if len(adapter.sentReplies) != 2 || adapter.sentReplies[1].message != "내일 휴가 일정을 캘린더에서 삭제했습니다." {
		t.Fatalf("expected final approved reply, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeHandlesDeterministicConfirmationReplyBeforeRouter(t *testing.T) {
	invokedTools := []string{}
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"blueclaw_task_intake_effort": {
				`{"classification":"bounded_task","taskShape":"approval_gated_task","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"calendar delete needs approval first","userFacingReply":"","workKinds":["calendar","destructive_action"]}`,
			},
			"blueclaw_execution_plan": {
				`{"originalInstruction":"내일 휴가 일정을 캘린더에서 삭제해줘","summary":"내일 휴가 일정을 삭제합니다.","targets":["calendar event"],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":false,"thirdPartyExternalSend":false,"repeated":false,"highFrequency":false,"destructive":true,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":[],"continuationInstruction":"내일 휴가 일정을 캘린더에서 삭제합니다. 이미 사용자가 확인했습니다."}`,
			},
			"blueclaw_confirmation_message": {
				`{"reply":"내일 휴가 일정을 캘린더에서 삭제하는 것으로 이해했습니다. 승인하면 바로 진행하겠습니다."}`,
			},
		},
		ActionResponses: []string{
			`{"action":"continue","toolName":"calendar.event.delete","toolInput":{"eventID":"event-1","userConfirmed":true}}`,
			connectorFinishMessageWithEvidence("내일 휴가 일정을 캘린더에서 삭제했습니다.", "obs-001", "calendar.event.delete", 0),
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntime.agentKernel.UseIntakeLanguageModelProvider(languageModel)
	connectorRuntime.agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true})
	useTestConnectorSkill(connectorRuntime, connectorCalendarSkill())
	connectorRuntime.UseAllowedToolNames([]string{"conversation.history", "memory.search", "ask.confirm", "calendar.event.add", "calendar.event.delete"})
	connectorRuntime.UseCapabilityTools(capability.Client{
		Endpoint: "http://capability.test",
		HTTPClient: testHTTPDoer(func(request *http.Request) (*http.Response, error) {
			invokedTools = append(invokedTools, strings.TrimPrefix(request.URL.Path, "/v1/tools/"))
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":"ok","content":"calendar event deleted","result":{"eventID":"event-1"}}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}),
	}, []string{"calendar.event.add", "calendar.event.delete"})

	firstEvent := testInboundEvent("message-1")
	firstEvent.Prompt = "내일 휴가 일정을 캘린더에서 삭제해줘"
	firstResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, firstEvent)
	if errorValue != nil {
		t.Fatalf("expected first event to process: %v", errorValue)
	}

	secondEvent := testInboundEvent("message-2")
	secondEvent.Prompt = "확인"
	secondResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, secondEvent)
	if errorValue != nil {
		t.Fatalf("expected deterministic approval reply to process: %v", errorValue)
	}

	if secondResult.TaskRunID != firstResult.TaskRunID || secondResult.TaskRunID == "" {
		t.Fatalf("expected approved continuation to reuse task, got first=%q second=%q", firstResult.TaskRunID, secondResult.TaskRunID)
	}
	if connectorContainsSchemaName(languageModel.Requests(), "blueclaw_confirmation_reply_decision") {
		t.Fatalf("deterministic confirmation reply must not call confirmation router, got schemas=%+v", connectorRequestSchemaNames(languageModel.Requests()))
	}
	if !connectorTaskEventsContain(connectorRuntime, firstResult.TaskRunID, "confirmation.reply_classified", "deterministic_confirm") {
		t.Fatal("expected deterministic confirmation classification event")
	}
	if len(invokedTools) != 1 || invokedTools[0] != "calendar.event.delete/invoke" {
		t.Fatalf("expected calendar delete tool invocation, got %+v", invokedTools)
	}
}

func TestConnectorRuntimeAnswersPendingConfirmationQuestionWithoutLaunching(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"blueclaw_task_intake_effort": {
				`{"classification":"bounded_task","taskShape":"approval_gated_task","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"calendar delete needs approval first","userFacingReply":"","workKinds":["calendar","destructive_action"]}`,
			},
			"blueclaw_execution_plan": {
				`{"originalInstruction":"내일 휴가 일정을 캘린더에서 삭제해줘","summary":"내일 휴가 일정을 삭제합니다.","targets":["calendar event"],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":false,"thirdPartyExternalSend":false,"repeated":false,"highFrequency":false,"destructive":true,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":[],"continuationInstruction":"내일 휴가 일정을 캘린더에서 삭제합니다."}`,
			},
			"blueclaw_confirmation_message": {
				`{"reply":"내일 휴가 일정을 캘린더에서 삭제하는 것으로 이해했습니다. 승인하면 바로 진행하겠습니다."}`,
			},
			"blueclaw_confirmation_reply_decision": {
				`{"decision":"question","reason":"user asked a follow-up instead of approving"}`,
			},
			"blueclaw_reply": {
				`{"reply":"요청하신 작업은 취소했습니다."}`,
			},
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntime.agentKernel.UseIntakeLanguageModelProvider(languageModel)
	connectorRuntime.agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true})
	useTestConnectorSkill(connectorRuntime, connectorCalendarSkill())
	connectorRuntime.UseAllowedToolNames([]string{"conversation.history", "memory.search", "ask.confirm", "calendar.event.delete"})

	firstEvent := testInboundEvent("message-1")
	firstEvent.Prompt = "내일 휴가 일정을 캘린더에서 삭제해줘"
	firstResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, firstEvent)
	if errorValue != nil {
		t.Fatalf("expected first event to process: %v", errorValue)
	}
	if firstResult.TaskRunID == "" || len(adapter.sentReplies) != 1 {
		t.Fatalf("expected confirmation request, result=%+v replies=%+v", firstResult, adapter.sentReplies)
	}

	secondEvent := testInboundEvent("message-2")
	secondEvent.Prompt = "왜 승인이 필요해?"
	secondResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, secondEvent)
	if errorValue != nil {
		t.Fatalf("expected pending confirmation reply to process: %v", errorValue)
	}
	if secondResult.TaskRunID != firstResult.TaskRunID || secondResult.Reason != "confirmation_question" {
		t.Fatalf("expected pending confirmation question to be answered after cancelling pending action, got %+v", secondResult)
	}
	if len(adapter.resolutions) != 1 || adapter.resolutions[0].DispatchID != "dispatch-1" {
		t.Fatalf("expected pending confirmation attachment to resolve, got %+v", adapter.resolutions)
	}
	if connectorContainsSchemaName(languageModel.Requests(), "blueclaw_agent_turn_action") {
		t.Fatalf("non-approval confirmation reply must not launch a new agent turn, got schemas=%+v", connectorRequestSchemaNames(languageModel.Requests()))
	}
}

func TestConnectorRuntimeRoutesPendingConfirmationRevisionAsNewTask(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"blueclaw_turn_router": {
				`{"route":"start_task","classification":"bounded_task","taskShape":"approval_gated_task","taskComplexity":"normal","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"calendar delete needs approval first","userFacingReply":"","workKinds":["calendar","destructive_action"]}`,
				`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","taskComplexity":"normal","effortLevel":"standard","requestedOutputFormats":null,"expectedResults":[{"id":"final-message","type":"message","description":"삭제 대상 정정 요청 처리 결과","required":true}],"responseLanguage":"ko","reason":"user replaced the pending confirmation with a different message deletion target","userFacingReply":"","approval":"unclear","workKinds":["destructive_action"]}`,
				`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","taskComplexity":"normal","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"corrected deletion target runs as new bounded work","userFacingReply":"","workKinds":["destructive_action"]}`,
			},
			"blueclaw_execution_plan": {
				`{"originalInstruction":"내일 휴가 일정을 캘린더에서 삭제해줘","summary":"내일 휴가 일정을 삭제합니다.","targets":["calendar event"],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":false,"thirdPartyExternalSend":false,"repeated":false,"highFrequency":false,"destructive":true,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":[],"continuationInstruction":"내일 휴가 일정을 캘린더에서 삭제합니다."}`,
				`{"originalInstruction":"가사랍시고 보낸 메시지를 삭제해줘","summary":"정정된 삭제 대상을 처리합니다.","targets":["platform message"],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":false,"thirdPartyExternalSend":false,"repeated":false,"highFrequency":false,"destructive":false,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":[],"continuationInstruction":"정정된 삭제 대상을 처리합니다."}`,
			},
			"blueclaw_confirmation_message": {
				`{"reply":"내일 휴가 일정을 캘린더에서 삭제하는 것으로 이해했습니다. 승인하면 바로 진행하겠습니다."}`,
			},
		},
		ActionResponses: []string{
			connectorFinishMessage("정정한 삭제 요청으로 새로 처리했습니다."),
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntime.agentKernel.UseIntakeLanguageModelProvider(languageModel)
	connectorRuntime.agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true})
	useTestConnectorSkill(connectorRuntime, connectorCalendarSkill())
	connectorRuntime.UseAllowedToolNames([]string{"conversation.history", "memory.search", "ask.confirm", "calendar.event.delete", "platform.message.search", "platform.message.delete"})

	firstEvent := testInboundEvent("message-1")
	firstEvent.Prompt = "내일 휴가 일정을 캘린더에서 삭제해줘"
	firstResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, firstEvent)
	if errorValue != nil {
		t.Fatalf("expected first event to process: %v", errorValue)
	}
	if firstResult.TaskRunID == "" || len(adapter.sentReplies) != 1 {
		t.Fatalf("expected confirmation request, result=%+v replies=%+v", firstResult, adapter.sentReplies)
	}

	secondEvent := testInboundEvent("message-2")
	secondEvent.Prompt = "아니 가사랍시고 보낸 것들 말야"
	secondResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, secondEvent)
	if errorValue != nil {
		t.Fatalf("expected pending confirmation revision to process: %v", errorValue)
	}
	if secondResult.TaskRunID == "" || secondResult.TaskRunID == firstResult.TaskRunID {
		t.Fatalf("expected corrected request to launch a replacement task, got first=%q second=%q result=%+v", firstResult.TaskRunID, secondResult.TaskRunID, secondResult)
	}
	if len(adapter.resolutions) != 1 || adapter.resolutions[0].DispatchID != "dispatch-1" {
		t.Fatalf("expected pending confirmation attachment to resolve, got %+v", adapter.resolutions)
	}
	if !connectorContainsSchemaName(languageModel.Requests(), "blueclaw_agent_turn_action") {
		t.Fatalf("expected corrected request to launch agent turn, got schemas=%+v", connectorRequestSchemaNames(languageModel.Requests()))
	}
	if len(adapter.sentReplies) != 2 || adapter.sentReplies[1].message != "정정한 삭제 요청으로 새로 처리했습니다." {
		t.Fatalf("expected replacement task final reply only, got %+v", adapter.sentReplies)
	}
}

func TestAskReplyConsumesChoiceRevision(t *testing.T) {
	interaction := AskInteraction{
		Kind: "ask_choice_single",
		Options: []AskChoiceOption{
			{Key: "one", Label: "선택지 1"},
			{Key: "two", Label: "선택지 2"},
		},
	}
	event := testInboundEvent("message-2")
	event.Prompt = "아니 새로 이걸 해줘"
	decision := agent.TurnDecision{
		Route:          agent.TurnRouteStartTask,
		Classification: agent.IntakeClassificationBoundedTask,
		TaskShape:      agent.TaskShapeMaintenanceTask,
		Choices:        nil,
	}

	if !askReplyConsumesInteraction(interaction, "선택지를 골라주세요", event, decision, true) {
		t.Fatal("expected non-choice replacement request to resolve the pending choice")
	}
}

func TestConnectorRuntimeConsumesInteractiveConfirmationCancel(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"blueclaw_task_intake_effort": {
				`{"classification":"bounded_task","taskShape":"approval_gated_task","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"calendar delete needs approval first","userFacingReply":"","workKinds":["calendar","destructive_action"]}`,
			},
			"blueclaw_execution_plan": {
				`{"originalInstruction":"내일 휴가 일정을 캘린더에서 삭제해줘","summary":"내일 휴가 일정을 삭제합니다.","targets":["calendar event"],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":false,"thirdPartyExternalSend":false,"repeated":false,"highFrequency":false,"destructive":true,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":[],"continuationInstruction":"내일 휴가 일정을 캘린더에서 삭제합니다."}`,
			},
			"blueclaw_confirmation_message": {
				`{"reply":"내일 휴가 일정을 캘린더에서 삭제하는 것으로 이해했습니다. 승인하면 바로 진행하겠습니다."}`,
			},
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntime.agentKernel.UseIntakeLanguageModelProvider(languageModel)
	connectorRuntime.agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true})
	useTestConnectorSkill(connectorRuntime, connectorCalendarSkill())
	connectorRuntime.UseAllowedToolNames([]string{"conversation.history", "memory.search", "ask.confirm", "calendar.event.delete"})

	firstEvent := testInboundEvent("message-1")
	firstEvent.Prompt = "내일 휴가 일정을 캘린더에서 삭제해줘"
	firstResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, firstEvent)
	if errorValue != nil {
		t.Fatalf("expected first event to process: %v", errorValue)
	}
	if firstResult.TaskRunID == "" || len(adapter.sentReplies) != 1 {
		t.Fatalf("expected confirmation request, result=%+v replies=%+v", firstResult, adapter.sentReplies)
	}

	secondEvent := testInboundEvent("message-2")
	secondEvent.Prompt = "rejected"
	secondEvent.LegacyFields = map[string]interface{}{"askAction": "cancel", "postID": "ask-post-1"}
	secondResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, secondEvent)
	if errorValue != nil {
		t.Fatalf("expected interactive cancel to process: %v", errorValue)
	}
	if secondResult.TaskRunID != firstResult.TaskRunID || secondResult.Reason != "confirmation_rejected" {
		t.Fatalf("expected pending confirmation to be rejected, got %+v", secondResult)
	}
	if len(adapter.resolutions) != 1 || adapter.resolutions[0].DispatchID != "ask-post-1" {
		t.Fatalf("expected ask message to resolve, got %+v", adapter.resolutions)
	}
	if connectorContainsSchemaName(languageModel.Requests(), "blueclaw_agent_turn_action") {
		t.Fatalf("interactive cancel must not launch agent with rejected prompt, got schemas=%+v", connectorRequestSchemaNames(languageModel.Requests()))
	}
}

func TestConnectorRuntimeConsumesBareConfirmationReplyWithoutPendingTask(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"blueclaw_turn_router": {
				`{"route":"consume","classification":"quick_reply","taskShape":"immediate_reply","effortLevel":"quick","requestedOutputFormats":null,"responseLanguage":"ko","reason":"orphan approval acknowledgement","userFacingReply":"","reactionEmojiName":"ok_hand"}`,
			},
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntime.agentKernel.UseIntakeLanguageModelProvider(languageModel)
	connectorRuntime.agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true})

	event := testInboundEvent("message-approved")
	event.Prompt = "approved"
	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected orphan approval reply to process: %v", errorValue)
	}
	if result.Reason != "consume_reacted" {
		t.Fatalf("expected orphan approval to be consumed via router, got %+v", result)
	}
	if !connectorContainsSchemaName(languageModel.Requests(), "blueclaw_turn_router") {
		t.Fatalf("expected router classification, got schemas=%+v", connectorRequestSchemaNames(languageModel.Requests()))
	}
	if connectorContainsSchemaName(languageModel.Requests(), "blueclaw_agent_turn_action") {
		t.Fatalf("orphan approval must not launch an agent turn, got schemas=%+v", connectorRequestSchemaNames(languageModel.Requests()))
	}
	if len(adapter.sentReplies) != 0 {
		t.Fatalf("orphan approval must not send a generic reply, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeContinuesWaitingUserInputGoal(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"blueclaw_task_intake_effort": {
				`{"classification":"bounded_task","taskShape":"approval_gated_task","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"business plan needs enough detail","userFacingReply":"","workKinds":["external_send"]}`,
				`{"classification":"bounded_task","taskShape":"research_task","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"continue active goal","userFacingReply":""}`,
			},
			"blueclaw_execution_plan": {
				`{"originalInstruction":"동하에게 DM 보내줘","summary":"동하에게 DM을 보냅니다.","targets":["동하"],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":true,"thirdPartyExternalSend":true,"repeated":false,"highFrequency":false,"destructive":false,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":["보낼 메시지"],"continuationInstruction":"동하에게 DM을 보냅니다."}`,
			},
			"blueclaw_confirmation_message": {
				`{"reply":"핵심 사업 내용을 알려주시면 더 정확히 작성하겠습니다."}`,
			},
		},
		ActionResponses: []string{
			`{"action":"continue","toolName":"platform.message.send","toolInput":{"deliveryTarget":{"type":"directMessage","personHint":"동하"},"message":"우선 진행합니다."}}`,
			connectorFinishMessageWithEvidence("동하에게 DM을 보냈습니다.", "obs-001", "platform.message.send", 0),
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntime.agentKernel.UseIntakeLanguageModelProvider(languageModel)
	connectorRuntime.agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true})
	useTestConnectorSkill(connectorRuntime, agent.SkillInstruction{
		Name:        "direct-message",
		Description: "사업계획서 작성과 메시지 전송 후보.",
		Prompt:      "Use platform.message.send only for explicit DM delivery.",
		Completion: agent.SkillCompletion{
			RequiredEvidenceTools: []string{"platform.message.send"},
		},
		AllowedTools: []string{"platform.message.send"},
		Source:       agent.InstructionSource{Path: "skills/direct-message/SKILL.md", SkillName: "direct-message"},
	})
	connectorRuntime.UseAllowedToolNames([]string{"conversation.history", "memory.search", "ask.confirm", "platform.message.send"})
	connectorRuntime.UseCapabilityTools(capability.Client{
		Endpoint: "http://capability.test",
		HTTPClient: testHTTPDoer(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":"ok","content":"sent","result":{"messageID":"dm-1"}}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}),
	}, []string{"platform.message.send"})
	firstEvent := testInboundEvent("message-1")
	firstEvent.Prompt = "동하에게 DM 보내줘"

	firstResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, firstEvent)
	if errorValue != nil {
		t.Fatalf("expected first event to process: %v", errorValue)
	}
	if firstResult.TaskRunID == "" || len(adapter.sentReplies) != 1 {
		t.Fatalf("expected waiting goal reply, result=%+v replies=%+v", firstResult, adapter.sentReplies)
	}

	secondEvent := testInboundEvent("message-2")
	secondEvent.Prompt = "우선 진행해"
	secondResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, secondEvent)
	if errorValue != nil {
		t.Fatalf("expected continuation to process: %v", errorValue)
	}
	if secondResult.TaskRunID != firstResult.TaskRunID {
		t.Fatalf("expected continuation to reuse waiting goal task, first=%q second=%q", firstResult.TaskRunID, secondResult.TaskRunID)
	}
	actionRequest, isFound := connectorFirstRequestBySchema(languageModel.Requests(), "blueclaw_agent_turn_action")
	if !isFound {
		t.Fatalf("expected action request, got schemas=%+v", connectorRequestSchemaNames(languageModel.Requests()))
	}
	if !structuredMessagesContain(actionRequest.Messages, "동하에게 DM 보내줘") {
		t.Fatalf("expected active goal original instruction in action context, got %+v", actionRequest.Messages)
	}
	if userMessageIndex(actionRequest.Messages, "우선 진행해") < 0 {
		t.Fatalf("expected latest user message to stay intact, got %+v", actionRequest.Messages)
	}
}

func TestConnectorRuntimeStartsNewTaskForClearNewRequest(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"blueclaw_task_intake_effort": {
				`{"classification":"bounded_task","taskShape":"approval_gated_task","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"dm needs message","userFacingReply":"","workKinds":["external_send"]}`,
				`{"classification":"bounded_task","taskShape":"calendar_task","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","route":"start_task","reason":"new calendar request","userFacingReply":"","workKinds":["calendar"]}`,
			},
			"blueclaw_execution_plan": {
				`{"originalInstruction":"동하에게 DM 보내줘","summary":"동하에게 DM을 보냅니다.","targets":["동하"],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":true,"thirdPartyExternalSend":true,"repeated":false,"highFrequency":false,"destructive":false,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":["보낼 메시지"],"continuationInstruction":"동하에게 DM을 보냅니다."}`,
			},
			"blueclaw_confirmation_message": {
				`{"reply":"보낼 메시지를 알려주세요."}`,
			},
		},
		ActionResponses: []string{
			connectorFinishMessage("캘린더 요청을 처리했습니다."),
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntime.agentKernel.UseIntakeLanguageModelProvider(languageModel)
	connectorRuntime.agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true})
	useTestConnectorSkill(connectorRuntime, agent.SkillInstruction{
		Name:         "direct-message",
		Description:  "DM 후보.",
		Completion:   agent.SkillCompletion{RequiredEvidenceTools: []string{"platform.message.send"}},
		AllowedTools: []string{"platform.message.send"},
	})

	firstEvent := testInboundEvent("message-1")
	firstEvent.Prompt = "동하에게 DM 보내줘"
	firstResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, firstEvent)
	if errorValue != nil {
		t.Fatalf("expected first event to process: %v", errorValue)
	}

	secondEvent := testInboundEvent("message-2")
	secondEvent.Prompt = "내일 휴가 일정 캘린더에 추가해줘"
	secondResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, secondEvent)
	if errorValue != nil {
		t.Fatalf("expected new request to process: %v", errorValue)
	}
	if secondResult.TaskRunID == "" || secondResult.TaskRunID == firstResult.TaskRunID {
		t.Fatalf("expected clear new request to start a new task, first=%q second=%q", firstResult.TaskRunID, secondResult.TaskRunID)
	}
	actionRequest, isFound := connectorFirstRequestBySchema(languageModel.Requests(), "blueclaw_agent_turn_action")
	if !isFound {
		t.Fatalf("expected action request, got schemas=%+v", connectorRequestSchemaNames(languageModel.Requests()))
	}
	if structuredMessagesContain(actionRequest.Messages, "동하에게 DM 보내줘") {
		t.Fatalf("expected new request not to inherit previous goal, got %+v", actionRequest.Messages)
	}
}

func TestConnectorRuntimeAddsCalendarEventWithoutApproval(t *testing.T) {
	invokedTools := []string{}
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{"blueclaw_task_intake_effort": {
			`{"classification":"bounded_task","taskShape":"maintenance_task","effortLevel":"standard","requestedOutputFormats":null,"responseLanguage":"ko","reason":"calendar add is non-destructive tool work","userFacingReply":"","workKinds":["calendar"]}`,
		}},
		ActionResponses: []string{
			`{"action":"continue","toolName":"calendar.event.add","toolInput":{"title":"휴가","startISO":"2026-05-09","endISO":"2026-05-10","isAllDay":true}}`,
			connectorFinishMessageWithEvidence("내일 휴가 일정을 캘린더에 추가했습니다.", "obs-001", "calendar.event.add", 0),
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntime.agentKernel.UseIntakeLanguageModelProvider(languageModel)
	connectorRuntime.agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true})
	useTestConnectorSkill(connectorRuntime, connectorCalendarSkill())
	connectorRuntime.UseAllowedToolNames([]string{"conversation.history", "memory.search", "ask.confirm", "calendar.event.add", "calendar.event.delete"})
	connectorRuntime.UseCapabilityTools(capability.Client{
		Endpoint: "http://capability.test",
		HTTPClient: testHTTPDoer(func(request *http.Request) (*http.Response, error) {
			invokedTools = append(invokedTools, strings.TrimPrefix(request.URL.Path, "/v1/tools/"))
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":"ok","content":"calendar event created","result":{"eventID":"event-1"}}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}),
	}, []string{"calendar.event.add", "calendar.event.delete"})
	event := testInboundEvent("message-1")
	event.Prompt = "나 내일 휴가라고 달력에 추가해줘"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected calendar add to process: %v", errorValue)
	}
	if result.TaskRunID == "" {
		t.Fatal("expected task run id")
	}
	requests := languageModel.Requests()
	if connectorContainsSchemaName(requests, "blueclaw_confirmation_reply_decision") {
		t.Fatalf("expected no approval continuation classification, got %+v", connectorRequestSchemaNames(requests))
	}
	if len(invokedTools) != 1 || invokedTools[0] != "calendar.event.add/invoke" {
		t.Fatalf("expected direct calendar add invocation, got %+v", invokedTools)
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "내일 휴가 일정을 캘린더에 추가했습니다." {
		t.Fatalf("expected final add reply, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeReadsTypedCapabilityToolResponse(t *testing.T) {
	languageModel := agenttest.NewActionScriptedLanguageModel(
		`{"action":"request_tools","toolNames":["browser.snapshot"],"skillNames":["browser-snapshot"],"executionStateUpdate":{"goal":"open browser and observe","workspace":"","knownFacts":[],"triedAndFailed":[],"currentBlocker":"","nextPlan":"observe the current browser"}}`,
		`{"action":"continue","toolName":"browser.snapshot","toolInput":{},"nextStepPlan":{"objective":"observe the current browser","expectedTools":[],"expectedNextResults":["browser snapshot is available"],"doneCriteria":["snapshot result is available"],"risk":"browser may be unavailable","workingSetReason":"browser.snapshot was explicitly required"}}`,
		connectorFinishMessageWithEvidence("브라우저를 확인했습니다", "obs-002", "browser.snapshot", 0),
	)
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	useTestConnectorSkill(connectorRuntime, connectorBrowserSnapshotSkill())
	connectorRuntime.UseAllowedToolNames([]string{"conversation.history", "memory.search", "browser.snapshot"})
	connectorRuntime.UseCapabilityTools(capability.Client{
		Endpoint: "http://capability.test",
		HTTPClient: testHTTPDoer(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != "/v1/tools/browser.snapshot/invoke" {
				t.Fatalf("unexpected capability path: %s", request.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"provider":"device","selectedBackend":"device_local","toolName":"browser.snapshot","status":"ok","result":{"url":"https://example.com","snapshotText":"Example","devicePath":"/tmp/internkim-companion-files/screen.png","filename":"screen.png","contentType":"image/png","sizeBytes":123}}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}),
	}, []string{"browser.snapshot"})

	event := testInboundEvent("message-1")
	event.Prompt = "open browser and observe"
	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected capability tool event to process: %v", errorValue)
	}

	requests := languageModel.Requests()
	if !structuredRequestsContainMessage(requests, "https://example.com") {
		t.Fatalf("expected typed capability result to be available as tool observation, got %+v", requests)
	}
	if adapter.sentReplies[0].message != "브라우저를 확인했습니다" {
		t.Fatalf("expected final reply, got %q", adapter.sentReplies[0].message)
	}
	if len(adapter.sentReplies[0].attachments) != 0 {
		t.Fatalf("expected observation attachment not to be delivered, got %+v", adapter.sentReplies[0].attachments)
	}
}

func structuredRequestsContainMessage(requests []llm.StructuredResponseRequest, text string) bool {
	for _, request := range requests {
		if structuredMessagesContain(request.Messages, text) {
			return true
		}
	}
	return false
}

func TestConnectorRuntimeExposesAllowedMcpSchemaCatalog(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "ok"})
	connectorRuntime.UseAllowedToolNames([]string{"allowed.tool"})
	mcpRegistry := mcp.NewMcpRegistry()
	inputSchema := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`)
	mcpRegistry.LoadServerDefinition([]config.MCPServerConfiguration{
		{
			Name: "workspace-mcp",
			Tools: []config.MCPToolConfiguration{
				{Name: "allowed.tool", Description: "Allowed MCP tool", InputSchema: inputSchema},
				{Name: "blocked.tool", Description: "Blocked MCP tool", InputSchema: inputSchema},
			},
		},
	})
	connectorRuntime.UseMCPRegistry(mcpRegistry)

	toolRegistry := connectorRuntime.buildTurnToolSet(adapter, testInboundEvent("message-1"), "person-1", policy.PersonAccess{})
	allowedToolDefinition, isFound := findAgentToolDefinition(toolRegistry.ListToolDefinitions(), "allowed.tool")
	if !isFound {
		t.Fatalf("expected allowed MCP tool definition, got %+v", toolRegistry.ListToolDefinitions())
	}
	if allowedToolDefinition.Description != "Allowed MCP tool" {
		t.Fatalf("expected MCP description, got %q", allowedToolDefinition.Description)
	}
	if string(allowedToolDefinition.InputSchema) != string(inputSchema) {
		t.Fatalf("expected MCP input schema, got %s", string(allowedToolDefinition.InputSchema))
	}
	if _, isFound := findAgentToolDefinition(toolRegistry.ListToolDefinitions(), "blocked.tool"); isFound {
		t.Fatalf("expected blocked MCP tool to be hidden, got %+v", toolRegistry.ListToolDefinitions())
	}

	toolResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{ToolName: "blocked.tool", Input: json.RawMessage(`{}`)})
	if errorValue != nil {
		t.Fatalf("expected policy denial as tool result: %v", errorValue)
	}
	if !toolResult.Failed() || toolResult.ContentText() != "tool is not allowed" {
		t.Fatalf("expected blocked MCP invocation to be denied, got %+v", toolResult)
	}
}

func TestConnectorRuntimeDetachesHTTPEventFromCanceledRequestContext(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "ok"})
	request, errorValue := http.NewRequest(http.MethodPost, "/connectors/test/events", strings.NewReader(`{}`))
	if errorValue != nil {
		t.Fatalf("expected request: %v", errorValue)
	}
	ctx, cancel := context.WithCancel(request.Context())
	cancel()
	request = request.WithContext(ctx)
	adapter.httpParseResult = HTTPParseResult{
		HasEvent: true,
		Event:    testInboundEvent("message-http"),
	}

	result, _, errorValue := connectorRuntime.HandleHTTPEvent(request.Context(), adapter.Name(), request)
	if errorValue != nil {
		t.Fatalf("expected detached http event to process: %v", errorValue)
	}
	if result.TaskRunID == "" {
		t.Fatalf("expected task run result, got %+v", result)
	}
}

func TestConnectorRuntimeQueuesHTTPEventAndSendsReplyThroughOutbox(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "queued reply"})
	repository := &testConnectorQueueRepository{}
	connectorRuntime.UseEventRepository(repository)
	event := testInboundEvent("message-http")
	adapter.httpParseResult = HTTPParseResult{HasEvent: true, Event: event}
	request, errorValue := http.NewRequest(http.MethodPost, "/connectors/test/events", strings.NewReader(`{}`))
	if errorValue != nil {
		t.Fatalf("expected request: %v", errorValue)
	}

	result, _, errorValue := connectorRuntime.HandleHTTPEvent(context.Background(), adapter.Name(), request)
	if errorValue != nil {
		t.Fatalf("expected http event to queue: %v", errorValue)
	}
	if result.Reason != "queued" {
		t.Fatalf("expected queued result, got %+v", result)
	}
	if len(adapter.sentReplies) != 0 {
		t.Fatalf("expected no synchronous reply, got %+v", adapter.sentReplies)
	}

	if !connectorRuntime.processNextQueuedConnectorEvent(context.Background()) {
		t.Fatal("expected queued connector event to process")
	}
	if len(repository.succeededEvents) != 1 {
		t.Fatalf("expected one succeeded event, got %+v", repository.succeededEvents)
	}
	if len(repository.pendingReplies) != 1 {
		t.Fatalf("expected one queued reply, got %+v", repository.pendingReplies)
	}
	taskRunID := repository.pendingReplies[0].Reply.TaskRunID
	if taskRunID == "" || repository.pendingReplies[0].Reply.ReplyKind != connectorReplyKindSuccess {
		t.Fatalf("expected queued reply delivery metadata, got %+v", repository.pendingReplies[0].Reply)
	}
	if !connectorTaskEventsContain(connectorRuntime, taskRunID, "connector.reply.enqueued", "success") {
		t.Fatal("expected queued reply enqueue event")
	}
	if len(adapter.sentReplies) != 0 {
		t.Fatalf("expected outbox to own reply send, got %+v", adapter.sentReplies)
	}

	if !connectorRuntime.processNextQueuedConnectorReply(context.Background()) {
		t.Fatal("expected queued connector reply to send")
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "queued reply" {
		t.Fatalf("expected outbox reply to send, got %+v", adapter.sentReplies)
	}
	if len(repository.sentReplies) != 1 || repository.sentReplies[0] != "dispatch-1" {
		t.Fatalf("expected dispatch id to be recorded, got %+v", repository.sentReplies)
	}
	if !connectorTaskEventsContain(connectorRuntime, taskRunID, "connector.reply.sent", "dispatch-1") {
		t.Fatal("expected queued reply sent event")
	}
}

func TestConnectorRuntimeRecordsQueuedOutboxSendFailure(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "queued reply"})
	repository := &testConnectorQueueRepository{}
	connectorRuntime.UseEventRepository(repository)
	adapter.sendReplyError = errors.New("mattermost send failed")
	event := testInboundEvent("message-http")
	adapter.httpParseResult = HTTPParseResult{HasEvent: true, Event: event}
	request, errorValue := http.NewRequest(http.MethodPost, "/connectors/test/events", strings.NewReader(`{}`))
	if errorValue != nil {
		t.Fatalf("expected request: %v", errorValue)
	}

	if _, _, errorValue := connectorRuntime.HandleHTTPEvent(context.Background(), adapter.Name(), request); errorValue != nil {
		t.Fatalf("expected http event to queue: %v", errorValue)
	}
	if !connectorRuntime.processNextQueuedConnectorEvent(context.Background()) {
		t.Fatal("expected queued connector event to process")
	}
	if len(repository.pendingReplies) != 1 {
		t.Fatalf("expected one queued reply, got %+v", repository.pendingReplies)
	}
	taskRunID := repository.pendingReplies[0].Reply.TaskRunID
	if !connectorRuntime.processNextQueuedConnectorReply(context.Background()) {
		t.Fatal("expected queued connector reply attempt")
	}

	if len(repository.failedReplies) != 1 || !strings.Contains(repository.failedReplies[0], "mattermost send failed") {
		t.Fatalf("expected failed reply to be recorded, got %+v", repository.failedReplies)
	}
	if !connectorTaskEventsContain(connectorRuntime, taskRunID, "connector.reply.failed", "mattermost send failed") {
		t.Fatal("expected queued reply failed event")
	}
}

func TestConnectorRuntimeSendsCheckpointReplyKind(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "ok"})
	event := testInboundEvent("message-checkpoint")
	replyTarget := ReplyTarget{ConversationID: event.ConversationID, ReplyTargetID: event.ReplyTargetID}

	errorValue := connectorRuntime.sendCheckpointReply(context.Background(), adapter.Name(), event, replyTarget, agent.AgentCheckpoint{
		TaskRunID: "task-1",
		Message:   "작업 중입니다.",
		ToolName:  "terminal.run",
	}, adapter.SendReply)
	if errorValue != nil {
		t.Fatalf("expected checkpoint reply to send: %v", errorValue)
	}
	if len(adapter.sentReplies) != 1 {
		t.Fatalf("expected one sent reply, got %+v", adapter.sentReplies)
	}
	reply := adapter.sentReplies[0]
	if reply.replyKind != connectorReplyKindCheckpoint || reply.taskRunID != "task-1" || reply.message != "작업 중입니다." {
		t.Fatalf("expected checkpoint reply kind and task run id, got %+v", reply)
	}
	if !connectorTaskEventsContain(connectorRuntime, "task-1", "connector.reply.sent", connectorReplyKindCheckpoint) {
		t.Fatal("expected checkpoint sent event")
	}
}

func TestConnectorProgressHeartbeatIntervalMaintainsTypingIndicator(t *testing.T) {
	if connectorProgressHeartbeatInterval > 5*time.Second {
		t.Fatalf("expected progress heartbeat to refresh before typing expires, got %s", connectorProgressHeartbeatInterval)
	}
}

func TestConnectorRuntimeDoesNotAutomaticallyIngestOrSearchMemory(t *testing.T) {
	languageModel := &recordingLanguageModel{reply: "ok"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	graphStore := &fakeGraphMemoryStore{
		facts: []memory.MemoryFact{
			{ScopeType: memory.ScopeTypeUser, NamespaceID: "user:person-1", Content: "사용자의 이름은 민수다."},
		},
	}
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(graphStore)
	connectorRuntime.UseMemoryService(memoryService)
	connectorRuntime.UseGraphitiIngestionRouter(memory.NewGraphitiIngestionRouter(staticScopeLanguageModel{content: `{"shouldStore":true,"storeWorkspace":false,"securityLevelRank":0,"requiredClasses":[],"reason":"user_fact","confidence":0.9}`}, "default"))

	channelEvent := testInboundEvent("message-1")
	channelEvent.ConversationID = "channel-1"
	channelEvent.Prompt = "내 이름은 민수야"
	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, channelEvent)
	if errorValue != nil {
		t.Fatalf("expected channel memory event to process: %v", errorValue)
	}

	directEvent := testInboundEvent("message-2")
	directEvent.ConversationID = "dm-1"
	directEvent.Prompt = "내 이름 뭐야?"
	_, errorValue = connectorRuntime.HandleInboundEvent(context.Background(), adapter, directEvent)
	if errorValue != nil {
		t.Fatalf("expected direct memory recall event to process: %v", errorValue)
	}

	if len(graphStore.episodes) != 0 {
		t.Fatalf("expected no automatic Graphiti episode ingestion, got %d", len(graphStore.episodes))
	}
	if structuredMessagesContain(languageModel.request.Messages, "민수") {
		t.Fatalf("expected graph memory to require explicit memory.search, got %+v", languageModel.request.Messages)
	}
}

func TestConnectorRuntimeDoesNotAutomaticallyIngestMemoryWhenReplySendFails(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "ok"})
	adapter.sendReplyError = errors.New("send failed")
	graphStore := &fakeGraphMemoryStore{}
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(graphStore)
	connectorRuntime.UseMemoryService(memoryService)
	connectorRuntime.UseGraphitiIngestionRouter(memory.NewGraphitiIngestionRouter(staticScopeLanguageModel{content: `{"shouldStore":true,"storeWorkspace":false,"securityLevelRank":0,"requiredClasses":[],"reason":"user_fact","confidence":0.9}`}, "default"))

	event := testInboundEvent("message-memory-reply-failed")
	event.Prompt = "내 선호는 Graphiti-only 메모리야"
	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}
	if result.Reason != "reply_failed" {
		t.Fatalf("expected reply failed result, got %+v", result)
	}
	if len(graphStore.episodes) != 0 {
		t.Fatalf("expected no automatic memory ingestion before reply success, got %d", len(graphStore.episodes))
	}
}

func TestConnectorRuntimeDoesNotAutomaticallyIngestMemoryWhenReplyIsBlocked(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "saved at /workspace/result.md"})
	graphStore := &fakeGraphMemoryStore{}
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(graphStore)
	connectorRuntime.UseMemoryService(memoryService)
	connectorRuntime.UseGraphitiIngestionRouter(memory.NewGraphitiIngestionRouter(staticScopeLanguageModel{content: `{"shouldStore":true,"storeWorkspace":false,"securityLevelRank":0,"requiredClasses":[],"reason":"user_fact","confidence":0.9}`}, "default"))

	event := testInboundEvent("message-memory-blocked")
	event.Prompt = "내 선호는 artifact 경로를 노출하지 않는 거야"
	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}
	if result.Reason != "task_not_completed" && result.Reason != "non_deliverable_artifact_locator" {
		t.Fatalf("expected blocked result, got %+v", result)
	}
	if len(graphStore.episodes) != 0 {
		t.Fatalf("expected no automatic memory ingestion before connector blocking, got %d", len(graphStore.episodes))
	}
}

func TestConnectorRuntimeDoesNotShareUserMemoryWithOtherPerson(t *testing.T) {
	memoryService := &memory.MemoryService{}
	memoryService.StoreMemoryFact(memory.MemoryFact{
		ScopeType:   memory.ScopeTypeUser,
		NamespaceID: "user:person-1",
		Content:     "사용자의 이름은 민수다.",
	})

	records, errorValue := memoryService.SearchMemory(context.Background(), memory.MemorySearchRequest{
		ReaderPersonID:          "person-2",
		ReaderSecurityLevelRank: 100,
		ReaderGrantedClasses:    []string{"internal"},
		Namespaces:              []memory.MemoryNamespace{memory.UserNamespace("person-2")},
	})
	if errorValue != nil {
		t.Fatalf("expected memory search to succeed: %v", errorValue)
	}
	if len(records) != 0 {
		t.Fatalf("expected person-2 not to read person-1 user memory, got %d", len(records))
	}
}

func TestConnectorRuntimeRejectsMissingHistoryCursorWhenMoreContextExists(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "ignored"})
	event := testInboundEvent("message-1")
	event.Context.HasMoreBefore = true

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected malformed event to be ignored: %v", errorValue)
	}
	if !result.Ignored || result.Reason != "missing_history_cursor" {
		t.Fatalf("expected missing history cursor rejection, got %+v", result)
	}
}

func TestPlatformInboundEventOnlyUsesTextAndSenderCompatibilityAliases(t *testing.T) {
	var event PlatformInboundEvent
	errorValue := json.Unmarshal([]byte(`{
		"conversationID":"conversation-1",
		"messageID":"message-1",
		"senderUserID":"sender-1",
		"text":"hello",
		"rootMessageID":"root-1",
		"replyParentID":"parent-1"
	}`), &event)
	if errorValue != nil {
		t.Fatalf("expected compatibility event to decode: %v", errorValue)
	}

	if event.SenderID != "sender-1" {
		t.Fatalf("expected sender compatibility alias, got %q", event.SenderID)
	}
	if event.Prompt != "hello" {
		t.Fatalf("expected text compatibility alias, got %q", event.Prompt)
	}
	if event.ReplyTargetID != "" {
		t.Fatalf("expected no reply target inference, got %q", event.ReplyTargetID)
	}
}

type testAdapter struct {
	senderEmail                   string
	sendReplyError                error
	reactionError                 error
	httpParseResult               HTTPParseResult
	inputAttachmentImportResult   InputAttachmentImportResult
	inputAttachmentImportError    error
	inputAttachmentImportRequests []InputAttachmentImportRequest
	historyContext                VisibleContext
	sentReplies                   []testReply
	reactions                     []ReactionTarget
	progressStarts                []ReplyTarget
	progressStops                 []ReplyTarget
	progressStopErrors            []error
	historyCursors                []string
	operationNames                []string
	resolutions                   []InteractionResolution
}

type testReply struct {
	target          ReplyTarget
	message         string
	taskRunID       string
	replyKind       string
	attachments     []agent.FileAttachment
	recoveryActions []agent.RecoveryAction
	failureNotice   agent.FailureNotice
}

type testConnectorQueueRepository struct {
	pendingEvents   []QueuedConnectorEvent
	succeededEvents []ConnectorRuntimeResult
	pendingReplies  []QueuedConnectorReply
	sentReplies     []string
	failedReplies   []string
}

type testTaskIntakeGate struct {
	isQuiesced bool
}

func (gate testTaskIntakeGate) IsQuiesced() bool {
	return gate.isQuiesced
}

type connectorTaskScheduleRepository struct {
	taskSchedules []task.TaskSchedule
}

func (repository *connectorTaskScheduleRepository) UpsertTaskSchedule(taskSchedule task.TaskSchedule) error {
	repository.taskSchedules = append(repository.taskSchedules, taskSchedule)
	return nil
}

func (repository *connectorTaskScheduleRepository) UpdateTaskSchedule(request task.TaskScheduleUpdateRequest) (task.TaskScheduleUpdateResult, error) {
	for index, taskSchedule := range repository.taskSchedules {
		if taskSchedule.TaskScheduleID != request.TaskScheduleID || taskSchedule.CreatorPersonID != request.RequesterPersonID || taskSchedule.NextRunAt == nil {
			continue
		}
		updatedTaskSchedule := taskSchedule
		var errorValue error
		if request.UpdateTaskSchedule != nil {
			updatedTaskSchedule, errorValue = request.UpdateTaskSchedule(taskSchedule)
			if errorValue != nil {
				return task.TaskScheduleUpdateResult{}, errorValue
			}
		}
		repository.taskSchedules[index] = updatedTaskSchedule
		return task.TaskScheduleUpdateResult{TaskSchedule: updatedTaskSchedule, IsFound: true}, nil
	}
	return task.TaskScheduleUpdateResult{}, nil
}

func (repository *connectorTaskScheduleRepository) ListTaskSchedules(request task.TaskScheduleListRequest) (task.TaskScheduleListResult, error) {
	taskSchedules := []task.TaskSchedule{}
	for _, taskSchedule := range repository.taskSchedules {
		if request.CreatorPersonID != "" && taskSchedule.CreatorPersonID != request.CreatorPersonID {
			continue
		}
		if !request.IncludeExpired && taskSchedule.NextRunAt == nil {
			continue
		}
		taskSchedules = append(taskSchedules, taskSchedule)
	}
	return task.TaskScheduleListResult{TaskSchedules: taskSchedules, TotalCount: len(taskSchedules), Page: 1, PageSize: len(taskSchedules)}, nil
}

func (repository *connectorTaskScheduleRepository) ClaimDueTaskSchedules(int, time.Duration, time.Time, string) ([]task.TaskSchedule, error) {
	return nil, nil
}

func (repository *connectorTaskScheduleRepository) MarkTaskScheduleSucceeded(task.TaskSchedule) error {
	return nil
}

func (repository *connectorTaskScheduleRepository) MarkTaskScheduleFailed(task.TaskSchedule, string, time.Time) error {
	return nil
}

func (repository *connectorTaskScheduleRepository) ExpireTaskSchedule(task.TaskSchedule, string, time.Time) error {
	return nil
}

func (repository *connectorTaskScheduleRepository) CancelTaskSchedules(task.TaskScheduleCancelRequest) (task.TaskScheduleCancelResult, error) {
	return task.TaskScheduleCancelResult{}, nil
}

func (repository *testConnectorQueueRepository) TryInsertConnectorEvent(PlatformInboundEvent) (bool, ConnectorRuntimeResult, error) {
	return false, ConnectorRuntimeResult{}, nil
}

func (repository *testConnectorQueueRepository) SaveConnectorResult(PlatformInboundEvent, ConnectorRuntimeResult) error {
	return nil
}

func (repository *testConnectorQueueRepository) TryEnqueueConnectorEvent(event PlatformInboundEvent) (bool, ConnectorRuntimeResult, error) {
	repository.pendingEvents = append(repository.pendingEvents, QueuedConnectorEvent{Event: event, AttemptCount: 0})
	return false, ConnectorRuntimeResult{}, nil
}

func (repository *testConnectorQueueRepository) ClaimPendingConnectorEvents(int, time.Duration) ([]QueuedConnectorEvent, error) {
	if len(repository.pendingEvents) == 0 {
		return nil, nil
	}
	queuedEvent := repository.pendingEvents[0]
	repository.pendingEvents = repository.pendingEvents[1:]
	queuedEvent.AttemptCount++
	return []QueuedConnectorEvent{queuedEvent}, nil
}

func (repository *testConnectorQueueRepository) MarkConnectorEventSucceeded(_ PlatformInboundEvent, result ConnectorRuntimeResult) error {
	repository.succeededEvents = append(repository.succeededEvents, result)
	return nil
}

func (repository *testConnectorQueueRepository) MarkConnectorEventFailed(QueuedConnectorEvent, error, time.Time) error {
	return nil
}

func (repository *testConnectorQueueRepository) EnqueueConnectorReply(event PlatformInboundEvent, replyTarget ReplyTarget, reply OutboundReply) (string, error) {
	outboxID := event.DedupeKey()
	repository.pendingReplies = append(repository.pendingReplies, QueuedConnectorReply{
		OutboxID:     outboxID,
		RawEventID:   event.DedupeKey(),
		Platform:     event.Platform,
		ReplyTarget:  replyTarget,
		Reply:        reply,
		AttemptCount: 0,
	})
	return outboxID, nil
}

func (repository *testConnectorQueueRepository) ClaimPendingConnectorReplies(int, time.Duration) ([]QueuedConnectorReply, error) {
	if len(repository.pendingReplies) == 0 {
		return nil, nil
	}
	queuedReply := repository.pendingReplies[0]
	repository.pendingReplies = repository.pendingReplies[1:]
	queuedReply.AttemptCount++
	return []QueuedConnectorReply{queuedReply}, nil
}

func (repository *testConnectorQueueRepository) MarkConnectorReplySent(_ QueuedConnectorReply, dispatchID string) error {
	repository.sentReplies = append(repository.sentReplies, dispatchID)
	return nil
}

func (repository *testConnectorQueueRepository) MarkConnectorReplyFailed(_ QueuedConnectorReply, errorValue error, _ time.Time) error {
	repository.failedReplies = append(repository.failedReplies, errorValue.Error())
	return nil
}

func (adapter *testAdapter) Name() string {
	return "test"
}

func (adapter *testAdapter) ParseHTTPEvent(context.Context, *http.Request) (HTTPParseResult, error) {
	return adapter.httpParseResult, nil
}

func (adapter *testAdapter) ParseRealtimeEvent(context.Context, []byte, string) (PlatformInboundEvent, bool, error) {
	return PlatformInboundEvent{}, false, nil
}

func (adapter *testAdapter) ResolveIdentity(context.Context, string) (identity.PlatformAccountIdentity, error) {
	return identity.PlatformAccountIdentity{
		Platform:       adapter.Name(),
		ExternalUserID: "sender-user",
		Email:          adapter.senderEmail,
		DisplayName:    "Sender",
	}, nil
}

func (adapter *testAdapter) ImportInputAttachments(_ context.Context, request InputAttachmentImportRequest) (InputAttachmentImportResult, error) {
	adapter.inputAttachmentImportRequests = append(adapter.inputAttachmentImportRequests, request)
	if adapter.inputAttachmentImportError != nil {
		return InputAttachmentImportResult{}, adapter.inputAttachmentImportError
	}
	return adapter.inputAttachmentImportResult, nil
}

func (adapter *testAdapter) StartProgress(_ context.Context, target ReplyTarget) error {
	adapter.operationNames = append(adapter.operationNames, "progress.start")
	adapter.progressStarts = append(adapter.progressStarts, target)
	return nil
}

func (adapter *testAdapter) StopProgress(ctx context.Context, target ReplyTarget) error {
	adapter.progressStops = append(adapter.progressStops, target)
	adapter.progressStopErrors = append(adapter.progressStopErrors, ctx.Err())
	return nil
}

func (adapter *testAdapter) SendReply(_ context.Context, target ReplyTarget, reply OutboundReply) (string, error) {
	if adapter.sendReplyError != nil {
		return "", adapter.sendReplyError
	}
	adapter.sentReplies = append(adapter.sentReplies, testReply{target: target, message: reply.Message, taskRunID: reply.TaskRunID, replyKind: reply.ReplyKind, attachments: reply.Attachments, recoveryActions: reply.RecoveryActions, failureNotice: reply.FailureNotice})
	return "dispatch-" + strconv.Itoa(len(adapter.sentReplies)), nil
}

func (adapter *testAdapter) AddReaction(_ context.Context, target ReactionTarget) error {
	if adapter.reactionError != nil {
		return adapter.reactionError
	}
	adapter.reactions = append(adapter.reactions, target)
	return nil
}

func (adapter *testAdapter) ResolveInteraction(_ context.Context, resolution InteractionResolution) error {
	adapter.resolutions = append(adapter.resolutions, resolution)
	return nil
}

func (adapter *testAdapter) FetchHistory(_ context.Context, historyCursor string, _ int) (VisibleContext, error) {
	adapter.operationNames = append(adapter.operationNames, "history.fetch")
	adapter.historyCursors = append(adapter.historyCursors, historyCursor)
	if len(adapter.historyContext.Messages) > 0 || len(adapter.historyContext.Materials) > 0 || len(adapter.historyContext.InputAttachments) > 0 {
		return adapter.historyContext, nil
	}
	return VisibleContext{
		Messages: []VisibleContextMessage{{Speaker: "admin", Text: "older message"}},
	}, nil
}

type testAdapterWithoutReaction struct {
	adapter *testAdapter
}

func (adapter testAdapterWithoutReaction) Name() string {
	return adapter.adapter.Name()
}

func (adapter testAdapterWithoutReaction) ParseHTTPEvent(ctx context.Context, request *http.Request) (HTTPParseResult, error) {
	return adapter.adapter.ParseHTTPEvent(ctx, request)
}

func (adapter testAdapterWithoutReaction) ParseRealtimeEvent(ctx context.Context, payload []byte, source string) (PlatformInboundEvent, bool, error) {
	return adapter.adapter.ParseRealtimeEvent(ctx, payload, source)
}

func (adapter testAdapterWithoutReaction) ResolveIdentity(ctx context.Context, senderID string) (identity.PlatformAccountIdentity, error) {
	return adapter.adapter.ResolveIdentity(ctx, senderID)
}

func (adapter testAdapterWithoutReaction) StartProgress(ctx context.Context, target ReplyTarget) error {
	return adapter.adapter.StartProgress(ctx, target)
}

func (adapter testAdapterWithoutReaction) StopProgress(ctx context.Context, target ReplyTarget) error {
	return adapter.adapter.StopProgress(ctx, target)
}

func (adapter testAdapterWithoutReaction) SendReply(ctx context.Context, target ReplyTarget, reply OutboundReply) (string, error) {
	return adapter.adapter.SendReply(ctx, target, reply)
}

func (adapter testAdapterWithoutReaction) ResolveInteraction(ctx context.Context, resolution InteractionResolution) error {
	return adapter.adapter.ResolveInteraction(ctx, resolution)
}

func (adapter testAdapterWithoutReaction) FetchHistory(ctx context.Context, historyCursor string, limit int) (VisibleContext, error) {
	return adapter.adapter.FetchHistory(ctx, historyCursor, limit)
}

type testLanguageModel struct {
	reply      string
	errorValue error
}

func (languageModel testLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return languageModel.reply, languageModel.errorValue
}

func (languageModel testLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if languageModel.errorValue != nil {
		return llm.StructuredResponse{}, languageModel.errorValue
	}
	return llm.StructuredResponse{Content: connectorFinishMessage(languageModel.reply)}, nil
}

type blockingTestLanguageModel struct {
	reply   string
	started chan struct{}
	release chan struct{}
}

func (languageModel *blockingTestLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return languageModel.reply, nil
}

func (languageModel *blockingTestLanguageModel) GenerateStructuredResponse(ctx context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	_ = request
	select {
	case <-languageModel.started:
	default:
		close(languageModel.started)
	}
	select {
	case <-languageModel.release:
		return llm.StructuredResponse{Content: connectorFinishMessage(languageModel.reply)}, nil
	case <-ctx.Done():
		return llm.StructuredResponse{}, ctx.Err()
	}
}

type addressingTestLanguageModel struct {
	addressingTarget string
	dutyMatch        bool
	dutyName         string
	dutyConfidence   float64
	addressingError  error
	reply            string
	requests         []llm.StructuredResponseRequest
}

func (languageModel *addressingTestLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return languageModel.reply, nil
}

func (languageModel *addressingTestLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	languageModel.requests = append(languageModel.requests, request)
	if request.StructuredOutputSchema.Name == "blueclaw_addressing_classification" {
		if languageModel.addressingError != nil {
			return llm.StructuredResponse{}, languageModel.addressingError
		}
		return llm.StructuredResponse{Content: `{"target":` + strconv.Quote(languageModel.addressingTarget) + `,"shouldReply":` + strconv.FormatBool(languageModel.addressingTarget == string(agent.AddressingTargetBot) || languageModel.dutyMatch) + `,"dutyMatch":` + strconv.FormatBool(languageModel.dutyMatch) + `,"dutyName":` + strconv.Quote(languageModel.dutyName) + `,"dutyConfidence":` + strconv.FormatFloat(languageModel.dutyConfidence, 'f', -1, 64) + `}`}, nil
	}
	return llm.StructuredResponse{Content: connectorFinishMessage(languageModel.reply)}, nil
}

type recordingLanguageModel struct {
	reply   string
	request llm.StructuredResponseRequest
}

func (languageModel *recordingLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return languageModel.reply, nil
}

func (languageModel *recordingLanguageModel) GenerateStructuredResponse(_ context.Context, structuredResponseRequest llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	languageModel.request = structuredResponseRequest
	return llm.StructuredResponse{Content: connectorFinishMessage(languageModel.reply)}, nil
}

type staticScopeLanguageModel struct {
	content string
}

func (languageModel staticScopeLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel staticScopeLanguageModel) GenerateStructuredResponse(_ context.Context, structuredResponseRequest llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if structuredResponseRequest.StructuredOutputSchema.Name == "blueclaw_graphiti_ingestion_route" {
		return llm.StructuredResponse{Content: languageModel.content}, nil
	}
	return llm.StructuredResponse{Content: connectorFinishMessage("ok")}, nil
}

type fakeGraphMemoryStore struct {
	episodes []memory.MemoryEpisode
	facts    []memory.MemoryFact
}

func (store *fakeGraphMemoryStore) AddEpisode(_ context.Context, episode memory.MemoryEpisode) (memory.MemoryIngestionResult, error) {
	store.episodes = append(store.episodes, episode)
	return memory.MemoryIngestionResult{EpisodeID: episode.EpisodeID, NamespaceCount: len(episode.Namespaces)}, nil
}

func (store *fakeGraphMemoryStore) SearchFacts(_ context.Context, request memory.MemorySearchRequest) ([]memory.MemoryFact, error) {
	facts := []memory.MemoryFact{}
	for _, fact := range store.facts {
		for _, namespace := range request.Namespaces {
			if fact.NamespaceID == namespace.NamespaceID {
				facts = append(facts, fact)
			}
		}
	}
	return facts, nil
}

func containsEpisodeNamespace(episode memory.MemoryEpisode, namespaceID string) bool {
	for _, namespace := range episode.Namespaces {
		if namespace.NamespaceID == namespaceID {
			return true
		}
	}
	return false
}

type testHTTPDoer func(*http.Request) (*http.Response, error)

func (doer testHTTPDoer) Do(request *http.Request) (*http.Response, error) {
	return doer(request)
}

func structuredMessagesContain(messages []llm.Message, fragment string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, fragment) {
			return true
		}
		for _, part := range message.Parts {
			if part.Type == "text" && strings.Contains(part.Text, fragment) {
				return true
			}
		}
	}
	return false
}

func joinConnectorMessageContent(messages []llm.Message) string {
	parts := []string{}
	for _, message := range messages {
		parts = append(parts, message.Content)
		for _, messagePart := range message.Parts {
			if messagePart.Type == "text" {
				parts = append(parts, messagePart.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func connectorMessagesContainImagePart(messages []llm.Message, mimeType string, dataBase64 string) bool {
	for _, message := range messages {
		for _, part := range message.Parts {
			if part.Type == "image" && part.MimeType == mimeType && part.DataBase64 == dataBase64 {
				return true
			}
		}
	}
	return false
}

func messageIndex(messages []llm.Message, fragment string) int {
	for index, message := range messages {
		if strings.Contains(message.Content, fragment) {
			return index
		}
	}
	return -1
}

func userMessageIndex(messages []llm.Message, fragment string) int {
	for index, message := range messages {
		if message.Role == "user" && strings.Contains(message.Content, fragment) {
			return index
		}
	}
	return -1
}

func connectorRequestSchemaNames(requests []llm.StructuredResponseRequest) []string {
	names := []string{}
	for _, request := range requests {
		names = append(names, request.StructuredOutputSchema.Name)
	}
	return names
}

func connectorSchemaIndexAfter(requests []llm.StructuredResponseRequest, schemaName string, afterIndex int) int {
	for index := afterIndex + 1; index < len(requests); index++ {
		if requests[index].StructuredOutputSchema.Name == schemaName {
			return index
		}
	}
	return -1
}

func connectorContainsSchemaName(requests []llm.StructuredResponseRequest, schemaName string) bool {
	for _, request := range requests {
		if request.StructuredOutputSchema.Name == schemaName {
			return true
		}
	}
	return false
}

func connectorTaskEventsContain(connectorRuntime *ConnectorRuntime, taskRunID string, name string, bodyFragment string) bool {
	for _, taskEvent := range connectorRuntime.agentKernel.ListTaskEvent(taskRunID) {
		if taskEvent.Name == name && strings.Contains(taskEvent.Body, bodyFragment) {
			return true
		}
	}
	return false
}

func connectorFirstRequestBySchema(requests []llm.StructuredResponseRequest, schemaName string) (llm.StructuredResponseRequest, bool) {
	for _, request := range requests {
		if request.StructuredOutputSchema.Name == schemaName {
			return request, true
		}
	}
	return llm.StructuredResponseRequest{}, false
}

func findAgentToolDefinition(toolDefinitions []agent.ToolDefinition, toolName string) (agent.ToolDefinition, bool) {
	for _, toolDefinition := range toolDefinitions {
		if toolDefinition.Name == toolName {
			return toolDefinition, true
		}
	}
	return agent.ToolDefinition{}, false
}

func connectorFinishMessage(reply string) string {
	return `{"action":"finish","message":` + strconv.Quote(reply) + `,"completionSummary":` + strconv.Quote(reply) + `,"replyParts":[{"type":"text","text":` + strconv.Quote(reply) + `}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[]}`
}

func connectorFinishMessageWithEvidence(reply string, observationID string, toolName string, attachmentIndex int) string {
	return `{"action":"finish","message":` + strconv.Quote(reply) + `,"completionSummary":` + strconv.Quote(reply) + `,"replyParts":[{"type":"text","text":` + strconv.Quote(reply) + `}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":` + strconv.Quote(observationID) + `,"toolName":` + strconv.Quote(toolName) + `,"attachmentIndex":` + strconv.Itoa(attachmentIndex) + `}]}`
}

func newTestConnectorRuntime(t *testing.T, languageModel llm.LanguageModelProvider) (*ConnectorRuntime, *testAdapter) {
	t.Helper()

	identityService := identity.NewIdentityService(policy.PolicyProjection{
		PersonIDByEmail: map[string]string{"invited@example.com": "person-1"},
		PersonAccessByPersonID: map[string]policy.PersonAccess{
			"person-1": {PersonID: "person-1", SecurityLevelRank: 100, GrantedClasses: []string{"internal", "finance"}},
		},
	})
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := agent.NewAgentKernel(taskRunService, task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(languageModel)

	connectorRuntime := NewConnectorRuntime(identityService, agentKernel, nil)
	connectorRuntime.UseTaskRunService(taskRunService)
	adapter := &testAdapter{senderEmail: "invited@example.com"}
	connectorRuntime.RegisterAdapter(adapter)
	return connectorRuntime, adapter
}

func newWaitRoutingTestConnectorRuntime(t *testing.T, languageModel llm.LanguageModelProvider) (*ConnectorRuntime, *testAdapter, *task.TaskRunService, *task.InMemoryTaskWaitTokenRepository) {
	t.Helper()

	identityService := identity.NewIdentityService(policy.PolicyProjection{
		PersonIDByEmail: map[string]string{"invited@example.com": "person-1"},
		PersonAccessByPersonID: map[string]policy.PersonAccess{
			"person-1": {PersonID: "person-1", SecurityLevelRank: 100, GrantedClasses: []string{"internal", "finance"}},
		},
	})
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := agent.NewAgentKernel(taskRunService, task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(languageModel)
	agentKernel.UseIntakeLanguageModelProvider(languageModel)
	agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true})
	taskWaitRepository := task.NewInMemoryTaskWaitTokenRepository()

	connectorRuntime := NewConnectorRuntime(identityService, agentKernel, nil)
	connectorRuntime.UseTaskRunService(taskRunService)
	connectorRuntime.UseTaskWaitTokenRepository(taskWaitRepository)
	adapter := &testAdapter{senderEmail: "invited@example.com"}
	connectorRuntime.RegisterAdapter(adapter)
	return connectorRuntime, adapter, taskRunService, taskWaitRepository
}

func createWaitingInputTaskRun(t *testing.T, taskRunService *task.TaskRunService, prompt string, interactionID string) task.TaskRun {
	t.Helper()

	taskRun := taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "direct-1", ReplyTargetID: "origin-reply-target"}, prompt)
	waitingTaskRun, errorValue := taskRunService.PauseTaskRun(taskRun.TaskRunID, task.TaskStatusWaitingUserInput, prompt)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "ask.requested", marshalConnectorEventBody(map[string]string{
		"interactionID":    interactionID,
		"kind":             "input",
		"question":         prompt,
		"message":          prompt,
		"responseLanguage": "ko",
	}))
	return waitingTaskRun
}

func waitRoutingTaskWaitToken(taskRun task.TaskRun, dispatchID string, interactionID string) task.TaskWaitToken {
	now := time.Now().UTC()
	return task.TaskWaitToken{
		WaitID:         "wait-" + dispatchID,
		TaskRunID:      taskRun.TaskRunID,
		PersonID:       taskRun.RequesterPersonID,
		Platform:       "test",
		ConversationID: taskRun.OriginConversationID,
		ReplyTargetID:  dispatchID,
		ThreadRootID:   taskRun.OriginReplyTargetID,
		DispatchID:     dispatchID,
		InteractionID:  interactionID,
		Kind:           "input",
		State:          "open",
		ExpiresAt:      now.Add(time.Hour),
		CreatedAt:      now,
	}
}

func newRepositoryBackedTestConnectorRuntime(t *testing.T, languageModel llm.LanguageModelProvider, taskRunRepository *testTaskRunRepository) (*ConnectorRuntime, *testAdapter, *task.TaskEventService) {
	t.Helper()

	identityService := identity.NewIdentityService(policy.PolicyProjection{
		PersonIDByEmail: map[string]string{"invited@example.com": "person-1"},
		PersonAccessByPersonID: map[string]policy.PersonAccess{
			"person-1": {PersonID: "person-1", SecurityLevelRank: 100, GrantedClasses: []string{"internal", "finance"}},
		},
	})
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskRunService.UseRepository(taskRunRepository)
	agentKernel := agent.NewAgentKernel(taskRunService, task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(languageModel)

	connectorRuntime := NewConnectorRuntime(identityService, agentKernel, nil)
	connectorRuntime.UseTaskRunService(taskRunService)
	adapter := &testAdapter{senderEmail: "invited@example.com"}
	connectorRuntime.RegisterAdapter(adapter)
	return connectorRuntime, adapter, taskEventService
}

type testTaskRunRepository struct {
	taskRuns     map[string]task.TaskRun
	taskAttempts map[string]task.TaskAttempt
}

func newTestTaskRunRepository() *testTaskRunRepository {
	return &testTaskRunRepository{
		taskRuns:     map[string]task.TaskRun{},
		taskAttempts: map[string]task.TaskAttempt{},
	}
}

func (repository *testTaskRunRepository) SaveTaskRun(taskRun task.TaskRun) error {
	repository.taskRuns[taskRun.TaskRunID] = taskRun
	return nil
}

func (repository *testTaskRunRepository) StartTaskRunAttempt(taskRun task.TaskRun, taskAttempt task.TaskAttempt) error {
	repository.taskRuns[taskRun.TaskRunID] = taskRun
	repository.taskAttempts[taskAttempt.TaskAttemptID] = taskAttempt
	return nil
}

func (repository *testTaskRunRepository) FinishTaskRunAttempt(taskRun task.TaskRun, taskAttempt task.TaskAttempt) error {
	repository.taskRuns[taskRun.TaskRunID] = taskRun
	if strings.TrimSpace(taskAttempt.TaskAttemptID) != "" {
		repository.taskAttempts[taskAttempt.TaskAttemptID] = taskAttempt
	}
	return nil
}

func (repository *testTaskRunRepository) TransitionTaskRun(transition task.TaskRunTransition) (task.TaskRun, error) {
	taskRun, isFound := repository.taskRuns[transition.TaskRunID]
	if !isFound {
		return task.TaskRun{}, errors.New("task run not found")
	}
	if !testTaskRunStatusAllowed(taskRun.Status, transition.FromStates) {
		return task.TaskRun{}, task.ErrIllegalTransition{
			TaskRunID:     transition.TaskRunID,
			CurrentStatus: taskRun.Status,
			FromStates:    append([]task.TaskStatus{}, transition.FromStates...),
			ToState:       transition.ToState,
		}
	}
	taskRun.Status = transition.ToState
	taskRun.UpdatedAt = transition.UpdatedAt
	if transition.StartedAttempt != nil {
		taskRun.CurrentAttemptID = transition.StartedAttempt.TaskAttemptID
		taskRun.CurrentAgentProfileName = transition.CurrentAgentProfileName
		repository.taskAttempts[transition.StartedAttempt.TaskAttemptID] = *transition.StartedAttempt
	}
	if transition.FailureReason != "" || transition.FinishCurrentAttempt {
		taskRun.FailureReason = transition.FailureReason
	}
	if transition.FinishCurrentAttempt && strings.TrimSpace(taskRun.CurrentAttemptID) != "" {
		taskAttempt := repository.taskAttempts[taskRun.CurrentAttemptID]
		taskAttempt.Status = transition.FinishedAttemptStatus
		taskAttempt.FinishedAt = &transition.UpdatedAt
		taskAttempt.FailureReason = strings.TrimSpace(transition.FailureReason)
		repository.taskAttempts[taskRun.CurrentAttemptID] = taskAttempt
	}
	repository.taskRuns[taskRun.TaskRunID] = taskRun
	return taskRun, nil
}

func testTaskRunStatusAllowed(status task.TaskStatus, allowedStatuses []task.TaskStatus) bool {
	for _, allowedStatus := range allowedStatuses {
		if status == allowedStatus {
			return true
		}
	}
	return false
}

func (repository *testTaskRunRepository) FindTaskRun(taskRunID string) (task.TaskRun, bool, error) {
	taskRun, isFound := repository.taskRuns[taskRunID]
	return taskRun, isFound, nil
}

func (repository *testTaskRunRepository) FindTaskAttempt(taskAttemptID string) (task.TaskAttempt, bool, error) {
	taskAttempt, isFound := repository.taskAttempts[taskAttemptID]
	return taskAttempt, isFound, nil
}

func (repository *testTaskRunRepository) ListTaskRun() ([]task.TaskRun, error) {
	taskRuns := make([]task.TaskRun, 0, len(repository.taskRuns))
	for _, taskRun := range repository.taskRuns {
		taskRuns = append(taskRuns, taskRun)
	}
	return taskRuns, nil
}

func (repository *testTaskRunRepository) ListTaskRunByPersonID(personID string) ([]task.TaskRun, error) {
	taskRuns := []task.TaskRun{}
	for _, taskRun := range repository.taskRuns {
		if taskRun.RequesterPersonID == personID {
			taskRuns = append(taskRuns, taskRun)
		}
	}
	return taskRuns, nil
}

func (repository *testTaskRunRepository) DeleteTaskRunsBefore(time.Time, []string) ([]string, error) {
	return nil, nil
}

func useTestConnectorSkill(connectorRuntime *ConnectorRuntime, skillInstruction agent.SkillInstruction) {
	connectorRuntime.agentKernel.UseSkillRetriever(agent.NewEmbeddingSkillRetriever(connectorSkillEmbeddingProvider{}, ""))
	connectorRuntime.agentKernel.UseInstructionBundleLoader(func() agent.InstructionBundle {
		return agent.InstructionBundle{Skills: []agent.SkillInstruction{skillInstruction}}
	})
}

type connectorSkillEmbeddingProvider struct{}

func (provider connectorSkillEmbeddingProvider) GenerateEmbedding(_ context.Context, input string) ([]float32, error) {
	normalizedInput := strings.ToLower(input)
	return []float32{
		connectorSkillEmbeddingValue(normalizedInput, []string{"schedule", "scheduled", "cron", "remind", "reminder", "매일", "예약", "알림", "마다"}),
		connectorSkillEmbeddingValue(normalizedInput, []string{"calendar", "event", "일정", "달력", "캘린더", "휴가"}),
		connectorSkillEmbeddingValue(normalizedInput, []string{"browser", "observe", "snapshot", "screenshot", "브라우저", "화면"}),
	}, nil
}

func connectorSkillEmbeddingValue(input string, keywords []string) float32 {
	for _, keyword := range keywords {
		if strings.Contains(input, keyword) {
			return 1
		}
	}
	return 0
}

func connectorScheduledTaskSkill() agent.SkillInstruction {
	return agent.SkillInstruction{
		Name:         "scheduled-task",
		Description:  "Create scheduled tasks.",
		WhenToUse:    "Use for schedule, remind, 매일, 예약, 알림, and 마다 requests.",
		Prompt:       "Use schedule.create with taskInstruction for only the work to perform at run time. Put cadence and stop conditions in structured fields such as runAt, intervalSecond, cronExpression, expiresAt, and maxRunCount.",
		TriggerHints: []string{"schedule", "remind", "매일", "예약", "알림", "마다"},
		Completion: agent.SkillCompletion{
			RequiredEvidenceTools: []string{"schedule.create"},
		},
		AllowedTools: []string{"schedule.create"},
		Source:       agent.InstructionSource{Path: "skills/scheduled-task/SKILL.md", SkillName: "scheduled-task"},
	}
}

func connectorCalendarSkill() agent.SkillInstruction {
	return agent.SkillInstruction{
		Name:         "calendar",
		Description:  "Create or list calendar events.",
		WhenToUse:    "Use for calendar, event, 일정, 달력, 캘린더, and 휴가 requests.",
		Prompt:       "Use calendar.event.add to create calendar events without approval. Use calendar.event.delete only after approval.",
		TriggerHints: []string{"calendar", "event", "일정", "달력", "캘린더", "휴가"},
		AllowedTools: []string{"calendar.event.add", "calendar.event.delete"},
		Source:       agent.InstructionSource{Path: "skills/calendar/SKILL.md", SkillName: "calendar"},
	}
}

func connectorBrowserSnapshotSkill() agent.SkillInstruction {
	return agent.SkillInstruction{
		Name:         "browser-snapshot",
		Description:  "Observe browser pages.",
		WhenToUse:    "Use for browser observe, snapshot, screenshot, 브라우저, and 화면 확인 requests.",
		Prompt:       "Use browser.snapshot to observe the current browser state.",
		TriggerHints: []string{"browser", "observe", "snapshot", "브라우저", "화면"},
		AllowedTools: []string{"browser.snapshot"},
		Source:       agent.InstructionSource{Path: "skills/browser-snapshot/SKILL.md", SkillName: "browser-snapshot"},
	}
}

func testInboundEvent(messageID string) PlatformInboundEvent {
	return PlatformInboundEvent{
		Platform:       "test",
		Source:         "test",
		ConversationID: "direct-1",
		MessageID:      messageID,
		SenderID:       "sender-user",
		ReplyTargetID:  "reply-target-1",
		Prompt:         "hello",
	}
}

func testChannelInboundEvent(messageID string) PlatformInboundEvent {
	event := testInboundEvent(messageID)
	event.ConversationID = "channel-1"
	event.ReplyTargetID = "channel-reply-target-1"
	event.Prompt = "이거 정리해줘"
	event.Context = VisibleContext{
		ConversationType: "O",
		ChannelID:        "channel-1",
		ChannelName:      "random-chat",
	}
	return event
}
