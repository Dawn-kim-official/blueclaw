package bluecollar

import (
	"context"
	"errors"
	"github.com/Dawn-kim-official/blueclaw/internal/toolcontract"
	"strings"
	"testing"
)

func TestAgentTurnRunnerRecordsToolRequestedEvent(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"alpha","toolInput":{"value":"one"}}`,
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestCapabilityToolSet([]string{"alpha"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "alpha"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("alpha result"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.alpha.requested", `"value":"one"`) {
		t.Fatal("expected requested direct tool event with typed input")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.alpha.result", "alpha result") {
		t.Fatal("expected result tool event")
	}
}

func TestAgentTurnRunnerTreatsToolFailureAsObservation(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"unstable","toolInput":{}}`,
		finishMessageDocument("handled failure"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestCapabilityToolSet([]string{"unstable"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "unstable"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{}, errors.New("tool failed")
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to recover: %v", errorValue)
	}
	if result.FinishMessage != "handled failure" {
		t.Fatalf("expected final reply after failure, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.unstable.result", "tool failed") {
		t.Fatal("expected the tool failure to be recorded as an observation the model can answer from")
	}
}

func TestAgentTurnRunnerStoresLargeToolResultAsArtifact(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"large","toolInput":{}}`,
		finishMessageDocument("summarized"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{ToolResultMaxBytes: 8})
	toolRegistry := newTestCapabilityToolSet([]string{"large"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "large"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess(strings.Repeat("x", 32)), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if len(services.taskArtifactService.ListTaskArtifact(result.TaskRun.TaskRunID)) != 1 {
		t.Fatalf("expected one task artifact, got %d", len(services.taskArtifactService.ListTaskArtifact(result.TaskRun.TaskRunID)))
	}
}

func TestModelVisibleToolResultSummaryKeepsPublishedSiteURL(t *testing.T) {
	content := `{"siteID":"site-1","slug":"tangerine-hub","mode":"publish","publishedURL":"https://tangerine-hub.example-device.example.test","sourceSHA256":"` + strings.Repeat("a", 64) + `","description":"` + strings.Repeat("x", 4096) + `"}`
	summary := modelVisibleToolResultSummary(context.Background(), nil, "site.serve", turnObservation{
		Tool: "site.serve",
		Output: toolcontract.ToolOutput{
			Content: content,
		},
	})

	if !strings.Contains(summary, "publishedURL=https://tangerine-hub.example-device.example.test") {
		t.Fatalf("expected exact publishedURL in summary, got %q", summary)
	}
	if strings.Contains(summary, strings.Repeat("x", 512)) {
		t.Fatalf("expected site summary to omit long nonessential fields, got %q", summary)
	}
}

func TestModelVisibleToolResultSummaryKeepsPreviewURLForPreviewServe(t *testing.T) {
	content := `{"siteID":"site-1","slug":"draft-site","mode":"preview","previewURL":"https://draft-site.example-device.example.test/__preview/preview-1","sourceSHA256":"` + strings.Repeat("a", 64) + `"}`
	summary := modelVisibleToolResultSummary(context.Background(), nil, "site.serve", turnObservation{
		Tool: "site.serve",
		Output: toolcontract.ToolOutput{
			Content: content,
		},
	})

	if !strings.Contains(summary, "previewURL=https://draft-site.example-device.example.test/__preview/preview-1") {
		t.Fatalf("expected exact previewURL in summary, got %q", summary)
	}
	if strings.Contains(summary, "publishedURL") {
		t.Fatalf("preview serve summary must not invent a publishedURL, got %q", summary)
	}
}
