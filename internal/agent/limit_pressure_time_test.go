package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/llm"
	"github.com/Dawn-kim-official/blueclaw/internal/task"
)

const elapsedReplySchemaName = "blueclaw_elapsed_reply"

func newTimePressureRunner() *AgentTurnRunner {
	return &AgentTurnRunner{
		options: TurnOptions{
			MaxIterationCount: 72,
			MaxToolCallCount:  30,
			MaxElapsedSecond:  int((40 * time.Minute).Seconds()),
		},
	}
}

func TestLimitPressureLevelRisesWithElapsedWhileStepsAreLow(t *testing.T) {
	runner := newTimePressureRunner()
	maxElapsed := runner.maximumWorkDuration()

	cases := []struct {
		elapsed time.Duration
		want    string
	}{
		{elapsed: 0, want: ""},
		{elapsed: time.Duration(float64(maxElapsed) * 0.4), want: ""},
		{elapsed: time.Duration(float64(maxElapsed) * 0.75), want: ""},
		{elapsed: time.Duration(float64(maxElapsed) * 0.8), want: limitPressureStageWrapUp},
		{elapsed: time.Duration(float64(maxElapsed) * 0.9), want: limitPressureStageWrapUp},
		{elapsed: time.Duration(float64(maxElapsed) * 0.95), want: limitPressureStageNarrowPalette},
	}
	for _, testCase := range cases {
		level := runner.limitPressureLevel(1, 0, testCase.elapsed)
		if level != testCase.want {
			t.Fatalf("elapsed %s: expected level %q, got %q", testCase.elapsed, testCase.want, level)
		}
	}
}

func TestLimitPressureLevelUsesMaxOfStepAndTime(t *testing.T) {
	runner := newTimePressureRunner()
	if level := runner.limitPressureLevel(68, 28, 0); level != limitPressureStageNarrowPalette {
		t.Fatalf("expected step pressure to still drive narrow_palette, got %q", level)
	}
	if level := runner.limitPressureLevel(1, 0, 38*time.Minute); level != limitPressureStageNarrowPalette {
		t.Fatalf("expected elapsed pressure to drive narrow_palette, got %q", level)
	}
}

func TestExecutionEffortClockDoesNotIncludePreflightTime(t *testing.T) {
	runner := &AgentTurnRunner{options: TurnOptions{MaxElapsedSecond: 30}}

	if runner.currentEffortElapsed(time.Now()) {
		t.Fatal("expected a fresh execution effort budget after preflight")
	}
	if runner.currentEffortElapsed(time.Now().Add(-19 * time.Second)) {
		t.Fatal("expected work to continue before the reserved closing window")
	}
	if !runner.currentEffortElapsed(time.Now().Add(-21 * time.Second)) {
		t.Fatal("expected work to stop with one third of the total budget reserved for closing")
	}
}

func TestElapsedClosingDurationIsPartOfTheTotalBudget(t *testing.T) {
	testCases := []struct {
		total   time.Duration
		closing time.Duration
		work    time.Duration
	}{
		{total: -time.Second},
		{total: 0},
		{total: time.Nanosecond, work: time.Nanosecond},
		{total: time.Second, closing: time.Second / 3, work: time.Second - time.Second/3},
		{total: 3 * time.Minute, closing: time.Minute, work: 2 * time.Minute},
		{total: 10 * time.Minute, closing: time.Minute, work: 9 * time.Minute},
		{total: time.Hour, closing: time.Minute, work: 59 * time.Minute},
	}
	for _, testCase := range testCases {
		if closing := elapsedClosingDuration(testCase.total); closing != testCase.closing {
			t.Fatalf("total %s: expected closing %s, got %s", testCase.total, testCase.closing, closing)
		}
		if work := workDurationWithinTotal(testCase.total); work != testCase.work {
			t.Fatalf("total %s: expected work %s, got %s", testCase.total, testCase.work, work)
		}
	}
}

