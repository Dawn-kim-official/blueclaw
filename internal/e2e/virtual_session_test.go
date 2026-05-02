package e2e

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/capability"
	"blueclaw/internal/llm"
)

func TestSlidesScenarioDoesNotScriptToolCalls(t *testing.T) {
	scenario := SlidesLocalMultiturnSuccessScenario(t.TempDir())
	if len(scenario.Turns) != 1 {
		t.Fatalf("expected one slides turn, got %d", len(scenario.Turns))
	}
	if len(scenario.Turns[0].ModelResponses) != 0 {
		t.Fatal("slides scenario must not script model tool calls or artifact creation")
	}
}

func TestSlidesLocalMultiturnSuccessLive(t *testing.T) {
	if !truthyEnvironmentValue(os.Getenv("BLUECLAW_E2E_LIVE")) {
		t.Skip("set BLUECLAW_E2E_LIVE=1 to explicitly run costed live slides virtual session")
	}
	endpoint := strings.TrimSpace(os.Getenv("BLUECLAW_E2E_LLM_ENDPOINT"))
	socketPath := strings.TrimSpace(os.Getenv("BLUECLAW_E2E_LLM_UNIX_SOCKET"))
	if endpoint == "" && socketPath == "" {
		t.Skip("set BLUECLAW_E2E_LLM_ENDPOINT or BLUECLAW_E2E_LLM_UNIX_SOCKET to run live slides virtual session")
	}
	scenario := SlidesLocalMultiturnSuccessScenario(t.TempDir())
	scenario.LanguageModel = llm.CapabilityLLMClient{
		CapabilityClient: capability.NewClient(capability.Configuration{
			Endpoint:       endpoint,
			UnixSocketPath: socketPath,
			Timeout:        90 * time.Second,
		}),
		ModelName:     os.Getenv("BLUECLAW_E2E_LLM_MODEL"),
		ExecutionMode: firstNonEmptyTestString(os.Getenv("BLUECLAW_E2E_LLM_EXECUTION_MODE"), "auto"),
	}

	result, errorValue := RunVirtualSession(context.Background(), scenario)
	if errorValue != nil {
		t.Fatalf("expected slides scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn result, got %d", len(result.TurnResults))
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "tool.terminal.run.result", "exitCode") {
		t.Fatal("expected terminal build to succeed")
	}
}

func TestMemoryGuidedFollowup(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), MemoryGuidedFollowupScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected memory scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turn results, got %d", len(result.TurnResults))
	}
	secondTurn := result.TurnResults[1]
	if !eventsContain(secondTurn.Events, "agent.task_launched", `"memoryFactCount":`) {
		t.Fatal("expected task launch memory fact count")
	}
	if strings.Contains(secondTurn.FinalReply, "아까") {
		t.Fatalf("expected concrete recalled preference, got %q", secondTurn.FinalReply)
	}
}

func TestToolPermissionHidesSkill(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), ToolPermissionHidesSkillScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected permission scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if eventsContain(turnResult.Events, "agent.instructions_loaded", `"status":"selected"`) {
		t.Fatal("expected missing terminal/file attach tools to hide full skill selection")
	}
	if !eventsContain(turnResult.Events, "agent.instructions_loaded", "missing_required_tools") {
		t.Fatal("expected missing tool skip reason")
	}
}

func TestGWSDisabled(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), GWSDisabledScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected gws disabled scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "tool.google.drive.import_pptx.requested", "google.drive.import_pptx") {
		t.Fatal("expected attempted google tool request to be audited")
	}
	if !eventsContain(turnResult.Events, "tool.google.drive.import_pptx.result", "tool is not allowed") {
		t.Fatal("expected google tool to be denied by catalog allowlist")
	}
}

func firstNonEmptyTestString(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}

func truthyEnvironmentValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}
