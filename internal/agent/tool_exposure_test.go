package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// newHybridKernelCapabilityToolSet mirrors production shape: kernel tools are
// exposed directly, while operationNames are only reachable through the
// neutral capability.invoke verb (see newTestCapabilityToolSet in
// tool_set_test.go for the capability-only variant of this pattern).
func newHybridKernelCapabilityToolSet(kernelToolNames []string, operationNames []string) *ToolSet {
	toolSet := NewToolSet(append([]string{CapabilityInvokeToolName}, kernelToolNames...))
	toolSet.RegisterTool(ToolDefinition{Name: CapabilityInvokeToolName}, func(ctx context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		var document struct {
			Operation string          `json:"operation"`
			Input     json.RawMessage `json:"input"`
		}
		if errorValue := json.Unmarshal(toolInvocation.Input, &document); errorValue != nil {
			return ToolInputFailure(errorValue.Error()), nil
		}
		return toolSet.InvokeRegistered(ctx, ToolInvocation{ToolName: document.Operation, Input: document.Input})
	})
	for _, toolName := range append(append([]string{}, kernelToolNames...), operationNames...) {
		toolSet.RegisterTool(ToolDefinition{Name: toolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolSuccess("ok"), nil
		})
	}
	return toolSet
}

// The candidate-group/fallback-group selection system and per-domain pinned
// exposure it drove are gone: toolSetForAgentTurnWithExposure now always
// exposes the fixed kernel (see TestToolExposureUsesFixedKernelOnly in
// tool_exposure_kernel_test.go). requestWithStepWorkingSetTools still pins
// tool names for other bookkeeping, but any non-kernel evidence tool is only
// reachable through capability.invoke now, so it collapses into that single
// pin instead of being pinned by its own operation name.
func TestStepWorkingSetPinsKernelEvidenceByNameAndCollapsesDomainEvidenceIntoCapabilityInvoke(t *testing.T) {
	toolSet := newHybridKernelCapabilityToolSet(
		[]string{"file.read", "file.write", "file.edit", "terminal.run"},
		[]string{"site.status", "site.create", "site.build", "artifact.review", "site.publish", "calendar.add", "task.add"},
	)
	request := requestWithStepWorkingSetTools(AgentTurnRequest{
		ToolSet: toolSet,
		RequiredEvidenceTools: []string{
			"file.read", "file.write", "file.edit", "terminal.run",
			"site.status", "site.create", "site.build", "artifact.review", "site.publish",
		},
	}, nil)

	for _, toolName := range []string{"file.read", "file.write", "file.edit", "terminal.run"} {
		if !containsString(request.PinnedToolNames, toolName) {
			t.Fatalf("expected kernel evidence tool %s to be pinned by name, got %+v", toolName, request.PinnedToolNames)
		}
	}
	if !containsString(request.PinnedToolNames, CapabilityInvokeToolName) {
		t.Fatalf("expected non-kernel evidence tools to collapse into a pinned %s, got %+v", CapabilityInvokeToolName, request.PinnedToolNames)
	}
	for _, toolName := range []string{"site.status", "site.create", "site.build", "artifact.review", "site.publish", "calendar.add", "task.add"} {
		if containsString(request.PinnedToolNames, toolName) {
			t.Fatalf("expected domain operation %s not to be pinned by its own name now that it is only reachable through capability.invoke, got %+v", toolName, request.PinnedToolNames)
		}
	}
}

func TestRecoveryWorkingSetDropsExhaustedTool(t *testing.T) {
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "file.edit",
		Failure: &ToolFailure{
			Kind:            FailureInvalidInput,
			Code:            FailureCodes.InvalidInput.String(),
			Stage:           "file_edit",
			UserSafeSummary: "oldText must match exactly once",
			RecoveryHints: []RecoveryHint{{
				Action:    "inspect_then_targeted_edit",
				ToolNames: []string{"file.read", "file.edit"},
			}},
		},
		ToolInputKey: "file.edit\x00{\"path\":\"App.tsx\"}",
	}, {
		ObservationID: "obs-002",
		Action:        "policy",
		Tool:          "file.edit",
		Output:        ToolOutput{Content: "The recovery budget for corrected_retry is exhausted."},
		RecoveryStep:  recoveryStepCorrectedRetry,
		PolicyCode:    "recovery_budget_exhausted",
		Summary:       "The recovery budget for corrected_retry is exhausted.",
	}}

	toolNames := recoveryPinnedToolNames(InstructionBundle{}, AgentRequest{PinnedToolNames: []string{"file.edit", "file.write"}}, observations)

	if stringSliceContains(toolNames, "file.edit") {
		t.Fatalf("expected exhausted file.edit to be removed, got %+v", toolNames)
	}
	if !stringSliceContains(toolNames, "file.write") {
		t.Fatalf("expected alternate edit tool file.write to remain, got %+v", toolNames)
	}
}

