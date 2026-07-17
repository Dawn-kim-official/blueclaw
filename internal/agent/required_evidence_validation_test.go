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

func TestValidateRequiredEvidenceAcceptsDirectTool(t *testing.T) {
	toolSet := newTestToolSet([]string{"calendar.add"})

	report := validateRequiredEvidenceTools(toolSet, []string{"calendar.add"})

	if report.HasInvalidEvidence() {
		t.Fatalf("expected direct calendar.add to be valid evidence, got %+v", report)
	}
	if report.EvidenceKinds["calendar.add"] != requiredEvidenceToolKindNativeTool {
		t.Fatalf("expected native evidence kind, got %+v", report.EvidenceKinds)
	}
	if !toolSet.IsAllowed("calendar.add") {
		t.Fatal("expected calendar.add to remain directly callable")
	}
}

func TestValidateRequiredEvidenceAcceptsRegisteredDirectTool(t *testing.T) {
	toolSet := NewToolSet([]string{TerminalRunToolName})
	for _, toolName := range []string{TerminalRunToolName, "calendar.add"} {
		currentToolName := toolName
		toolSet.RegisterTool(ToolDefinition{Name: currentToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolSuccess("ok"), nil
		})
	}

	report := validateRequiredEvidenceTools(toolSet, []string{"calendar.add"})

	if report.HasInvalidEvidence() {
		t.Fatalf("expected registered calendar.add to be valid evidence, got %+v", report)
	}
	if report.EvidenceKinds["calendar.add"] != requiredEvidenceToolKindCapabilityOperation {
		t.Fatalf("expected direct capability operation evidence, got %+v", report.EvidenceKinds)
	}
	if toolSet.IsAllowed("calendar.add") {
		t.Fatal("expected calendar.add to remain hidden until selected")
	}
}

