package bluecollar

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/llm"
	"github.com/Dawn-kim-official/blueclaw/internal/model"
	"github.com/Dawn-kim-official/blueclaw/internal/taskstate"
)

func TestAgentKernelRejectsProviderWithoutChatCompletion(t *testing.T) {
	taskEventService := taskstate.NewTaskEventService()
	taskRunService := taskstate.NewTaskRunService(taskEventService)
	taskStepService := taskstate.NewTaskStepService()
	agentKernel := NewAgentKernel(taskRunService, taskStepService)
	replyProvider := &structuredOnlyReplyProvider{}
	agentKernel.UseLanguageModelProvider(replyProvider)

	_, errorValue := agentKernel.GenerateReply(context.Background(), "hello")
	if errorValue == nil || errorValue.Error() != "language model provider does not support chat completion" {
		t.Fatalf("expected unavailable chat completion error, got %v", errorValue)
	}
	if replyProvider.structuredCallCount != 0 {
		t.Fatalf("expected missing chat completion not to downgrade to structured generation, got %d calls", replyProvider.structuredCallCount)
	}
}

func TestAgentKernelGeneratesChatReplyWithoutTools(t *testing.T) {
	taskEventService := taskstate.NewTaskEventService()
	taskRunService := taskstate.NewTaskRunService(taskEventService)
	taskStepService := taskstate.NewTaskStepService()
	agentKernel := NewAgentKernel(taskRunService, taskStepService)
	replyProvider := &chatReplyProvider{
		response: model.ChatCompletionResponse{Message: model.ChatCompletionMessage{Role: "assistant", Content: "  hello from chat  "}},
	}
	agentKernel.UseLanguageModelProvider(replyProvider)

	reply, errorValue := agentKernel.GenerateReply(context.Background(), "hello")
	if errorValue != nil {
		t.Fatalf("expected chat reply generation: %v", errorValue)
	}
	if reply != "hello from chat" {
		t.Fatalf("expected trimmed chat reply, got %q", reply)
	}
	if len(replyProvider.request.Tools) != 0 {
		t.Fatalf("expected no chat tools, got %+v", replyProvider.request.Tools)
	}
	if replyProvider.request.SchemaName != "blueclaw_reply" {
		t.Fatalf("expected blueclaw_reply schema name, got %q", replyProvider.request.SchemaName)
	}
	expectedMessages := chatMessages(buildReplyMessagesWithInstructions("hello", VisibleContext{}, nil, ""))
	if !reflect.DeepEqual(replyProvider.request.Messages, expectedMessages) {
		t.Fatalf("expected existing reply messages to be preserved, got %+v", replyProvider.request.Messages)
	}
	if replyProvider.structuredCallCount != 0 {
		t.Fatalf("expected chat reply not to call structured generation, got %d calls", replyProvider.structuredCallCount)
	}
}

func TestAgentKernelResolvesChatReplyThroughFallbackProvider(t *testing.T) {
	taskEventService := taskstate.NewTaskEventService()
	taskRunService := taskstate.NewTaskRunService(taskEventService)
	taskStepService := taskstate.NewTaskStepService()
	agentKernel := NewAgentKernel(taskRunService, taskStepService)
	replyProvider := &chatReplyProvider{
		response: model.ChatCompletionResponse{Message: model.ChatCompletionMessage{Content: "fallback chat"}},
	}
	agentKernel.UseLanguageModelProvider(llm.FallbackLanguageModelProvider{
		PrimaryProvider:  staticReplyProvider{content: `{"reply":"structured"}`},
		FallbackProvider: replyProvider,
	})

	reply, errorValue := agentKernel.GenerateReply(context.Background(), "hello")
	if errorValue != nil {
		t.Fatalf("expected fallback chat reply generation: %v", errorValue)
	}
	if reply != "fallback chat" {
		t.Fatalf("expected fallback chat reply, got %q", reply)
	}
}

func TestAgentKernelRejectsEmptyChatReply(t *testing.T) {
	taskEventService := taskstate.NewTaskEventService()
	taskRunService := taskstate.NewTaskRunService(taskEventService)
	taskStepService := taskstate.NewTaskStepService()
	agentKernel := NewAgentKernel(taskRunService, taskStepService)
	replyProvider := &chatReplyProvider{
		response: model.ChatCompletionResponse{Message: model.ChatCompletionMessage{Content: "  "}},
	}
	agentKernel.UseLanguageModelProvider(replyProvider)

	_, errorValue := agentKernel.GenerateReply(context.Background(), "hello")
	if errorValue == nil || errorValue.Error() != "language model reply is empty" {
		t.Fatalf("expected empty chat reply error, got %v", errorValue)
	}
	if replyProvider.structuredCallCount != 0 {
		t.Fatalf("expected empty chat reply not to use structured fallback, got %d calls", replyProvider.structuredCallCount)
	}
}

