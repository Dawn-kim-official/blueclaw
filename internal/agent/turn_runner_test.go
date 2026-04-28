package agent

import (
	"context"
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

func TestAgentTurnRunnerInjectsInstructionPrompt(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"final_reply","finalReply":"done"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})

	_, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		InstructionPrompt: "Use agent-browser for web automation.",
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if !messagesContain(languageModel.requests[0].Messages, "Use agent-browser for web automation.") {
		t.Fatal("expected instruction prompt to be injected")
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

func TestAgentTurnRunnerRejectsEmptyBrowserPressAfterFill(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"browser.fill","toolInput":{"target":"@e5","text":"hello world"}}`,
		`{"action":"call_tool","toolName":"browser.press","toolInput":{}}`,
		`{"action":"final_reply","finalReply":"searched"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	pressCallCount := 0
	toolRegistry := NewToolRegistry([]string{"browser.fill", "browser.press"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.fill"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: `{"ok":true}`}, nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.press"}, func(_ context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		pressCallCount++
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
	if pressCallCount != 0 {
		t.Fatalf("expected malformed press input not to invoke tool, got %d calls", pressCallCount)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_input_malformed", "browser.press") {
		t.Fatal("expected malformed browser press event")
	}
}

func TestAgentTurnRunnerRejectsBrowserFillWithoutRequiredInput(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"browser.snapshot","toolInput":{}}`,
		`{"action":"call_tool","toolName":"browser.fill","toolInput":{}}`,
		`{"action":"final_reply","finalReply":"filled"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	fillCallCount := 0
	toolRegistry := NewToolRegistry([]string{"browser.snapshot", "browser.fill"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.snapshot"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: `{"snapshotText":"- textbox \"Google 검색\" [ref=e5]"}`}, nil
	})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.fill"}, func(_ context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		fillCallCount++
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
	if fillCallCount != 0 {
		t.Fatalf("expected malformed fill input not to invoke tool, got %d calls", fillCallCount)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_input_malformed", "target/ref/selector, text") {
		t.Fatal("expected malformed browser fill event")
	}
}

func TestAgentTurnRunnerRejectsEmptyGoogleNavigate(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"browser.open","toolInput":{}}`,
		`{"action":"final_reply","finalReply":"opened"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	navigateCallCount := 0
	toolRegistry := NewToolRegistry([]string{"browser.open"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.open"}, func(_ context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		navigateCallCount++
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
	if navigateCallCount != 0 {
		t.Fatalf("expected malformed navigate input not to invoke tool, got %d calls", navigateCallCount)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_input_malformed", "url") {
		t.Fatal("expected malformed browser navigate event")
	}
}

func TestBrowserActionSchemaUsesProviderCompatibleObjectInputs(t *testing.T) {
	runner := NewAgentTurnRunner(nil, nil, nil, nil, TurnOptions{})
	toolRegistry := NewToolRegistry([]string{"browser.open", "browser.click", "browser.fill", "browser.select", "browser.wait"})
	for _, toolName := range []string{"browser.open", "browser.click", "browser.fill", "browser.select", "browser.wait"} {
		toolRegistry.RegisterTool(ToolDefinition{Name: toolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolResult{}, nil
		})
	}
	schemaDocument := runner.buildActionSchema(toolRegistry)

	if strings.Contains(schemaDocument, "anyOf") {
		t.Fatalf("expected browser action schema to avoid anyOf, got %s", schemaDocument)
	}
	if strings.Contains(schemaDocument, `"toolInput":{"oneOf"`) {
		t.Fatalf("expected browser tool inputs to avoid oneOf unions, got %s", schemaDocument)
	}
	if strings.Contains(schemaDocument, `{"type":"string","minLength":1}`) {
		t.Fatalf("expected browser tool inputs to avoid string shortcut branches, got %s", schemaDocument)
	}
	for _, fragment := range []string{
		`"toolName":{"enum":["browser.open"],"type":"string"}`,
		`"required":["url"]`,
		`"required":["text"]`,
		`"required":["value"]`,
		`"properties":{"milliseconds":{"type":"number"},"ref":{"type":"string"},"selector":{"type":"string"},"target":{"type":"string"}}`,
	} {
		if !strings.Contains(schemaDocument, fragment) {
			t.Fatalf("expected action schema to include %q, got %s", fragment, schemaDocument)
		}
	}
}

func TestAgentTurnRunnerStopsRepeatedMalformedToolInputByBudget(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"browser.fill","toolInput":{}}`,
		`{"action":"call_tool","toolName":"browser.fill","toolInput":{}}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterations: 4})
	fillCallCount := 0
	toolRegistry := NewToolRegistry([]string{"browser.fill"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.fill"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		fillCallCount++
		return ToolResult{Content: `{"ok":true}`}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "fill the search box",
		ToolRegistry:      toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected budget result, got error: %v", errorValue)
	}
	if !strings.Contains(result.FinalReply, "thirty-minute budget") {
		t.Fatalf("expected budget reply, got %q", result.FinalReply)
	}
	if result.TaskRun.Status != task.TaskStatusBlocked {
		t.Fatalf("expected blocked task, got %s", result.TaskRun.Status)
	}
	if fillCallCount != 0 {
		t.Fatalf("expected malformed fill input not to invoke tool, got %d calls", fillCallCount)
	}
}

func TestAgentTurnRunnerDoesNotChargeMalformedInputToToolBudget(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"browser.fill","toolInput":{}}`,
		`{"action":"call_tool","toolName":"alpha","toolInput":{}}`,
		`{"action":"call_tool","toolName":"beta","toolInput":{}}`,
		`{"action":"final_reply","finalReply":"done"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterations: 4, MaxToolCalls: 2})
	toolRegistry := NewToolRegistry([]string{"browser.fill", "alpha", "beta"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.fill"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{Content: `{"ok":true}`}, nil
	})
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
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_input_malformed", "browser.fill") {
		t.Fatal("expected malformed tool event")
	}
}

func TestAgentTurnRunnerLocalizesKoreanBudgetReply(t *testing.T) {
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
		Prompt:            "구글에서 검색해줘",
		ToolRegistry:      toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected budget result, got error: %v", errorValue)
	}
	if !strings.Contains(result.FinalReply, "30분 예산") {
		t.Fatalf("expected korean budget reply, got %q", result.FinalReply)
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

func messagesContain(messages []llm.Message, fragment string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, fragment) {
			return true
		}
	}
	return false
}