func TestElapsedClosingCompletesFromExactEvidenceBeforeReply(t *testing.T) {
	languageModel := newElapsedClosingLanguageModel("task.add", `{"title":"분기 결산 운영 검토"}`, "분기 결산 운영 검토 업무를 등록했습니다.")
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxElapsedSecond: 1})
	languageModel.observeTaskStatus = func() task.TaskStatus {
		return onlyTaskStatus(services.taskRunService, "person-1")
	}
	toolSet := newTestCapabilityToolSet([]string{"task.add"})
	toolCallCount := 0
	registerTestTool(toolSet, ToolDefinition{Name: "task.add"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"taskID":"task-1"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "분기 결산 운영 검토 업무를 등록해줘",
		ResponseLanguage:      ResponseLanguageKorean,
		ToolSet:               toolSet,
		PinnedToolNames:       toolSet.ListToolNames(),
		RequiredEvidenceTools: []string{"task.add"},
		EffortStartedAt:       time.Now().Add(-500 * time.Millisecond),
	})

	if errorValue != nil {
		t.Fatalf("expected elapsed completion, got %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted || languageModel.statusAtClosing != task.TaskStatusCompleted {
		t.Fatalf("expected completed status before closing, got result=%s closing=%s", result.TaskRun.Status, languageModel.statusAtClosing)
	}
	if result.FinishMessage != languageModel.closingReply {
		t.Fatalf("expected closing reply %q, got %q", languageModel.closingReply, result.FinishMessage)
	}
	assertSingleElapsedClosing(t, languageModel)
	if toolCallCount != 1 {
		t.Fatalf("expected one pre-cutoff tool call and no post-cutoff call, got %d", toolCallCount)
	}
	if languageModel.structuredCalls != 0 {
		t.Fatalf("expected no structured finalizer or verifier after cutoff, got %d calls", languageModel.structuredCalls)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.limit_completed_from_evidence", "max_elapsed") {
		t.Fatal("expected exact-evidence completion event")
	}
}

func TestElapsedClosingBlocksBeforeReplyWhenEvidenceIsMissing(t *testing.T) {
	languageModel := newElapsedClosingLanguageModel("", "", "작업 시간이 끝나 진행 상황을 저장했습니다.")
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxElapsedSecond: 1})
	languageModel.observeTaskStatus = func() task.TaskStatus {
		return onlyTaskStatus(services.taskRunService, "person-1")
	}
	toolSet := newTestCapabilityToolSet([]string{"task.add"})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "분기 결산 운영 검토 업무를 등록해줘",
		ResponseLanguage:      ResponseLanguageKorean,
		ToolSet:               toolSet,
		PinnedToolNames:       toolSet.ListToolNames(),
		RequiredEvidenceTools: []string{"task.add"},
		EffortStartedAt:       time.Now().Add(-500 * time.Millisecond),
	})

	if errorValue != nil {
		t.Fatalf("expected elapsed block, got %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked || result.TaskRun.FailureReason != "max_elapsed" {
		t.Fatalf("expected max_elapsed block, got %+v", result.TaskRun)
	}
	if languageModel.statusAtClosing != task.TaskStatusBlocked {
		t.Fatalf("expected blocked status before closing, got %s", languageModel.statusAtClosing)
	}
	if result.UserNotice != languageModel.closingReply {
		t.Fatalf("expected closing notice %q, got %q", languageModel.closingReply, result.UserNotice)
	}
	assertSingleElapsedClosing(t, languageModel)
	if languageModel.structuredCalls != 0 {
		t.Fatalf("expected no structured finalizer or verifier after cutoff, got %d calls", languageModel.structuredCalls)
	}
}