func TestValidateRequiredEvidenceRejectsUnavailableDirectTool(t *testing.T) {
	toolSet := NewToolSet([]string{TerminalRunToolName})
	toolSet.RegisterBoundTool(BoundTool{
		Definition:   ToolDefinition{Name: "calendar.add"},
		Availability: ToolAvailability{Status: ToolAvailabilityDenied},
		Handler: func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolSuccess("ok"), nil
		},
	})

	report := validateRequiredEvidenceTools(toolSet, []string{"calendar.add"})

	if !report.HasInvalidEvidence() {
		t.Fatal("expected unavailable calendar.add to be invalid")
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

func TestValidateRequiredEvidenceAcceptsCanonicalDeliveryAlias(t *testing.T) {
	toolSet := newTestToolSet([]string{FileDeliverToolName})

	report := validateRequiredEvidenceTools(toolSet, []string{FileAttachToolName})

	if report.HasInvalidEvidence() {
		t.Fatalf("expected file.attach alias to match registered file.deliver evidence, got %+v", report)
	}
}

func TestRequiredEvidenceDoesNotAssumeSideEffectForMaintenanceTask(t *testing.T) {
	intakeDecision := IntakeDecision{
		Classification: IntakeClassificationBoundedTask,
		TaskShape:      TaskShapeMaintenanceTask,
	}

	isMissing := requiredEvidenceMissingForSideEffect(intakeDecision, OutcomeContract{}, newTestToolSet([]string{"task.add"}))

	if isMissing {
		t.Fatal("expected maintenance task without a typed side-effect signal not to require recovery")
	}
}

func TestRequiredEvidenceNotMissingForReadOnlyResearchTask(t *testing.T) {
	intakeDecision := IntakeDecision{
		Classification:   IntakeClassificationBoundedTask,
		TaskShape:        TaskShapeResearchTask,
		InitialToolNames: []string{"web.search"},
	}

	isMissing := requiredEvidenceMissingForSideEffect(intakeDecision, OutcomeContract{}, newTestToolSet([]string{"web.search"}))

	if isMissing {
		t.Fatal("expected read-only research task not to require side-effect evidence")
	}
}

func TestRequiredEvidencePreservesReadOnlyMaintenanceEvidence(t *testing.T) {
	intakeDecision := IntakeDecision{
		Classification: IntakeClassificationBoundedTask,
		TaskShape:      TaskShapeMaintenanceTask,
	}
	toolSet := newTestCapabilityToolSet([]string{"task.history", "task.update"})
	outcomeContract := OutcomeContract{RequiredEvidenceTools: []string{"task.history"}}

	isMissing := requiredEvidenceMissingForSideEffect(intakeDecision, outcomeContract, toolSet)

	if isMissing {
		t.Fatal("expected read-only task.history evidence to remain valid for maintenance lookup")
	}
}

func TestRequiredEvidenceIgnoresSpeculativeInitialSideEffect(t *testing.T) {
	intakeDecision := IntakeDecision{
		Classification:        IntakeClassificationBoundedTask,
		TaskShape:             TaskShapeMaintenanceTask,
		InitialToolNames:      []string{TerminalRunToolName},
		RequiredEvidenceTools: []string{"task.list"},
	}
	toolSet := newTestCapabilityToolSet([]string{TerminalRunToolName, "task.list"})
	outcomeContract := OutcomeContract{RequiredEvidenceTools: []string{"task.list"}}

	if requiredEvidenceMissingForSideEffect(intakeDecision, outcomeContract, toolSet) {
		t.Fatal("expected typed task.list evidence to remain authoritative")
	}
}

func TestRequiredEvidenceRequiresExplicitInitialSideEffectTool(t *testing.T) {
	intakeDecision := IntakeDecision{
		Classification:   IntakeClassificationBoundedTask,
		TaskShape:        TaskShapeResearchTask,
		InitialToolNames: []string{"task.update"},
	}
	toolSet := newTestCapabilityToolSet([]string{"task.history", "task.update"})

	isMissing := requiredEvidenceMissingForSideEffect(intakeDecision, OutcomeContract{}, toolSet)

	if !isMissing {
		t.Fatal("expected an explicitly selected task.update tool to require side-effect evidence")
	}
}

func TestRequiredEvidencePreservesReadOnlyResearchEvidence(t *testing.T) {
	intakeDecision := IntakeDecision{
		Classification: IntakeClassificationBoundedTask,
		TaskShape:      TaskShapeResearchTask,
	}
	toolSet := newTestCapabilityToolSet([]string{"task.history"})
	outcomeContract := OutcomeContract{RequiredEvidenceTools: []string{"task.history"}}

	isMissing := requiredEvidenceMissingForSideEffect(intakeDecision, outcomeContract, toolSet)

	if isMissing {
		t.Fatal("expected task.history to remain valid evidence for read-only research")
	}
}

func TestRequiredEvidenceRequiresSiteSideEffectAlongsideStatus(t *testing.T) {
	intakeDecision := IntakeDecision{
		Classification:        IntakeClassificationBoundedTask,
		TaskShape:             TaskShapeMaintenanceTask,
		RequiredEvidenceTools: []string{"site.status"},
	}
	toolSet := newTestToolSetWithDefinitions([]ToolDefinition{
		{Name: "site.status", Namespace: "site", SideEffectClass: ToolSideEffectRead},
		{Name: "site.publish", Namespace: "site", SideEffectClass: ToolSideEffectExternalPublish},
	})

	if !requiredEvidenceMissingForSideEffect(intakeDecision, OutcomeContract{RequiredEvidenceTools: []string{"site.status"}}, toolSet) {
		t.Fatal("expected site.status alone not to prove a site side effect")
	}
	if requiredEvidenceMissingForSideEffect(intakeDecision, OutcomeContract{RequiredEvidenceTools: []string{"site.publish", "site.status"}}, toolSet) {
		t.Fatal("expected site.publish and site.status evidence to preserve the publish contract")
	}
}

func TestRequiredEvidencePreservesDeliveryAndSendSideEffects(t *testing.T) {
	intakeDecision := IntakeDecision{
		Classification: IntakeClassificationBoundedTask,
		TaskShape:      TaskShapeMaintenanceTask,
	}
	toolSet := newTestCapabilityToolSet([]string{FileDeliverToolName, "message.send"})
	toolSet.RegisterTool(ToolDefinition{Name: FileDeliverToolName, SideEffectClass: ToolSideEffectExternalWrite}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("ok"), nil
	})

	for _, toolName := range []string{FileDeliverToolName, "message.send"} {
		outcomeContract := OutcomeContract{RequiredEvidenceTools: []string{toolName}}
		if requiredEvidenceMissingForSideEffect(intakeDecision, outcomeContract, toolSet) {
			t.Fatalf("expected %s to remain valid side-effect evidence", toolName)
		}
	}
}
