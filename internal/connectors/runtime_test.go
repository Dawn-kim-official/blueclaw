package connectors

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/identity"
	"blueclaw/internal/llm"
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
	if len(adapter.typingTargets) != 1 {
		t.Fatalf("expected one immediate typing event, got %d", len(adapter.typingTargets))
	}
}

func TestConnectorRuntimeSuppressesSelfMessage(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "ignored"})
	event := testInboundEvent("message-1")
	event.SenderUserID = "bot-user"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected self message to be ignored: %v", errorValue)
	}

	if !result.Ignored || result.Reason != "self" {
		t.Fatalf("expected self suppression, got %+v", result)
	}
	if len(adapter.sentReplies) != 0 {
		t.Fatalf("expected no reply, got %d", len(adapter.sentReplies))
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

func TestConnectorRuntimeUsesFallbackReplyWhenLanguageModelFails(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{errorValue: errors.New("model unavailable")})

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testInboundEvent("message-1"))
	if errorValue != nil {
		t.Fatalf("expected fallback reply: %v", errorValue)
	}

	if result.TaskRunID == "" {
		t.Fatal("expected task run id")
	}
	if len(adapter.sentReplies) != 1 {
		t.Fatalf("expected one reply, got %d", len(adapter.sentReplies))
	}
	if adapter.sentReplies[0].message != "Working on it: "+result.TaskRunID {
		t.Fatalf("expected fallback reply, got %q", adapter.sentReplies[0].message)
	}
}

func TestConnectorRuntimeRoutesDirectChannelRootAndThread(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "reply"})

	directEvent := testInboundEvent("direct-message")
	directEvent.ConversationID = "direct-1"
	directEvent.ChannelType = "direct"
	channelRootEvent := testInboundEvent("channel-root")
	channelRootEvent.ConversationID = "channel-1"
	channelRootEvent.ChannelType = "channel"
	threadEvent := testInboundEvent("thread-message")
	threadEvent.ConversationID = "channel-1"
	threadEvent.ChannelType = "channel"
	threadEvent.RootMessageID = "root-1"

	_, _ = connectorRuntime.HandleInboundEvent(context.Background(), adapter, directEvent)
	_, _ = connectorRuntime.HandleInboundEvent(context.Background(), adapter, channelRootEvent)
	_, _ = connectorRuntime.HandleInboundEvent(context.Background(), adapter, threadEvent)

	if adapter.sentReplies[0].target.ParentID != "" {
		t.Fatalf("expected direct reply without parent, got %q", adapter.sentReplies[0].target.ParentID)
	}
	if adapter.sentReplies[1].target.ParentID != "channel-root" {
		t.Fatalf("expected channel root reply to create thread, got %q", adapter.sentReplies[1].target.ParentID)
	}
	if adapter.sentReplies[2].target.ParentID != "root-1" {
		t.Fatalf("expected thread reply to use root, got %q", adapter.sentReplies[2].target.ParentID)
	}
}

type testAdapter struct {
	senderEmail   string
	sentReplies   []testReply
	typingTargets []ReplyTarget
}

type testReply struct {
	target  ReplyTarget
	message string
}

func (adapter *testAdapter) Name() string {
	return "test"
}

func (adapter *testAdapter) ParseHTTPEvent(context.Context, *http.Request) (HTTPParseResult, error) {
	return HTTPParseResult{}, nil
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

func (adapter *testAdapter) ResolveBotUserID(context.Context) (string, error) {
	return "bot-user", nil
}

func (adapter *testAdapter) ResolveConversationKind(_ context.Context, event PlatformInboundEvent) (ConversationKind, error) {
	return ConversationKind{IsDirect: event.ChannelType == "direct"}, nil
}

func (adapter *testAdapter) PublishTyping(_ context.Context, _ string, target ReplyTarget) error {
	adapter.typingTargets = append(adapter.typingTargets, target)
	return nil
}

func (adapter *testAdapter) SendReply(_ context.Context, target ReplyTarget, message string) (string, error) {
	adapter.sentReplies = append(adapter.sentReplies, testReply{target: target, message: message})
	return "dispatch-" + strconv.Itoa(len(adapter.sentReplies)), nil
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
	return llm.StructuredResponse{Content: `{"reply":"` + languageModel.reply + `"}`}, nil
}

func newTestConnectorRuntime(t *testing.T, languageModel llm.LanguageModelProvider) (*ConnectorRuntime, *testAdapter) {
	t.Helper()

	identityService := identity.NewIdentityService(policy.PolicyProjection{
		PersonIDByEmail: map[string]string{"invited@example.com": "person-1"},
	})
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := agent.NewAgentKernel(taskRunService, task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(languageModel)

	connectorRuntime := NewConnectorRuntime(identityService, agentKernel, nil)
	connectorRuntime.UseTypingTiming(time.Hour, time.Hour)
	adapter := &testAdapter{senderEmail: "invited@example.com"}
	connectorRuntime.RegisterAdapter(adapter)
	return connectorRuntime, adapter
}

func testInboundEvent(messageID string) PlatformInboundEvent {
	return PlatformInboundEvent{
		Platform:       "test",
		Source:         "test",
		EventID:        messageID,
		ConversationID: "direct-1",
		MessageID:      messageID,
		SenderUserID:   "sender-user",
		ChannelType:    "direct",
		Text:           "hello",
	}
}
