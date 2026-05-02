package e2e

import (
	"context"
	"strings"
	"testing"
)

func TestSlidesLocalMultiturnSuccess(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), SlidesLocalMultiturnSuccessScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected slides scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn result, got %d", len(result.TurnResults))
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "agent.completion_required", "file.attach") {
		t.Fatal("expected bad final reply to be rejected by required evidence gate")
	}
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