func TestElapsedClosingUsesRemainingTotalBudget(t *testing.T) {
	languageModel := newElapsedClosingLanguageModel("task.add", `{}`, "")
	languageModel.closingStarted = make(chan struct{})
	languageModel.blockClosing = true
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxElapsedSecond: 1})
	languageModel.observeTaskStatus = func() task.TaskStatus {
		return onlyTaskStatus(services.taskRunService, "person-1")
	}
	toolSet := newTestCapabilityToolSet([]string{"task.add"})
	registerTestTool(toolSet, ToolDefinition{Name: "task.add"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess(`{"taskID":"task-1"}`), nil
	})
	resultChannel := make(chan AgentTurnResult, 1)
	startedAt := time.Now()

	go func() {
		result, _ := services.runner.RunTurn(context.Background(), AgentTurnRequest{
			RequesterPersonID:     "person-1",
			ConversationID:        "conversation-1",
			Prompt:                "분기 결산 운영 검토 업무를 등록해줘",
			ToolSet:               toolSet,
			PinnedToolNames:       toolSet.ListToolNames(),
			RequiredEvidenceTools: []string{"task.add"},
			EffortStartedAt:       time.Now().Add(-500 * time.Millisecond),
		})
		resultChannel <- result
	}()

	select {
	case <-languageModel.closingStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("expected elapsed closing to start")
	}
	if !languageModel.closingHasDeadline {
		t.Fatal("expected elapsed closing to use the hard total deadline")
	}
	if languageModel.statusAtClosing != task.TaskStatusCompleted {
		t.Fatalf("expected completed status before closing, got %s", languageModel.statusAtClosing)
	}

	select {
	case result := <-resultChannel:
		if result.TaskRun.Status != task.TaskStatusCompleted {
			t.Fatalf("expected persisted completion to survive closing cancellation, got %+v", result.TaskRun)
		}
		if result.ReplySuppressed {
			t.Fatal("expected the hard deadline to fall back to a compact reply")
		}
		if result.FailureNotice.Source != "" || strings.TrimSpace(result.FinishMessage) == "" {
			t.Fatalf("expected compact completed reply, got %+v", result)
		}
		if time.Since(startedAt) > time.Second {
			t.Fatalf("expected the complete turn to stay inside the one-second total budget, took %s", time.Since(startedAt))
		}
	case <-time.After(time.Second):
		t.Fatal("expected closing to stop at the hard total deadline")
	}
	assertSingleElapsedClosing(t, languageModel)
}

func TestElapsedClosingTotalDeadlinePersistsRawFallback(t *testing.T) {
	languageModel := newElapsedClosingLanguageModel("", "", "")
	languageModel.closingStarted = make(chan struct{})
	languageModel.blockClosing = true
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxElapsedSecond: 1})
	resultChannel := make(chan AgentTurnResult, 1)

	go func() {
		result, _ := services.runner.RunTurn(context.Background(), AgentTurnRequest{
			RequesterPersonID: "person-1",
			ConversationID:    "conversation-1",
			Prompt:            "진행 상황을 정리해줘",
			ResponseLanguage:  ResponseLanguageKorean,
			ToolSet:           newTestToolSet(nil),
			EffortStartedAt:   time.Now().Add(-500 * time.Millisecond),
		})
		resultChannel <- result
	}()

	select {
	case <-languageModel.closingStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("expected elapsed closing to start")
	}

	select {
	case result := <-resultChannel:
		if result.TaskRun.Status != task.TaskStatusBlocked || result.TaskRun.FailureReason != "max_elapsed" {
			t.Fatalf("expected max elapsed block, got %+v", result.TaskRun)
		}
		if result.ReplySuppressed {
			t.Fatal("expected the internal total deadline to preserve the raw fallback")
		}
		if result.FailureNotice.Source != "raw_error" || strings.TrimSpace(result.UserNotice) == "" {
			t.Fatalf("expected a compact raw failure notice, got %+v", result)
		}
		if result.TaskRun.Result != result.UserNotice {
			t.Fatalf("expected persisted fallback %q, got %q", result.UserNotice, result.TaskRun.Result)
		}
	case <-time.After(time.Second):
		t.Fatal("expected closing to stop at the hard total deadline")
	}
	assertSingleElapsedClosing(t, languageModel)
}

