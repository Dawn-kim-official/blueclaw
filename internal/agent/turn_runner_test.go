package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"blueclaw/internal/llm"
	"blueclaw/internal/task"
)

func TestAgentTurnRunnerCallsToolsUntilFinalReply(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"alpha","toolInput":{"value":"one"}}`,
		`{"action":"call_tool","toolName":"beta","toolInput":{"value":"two"}}`,
		`{"action":"final_reply","finalReply":"done"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := NewToolRegistry([]string{"alpha", "beta"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "alpha result"}, nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "beta"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "beta result"}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolRegistry:      toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinalReply != "done" {
		t.Fatalf("expected final reply, got %q", result.FinalReply)
	}
	if result.TaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if len(services.taskStepService.ListTaskStep(result.TaskRun.TaskRunID)) != 3 {
		t.Fatalf("expected three task steps, got %d", len(services.taskStepService.ListTaskStep(result.TaskRun.TaskRunID)))
	}
	if len(languageModel.requests) != 3 {
		t.Fatalf("expected three model calls, got %d", len(languageModel.requests))
	}
}

func TestAgentTurnRunnerRecordsDeniedToolAsObservation(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"forbidden","toolInput":{}}`,
		`{"action":"final_reply","finalReply":"recovered"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := NewToolRegistry([]string{"allowed"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "forbidden"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "should not run"}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolRegistry:      toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to recover: %v", errorValue)
	}
	if result.FinalReply != "recovered" {
		t.Fatalf("expected recovered reply, got %q", result.FinalReply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.forbidden.result", "not allowed") {
		t.Fatal("expected denied tool event")
	}
}

func TestAgentTurnRunnerRequiresToolEvidenceBeforeFinalReply(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"final_reply","finalReply":"browser tool is unavailable"}`,
		`{"action":"call_tool","toolName":"browser.screenshot","toolInput":{}}`,
		`{"action":"final_reply","finalReply":"observed"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := NewToolRegistry([]string{"browser.screenshot"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.screenshot"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: `{"devicePath":"/tmp/screenshot.png"}`}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "구글 서치바에 hello world라고 치고 스크린샷",
		ToolRegistry:      toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected browser tool requirement to recover: %v", errorValue)
	}
	if result.FinalReply != "observed" {
		t.Fatalf("expected final reply after tool use, got %q", result.FinalReply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_required", "browser.screenshot") {
		t.Fatal("expected tool requirement event")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.browser.screenshot.result", "/tmp/screenshot.png") {
		t.Fatal("expected browser screenshot observation")
	}
}

func TestAgentTurnRunnerTreatsToolFailureAsObservation(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"unstable","toolInput":{}}`,
		`{"action":"final_reply","finalReply":"handled failure"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := NewToolRegistry([]string{"unstable"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "unstable"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{}, errors.New("tool failed")
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolRegistry:      toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to recover: %v", errorValue)
	}
	if result.FinalReply != "handled failure" {
		t.Fatalf("expected final reply after failure, got %q", result.FinalReply)
	}
}

func TestAgentTurnRunnerNormalizesEmptyBrowserPressAfterFill(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"browser.fill","toolInput":{"target":"@e5","text":"hello world"}}`,
		`{"action":"call_tool","toolName":"browser.press","toolInput":{}}`,
		`{"action":"final_reply","finalReply":"searched"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := NewToolRegistry([]string{"browser.fill", "browser.press"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.fill"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: `{"ok":true}`}, nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.press"}, func(_ context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		var inputDocument map[string]string
		if errorValue := json.Unmarshal(toolInvocation.Input, &inputDocument); errorValue != nil {
			t.Fatalf("expected normalized press input: %v", errorValue)
		}
		if inputDocument["key"] != "Enter" {
			t.Fatalf("expected Enter key normalization, got %+v", inputDocument)
		}
		return ToolResult{Content: `{"ok":true}`}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "구글 서치바에 hello world라고 치고 스크린샷",
		ToolRegistry:      toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinalReply != "searched" {
		t.Fatalf("expected searched reply, got %q", result.FinalReply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_input_normalized", "Enter") {
		t.Fatal("expected normalized tool input event")
	}
}

func TestAgentTurnRunnerNormalizesBrowserFillFromObservationAndReason(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"browser.observe","toolInput":{}}`,
		`{"action":"call_tool","toolName":"browser.fill","toolInput":{}}`,
		`{"action":"final_reply","finalReply":"filled"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := NewToolRegistry([]string{"browser.observe", "browser.fill"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.observe"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: `{"snapshotText":"- textbox \"Google 검색\" [ref=e5]"}`}, nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.fill"}, func(_ context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		var inputDocument map[string]string
		if errorValue := json.Unmarshal(toolInvocation.Input, &inputDocument); errorValue != nil {
			t.Fatalf("expected normalized fill input: %v", errorValue)
		}
		if inputDocument["target"] != "@e5" || inputDocument["text"] != "hello world" {
			t.Fatalf("expected target and text normalization, got %+v", inputDocument)
		}
		return ToolResult{Content: `{"ok":true}`}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "구글 서치바에 hello world라고 치고 스크린샷",
		ToolRegistry:      toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinalReply != "filled" {
		t.Fatalf("expected filled reply, got %q", result.FinalReply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_input_normalized", "browser.fill") {
		t.Fatal("expected normalized browser fill event")
	}
}

func TestAgentTurnRunnerNormalizesEmptyGoogleNavigate(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"browser.navigate","toolInput":{}}`,
		`{"action":"final_reply","finalReply":"opened"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := NewToolRegistry([]string{"browser.navigate"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.navigate"}, func(_ context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		var inputDocument map[string]string
		if errorValue := json.Unmarshal(toolInvocation.Input, &inputDocument); errorValue != nil {
			t.Fatalf("expected normalized navigate input: %v", errorValue)
		}
		if inputDocument["url"] != "https://www.google.com" {
			t.Fatalf("expected Google URL normalization, got %+v", inputDocument)
		}
		return ToolResult{Content: `{"url":"https://www.google.com"}`}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "구글 서치바에 hello world라고 치고 스크린샷",
		ToolRegistry:      toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinalReply != "opened" {
		t.Fatalf("expected opened reply, got %q", result.FinalReply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_input_normalized", "browser.navigate") {
		t.Fatal("expected normalized browser navigate event")
	}
}

func TestAgentTurnRunnerStoresLargeToolResultAsArtifact(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"large","toolInput":{}}`,
		`{"action":"final_reply","finalReply":"summarized"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{ToolResultMaxBytes: 8})
	toolRegistry := NewToolRegistry([]string{"large"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "large"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: strings.Repeat("x", 32)}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolRegistry:      toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if len(services.taskArtifactService.ListTaskArtifact(result.TaskRun.TaskRunID)) != 1 {
		t.Fatalf("expected one task artifact, got %d", len(services.taskArtifactService.ListTaskArtifact(result.TaskRun.TaskRunID)))
	}
}

func TestAgentTurnRunnerFailsWhenMaximumIterationsAreExceeded(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"loop","toolInput":{}}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterations: 1})
	toolRegistry := NewToolRegistry([]string{"loop"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "loop"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "again"}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolRegistry:      toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected fallback result, got error: %v", errorValue)
	}
	if !strings.Contains(result.FinalReply, "thirty-minute budget") {
		t.Fatalf("expected budget reply, got %q", result.FinalReply)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked {
		t.Fatalf("expected blocked task run, got %s", result.TaskRun.Status)
	}
}

func TestAgentTurnRunnerStopsWhenToolBudgetIsExceeded(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"loop","toolInput":{}}`,
		`{"action":"call_tool","toolName":"loop","toolInput":{}}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterations: 3, MaxToolCalls: 1})
	toolRegistry := NewToolRegistry([]string{"loop"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "loop"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: "again"}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolRegistry:      toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected budget result, got error: %v", errorValue)
	}
	if !strings.Contains(result.FinalReply, "thirty-minute budget") {
		t.Fatalf("expected budget reply, got %q", result.FinalReply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.budget_stop", "maximum tool calls exceeded") {
		t.Fatal("expected budget stop event")
	}
}

type turnRunnerTestServices struct {
	runner              *AgentTurnRunner
	taskEventService    *task.TaskEventService
	taskStepService     *task.TaskStepService
	taskArtifactService *task.TaskArtifactService
}

func newTurnRunnerTestServices(languageModel llm.LanguageModelProvider, options TurnOptions) turnRunnerTestServices {
	taskEventService := task.NewTaskEventService()
	taskStepService := task.NewTaskStepService()
	taskArtifactService := task.NewTaskArtifactService()
	taskRunService := task.NewTaskRunService(taskEventService)
	return turnRunnerTestServices{
		runner:              NewAgentTurnRunner(taskRunService, taskStepService, taskArtifactService, languageModel, options),
		taskEventService:    taskEventService,
		taskStepService:     taskStepService,
		taskArtifactService: taskArtifactService,
	}
}

type sequenceLanguageModel struct {
	contents []string
	requests []llm.StructuredResponseRequest
}

func (languageModel *sequenceLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *sequenceLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	languageModel.requests = append(languageModel.requests, request)
	index := len(languageModel.requests) - 1
	if index >= len(languageModel.contents) {
		index = len(languageModel.contents) - 1
	}
	return llm.StructuredResponse{Content: languageModel.contents[index]}, nil
}

func taskEventsContain(taskEvents []task.TaskEvent, name string, bodyFragment string) bool {
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == name && strings.Contains(taskEvent.Body, bodyFragment) {
			return true
		}
	}
	return false
}
