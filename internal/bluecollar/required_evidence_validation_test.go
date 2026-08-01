package bluecollar

import (
	"context"
	"github.com/Dawn-kim-official/blueclaw/internal/toolcontract"
	"testing"
)

func TestRequiredEvidenceToolCanBeSatisfiedAcceptsDirectTool(t *testing.T) {
	toolSet := newTestToolSet([]string{"calendar_add"})

	if !requiredEvidenceToolCanBeSatisfied(toolSet, "calendar_add") {
		t.Fatal("expected directly callable calendar_add to be satisfiable")
	}
}

func TestRequiredEvidenceToolCanBeSatisfiedAcceptsRegisteredCapabilityOperation(t *testing.T) {
	toolSet := toolcontract.NewToolSet([]string{toolcontract.TerminalRunToolName})
	for _, toolName := range []string{toolcontract.TerminalRunToolName, "calendar_add"} {
		currentToolName := toolName
		registerTestTool(toolSet, toolcontract.ToolDefinition{Name: currentToolName}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			return testToolSuccess("ok"), nil
		})
	}

	if !requiredEvidenceToolCanBeSatisfied(toolSet, "calendar_add") {
		t.Fatal("expected registered capability operation calendar_add to be satisfiable")
	}
	if toolSet.IsAllowed("calendar_add") {
		t.Fatal("expected calendar_add to remain hidden until selected")
	}
}

func TestRequiredEvidenceToolCanBeSatisfiedRejectsUnavailableTool(t *testing.T) {
	toolSet := toolcontract.NewToolSet([]string{toolcontract.TerminalRunToolName})
	toolSet.RegisterBoundTool(toolcontract.BoundTool{
		Definition:   toolcontract.ToolDefinition{Name: "calendar_add"},
		Availability: toolcontract.ToolAvailability{Status: toolcontract.ToolAvailabilityDenied},
		Handler: func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			return testToolSuccess("ok"), nil
		},
	})

	if requiredEvidenceToolCanBeSatisfied(toolSet, "calendar_add") {
		t.Fatal("expected an unavailable calendar_add to be unsatisfiable")
	}
}

func TestRequiredEvidenceToolCanBeSatisfiedRejectsDisallowedKernelTool(t *testing.T) {
	toolSet := toolcontract.NewToolSet([]string{"file_write"})
	for _, toolName := range []string{"file_write", toolcontract.FileDeliverToolName} {
		registerTestTool(toolSet, toolcontract.ToolDefinition{Name: toolName}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			return testToolSuccess("ok"), nil
		})
	}

	if requiredEvidenceToolCanBeSatisfied(toolSet, toolcontract.FileDeliverToolName) {
		t.Fatal("expected a disallowed kernel tool to be unsatisfiable")
	}
}

func TestRequiredEvidenceToolCanBeSatisfiedRejectsUnregisteredName(t *testing.T) {
	toolSet := newTestToolSet([]string{"calendar_add", "schedule_create"})

	if requiredEvidenceToolCanBeSatisfied(toolSet, "calendar.create") {
		t.Fatal("expected an unregistered tool name to be unsatisfiable")
	}
	if !requiredEvidenceToolCanBeSatisfied(toolSet, "schedule_create") {
		t.Fatal("expected a registered tool name to remain satisfiable")
	}
}

func TestWorkingSetEvidenceGroupKeepsReadsAndWrites(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		{Name: "task_add", Namespace: "task", SideEffectClass: toolcontract.ToolSideEffectStateChange},
		{Name: "task_list", Namespace: "task", SideEffectClass: toolcontract.ToolSideEffectRead},
		{Name: "task_update", Namespace: "task", SideEffectClass: toolcontract.ToolSideEffectStateChange},
	})

	group := workingSetEvidenceGroup(toolSet, []string{"task_add", "task_list", "task_update", "task_add", "unregistered.operation"})

	if len(group) != 3 || !stringSliceContains(group, "task_add") || !stringSliceContains(group, "task_list") || !stringSliceContains(group, "task_update") {
		t.Fatalf("expected deduplicated satisfiable tools including reads, got %+v", group)
	}
	if stringSliceContains(group, "unregistered.operation") {
		t.Fatalf("expected the unregistered tool to be excluded, got %+v", group)
	}
}

func TestWorkingSetEvidenceGroupEmptyWhenNoCandidatesAreDerivable(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		{Name: "task_list", Namespace: "task", SideEffectClass: toolcontract.ToolSideEffectRead},
	})

	group := workingSetEvidenceGroup(toolSet, []string{"unregistered.operation"})

	if len(group) != 0 {
		t.Fatalf("expected no derivable evidence candidates, got %+v", group)
	}
}