func TestElapsedClosingFailureDoesNotRetryOrUseLegacyFallback(t *testing.T) {
	languageModel := newElapsedClosingLanguageModel("", "", "")
	languageModel.closingError = errors.New("closing model unavailable")
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxElapsedSecond: 1})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "진행 상황을 정리해줘",
		ToolSet:           newTestToolSet(nil),
		EffortStartedAt:   time.Now().Add(-500 * time.Millisecond),
	})

	if errorValue != nil {
		t.Fatalf("expected compact fallback result, got %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked || result.FailureNotice.Source != "raw_error" {
		t.Fatalf("expected persisted block with raw notice, got %+v", result)
	}
	assertSingleElapsedClosing(t, languageModel)
	if languageModel.legacyCalls != 0 || languageModel.structuredCalls != 0 {
		t.Fatalf("expected no retry or legacy path, got legacy=%d structured=%d", languageModel.legacyCalls, languageModel.structuredCalls)
	}
	if strings.TrimSpace(result.UserNotice) == "" || strings.Contains(result.UserNotice, "max_elapsed") {
		t.Fatalf("expected compact user-safe raw notice, got %q", result.UserNotice)
	}
}

func TestAgentTurnRunnerPreservesCallerCancellationBeforeEffortDeadline(t *testing.T) {
	services := newTurnRunnerTestServices(deadlineBlockingLanguageModel{}, TurnOptions{MaxElapsedSecond: 30})
	runContext, cancelRun := context.WithCancel(context.Background())
	cancelRun()

	result, errorValue := services.runner.RunTurn(runContext, AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "취소할 작업",
		ToolSet:           newTestToolSet(nil),
	})

	if errorValue != nil {
		t.Fatalf("expected cancellation result, got %v", errorValue)
	}
	if !result.ReplySuppressed {
		t.Fatal("expected caller cancellation to suppress the reply")
	}
	if result.TaskRun.FailureReason == "max_elapsed" {
		t.Fatalf("expected caller cancellation to remain distinct from max_elapsed, got %+v", result.TaskRun)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.limit_stop", "max_elapsed") {
		t.Fatal("expected no max_elapsed stop event for caller cancellation")
	}
}

func TestAgentTurnRunnerPreservesCallerDeadlineBeforeEffortDeadline(t *testing.T) {
	services := newTurnRunnerTestServices(deadlineBlockingLanguageModel{}, TurnOptions{MaxElapsedSecond: 30})
	runContext, cancelRun := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancelRun()

	result, errorValue := services.runner.RunTurn(runContext, AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "시간 제한 전에 취소할 작업",
		ToolSet:           newTestToolSet(nil),
	})

	if errorValue != nil {
		t.Fatalf("expected caller deadline result, got %v", errorValue)
	}
	if !result.ReplySuppressed {
		t.Fatal("expected caller deadline to suppress the reply")
	}
	if result.TaskRun.FailureReason == "max_elapsed" {
		t.Fatalf("expected caller deadline to remain distinct from max_elapsed, got %+v", result.TaskRun)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.limit_stop", "max_elapsed") {
		t.Fatal("expected no max_elapsed stop event for caller deadline")
	}
}

func TestAgentTurnRunnerCancelsToolCallAtExecutionEffortDeadline(t *testing.T) {
	languageModel := newElapsedClosingLanguageModel("slow.tool", `{}`, "작업 시간이 끝나 진행 상황을 저장했습니다.")
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxElapsedSecond: 1})
	toolSet := newTestToolSet([]string{"slow.tool"})
	toolCancelled := make(chan struct{})
	registerTestTool(toolSet, ToolDefinition{Name: "slow.tool"}, func(toolContext context.Context, _ ToolInvocation) (ToolResult, error) {
		<-toolContext.Done()
		close(toolCancelled)
		return ToolFailureResult(FailureExternalService, FailureCodes.OperationFailed, "slow.tool", toolContext.Err().Error()), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "bounded tool call",
		ToolSet:           toolSet,
		PinnedToolNames:   toolSet.ListToolNames(),
		EffortStartedAt:   time.Now().Add(-500 * time.Millisecond),
	})

	if errorValue != nil {
		t.Fatalf("expected bounded limit result, got %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked || result.TaskRun.FailureReason != "max_elapsed" {
		t.Fatalf("expected max_elapsed block, got %+v", result.TaskRun)
	}
	select {
	case <-toolCancelled:
	default:
		t.Fatal("expected in-progress tool call to receive the effort cutoff")
	}
	if languageModel.actionCalls != 1 {
		t.Fatalf("expected no post-cutoff action call, got %d", languageModel.actionCalls)
	}
	assertSingleElapsedClosing(t, languageModel)
}

