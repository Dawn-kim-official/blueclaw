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

	if len(replyProvider.request.Messages) != 3 {
		t.Fatalf("expected system, memory, user messages, got %d", len(replyProvider.request.Messages))
	}
	if !strings.Contains(replyProvider.request.Messages[1].Content, "매터모스트 DM 답장 디버깅") {
		t.Fatalf("expected memory context to be injected, got %q", replyProvider.request.Messages[1].Content)
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

	memoryMessage := replyProvider.request.Messages[1].Content
	if !strings.Contains(memoryMessage, "Relevant Blueclaw memory") {
		t.Fatalf("expected compact memory heading, got %q", memoryMessage)
	}
	if !strings.Contains(memoryMessage, "score=0.87") || !strings.Contains(memoryMessage, "source=episode-1") {
		t.Fatalf("expected memory attribution, got %q", memoryMessage)
	}
	if strings.Contains(memoryMessage, "RAW_TAIL_SHOULD_NOT_APPEAR") {
		t.Fatalf("expected long raw memory content to be compacted, got %q", memoryMessage)
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

	if len(replyProvider.request.Messages) != 4 {
		t.Fatalf("expected system, visible context, memory, prompt messages, got %d", len(replyProvider.request.Messages))
	}
	if !strings.Contains(replyProvider.request.Messages[1].Content, "admin: A로 가자") {
		t.Fatalf("expected visible context before memory, got %q", replyProvider.request.Messages[1].Content)
	}
	if !strings.Contains(replyProvider.request.Messages[1].Content, "conversation.history") {
		t.Fatalf("expected history tool hint, got %q", replyProvider.request.Messages[1].Content)
	}
	if !strings.Contains(replyProvider.request.Messages[2].Content, "redundancy 없는 설계") {
		t.Fatalf("expected memory after visible context, got %q", replyProvider.request.Messages[2].Content)
	}
	if replyProvider.request.Messages[3].Content != "그래서 어떻게 할까?" {
		t.Fatalf("expected prompt last, got %q", replyProvider.request.Messages[3].Content)
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
