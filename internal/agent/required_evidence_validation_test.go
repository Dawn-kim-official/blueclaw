package agent

import (
	"context"
	"testing"
)

func TestRequiredEvidenceToolCanBeSatisfiedAcceptsDirectTool(t *testing.T) {
	toolSet := newTestToolSet([]string{"calendar.add"})

	if !requiredEvidenceToolCanBeSatisfied(toolSet, "calendar.add") {
		t.Fatal("expected directly callable calendar.add to be satisfiable")
	}
}

func TestRequiredEvidenceToolCanBeSatisfiedAcceptsRegisteredCapabilityOperation(t *testing.T) {
	toolSet := NewToolSet([]string{TerminalRunToolName})
	for _, toolName := range []string{TerminalRunToolName, "calendar.add"} {
		currentToolName := toolName
		registerTestTool(toolSet, ToolDefinition{Name: currentToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return testToolSuccess("ok"), nil
		})
	}

	if !requiredEvidenceToolCanBeSatisfied(toolSet, "calendar.add") {
		t.Fatal("expected registered capability operation calendar.add to be satisfiable")
	}
	if toolSet.IsAllowed("calendar.add") {
		t.Fatal("expected calendar.add to remain hidden until selected")
	}
}

func TestRequiredEvidenceToolCanBeSatisfiedRejectsUnavailableTool(t *testing.T) {
	toolSet := NewToolSet([]string{TerminalRunToolName})
	toolSet.RegisterBoundTool(BoundTool{
		Definition:   ToolDefinition{Name: "calendar.add"},
		Availability: ToolAvailability{Status: ToolAvailabilityDenied},
		Handler: func(context.Context, ToolInvocation) (ToolResult, error) {
			return testToolSuccess("ok"), nil
		},
	})

	if requiredEvidenceToolCanBeSatisfied(toolSet, "calendar.add") {
		t.Fatal("expected an unavailable calendar.add to be unsatisfiable")
	}
}

func TestRequiredEvidenceToolCanBeSatisfiedRejectsDisallowedKernelTool(t *testing.T) {
	toolSet := NewToolSet([]string{"file.write"})
	for _, toolName := range []string{"file.write", FileDeliverToolName} {
		registerTestTool(toolSet, ToolDefinition{Name: toolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return testToolSuccess("ok"), nil
		})
	}

	if requiredEvidenceToolCanBeSatisfied(toolSet, FileDeliverToolName) {
		t.Fatal("expected a disallowed kernel tool to be unsatisfiable")
	}
}

func TestRequiredEvidenceToolCanBeSatisfiedRejectsUnregisteredName(t *testing.T) {
	toolSet := newTestToolSet([]string{"calendar.add", "schedule.create"})

	if requiredEvidenceToolCanBeSatisfied(toolSet, "calendar.create") {
		t.Fatal("expected an unregistered tool name to be unsatisfiable")
	}
	if !requiredEvidenceToolCanBeSatisfied(toolSet, "schedule.create") {
		t.Fatal("expected a registered tool name to remain satisfiable")
	}
}

func TestWorkingSetSideEffectEvidenceGroup(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{
		{Name: "task.add", Namespace: "task", SideEffectClass: ToolSideEffectStateChange},
		{Name: "task.list", Namespace: "task", SideEffectClass: ToolSideEffectRead},
		{Name: "task.update", Namespace: "task", SideEffectClass: ToolSideEffectStateChange},
	})

	group := workingSetSideEffectEvidenceGroup(toolSet, []string{"task.add", "task.list", "task.update", "task.add", "unregistered.operation"})

	if len(group) != 2 || !stringSliceContains(group, "task.add") || !stringSliceContains(group, "task.update") {
		t.Fatalf("expected deduplicated side-effect tools only, got %+v", group)
	}
	if stringSliceContains(group, "task.list") {
		t.Fatalf("expected the read-only tool to be excluded, got %+v", group)
	}
	if stringSliceContains(group, "unregistered.operation") {
		t.Fatalf("expected the unregistered tool to be excluded, got %+v", group)
	}
}

func TestWorkingSetSideEffectEvidenceGroupEmptyWhenNoCandidatesAreDerivable(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{
		{Name: "task.list", Namespace: "task", SideEffectClass: ToolSideEffectRead},
	})

	group := workingSetSideEffectEvidenceGroup(toolSet, []string{"task.list"})

	if len(group) != 0 {
		t.Fatalf("expected no derivable side-effect evidence, got %+v", group)
	}
}