func TestAgentKernelPropagatesChatErrorWithoutStructuredRetry(t *testing.T) {
	taskEventService := taskstate.NewTaskEventService()
	taskRunService := taskstate.NewTaskRunService(taskEventService)
	taskStepService := taskstate.NewTaskStepService()
	agentKernel := NewAgentKernel(taskRunService, taskStepService)
	chatError := errors.New("chat contract rejected")
	replyProvider := &chatReplyProvider{chatError: chatError}
	agentKernel.UseLanguageModelProvider(replyProvider)

	_, errorValue := agentKernel.GenerateReply(context.Background(), "hello")
	if !errors.Is(errorValue, chatError) {
		t.Fatalf("expected chat error to propagate, got %v", errorValue)
	}
	if replyProvider.structuredCallCount != 0 {
		t.Fatalf("expected chat contract error not to trigger structured retry, got %d calls", replyProvider.structuredCallCount)
	}
}

func TestAgentKernelPreservesChatCancellationContext(t *testing.T) {
	taskEventService := taskstate.NewTaskEventService()
	taskRunService := taskstate.NewTaskRunService(taskEventService)
	taskStepService := taskstate.NewTaskStepService()
	agentKernel := NewAgentKernel(taskRunService, taskStepService)
	replyProvider := &chatReplyProvider{chatError: context.Canceled}
	agentKernel.UseLanguageModelProvider(replyProvider)
	responseContext, cancel := context.WithCancel(context.Background())
	cancel()

	_, errorValue := agentKernel.GenerateReply(responseContext, "hello")
	if !errors.Is(errorValue, context.Canceled) {
		t.Fatalf("expected cancellation to propagate, got %v", errorValue)
	}
	if replyProvider.responseContext.Err() != context.Canceled {
		t.Fatalf("expected canceled context to reach chat completer, got %v", replyProvider.responseContext.Err())
	}
}

func TestAgentKernelInjectsMemoryIntoChatReplyRequest(t *testing.T) {
	taskEventService := taskstate.NewTaskEventService()
	taskRunService := taskstate.NewTaskRunService(taskEventService)
	taskStepService := taskstate.NewTaskStepService()
	agentKernel := NewAgentKernel(taskRunService, taskStepService)
	replyProvider := &capturingReplyProvider{content: "remembered"}
	agentKernel.UseLanguageModelProvider(replyProvider)

	_, errorValue := agentKernel.GenerateReplyWithMemory(
		context.Background(),
		"what did I ask for last time?",
		[]MemoryFact{
			{
				Content: "the user asked for help debugging Mattermost DM replies.",
			},
		},
	)
	if errorValue != nil {
		t.Fatalf("expected reply generation: %v", errorValue)
	}

	body := joinChatMessageContent(replyProvider.request.Messages)
	if len(replyProvider.request.Messages) != 3 {
		t.Fatalf("expected system, flattened context, user messages, got %d", len(replyProvider.request.Messages))
	}
	if !strings.Contains(body, "Runtime:") || !strings.Contains(body, "Current weekday:") {
		t.Fatalf("expected runtime context to be injected, got %q", body)
	}
	if !strings.Contains(body, "debugging Mattermost DM replies") {
		t.Fatalf("expected memory context to be injected, got %q", body)
	}
}

