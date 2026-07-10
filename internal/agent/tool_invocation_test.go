package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAgentTurnRunnerRecordsToolRequestedEvent(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"alpha","input":{"value":"one"}}}`,
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestCapabilityToolSet([]string{"alpha"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "alpha"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("alpha result"), nil
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
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.capability.invoke.requested", `"operation":"alpha"`) {
		t.Fatal("expected requested tool event with the wrapped operation and input")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.capability.invoke.result", "alpha result") {
		t.Fatal("expected result tool event")
	}
}

func TestWrappedToolObservationUsesEffectiveOperationSanitizer(t *testing.T) {
	services := newTurnRunnerTestServices(nil, TurnOptions{})
	content := `{"url":"https://example.com/inbox","title":"Inbox","snapshotText":"Visible message","interactiveRefs":["@e1"],"cdpURL":"ws://secret","profilePath":"/Users/private/profile"}`
	toolRegistry := newTestCapabilityToolSet([]string{"browser.snapshot"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "browser.snapshot"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(content), nil
	})
	workspaceRootPath := t.TempDir()
	wrappedObservation := services.runner.invokeTool(
		context.Background(),
		toolRegistry,
		"wrapped-run",
		"obs-001",
		CapabilityInvokeToolName,
		json.RawMessage(`{"operation":"browser.snapshot","input":{}}`),
		workspaceRootPath,
		time.Time{},
		ResponseLanguageEnglish,
		"",
	)
	directObservation := services.runner.invokeTool(
		context.Background(),
		toolRegistry.WithAdditionalAllowedToolNames([]string{"browser.snapshot"}),
		"direct-run",
		"obs-001",
		"browser.snapshot",
		json.RawMessage(`{}`),
		workspaceRootPath,
		time.Time{},
		ResponseLanguageEnglish,
		"",
	)

	if wrappedObservation.Summary != directObservation.Summary {
		t.Fatalf("expected wrapped and direct summaries to match, wrapped=%q direct=%q", wrappedObservation.Summary, directObservation.Summary)
	}
	for _, expectedText := range []string{"url=https://example.com/inbox", "title=Inbox", "visibleText=Visible message", "interactiveRefs=@e1"} {
		if !strings.Contains(wrappedObservation.Summary, expectedText) {
			t.Fatalf("expected sanitized summary to contain %q, got %q", expectedText, wrappedObservation.Summary)
		}
	}
	for _, privateText := range []string{"ws://secret", "/Users/private/profile", "cdpURL", "profilePath"} {
		if strings.Contains(wrappedObservation.Summary, privateText) {
			t.Fatalf("expected sanitized summary to omit %q, got %q", privateText, wrappedObservation.Summary)
		}
	}
	wrappedEvents := services.taskEventService.ListTaskEvent("wrapped-run")
	if !taskEventsContain(wrappedEvents, "tool."+CapabilityInvokeToolName+".requested", `"operation":"browser.snapshot"`) {
		t.Fatalf("expected wrapped request to retain the outer event name, got %+v", wrappedEvents)
	}
	if !taskEventsContain(wrappedEvents, "tool."+CapabilityInvokeToolName+".result", `"tool":"browser.snapshot"`) {
		t.Fatalf("expected outer compatibility event with effective operation, got %+v", wrappedEvents)
	}
	if taskEventsContain(wrappedEvents, "tool.browser.snapshot.requested", "") || taskEventsContain(wrappedEvents, "tool.browser.snapshot.result", "") {
		t.Fatalf("expected wrapped events to retain the outer event name, got %+v", wrappedEvents)
	}
}

func TestAgentTurnRunnerTreatsToolFailureAsObservation(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"unstable","input":{}}}`,
		finishMessageDocument("handled failure"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestCapabilityToolSet([]string{"unstable"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "unstable"}, func(context.Context, ToolInvocation) (ToolResult, error) {
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
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.capability.invoke.result", "tool failed") {
		t.Fatal("expected the tool failure to be recorded as an observation the model can answer from")
	}
}

func TestAgentTurnRunnerStoresLargeToolResultAsArtifact(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"capability.invoke","toolInput":{"operation":"large","input":{}}}`,
		finishMessageDocument("summarized"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{ToolResultMaxBytes: 8})
	toolRegistry := newTestCapabilityToolSet([]string{"large"})
	toolRegistry.RegisterTool(ToolDefinition{Name: "large"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess(strings.Repeat("x", 32)), nil
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
	content := `{"siteID":"site-1","slug":"tangerine-hub","title":"맛있는 귤 사이트","status":"published","publishedURL":"https://tangerine-hub.example-device.example.test","description":"` + strings.Repeat("x", 4096) + `"}`
	summary := modelVisibleToolResultSummary(context.Background(), nil, "site.publish", turnObservation{
		Tool: "site.publish",
		Output: ToolOutput{
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

func TestModelVisibleToolResultSummaryHidesDraftSiteCreateURL(t *testing.T) {
	content := `{"siteID":"site-1","slug":"draft-site","title":"Draft","status":"draft","publishedURL":"https://draft-site.example-device.example.test","sourceWorkspacePath":"/workspace/circles/staff/sites/site-1/draft"}`
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
