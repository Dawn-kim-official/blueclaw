package agent

import (
	"context"
	"encoding/json"
	"testing"
)

func TestToolExposureUsesOnlyBaseToolsWithoutSelectedSkills(t *testing.T) {
	toolSet := newExposureTestToolSet(map[string]json.RawMessage{
		"site.create": json.RawMessage(`{"type":"object"}`),
	})

	filteredToolSet, event := toolSetForAgentTurnWithExposure(
		toolSet,
		InstructionBundle{},
	)

	if !sameStringSet(filteredToolSet.ListToolNames(), baseToolNames(toolSet)) {
		t.Fatalf("expected only base tools, got %+v", filteredToolSet.ListToolNames())
	}
	for _, toolName := range []string{CapabilityInvokeToolName, "site.create"} {
		if filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to stay hidden without a selected skill", toolName)
		}
	}
	if event.SelectionSource != "selected_skill_allowed_tools" {
		t.Fatalf("expected selected-skill exposure, got %+v", event)
	}
}

func TestToolExposureAddsEverySelectedSkillAllowedTool(t *testing.T) {
	toolSet := newExposureTestToolSet(map[string]json.RawMessage{
		"schedule.create": json.RawMessage(`{"type":"object"}`),
		"schedule.cancel": json.RawMessage(`{"type":"object"}`),
		"web.search":      json.RawMessage(`{"type":"object"}`),
		"site.delete":     json.RawMessage(`{"type":"object"}`),
	})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{Name: "scheduled-task", AllowedTools: []string{CapabilityInvokeToolName, "schedule.create", "schedule.cancel"}},
			{Name: "web-search", AllowedTools: []string{"web.search"}},
			{Name: "website-management", AllowedTools: []string{"site.delete"}},
		},
		SkillDecisions: []SkillSelectionDecision{
			{Name: "scheduled-task", Status: "selected"},
			{Name: "web-search", Status: "selected"},
			{Name: "website-management", Status: "skipped"},
		},
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(
		toolSet,
		instructionBundle,
	)

	for _, toolName := range []string{"schedule.create", "schedule.cancel", "web.search"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected selected skill tool %s, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
	for _, toolName := range baseToolNames(toolSet) {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected base tool %s, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
	for _, toolName := range []string{CapabilityInvokeToolName, "site.delete"} {
		if filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected unselected tool %s to stay hidden", toolName)
		}
	}
	if !sameStringSet(event.SelectedSkillToolIDs, []string{"schedule.create", "schedule.cancel", "web.search"}) {
		t.Fatalf("expected selected skill tools in the exposure event, got %+v", event)
	}
}

func TestToolExposureAlwaysIncludesRegisteredBaseTools(t *testing.T) {
	toolSet := NewToolSet(KernelToolNames())
	for _, toolName := range KernelToolNames() {
		toolSet.RegisterBoundTool(BoundTool{
			Definition:   ToolDefinition{Name: toolName},
			Availability: ToolAvailability{Status: ToolAvailabilityDenied},
			Handler:      func(context.Context, ToolInvocation) (ToolResult, error) { return ToolSuccess("ok"), nil },
		})
	}

	filteredToolSet := toolSetForAgentTurn(toolSet, InstructionBundle{})

	for _, toolName := range baseToolNames(toolSet) {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected registered base tool %s, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestToolExposureDoesNotPromoteTemporaryAllowedToolsToBaseTools(t *testing.T) {
	toolSet := newExposureTestToolSet(map[string]json.RawMessage{
		"site.create": json.RawMessage(`{"type":"object"}`),
	})
	temporarilyExpandedToolSet := toolSet.WithAdditionalAllowedToolNames([]string{"site.create"})

	if !temporarilyExpandedToolSet.IsAllowed("site.create") {
		t.Fatal("expected temporary expansion to allow site.create")
	}
	if stringSliceContains(temporarilyExpandedToolSet.ListBaseToolNames(), "site.create") {
		t.Fatal("expected temporary expansion not to change base tool provenance")
	}

	filteredToolSet := toolSetForAgentTurn(temporarilyExpandedToolSet, InstructionBundle{})
	if filteredToolSet.IsAllowed("site.create") {
		t.Fatal("expected site.create to be hidden again without a selected skill")
	}
}

func TestToolExposureRespectsPermanentBaseNarrowing(t *testing.T) {
	toolSet := newExposureTestToolSet(map[string]json.RawMessage{
		"task.add": json.RawMessage(`{"type":"object"}`),
	})
	restrictedToolSet := toolSet.WithAllowedToolNames([]string{"task.add"})

	filteredToolSet := toolSetForAgentTurn(restrictedToolSet, InstructionBundle{
		Skills:         []SkillInstruction{{Name: "internkim-flow", AllowedTools: []string{"task.add"}}},
		SkillDecisions: []SkillSelectionDecision{{Name: "internkim-flow", Status: "selected"}},
	})

	if !filteredToolSet.IsAllowed("task.add") {
		t.Fatal("expected permanently allowed task.add to remain exposed")
	}
	for _, forbiddenToolName := range []string{TerminalRunToolName, FileWriteToolName, CapabilityInvokeToolName} {
		if filteredToolSet.IsAllowed(forbiddenToolName) {
			t.Fatalf("expected permanent narrowing to keep %s hidden", forbiddenToolName)
		}
	}
}

func TestModelCallableToolSetPreservesEmptyPaletteRestriction(t *testing.T) {
	toolSet := NewToolSet([]string{"missing.tool"})
	toolSet.RegisterTool(ToolDefinition{Name: "hidden.tool"}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("ok"), nil
	})

	if modelCallableToolSet(toolSet).IsAllowed("hidden.tool") {
		t.Fatal("expected an empty callable palette to remain restricted")
	}
}

func TestModelCallableToolSetAlwaysHidesCapabilityInvoke(t *testing.T) {
	toolSet := newTestCapabilityToolSet([]string{"site.create"})

	modelToolSet := modelCallableToolSet(toolSet)

	if modelToolSet.IsAllowed(CapabilityInvokeToolName) {
		t.Fatal("expected final model boundary to hide capability.invoke")
	}
	if !modelToolSet.IsAllowed("site.create") {
		t.Fatal("expected final model boundary to preserve other allowed tools")
	}
}

func sameStringSet(leftValues []string, rightValues []string) bool {
	if len(leftValues) != len(rightValues) {
		return false
	}
	rightValueByValue := map[string]bool{}
	for _, rightValue := range rightValues {
		rightValueByValue[rightValue] = true
	}
	for _, leftValue := range leftValues {
		if !rightValueByValue[leftValue] {
			return false
		}
	}
	return true
}