func TestAgentKernelInjectsCompactAttributedMemorySummary(t *testing.T) {
	taskEventService := taskstate.NewTaskEventService()
	taskRunService := taskstate.NewTaskRunService(taskEventService)
	taskStepService := taskstate.NewTaskStepService()
	agentKernel := NewAgentKernel(taskRunService, taskStepService)
	replyProvider := &capturingReplyProvider{content: "remembered"}
	agentKernel.UseLanguageModelProvider(replyProvider)
	longContent := strings.Repeat("a detailed memory that needs summarizing ", 30) + "RAW_TAIL_SHOULD_NOT_APPEAR"

	_, errorValue := agentKernel.GenerateReplyWithMemory(
		context.Background(),
		"use what you remember",
		[]MemoryFact{
			{
				ScopeType:       MemoryScopeWorkspace,
				Content:         longContent,
				Score:           0.87,
				SourceEpisodeID: "episode-1",
				ValidAt:         time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	)
	if errorValue != nil {
		t.Fatalf("expected reply generation: %v", errorValue)
	}

	body := joinChatMessageContent(replyProvider.request.Messages)
	if !strings.Contains(body, "Relevant Blueclaw memory") {
		t.Fatalf("expected compact memory heading, got %q", body)
	}
	if !strings.Contains(body, "score=0.87") || !strings.Contains(body, "source=episode-1") {
		t.Fatalf("expected memory attribution, got %q", body)
	}
	if strings.Contains(body, "RAW_TAIL_SHOULD_NOT_APPEAR") {
		t.Fatalf("expected long raw memory content to be compacted, got %q", body)
	}
}

func TestAgentKernelPlacesVisibleContextBeforeMemoryAndPrompt(t *testing.T) {
	taskEventService := taskstate.NewTaskEventService()
	taskRunService := taskstate.NewTaskRunService(taskEventService)
	taskStepService := taskstate.NewTaskStepService()
	agentKernel := NewAgentKernel(taskRunService, taskStepService)
	replyProvider := &capturingReplyProvider{content: "contextual"}
	agentKernel.UseLanguageModelProvider(replyProvider)

	_, errorValue := agentKernel.GenerateReplyWithContext(
		context.Background(),
		"so what should we do?",
		VisibleContext{
			Messages: []VisibleContextMessage{
				{Speaker: "admin", Text: "let's go with A"},
			},
			HasMoreBefore: true,
			HistoryCursor: "cursor-1",
		},
		[]MemoryFact{
			{Content: "the user prefers a design without redundancy."},
		},
	)
	if errorValue != nil {
		t.Fatalf("expected reply generation: %v", errorValue)
	}

	body := joinChatMessageContent(replyProvider.request.Messages)
	if len(replyProvider.request.Messages) != 3 {
		t.Fatalf("expected system, flattened context, prompt messages, got %d", len(replyProvider.request.Messages))
	}
	visibleIndex := strings.Index(body, "admin: let's go with A")
	memoryIndex := strings.Index(body, "a design without redundancy")
	runtimeIndex := strings.Index(body, "Runtime:")
	promptIndex := strings.LastIndex(body, "so what should we do?")
	if visibleIndex < 0 || memoryIndex < 0 || runtimeIndex < 0 || promptIndex < 0 {
		t.Fatalf("expected visible context, memory, runtime, and prompt, got %q", body)
	}
	if !(visibleIndex < memoryIndex && memoryIndex < runtimeIndex && runtimeIndex < promptIndex) {
		t.Fatalf("expected visible context before memory before the volatile runtime timestamp before the final prompt, got %q", body)
	}
}

type staticReplyProvider struct {
	content string
}

type structuredOnlyReplyProvider struct {
	structuredCallCount int
}

func (replyProvider staticReplyProvider) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	_ = responseContext
	_ = prompt
	return replyProvider.content, nil
}

func (replyProvider staticReplyProvider) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest model.StructuredResponseRequest) (model.StructuredResponse, error) {
	_ = responseContext
	_ = structuredResponseRequest
	return model.StructuredResponse{Content: replyProvider.content}, nil
}

func (replyProvider *structuredOnlyReplyProvider) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	_ = responseContext
	_ = prompt
	return "", nil
}

func (replyProvider *structuredOnlyReplyProvider) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest model.StructuredResponseRequest) (model.StructuredResponse, error) {
	_ = responseContext
	_ = structuredResponseRequest
	replyProvider.structuredCallCount++
	return model.StructuredResponse{}, nil
}

type capturingReplyProvider struct {
	content string
	request model.ChatCompletionRequest
}

type chatReplyProvider struct {
	response            model.ChatCompletionResponse
	chatError           error
	request             model.ChatCompletionRequest
	responseContext     context.Context
	structuredCallCount int
}

func (replyProvider *chatReplyProvider) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	_ = responseContext
	_ = prompt
	return "", nil
}

func (replyProvider *chatReplyProvider) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest model.StructuredResponseRequest) (model.StructuredResponse, error) {
	_ = responseContext
	_ = structuredResponseRequest
	replyProvider.structuredCallCount++
	return model.StructuredResponse{}, nil
}

func (replyProvider *chatReplyProvider) GenerateChatCompletion(responseContext context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	replyProvider.responseContext = responseContext
	replyProvider.request = request
	return replyProvider.response, replyProvider.chatError
}

func (replyProvider *capturingReplyProvider) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	_ = responseContext
	_ = prompt
	return replyProvider.content, nil
}

func (replyProvider *capturingReplyProvider) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest model.StructuredResponseRequest) (model.StructuredResponse, error) {
	_ = responseContext
	_ = structuredResponseRequest
	return model.StructuredResponse{}, errors.New("structured reply generation is not supported")
}

func (replyProvider *capturingReplyProvider) GenerateChatCompletion(responseContext context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	_ = responseContext
	replyProvider.request = request
	return model.ChatCompletionResponse{
		Message: model.ChatCompletionMessage{
			Role:    "assistant",
			Content: replyProvider.content,
		},
	}, nil
}

func joinChatMessageContent(messages []model.ChatCompletionMessage) string {
	content := make([]string, 0, len(messages))
	for _, message := range messages {
		content = append(content, message.Content)
	}
	return strings.Join(content, "\n")
}
