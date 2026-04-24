package agent

import (
	"context"
	"testing"

	"blueclaw/internal/llm"
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
