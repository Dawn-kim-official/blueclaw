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
}

func TestOutboundReplyJSONPreservesInlineAttachmentPayload(t *testing.T) {
	reply := OutboundReply{
		Message: "attached",
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
	if adapter.sentReplies[0].message != adapter.NotInvitedReply() {
		t.Fatalf("expected not invited reply, got %q", adapter.sentReplies[0].message)
	}
}

func TestConnectorRuntimeSendsDynamicReplyWhenTaskDoesNotComplete(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{errorValue: errors.New("model unavailable")})

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testInboundEvent("message-1"))
	if errorValue != nil {
		t.Fatalf("expected incomplete task to be recorded: %v", errorValue)
	}

	if result.TaskRunID == "" {
		t.Fatal("expected task run id")
	}
	if result.Reason != "task_not_completed" {
		t.Fatalf("expected task_not_completed result, got %+v", result)
	}
	if result.ReplyDispatchID != "dispatch-1" {
		t.Fatalf("expected dynamic failure reply dispatch id, got %q", result.ReplyDispatchID)
	}
	if len(adapter.sentReplies) != 1 {
		t.Fatalf("expected one dynamic failure reply, got %+v", adapter.sentReplies)
	}
	if strings.Contains(adapter.sentReplies[0].message, "I am having trouble reaching the language model") || strings.Contains(adapter.sentReplies[0].message, "model configuration") {
		t.Fatalf("expected non-static failure reply, got %q", adapter.sentReplies[0].message)
	}
}

