package connectors

import (
	"context"
	"errors"
	"strings"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/llm"
)

func TestDebugControlRepliesWithLastFailureSnapshot(t *testing.T) {
	connectorRuntime, _ := newTestConnectorRuntime(t, failingConnectorLanguageModel{})
	connectorRuntime.UseAdminTaskLinkBaseURL("https://demo.intern.kim")
	failedResult := connectorRuntime.agentKernel.CompleteLaunchFailure(context.Background(), agent.AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "direct-1",
		Prompt:            "발표자료 만들어줘",
		ResponseLanguage:  "ko",
	}, "launch", "build_tool_set", errors.New("tool registry mismatch"))

	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-debug")
	event.Prompt = "/debug"
	result, isHandled := connectorRuntime.handleDebugControlIfRequested(
		context.Background(),
		"test",
		event,
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		"person-1",
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if !isHandled || result.Reason != "debug_control" {
		t.Fatalf("expected debug control handling, got %+v", result)
	}
	if len(sentReplies) != 1 {
		t.Fatalf("expected one debug reply, got %+v", sentReplies)
	}
	message := sentReplies[0].Message
	if !strings.Contains(message, failedResult.TaskRun.TaskRunID) {
		t.Fatalf("expected run id in debug reply, got %q", message)
	}
	if !strings.Contains(message, "tool registry mismatch") {
		t.Fatalf("expected safe failure summary, got %q", message)
	}
	if !strings.Contains(message, "notice: raw_error") {
		t.Fatalf("expected notice generation source, got %q", message)
	}
	if !strings.Contains(message, "https://demo.intern.kim/tasks/"+failedResult.TaskRun.TaskRunID) {
		t.Fatalf("expected admin link, got %q", message)
	}
}

func TestDebugControlReportsNoFailureInConversation(t *testing.T) {
	connectorRuntime, _ := newTestConnectorRuntime(t, failingConnectorLanguageModel{})

	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-debug")
	event.Prompt = "/debug"
	_, isHandled := connectorRuntime.handleDebugControlIfRequested(
		context.Background(),
		"test",
		event,
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		"person-1",
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if !isHandled || len(sentReplies) != 1 {
		t.Fatalf("expected handled no-failure reply, got %+v", sentReplies)
	}
	if sentReplies[0].Message != "이 대화에는 실패한 작업이 없습니다." {
		t.Fatalf("expected no-failure reply, got %q", sentReplies[0].Message)
	}
}

type failingConnectorLanguageModel struct{}

func (failingConnectorLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("connector test model unavailable")
}

func (failingConnectorLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, errors.New("connector test model unavailable")
}