func TestMaxIterationsClosingDefersToElapsedClosing(t *testing.T) {
	languageModel := newElapsedClosingLanguageModel("task.add", `{"title":"분기 결산 운영 검토"}`, "작업 시간이 끝나 진행 상황을 저장했습니다.")
	languageModel.blockStructured = true
	services := newTurnRunnerTestServices(languageModel, TurnOptions{
		MaxIterationCount: 1,
		MaxToolCallCount:  4,
		MaxElapsedSecond:  1,
	})
	toolSet := newTestCapabilityToolSet([]string{"task.add"})
	registerTestTool(toolSet, ToolDefinition{Name: "task.add"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess(`{"taskID":"task-1"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "분기 결산 운영 검토 업무를 등록해줘",
		ResponseLanguage:  ResponseLanguageKorean,
		ToolSet:           toolSet,
		PinnedToolNames:   toolSet.ListToolNames(),
		EffortStartedAt:   time.Now().Add(-500 * time.Millisecond),
	})

	if errorValue != nil {
		t.Fatalf("expected elapsed result, got %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked || result.TaskRun.FailureReason != "max_elapsed" {
		t.Fatalf("expected max_elapsed to own the terminal result, got %+v", result.TaskRun)
	}
	if languageModel.structuredCalls == 0 {
		t.Fatal("expected max-iterations finalization to be interrupted by the effort deadline")
	}
	assertSingleElapsedClosing(t, languageModel)
}

func TestMaxToolCallsClosingDefersToElapsedClosing(t *testing.T) {
	languageModel := newElapsedClosingLanguageModel("", "", "작업 시간이 끝나 진행 상황을 저장했습니다.")
	languageModel.actionToolNames = []string{"first.tool", "second.tool"}
	languageModel.actionToolInputs = []string{`{"value":"first"}`, `{"value":"second"}`}
	languageModel.blockStructured = true
	services := newTurnRunnerTestServices(languageModel, TurnOptions{
		MaxIterationCount: 4,
		MaxToolCallCount:  1,
		MaxElapsedSecond:  1,
	})
	toolSet := newTestCapabilityToolSet([]string{"first.tool", "second.tool"})
	registerTestTool(toolSet, ToolDefinition{Name: "first.tool"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return testToolSuccess(`{"status":"recorded"}`), nil
	})
	registerTestTool(toolSet, ToolDefinition{Name: "second.tool"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		t.Fatal("expected the tool-call limit before second tool execution")
		return ToolResult{}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "두 단계 업무를 처리해줘",
		ResponseLanguage:  ResponseLanguageKorean,
		ToolSet:           toolSet,
		PinnedToolNames:   toolSet.ListToolNames(),
		EffortStartedAt:   time.Now().Add(-500 * time.Millisecond),
	})

	if errorValue != nil {
		t.Fatalf("expected elapsed result, got %v", errorValue)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked || result.TaskRun.FailureReason != "max_elapsed" {
		t.Fatalf("expected max_elapsed to own the terminal result, got %+v", result.TaskRun)
	}
	assertSingleElapsedClosing(t, languageModel)
}

func assertSingleElapsedClosing(t *testing.T, languageModel *elapsedClosingLanguageModel) {
	t.Helper()
	if languageModel.closingCalls != 1 {
		t.Fatalf("expected exactly one elapsed closing call, got %d", languageModel.closingCalls)
	}
	if languageModel.closingRequest.SchemaName != elapsedReplySchemaName {
		t.Fatalf("expected %s schema provenance, got %q", elapsedReplySchemaName, languageModel.closingRequest.SchemaName)
	}
	if len(languageModel.closingRequest.Tools) != 0 || len(languageModel.closingRequest.ToolChoice) != 0 || languageModel.closingRequest.ParallelToolCalls {
		t.Fatalf("expected no-tools closing request, got %+v", languageModel.closingRequest)
	}
}

func onlyTaskStatus(taskRunService *task.TaskRunService, personID string) task.TaskStatus {
	taskRuns := taskRunService.ListTaskRunByPersonID(personID)
	if len(taskRuns) != 1 {
		return ""
	}
	return taskRuns[0].Status
}

type deadlineBlockingLanguageModel struct{}

func (deadlineBlockingLanguageModel) GenerateResponse(responseContext context.Context, _ string) (string, error) {
	<-responseContext.Done()
	return "", responseContext.Err()
}

func (deadlineBlockingLanguageModel) GenerateStructuredResponse(responseContext context.Context, _ llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	<-responseContext.Done()
	return llm.StructuredResponse{}, responseContext.Err()
}

type elapsedClosingLanguageModel struct {
	actionToolName     string
	actionToolInput    string
	actionToolNames    []string
	actionToolInputs   []string
	closingReply       string
	closingError       error
	closingStarted     chan struct{}
	blockClosing       bool
	blockStructured    bool
	observeTaskStatus  func() task.TaskStatus
	statusAtClosing    task.TaskStatus
	closingHasDeadline bool
	closingRequest     llm.ChatCompletionRequest
	actionCalls        int
	closingCalls       int
	structuredCalls    int
	legacyCalls        int
}

func newElapsedClosingLanguageModel(actionToolName string, actionToolInput string, closingReply string) *elapsedClosingLanguageModel {
	return &elapsedClosingLanguageModel{
		actionToolName:  actionToolName,
		actionToolInput: actionToolInput,
		closingReply:    closingReply,
	}
}

func (languageModel *elapsedClosingLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	languageModel.legacyCalls++
	return "", errors.New("legacy response path is not allowed")
}

func (languageModel *elapsedClosingLanguageModel) GenerateStructuredResponse(responseContext context.Context, _ llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	languageModel.structuredCalls++
	if languageModel.blockStructured {
		<-responseContext.Done()
		return llm.StructuredResponse{}, responseContext.Err()
	}
	return llm.StructuredResponse{}, errors.New("structured path is not allowed after elapsed cutoff")
}

func (languageModel *elapsedClosingLanguageModel) GenerateChatCompletion(responseContext context.Context, request llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	if request.SchemaName == elapsedReplySchemaName {
		return languageModel.generateElapsedClosing(responseContext, request)
	}
	if request.SchemaName != agentActionSchemaName {
		return llm.ChatCompletionResponse{}, errors.New("unexpected chat schema")
	}
	languageModel.actionCalls++
	actionIndex := languageModel.actionCalls - 1
	if actionIndex < len(languageModel.actionToolNames) {
		return llm.ChatCompletionResponse{
			FinishReason: "tool_calls",
			Message: llm.ChatCompletionMessage{
				Role:      "assistant",
				ToolCalls: []llm.ChatCompletionToolCall{nativeAgentActionToolCall(languageModel.actionToolNames[actionIndex], languageModel.actionToolInputs[actionIndex])},
			},
		}, nil
	}
	if languageModel.actionCalls == 1 && languageModel.actionToolName != "" {
		return llm.ChatCompletionResponse{
			FinishReason: "tool_calls",
			Message: llm.ChatCompletionMessage{
				Role:      "assistant",
				ToolCalls: []llm.ChatCompletionToolCall{nativeAgentActionToolCall(languageModel.actionToolName, languageModel.actionToolInput)},
			},
		}, nil
	}
	<-responseContext.Done()
	return llm.ChatCompletionResponse{}, responseContext.Err()
}

func (languageModel *elapsedClosingLanguageModel) generateElapsedClosing(responseContext context.Context, request llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	languageModel.closingCalls++
	languageModel.closingRequest = request
	_, languageModel.closingHasDeadline = responseContext.Deadline()
	if languageModel.observeTaskStatus != nil {
		languageModel.statusAtClosing = languageModel.observeTaskStatus()
	}
	if languageModel.closingStarted != nil {
		close(languageModel.closingStarted)
	}
	if languageModel.blockClosing {
		<-responseContext.Done()
		return llm.ChatCompletionResponse{}, responseContext.Err()
	}
	if languageModel.closingError != nil {
		return llm.ChatCompletionResponse{}, languageModel.closingError
	}
	return llm.ChatCompletionResponse{
		FinishReason: "stop",
		Message:      llm.ChatCompletionMessage{Role: "assistant", Content: languageModel.closingReply},
	}, nil
}

func TestEstimateRemainingToolCallCountUsesObservedAverageLatency(t *testing.T) {
	cases := []struct {
		name                   string
		elapsed                time.Duration
		maxElapsed             time.Duration
		completedToolCallCount int
		want                   int
	}{
		{name: "no history yet", elapsed: 0, maxElapsed: 10 * time.Minute, completedToolCallCount: 0, want: defaultRemainingCallEstimate},
		{name: "no completed calls", elapsed: 5 * time.Minute, maxElapsed: 10 * time.Minute, completedToolCallCount: 0, want: defaultRemainingCallEstimate},
		{name: "no elapsed limit", elapsed: 5 * time.Minute, maxElapsed: 0, completedToolCallCount: 5, want: defaultRemainingCallEstimate},
		{name: "floors at one when budget nearly exhausted", elapsed: 39 * time.Minute, maxElapsed: 40 * time.Minute, completedToolCallCount: 20, want: minimumRemainingCallEstimate},
		{name: "floors at one when budget already exhausted", elapsed: 41 * time.Minute, maxElapsed: 40 * time.Minute, completedToolCallCount: 20, want: minimumRemainingCallEstimate},
		{name: "caps at five for a fast-running task", elapsed: time.Minute, maxElapsed: 40 * time.Minute, completedToolCallCount: 10, want: maximumRemainingCallEstimate},
		{name: "computes from observed average pace", elapsed: 36 * time.Minute, maxElapsed: 40 * time.Minute, completedToolCallCount: 36, want: 4},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			estimate := estimateRemainingToolCallCount(testCase.elapsed, testCase.maxElapsed, testCase.completedToolCallCount)
			if estimate != testCase.want {
				t.Fatalf("expected estimate %d, got %d", testCase.want, estimate)
			}
		})
	}
}

func TestLimitPressureWarningInjectsSingleWrapUpMessageAtEightyPercent(t *testing.T) {
	runner := &AgentTurnRunner{options: TurnOptions{
		MaxIterationCount: 72,
		MaxToolCallCount:  30,
		MaxElapsedSecond:  int((40 * time.Minute).Seconds()),
	}}
	sentWarnings := map[string]bool{}

	belowThreshold := runner.nextLimitPressureWarning(1, 5, 30*time.Minute, 1, sentWarnings)
	if belowThreshold != nil {
		t.Fatalf("expected no warning below 80%% elapsed, got %+v", belowThreshold)
	}

	warning := runner.nextLimitPressureWarning(1, 10, 34*time.Minute, 1, sentWarnings)
	if warning == nil || warning.Stage != limitPressureStageWrapUp {
		t.Fatalf("expected wrap_up warning at 80%% elapsed, got %+v", warning)
	}
	if warning.Observation == nil {
		t.Fatal("expected the wrap_up stage to inject an observation")
	}
	if !strings.Contains(warning.Observation.ContentText(), "Budget check: roughly") {
		t.Fatalf("expected the runtime-computed remaining call estimate in the message, got %q", warning.Observation.ContentText())
	}
	sentWarnings[warning.Stage] = true

	repeat := runner.nextLimitPressureWarning(1, 11, 35*time.Minute, 1, sentWarnings)
	if repeat != nil {
		t.Fatalf("expected the wrap_up warning to fire only once, got %+v", repeat)
	}
}

func TestLimitPressureWarningNarrowsPaletteWithoutTextAtNinetyTwoPercent(t *testing.T) {
	runner := &AgentTurnRunner{options: TurnOptions{
		MaxIterationCount: 72,
		MaxToolCallCount:  30,
		MaxElapsedSecond:  int((40 * time.Minute).Seconds()),
	}}
	sentWarnings := map[string]bool{limitPressureStageWrapUp: true}

	warning := runner.nextLimitPressureWarning(1, 12, 38*time.Minute, 1, sentWarnings)
	if warning == nil || warning.Stage != limitPressureStageNarrowPalette {
		t.Fatalf("expected narrow_palette warning at 92%% elapsed, got %+v", warning)
	}
	if warning.Observation != nil {
		t.Fatalf("expected no injected text at the narrow_palette stage, got %q", warning.Observation.ContentText())
	}
	if warning.EventBody["stage"] != limitPressureStageNarrowPalette {
		t.Fatalf("expected the ledger event to record the stage, got %+v", warning.EventBody)
	}
	if _, hasEstimate := warning.EventBody["remainingCallEstimate"]; !hasEstimate {
		t.Fatalf("expected the ledger event to record remainingCallEstimate, got %+v", warning.EventBody)
	}
}

func TestRequestForStepNarrowsActionPaletteAtNinetyTwoPercentElapsed(t *testing.T) {
	runner := &AgentTurnRunner{options: TurnOptions{
		MaxIterationCount: 72,
		MaxToolCallCount:  30,
		MaxElapsedSecond:  int((40 * time.Minute).Seconds()),
	}}
	toolSet := newTestCapabilityToolSet([]string{"file.read", FileDeliverToolName})
	request := AgentTurnRequest{
		ToolSet:         toolSet,
		PinnedToolNames: []string{"file.read", FileDeliverToolName},
		OutcomeContract: OutcomeContract{ArtifactRequirement: ArtifactRequirementRequired},
	}

	belowNarrowStage := agentTaskState{IterationCount: 1, ToolCallCount: 5}
	request.EffortStartedAt = time.Now().Add(-30 * time.Minute)
	beforeNarrowing := runner.requestForStep(context.Background(), request, belowNarrowStage)
	exploratoryToolNames := beforeNarrowing.ToolSet.ListToolNames()
	if !stringSliceContains(exploratoryToolNames, "file.read") || !stringSliceContains(exploratoryToolNames, FileDeliverToolName) {
		t.Fatalf("expected the full working set below the narrow stage, got %v", exploratoryToolNames)
	}

	atNarrowStage := agentTaskState{IterationCount: 1, ToolCallCount: 12}
	request.EffortStartedAt = time.Now().Add(-38 * time.Minute)
	afterNarrowing := runner.requestForStep(context.Background(), request, atNarrowStage)
	narrowedToolNames := afterNarrowing.ToolSet.ListToolNames()
	if stringSliceContains(narrowedToolNames, "file.read") {
		t.Fatalf("expected exploration tools dropped at the narrow_palette stage, got %v", narrowedToolNames)
	}
	if !stringSliceContains(narrowedToolNames, FileDeliverToolName) {
		t.Fatalf("expected the delivery-required tool retained at the narrow_palette stage, got %v", narrowedToolNames)
	}

	actionSchema := afterNarrowing.ToolSet.ActionSchema(false, nil, false)
	if !strings.Contains(actionSchema, `"enum":["finish"]`) {
		t.Fatalf("expected the finish action to remain available at the narrow_palette stage, got %s", actionSchema)
	}
	if !strings.Contains(actionSchema, `"enum":["fail"]`) {
		t.Fatalf("expected the fail action to remain available at the narrow_palette stage, got %s", actionSchema)
	}
	if strings.Contains(actionSchema, `"file.read"`) {
		t.Fatalf("expected no continue variant for the dropped exploration tool, got %s", actionSchema)
	}
}