func TestConnectorRuntimeRecoversIncompleteAttachmentClaims(t *testing.T) {
	connectorRuntime, _ := newTestConnectorRuntime(t, testLanguageModel{reply: "unused"})
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")
	event.Prompt = "파일 만들어줘"
	dispatchID, isSent := connectorRuntime.sendIncompleteTaskReply(
		context.Background(),
		"test",
		event,
		"task-1",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agent.AgentTurnResult{FinalReply: "파일을 생성해 첨부했습니다."},
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if !isSent || dispatchID != "dispatch-1" {
		t.Fatalf("expected recovered incomplete reply, got dispatchID=%q sent=%v", dispatchID, isSent)
	}
	if len(sentReplies) != 1 {
		t.Fatalf("expected one recovered reply, got %+v", sentReplies)
	}
	if strings.Contains(sentReplies[0].Message, "첨부했습니다") || strings.Contains(sentReplies[0].Message, "보냈습니다") {
		t.Fatalf("expected recovered reply without delivery claim, got %q", sentReplies[0].Message)
	}
}

func TestConnectorRuntimeRecoversIncompleteUnattachedFilenames(t *testing.T) {
	connectorRuntime, _ := newTestConnectorRuntime(t, testLanguageModel{reply: "unused"})
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")
	event.Prompt = "html 파일 만들어줘"
	dispatchID, isSent := connectorRuntime.sendIncompleteTaskReply(
		context.Background(),
		"test",
		event,
		"task-1",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agent.AgentTurnResult{FinalReply: "아래 파일을 확인해 주세요.\n[Hermes_Agent_Slide_Part1.html]"},
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if !isSent || dispatchID != "dispatch-1" {
		t.Fatalf("expected recovered incomplete reply, got dispatchID=%q sent=%v", dispatchID, isSent)
	}
	if len(sentReplies) != 1 {
		t.Fatalf("expected one recovered reply, got %+v", sentReplies)
	}
	if strings.Contains(sentReplies[0].Message, "Hermes_Agent_Slide_Part1.html") {
		t.Fatalf("expected recovered reply without unattached filename, got %q", sentReplies[0].Message)
	}
}

func TestConnectorRuntimeAddsSenderToRecoveryActions(t *testing.T) {
	connectorRuntime, _ := newTestConnectorRuntime(t, testLanguageModel{reply: "unused"})
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")
	event.SenderID = "sender-user-1"

	_, isSent := connectorRuntime.sendIncompleteTaskReply(
		context.Background(),
		"test",
		event,
		"task-1",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agent.AgentTurnResult{
			FinalReply: "Companion 연결이 필요합니다.",
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

func TestConnectorRuntimeInjectsRequesterMemoryIntoLanguageModel(t *testing.T) {
	languageModel := &recordingLanguageModel{reply: "기억했습니다"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	memoryService := &memory.MemoryService{}
	memoryService.StoreMemoryFact(memory.MemoryFact{
		ScopeType:   memory.ScopeTypeUser,
		NamespaceID: "user:person-1",
		Content:     "사용자는 Graphiti 메모리 설계를 선택했다.",
	})
	connectorRuntime.UseMemoryService(memoryService)

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
	memoryService := &memory.MemoryService{}
	memoryService.StoreMemoryFact(memory.MemoryFact{
		ScopeType:   memory.ScopeTypeUser,
		NamespaceID: "user:person-1",
		Content:     "사용자는 간결한 설계를 선호한다.",
	})
	connectorRuntime.UseMemoryService(memoryService)

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}

	if !strings.Contains(languageModel.request.Messages[1].Content, "conversation.history") {
		t.Fatalf("expected tool context first, got %q", languageModel.request.Messages[1].Content)
	}
	visibleContextIndex := messageIndex(languageModel.request.Messages, "admin: 이전 메시지")
	memoryIndex := messageIndex(languageModel.request.Messages, "간결한 설계")
	promptIndex := messageIndex(languageModel.request.Messages, event.Prompt)
	if visibleContextIndex < 0 || memoryIndex < 0 || promptIndex < 0 {
		t.Fatalf("expected visible context, memory, and prompt messages, got %+v", languageModel.request.Messages)
	}
	if !(visibleContextIndex < memoryIndex && memoryIndex < promptIndex) {
		t.Fatalf("expected visible context before memory before prompt, got visible=%d memory=%d prompt=%d", visibleContextIndex, memoryIndex, promptIndex)
	}
}

func TestConnectorRuntimeRunsAgentHistoryToolAndSendsOneFinalReply(t *testing.T) {
	languageModel := &connectorSequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"conversation.history","toolInput":{"limit":20}}`,
		connectorFinalReplyWithEvidence("이전 대화를 확인했습니다", "obs-001", "conversation.history", 0),
	}}
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
	if len(adapter.historyCursors) != 1 || adapter.historyCursors[0] != "cursor-1" {
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
	languageModel := &connectorSequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"schedule.create","toolInput":{"name":"daily research brief","prompt":"매일 업계 뉴스를 조사해서 핵심만 보고해줘.","kind":"cron","cronExpression":"0 7 * * *","timeZone":"Asia/Seoul","platform":"spoofed","conversationID":"spoofed","replyTargetID":"spoofed"}}`,
		connectorFinalReply("매일 아침 7시에 조사해서 알려드릴게요."),
	}}
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
	if taskSchedule.Prompt != "매일 업계 뉴스를 조사해서 핵심만 보고해줘." {
		t.Fatalf("expected stored research prompt, got %q", taskSchedule.Prompt)
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

func TestConnectorRuntimeClassifiesApprovalReplyBeforeStartingNewTask(t *testing.T) {
	invokedTools := []string{}
	languageModel := &connectorSequenceLanguageModel{contents: []string{
		`{"classification":"bounded_task","taskShape":"approval_gated_task","effortLevel":"standard","requestedOutputFormats":null,"reason":"calendar delete needs approval first","userFacingReply":""}`,
		`{"action":"call_tool","toolName":"approval.request","toolInput":{"message":"내일 휴가 일정을 캘린더에서 삭제하겠습니다. 진행해도 될까요?"}}`,
		`{"isApproval":true,"reason":"응 is an affirmative answer to the pending approval question."}`,
		`{"classification":"bounded_task","taskShape":"maintenance_task","effortLevel":"standard","requestedOutputFormats":null,"reason":"approved calendar tool work","userFacingReply":""}`,
		`{"action":"call_tool","toolName":"calendar.event.delete","toolInput":{"eventID":"event-1","userConfirmed":true}}`,
		connectorFinalReplyWithEvidence("내일 휴가 일정을 캘린더에서 삭제했습니다.", "obs-001", "calendar.event.delete", 0),
	}}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntime.agentKernel.UseIntakeLanguageModelProvider(languageModel)
	connectorRuntime.agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true})
	useTestConnectorSkill(connectorRuntime, connectorCalendarSkill())
	connectorRuntime.UseAllowedToolNames([]string{"conversation.history", "memory.search", "approval.request", "calendar.event.add", "calendar.event.delete"})
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

	secondEvent := testInboundEvent("message-2")
	secondEvent.Prompt = "응"
	secondResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, secondEvent)
	if errorValue != nil {
		t.Fatalf("expected approval reply to process: %v", errorValue)
	}

	if secondResult.TaskRunID == firstResult.TaskRunID || secondResult.TaskRunID == "" {
		t.Fatalf("expected approved continuation task, got first=%q second=%q", firstResult.TaskRunID, secondResult.TaskRunID)
	}
	if len(languageModel.requests) != 6 {
		t.Fatalf("expected approval classification before continuation turn, got %d requests", len(languageModel.requests))
	}
	if languageModel.requests[2].StructuredOutputSchema.Name != "blueclaw_approval_reply_decision" {
		t.Fatalf("expected third model request to classify approval, got %q", languageModel.requests[2].StructuredOutputSchema.Name)
	}
	if !structuredMessagesContain(languageModel.requests[4].Messages, "The user has approved this pending action") {
		t.Fatalf("expected continuation prompt to carry approval context, got %+v", languageModel.requests[4].Messages)
	}
	if len(invokedTools) != 1 || invokedTools[0] != "calendar.event.delete/invoke" {
		t.Fatalf("expected calendar delete tool invocation, got %+v", invokedTools)
	}
	if len(adapter.sentReplies) != 2 || adapter.sentReplies[1].message != "내일 휴가 일정을 캘린더에서 삭제했습니다." {
		t.Fatalf("expected final approved reply, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeAddsCalendarEventWithoutApproval(t *testing.T) {
	invokedTools := []string{}
	languageModel := &connectorSequenceLanguageModel{contents: []string{
		`{"classification":"bounded_task","taskShape":"maintenance_task","effortLevel":"standard","requestedOutputFormats":null,"reason":"calendar add is non-destructive tool work","userFacingReply":""}`,
		`{"action":"call_tool","toolName":"calendar.event.add","toolInput":{"title":"휴가","startISO":"2026-05-09","endISO":"2026-05-10","isAllDay":true}}`,
		connectorFinalReplyWithEvidence("내일 휴가 일정을 캘린더에 추가했습니다.", "obs-001", "calendar.event.add", 0),
	}}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntime.agentKernel.UseIntakeLanguageModelProvider(languageModel)
	connectorRuntime.agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true})
	useTestConnectorSkill(connectorRuntime, connectorCalendarSkill())
	connectorRuntime.UseAllowedToolNames([]string{"conversation.history", "memory.search", "approval.request", "calendar.event.add", "calendar.event.delete"})
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
	if connectorContainsSchemaName(languageModel.requests, "blueclaw_approval_reply_decision") {
		t.Fatalf("expected no approval continuation classification, got %+v", connectorRequestSchemaNames(languageModel.requests))
	}
	if len(invokedTools) != 1 || invokedTools[0] != "calendar.event.add/invoke" {
		t.Fatalf("expected direct calendar add invocation, got %+v", invokedTools)
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "내일 휴가 일정을 캘린더에 추가했습니다." {
		t.Fatalf("expected final add reply, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeReadsTypedCapabilityToolResponse(t *testing.T) {
	languageModel := &connectorSequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"browser.snapshot","toolInput":{}}`,
		connectorFinalReplyWithEvidence("브라우저를 확인했습니다", "obs-001", "browser.snapshot", 0),
	}}
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

	if len(languageModel.requests) < 2 || !structuredMessagesContain(languageModel.requests[1].Messages, "https://example.com") {
		t.Fatalf("expected typed capability result to be available as tool observation, got %+v", languageModel.requests)
	}
	if adapter.sentReplies[0].message != "브라우저를 확인했습니다" {
		t.Fatalf("expected final reply, got %q", adapter.sentReplies[0].message)
	}
	if len(adapter.sentReplies[0].attachments) != 1 || adapter.sentReplies[0].attachments[0].DevicePath != "/tmp/internkim-companion-files/screen.png" {
		t.Fatalf("expected final reply attachment, got %+v", adapter.sentReplies[0].attachments)
	}
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
	if !toolResult.IsError || toolResult.Content != "tool is not allowed" {
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
}

func TestConnectorRuntimeStoresUserMemoryAcrossConversations(t *testing.T) {
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

	if len(graphStore.episodes) != 2 {
		t.Fatalf("expected Graphiti episode ingestion for both routed messages, got %d", len(graphStore.episodes))
	}
	if !containsEpisodeNamespace(graphStore.episodes[0], "user:person-1") {
		t.Fatalf("expected user namespace ingestion, got %+v", graphStore.episodes[0].Namespaces)
	}
	if !structuredMessagesContain(languageModel.request.Messages, "민수") {
		t.Fatalf("expected user memory from graph search in direct reply context, got %+v", languageModel.request.Messages)
	}
}

func TestConnectorRuntimeIngestsMemoryWhenReplySendFails(t *testing.T) {
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
	if len(graphStore.episodes) != 1 {
		t.Fatalf("expected memory ingestion before reply success, got %d", len(graphStore.episodes))
	}
}

func TestConnectorRuntimeIngestsMemoryWhenReplyIsBlocked(t *testing.T) {
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
	if len(graphStore.episodes) != 1 {
		t.Fatalf("expected memory ingestion before connector blocking, got %d", len(graphStore.episodes))
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
	senderEmail        string
	sendReplyError     error
	httpParseResult    HTTPParseResult
	sentReplies        []testReply
	progressStarts     []ReplyTarget
	progressStops      []ReplyTarget
	progressStopErrors []error
	historyCursors     []string
}

type testReply struct {
	target          ReplyTarget
	message         string
	attachments     []agent.FileAttachment
	recoveryActions []agent.RecoveryAction
}

type testConnectorQueueRepository struct {
	pendingEvents   []QueuedConnectorEvent
	succeededEvents []ConnectorRuntimeResult
	pendingReplies  []QueuedConnectorReply
	sentReplies     []string
}

type connectorTaskScheduleRepository struct {
	taskSchedules []task.TaskSchedule
}

func (repository *connectorTaskScheduleRepository) UpsertTaskSchedule(taskSchedule task.TaskSchedule) error {
	repository.taskSchedules = append(repository.taskSchedules, taskSchedule)
	return nil
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

func (repository *testConnectorQueueRepository) MarkConnectorReplyFailed(QueuedConnectorReply, error, time.Time) error {
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

func (adapter *testAdapter) StartProgress(_ context.Context, target ReplyTarget) error {
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
	adapter.sentReplies = append(adapter.sentReplies, testReply{target: target, message: reply.Message, attachments: reply.Attachments, recoveryActions: reply.RecoveryActions})
	return "dispatch-" + strconv.Itoa(len(adapter.sentReplies)), nil
}

func (adapter *testAdapter) FetchHistory(_ context.Context, historyCursor string, _ int) (VisibleContext, error) {
	adapter.historyCursors = append(adapter.historyCursors, historyCursor)
	return VisibleContext{
		Messages: []VisibleContextMessage{{Speaker: "admin", Text: "older message"}},
	}, nil
}

func (adapter *testAdapter) NotInvitedReply() string {
	return "not invited"
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
	return llm.StructuredResponse{Content: connectorFinalReply(languageModel.reply)}, nil
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
	return llm.StructuredResponse{Content: connectorFinalReply(languageModel.reply)}, nil
}

type connectorSequenceLanguageModel struct {
	contents []string
	requests []llm.StructuredResponseRequest
}

func (languageModel *connectorSequenceLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *connectorSequenceLanguageModel) GenerateStructuredResponse(_ context.Context, structuredResponseRequest llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	languageModel.requests = append(languageModel.requests, structuredResponseRequest)
	index := len(languageModel.requests) - 1
	if index >= len(languageModel.contents) {
		index = len(languageModel.contents) - 1
	}
	return llm.StructuredResponse{Content: languageModel.contents[index]}, nil
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
	return llm.StructuredResponse{Content: connectorFinalReply("ok")}, nil
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

func connectorRequestSchemaNames(requests []llm.StructuredResponseRequest) []string {
	names := []string{}
	for _, request := range requests {
		names = append(names, request.StructuredOutputSchema.Name)
	}
	return names
}

func connectorContainsSchemaName(requests []llm.StructuredResponseRequest, schemaName string) bool {
	for _, request := range requests {
		if request.StructuredOutputSchema.Name == schemaName {
			return true
		}
	}
	return false
}

func findAgentToolDefinition(toolDefinitions []agent.ToolDefinition, toolName string) (agent.ToolDefinition, bool) {
	for _, toolDefinition := range toolDefinitions {
		if toolDefinition.Name == toolName {
			return toolDefinition, true
		}
	}
	return agent.ToolDefinition{}, false
}

func connectorFinalReply(reply string) string {
	return `{"action":"final_reply","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[],"finalReply":` + strconv.Quote(reply) + `}`
}

func connectorFinalReplyWithEvidence(reply string, observationID string, toolName string, attachmentIndex int) string {
	return `{"action":"final_reply","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":` + strconv.Quote(observationID) + `,"toolName":` + strconv.Quote(toolName) + `,"attachmentIndex":` + strconv.Itoa(attachmentIndex) + `}],"finalReply":` + strconv.Quote(reply) + `}`
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
		Prompt:       "Use schedule.create with executionMode message for exact reminders and executionMode agent for scheduled work.",
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
