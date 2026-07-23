package agent

import (
	"context"
	"errors"
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
	registerTestTool(toolRegistry, ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
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
	registerTestTool(toolRegistry, ToolDefinition{Name: "unstable"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolResult{}, errors.New("tool failed")
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
	registerTestTool(toolRegistry, ToolDefinition{Name: "large"}, func(context.Context, ToolInvocation) (ToolResult, error) {
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
	content := `{"siteID":"site-1","slug":"tangerine-hub","title":"맛있는 귤 사이트","status":"published","publishedURL":"https://tangerine-hub.zd2df6qt6jmc.intern.kim","description":"` + strings.Repeat("x", 4096) + `"}`
	summary := modelVisibleToolResultSummary(context.Background(), nil, "site.publish", turnObservation{
		Tool: "site.publish",
		Output: ToolOutput{
			Content: content,
		},
	})

	if !strings.Contains(summary, "publishedURL=https://tangerine-hub.zd2df6qt6jmc.intern.kim") {
		t.Fatalf("expected exact publishedURL in summary, got %q", summary)
	}
	if strings.Contains(summary, strings.Repeat("x", 512)) {
		t.Fatalf("expected site summary to omit long nonessential fields, got %q", summary)
	}
}

func TestModelVisibleToolResultSummaryHidesDraftSiteCreateURL(t *testing.T) {
	content := `{"siteID":"site-1","slug":"draft-site","title":"Draft","status":"draft","publishedURL":"https://draft-site.zd2df6qt6jmc.intern.kim","sourceWorkspacePath":"/workspace/circles/staff/sites/site-1/draft"}`
	summary := modelVisibleToolResultSummary(context.Background(), nil, "site.create", turnObservation{
		Tool: "site.create",
		Output: ToolOutput{
			Content: content,
		},
	})

	if strings.Contains(summary, "publishedURL") {
		t.Fatalf("draft create summary must not expose publishedURL, got %q", summary)
	}
	if !strings.Contains(summary, "sourceWorkspacePath=/workspace/circles/staff/sites/site-1/draft") {
		t.Fatalf("expected workspace path in summary, got %q", summary)
	}
}
