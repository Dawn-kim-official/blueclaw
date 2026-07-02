package agent

import (
	"context"
	"testing"
)

func TestValidateRequiredEvidencePreservesInvalidEvidence(t *testing.T) {
	toolSet := newTestToolSet([]string{"calendar.add", "schedule.create"})

	report := validateRequiredEvidenceTools(toolSet, []string{"calendar.create", "schedule.create"})

	if !report.HasInvalidEvidence() {
		t.Fatal("expected invalid evidence to be reported")
	}
	if len(report.InvalidEvidence) != 1 || report.InvalidEvidence[0] != "calendar.create" {
		t.Fatalf("expected calendar.create invalid evidence, got %+v", report.InvalidEvidence)
	}
	if len(report.RequiredEvidence) != 2 {
		t.Fatalf("expected original required evidence to be preserved, got %+v", report.RequiredEvidence)
	}
}

func TestValidateRequiredEvidenceAcceptsHiddenCapabilityOperationThroughInvoke(t *testing.T) {
	toolSet := NewToolSet([]string{CapabilityInvokeToolName})
	for _, toolName := range []string{CapabilityInvokeToolName, "calendar.add"} {
		currentToolName := toolName
		toolSet.RegisterTool(ToolDefinition{Name: currentToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolSuccess("ok"), nil
		})
	}

	report := validateRequiredEvidenceTools(toolSet, []string{"calendar.add"})

	if report.HasInvalidEvidence() {
		t.Fatalf("expected hidden calendar.add to be valid capability evidence, got %+v", report)
	}
	if report.EvidenceKinds["calendar.add"] != requiredEvidenceToolKindCapabilityOperation {
		t.Fatalf("expected capability evidence kind, got %+v", report.EvidenceKinds)
	}
	if toolSet.IsAllowed("calendar.add") {
		t.Fatal("expected calendar.add to remain hidden from direct model calls")
	}
}

func TestValidateRequiredEvidenceRejectsHiddenOperationWithoutCapabilityInvoke(t *testing.T) {
	toolSet := NewToolSet([]string{TerminalRunToolName})
	for _, toolName := range []string{TerminalRunToolName, "calendar.add"} {
		currentToolName := toolName
		toolSet.RegisterTool(ToolDefinition{Name: currentToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolSuccess("ok"), nil
		})
	}

	report := validateRequiredEvidenceTools(toolSet, []string{"calendar.add"})

	if !report.HasInvalidEvidence() {
		t.Fatal("expected hidden calendar.add to be invalid without capability.invoke")
	}
}

func TestValidateRequiredEvidenceClassifiesNativeTool(t *testing.T) {
	toolSet := newTestToolSet([]string{FileDeliverToolName})

	report := validateRequiredEvidenceTools(toolSet, []string{FileDeliverToolName})

	if report.HasInvalidEvidence() {
		t.Fatalf("expected file.deliver to be valid native evidence, got %+v", report)
	}
	if report.EvidenceKinds[FileDeliverToolName] != requiredEvidenceToolKindNativeTool {
		t.Fatalf("expected native evidence kind, got %+v", report.EvidenceKinds)
	}
}

func TestValidateRequiredEvidenceRejectsCapabilityInvokeAsEvidence(t *testing.T) {
	toolSet := newTestToolSet([]string{CapabilityInvokeToolName, "calendar.add"})

	report := validateRequiredEvidenceTools(toolSet, []string{CapabilityInvokeToolName})

	if !report.HasInvalidEvidence() {
		t.Fatal("expected capability.invoke evidence to be invalid")
	}
}

func TestValidateRequiredEvidenceAcceptsCanonicalDeliveryAlias(t *testing.T) {
	toolSet := newTestToolSet([]string{FileDeliverToolName})

	report := validateRequiredEvidenceTools(toolSet, []string{FileAttachToolName})

	if report.HasInvalidEvidence() {
		t.Fatalf("expected file.attach alias to match registered file.deliver evidence, got %+v", report)
	}
}