func TestPlannedToolsDropRepeatedFileRead(t *testing.T) {
	observations := []turnObservation{
		newFailureObservation("obs-001", "policy", "file.read", "Already read tmp/deck/presentation.md lines 1-400.", FailurePolicyBlocked, FailureCodes.PolicyBlocked, "file_read_repeat"),
	}

	toolNames := filterExhaustedRecoveryToolNames([]string{"file.read", "terminal.run", "file.deliver"}, observations)

	if stringSliceContains(toolNames, "file.read") {
		t.Fatalf("expected repeated file.read to be removed, got %+v", toolNames)
	}
	for _, toolName := range []string{"terminal.run", "file.deliver"} {
		if !stringSliceContains(toolNames, toolName) {
			t.Fatalf("expected %s to remain available, got %+v", toolName, toolNames)
		}
	}
}

func TestToolSelectionContextUsesCompactCards(t *testing.T) {
	toolSet := testToolSet([]string{"site.status"})
	cards := renderCompactToolCards(toolSet, []toolExposureGroup{{Name: "G5 selected-skill candidates", ToolIDs: []string{"site.status"}}})
	summary := renderCoreGroupSummary(collectCoreGroups(testToolSet([]string{"skill.search", "terminal.run", "memory.remember"})))

	if !strings.Contains(cards, "- site.status:") {
		t.Fatalf("expected compact card to include tool id, got %s", cards)
	}
	if strings.Contains(cards, "inputSchema") || strings.Contains(cards, "properties") {
		t.Fatalf("expected compact card to omit full schema, got %s", cards)
	}
	if !strings.Contains(summary, "fixed kernel: terminal.run, skill.search") {
		t.Fatalf("expected fixed kernel core summary, got %s", summary)
	}
	if strings.Contains(summary, "memory.remember") {
		t.Fatalf("expected non-kernel tool memory.remember to be absent from the fixed kernel summary, got %s", summary)
	}
}

func newExposureTestToolSet(capabilityOperations map[string]json.RawMessage) *ToolSet {
	toolSet := NewToolSet(KernelToolNames())
	for _, kernelToolName := range KernelToolNames() {
		toolSet.RegisterBoundTool(BoundTool{
			Definition: ToolDefinition{Name: kernelToolName},
			Handler:    func(context.Context, ToolInvocation) (ToolResult, error) { return ToolSuccess("ok"), nil },
		})
	}
	for operationName, inputSchema := range capabilityOperations {
		toolSet.RegisterBoundTool(BoundTool{
			Definition: ToolDefinition{Name: operationName, InputSchema: inputSchema},
			Handler:    func(context.Context, ToolInvocation) (ToolResult, error) { return ToolSuccess("ok"), nil },
		})
	}
	return toolSet
}

func invalidInputCapabilityObservation(observationID string, operationName string) turnObservation {
	return newFailureObservation(observationID, "continue", operationName, operationName+" needs these input fields: slug.", FailureInvalidInput, FailureCodes.InvalidInput, "capability_input")
}

