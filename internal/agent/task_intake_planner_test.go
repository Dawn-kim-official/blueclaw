package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"blueclaw/internal/llm"
	"blueclaw/internal/task"
)

func TestTaskIntakePlannerUsesStructuredModelDecision(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"bounded_task","maxIterationsPerRequest":3,"maxToolCallsPerRequest":2,"maxWallClockSecond":30,"reason":"bounded tool work","userFacingReply":""}`,
	}}
	toolRegistry := NewToolRegistry([]string{"memory.search"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "memory.search"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{}, nil
	})
	planner := NewTaskIntakePlanner(languageModel, IntakeOptions{
		IsEnabled:               true,
		MaxIterationsPerRequest: 8,
		MaxToolCallsPerRequest:  8,
		MaxWallClockSecond:      120,
	})

	decision := planner.Plan(context.Background(), AgentRequest{
		Prompt:       "search memory",
		ToolRegistry: toolRegistry,
	})

	if decision.Classification != IntakeClassificationBoundedTask {
		t.Fatalf("expected bounded task, got %q", decision.Classification)
	}
	if decision.MaxIterationsPerRequest != 3 || decision.MaxToolCallsPerRequest != 2 || decision.MaxWallClockSecond != 30 {
		t.Fatalf("expected selected budgets, got %+v", decision)
	}
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected one intake model call, got %d", len(languageModel.requests))
	}
	if languageModel.requests[0].StructuredOutputSchema.Name != "blueclaw_task_intake_budget" {
		t.Fatalf("expected intake schema, got %q", languageModel.requests[0].StructuredOutputSchema.Name)
	}
}

func TestTaskIntakePlannerFallsBackDeterministically(t *testing.T) {
	planner := NewTaskIntakePlanner(failingLanguageModel{}, IntakeOptions{IsEnabled: true})

	decision := planner.Plan(context.Background(), AgentRequest{Prompt: "please analyze the whole repo"})

	if decision.Classification != IntakeClassificationNeedsConfirmation {
		t.Fatalf("expected confirmation fallback, got %q", decision.Classification)
	}
	if !decision.UsedDeterministicFallback {
		t.Fatal("expected deterministic fallback marker")
	}
}

func TestAgentKernelUsesIntakeBeforeRunningTools(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"needs_confirmation","maxIterationsPerRequest":8,"maxToolCallsPerRequest":8,"maxWallClockSecond":120,"reason":"too broad","userFacingReply":"Please narrow this first."}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"final_reply","finalReply":"should not run"}`,
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := NewToolRegistry([]string{"expensive"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "expensive"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "expensive result"}, nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do the entire thing",
		ToolRegistry:      toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected intake-only result: %v", errorValue)
	}
	if result.FinalReply != "Please narrow this first." {
		t.Fatalf("expected confirmation reply, got %q", result.FinalReply)
	}
	if result.TaskRun.Status != task.TaskStatusWaitingUserInput {
		t.Fatalf("expected waiting user input, got %s", result.TaskRun.Status)
	}
	if len(replyLanguageModel.requests) != 0 {
		t.Fatalf("expected agent loop not to run, got %d model calls", len(replyLanguageModel.requests))
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.intake", "needs_confirmation") {
		t.Fatal("expected intake event")
	}
}

func TestAgentKernelQuickReplyDoesNotExposeTools(t *testing.T) {
	intakeLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"classification":"quick_reply","maxIterationsPerRequest":8,"maxToolCallsPerRequest":8,"maxWallClockSecond":120,"reason":"direct answer","userFacingReply":""}`,
	}}
	replyLanguageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"final_reply","finalReply":"hello"}`,
	}}
	services := newKernelIntakeTestServices(replyLanguageModel, intakeLanguageModel)
	toolRegistry := NewToolRegistry([]string{"expensive"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "expensive"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "expensive result"}, nil
	})

	result, errorValue := services.kernel.RunAgentRequest(context.Background(), AgentRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "hello",
		ToolRegistry:      toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected quick reply: %v", errorValue)
	}
	if result.FinalReply != "hello" {
		t.Fatalf("expected final reply, got %q", result.FinalReply)
	}
	if strings.Contains(replyLanguageModel.requests[0].Messages[0].Content, "Available tools") {
		t.Fatal("expected quick reply to hide tools")
	}
}

type kernelIntakeTestServices struct {
	kernel           *AgentKernel
	taskEventService *task.TaskEventService
}

func newKernelIntakeTestServices(replyLanguageModel llm.LanguageModelProvider, intakeLanguageModel llm.LanguageModelProvider) kernelIntakeTestServices {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskStepService := task.NewTaskStepService()
	kernel := NewAgentKernel(taskRunService, taskStepService)
	kernel.UseLanguageModelProvider(replyLanguageModel)
	kernel.UseIntakeLanguageModelProvider(intakeLanguageModel)
	kernel.UseIntakeOptions(IntakeOptions{
		IsEnabled:               true,
		MaxIterationsPerRequest: 8,
		MaxToolCallsPerRequest:  8,
		MaxWallClockSecond:      120,
	})
	return kernelIntakeTestServices{kernel: kernel, taskEventService: taskEventService}
}

type failingLanguageModel struct{}

func (failingLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("model failed")
}

func (failingLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, errors.New("model failed")
}
