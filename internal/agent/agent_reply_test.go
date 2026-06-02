package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/llm"
	"blueclaw/internal/memory"
	"blueclaw/internal/task"
)

func TestAgentKernelGeneratesStructuredReply(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskStepService := task.NewTaskStepService()
	agentKernel := NewAgentKernel(taskRunService, taskStepService)
	agentKernel.UseLanguageModelProvider(staticReplyProvider{content: `{"reply":"hello from model"}`})

	reply, errorValue := agentKernel.GenerateReply(context.Background(), "hello")
	if errorValue != nil {
		t.Fatalf("expected reply generation: %v", errorValue)
	}
	if reply != "hello from model" {
		t.Fatalf("expected model reply, got %q", reply)
	}
}

func TestAgentKernelInjectsMemoryIntoStructuredReplyRequest(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskStepService := task.NewTaskStepService()
	agentKernel := NewAgentKernel(taskRunService, taskStepService)
	replyProvider := &capturingReplyProvider{content: `{"reply":"remembered"}`}
	agentKernel.UseLanguageModelProvider(replyProvider)

	_, errorValue := agentKernel.GenerateReplyWithMemory(
		context.Background(),
		"저번에 뭐 부탁했지?",
		[]memory.MemoryFact{
			{
				Content: "사용자는 매터모스트 DM 답장 디버깅을 부탁했다.",
			},
		},
	)
	if errorValue != nil {
		t.Fatalf("expected reply generation: %v", errorValue)
	}

	body := joinMessageContent(replyProvider.request.Messages)
	if len(replyProvider.request.Messages) != 3 {
		t.Fatalf("expected system, flattened context, user messages, got %d", len(replyProvider.request.Messages))
	}
	if !strings.Contains(body, "Runtime:") || !strings.Contains(body, "Current weekday:") {
		t.Fatalf("expected runtime context to be injected, got %q", body)
	}
	if !strings.Contains(body, "매터모스트 DM 답장 디버깅") {
		t.Fatalf("expected memory context to be injected, got %q", body)
	}
}

func TestAgentKernelInjectsCompactAttributedMemorySummary(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskStepService := task.NewTaskStepService()
	agentKernel := NewAgentKernel(taskRunService, taskStepService)
	replyProvider := &capturingReplyProvider{content: `{"reply":"remembered"}`}
	agentKernel.UseLanguageModelProvider(replyProvider)
	longContent := strings.Repeat("요약해야 하는 상세 메모리 ", 30) + "RAW_TAIL_SHOULD_NOT_APPEAR"

	_, errorValue := agentKernel.GenerateReplyWithMemory(
		context.Background(),
		"기억 참고해줘",
		[]memory.MemoryFact{
			{
				ScopeType:       memory.ScopeTypeWorkspace,
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

	body := joinMessageContent(replyProvider.request.Messages)
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
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskStepService := task.NewTaskStepService()
	agentKernel := NewAgentKernel(taskRunService, taskStepService)
	replyProvider := &capturingReplyProvider{content: `{"reply":"contextual"}`}
	agentKernel.UseLanguageModelProvider(replyProvider)

	_, errorValue := agentKernel.GenerateReplyWithContext(
		context.Background(),
		"그래서 어떻게 할까?",
		VisibleContext{
			Messages: []VisibleContextMessage{
				{Speaker: "admin", Text: "A로 가자"},
			},
			HasMoreBefore: true,
			HistoryCursor: "cursor-1",
		},
		[]memory.MemoryFact{
			{Content: "사용자는 redundancy 없는 설계를 선호한다."},
		},
	)
	if errorValue != nil {
		t.Fatalf("expected reply generation: %v", errorValue)
	}

	body := joinMessageContent(replyProvider.request.Messages)
	if len(replyProvider.request.Messages) != 3 {
		t.Fatalf("expected system, flattened context, prompt messages, got %d", len(replyProvider.request.Messages))
	}
	runtimeIndex := strings.Index(body, "Runtime:")
	visibleIndex := strings.Index(body, "admin: A로 가자")
	memoryIndex := strings.Index(body, "redundancy 없는 설계")
	promptIndex := strings.LastIndex(body, "그래서 어떻게 할까?")
	if runtimeIndex < 0 || visibleIndex < 0 || memoryIndex < 0 || promptIndex < 0 {
		t.Fatalf("expected runtime, visible context, memory, and prompt, got %q", body)
	}
	if !(runtimeIndex < visibleIndex && visibleIndex < memoryIndex && memoryIndex < promptIndex) {
		t.Fatalf("expected runtime before visible context before memory before prompt, got %q", body)
	}
}

type staticReplyProvider struct {
	content string
}

func (replyProvider staticReplyProvider) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	_ = responseContext
	_ = prompt
	return replyProvider.content, nil
}

func (replyProvider staticReplyProvider) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	_ = responseContext
	_ = structuredResponseRequest
	return llm.StructuredResponse{Content: replyProvider.content}, nil
}

type capturingReplyProvider struct {
	content string
	request llm.StructuredResponseRequest
}

func (replyProvider *capturingReplyProvider) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	_ = responseContext
	_ = prompt
	return replyProvider.content, nil
}

func (replyProvider *capturingReplyProvider) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	_ = responseContext
	replyProvider.request = structuredResponseRequest
	return llm.StructuredResponse{Content: replyProvider.content}, nil
}