func TestPromotedOperationExposedWithFlatSchemaAfterInvalidInputFailure(t *testing.T) {
	siteCreateSchema := json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"]}`)
	toolSet := newExposureTestToolSet(map[string]json.RawMessage{"site.create": siteCreateSchema})
	observations := []turnObservation{invalidInputCapabilityObservation("obs-001", "site.create")}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(
		toolSet,
		InstructionBundle{},
		AgentRequest{Prompt: "build the mealkit reservation site"},
		ExecutionPlan{},
		false,
		OutcomeContract{},
		ToolExposureEvent{},
		observations,
	)

	if !filteredToolSet.IsAllowed("site.create") {
		t.Fatalf("expected promoted site.create to be exposed, got %+v", filteredToolSet.ListToolNames())
	}
	toolDefinition, isFound := filteredToolSet.ToolDefinition("site.create")
	if !isFound || string(toolDefinition.InputSchema) != string(siteCreateSchema) {
		t.Fatalf("expected promoted tool to use its own flat input schema, got %+v", toolDefinition)
	}
	if !stringSliceContains(event.PromotedOperationToolIDs, "site.create") {
		t.Fatalf("expected exposure event to record the promoted operation, got %+v", event)
	}
	for _, kernelToolName := range KernelToolNames() {
		if !filteredToolSet.IsAllowed(kernelToolName) {
			t.Fatalf("expected kernel tool %s to remain exposed, got %+v", kernelToolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestPromotedCapabilityOperationCapAtTwoMostRecentWins(t *testing.T) {
	toolSet := newExposureTestToolSet(map[string]json.RawMessage{
		"site.create":  json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"]}`),
		"calendar.add": json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`),
		"task.add":     json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"}},"required":["prompt"]}`),
	})
	observations := []turnObservation{
		invalidInputCapabilityObservation("obs-001", "site.create"),
		invalidInputCapabilityObservation("obs-002", "calendar.add"),
		invalidInputCapabilityObservation("obs-003", "task.add"),
	}

	promoted := promotedCapabilityOperationNames(toolSet, observations)

	if len(promoted) != 2 {
		t.Fatalf("expected promotion cap of 2, got %+v", promoted)
	}
	if !stringSliceContains(promoted, "task.add") || !stringSliceContains(promoted, "calendar.add") {
		t.Fatalf("expected the two most recent failures to win, got %+v", promoted)
	}
	if stringSliceContains(promoted, "site.create") {
		t.Fatalf("expected the oldest failure to be dropped under the cap, got %+v", promoted)
	}
}

func TestPromotedCapabilityOperationIgnoresUnregisteredOperation(t *testing.T) {
	toolSet := newExposureTestToolSet(nil)
	observations := []turnObservation{invalidInputCapabilityObservation("obs-001", "site.remove")}

	if promoted := promotedCapabilityOperationNames(toolSet, observations); len(promoted) != 0 {
		t.Fatalf("expected unregistered operation to be ignored, got %+v", promoted)
	}
}

func TestPromotedCapabilityOperationIgnoresKernelToolAndUnrelatedFailures(t *testing.T) {
	toolSet := newExposureTestToolSet(map[string]json.RawMessage{
		"site.create": json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"]}`),
	})
	observations := []turnObservation{
		newFailureObservation("obs-001", "continue", "file.edit", "oldText must match exactly once", FailureInvalidInput, FailureCodes.InvalidInput, "file_edit"),
		newFailureObservation("obs-002", "continue", "web.search", "temporarily unavailable", FailureExternalService, FailureCodes.OperationFailed, "web_search"),
	}

	if promoted := promotedCapabilityOperationNames(toolSet, observations); len(promoted) != 0 {
		t.Fatalf("expected unrelated kernel/non-schema failures not to promote anything, got %+v", promoted)
	}
}

func TestFileToolCardsSeparateWriteAndEditRoles(t *testing.T) {
	toolSet := NewToolSet([]string{"file.write", "file.edit"})
	handler := func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("ok"), nil
	}
	toolSet.RegisterTool(ToolDefinition{
		Name: "file.write",
		RecoveryCard: ToolRecoveryCard{
			Does:      "Overwrites one workspace text file with the exact content string.",
			UseWhen:   "A new file or full rewrite is needed.",
			AvoidWhen: "A small targeted source change is needed.",
		},
	}, handler)
	toolSet.RegisterTool(ToolDefinition{
		Name: "file.edit",
		RecoveryCard: ToolRecoveryCard{
			Does:      "Replaces exact oldText occurrences with newText across one or more files.",
			UseWhen:   "One or more targeted source changes are needed.",
			AvoidWhen: "A new file or full rewrite is needed, or oldText is ambiguous.",
		},
	}, handler)

	cards := renderCompactToolCards(toolSet, []toolExposureGroup{{Name: "file operations", ToolIDs: []string{"file.write", "file.edit"}}})

	for _, expectedText := range []string{"file.write", "full rewrite", "file.edit", "oldText"} {
		if !strings.Contains(cards, expectedText) {
			t.Fatalf("expected file tool card text %q in %s", expectedText, cards)
		}
	}
}
