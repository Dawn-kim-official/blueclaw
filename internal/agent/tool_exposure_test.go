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
		[]string{"file.read", "file.write", "file.edit", "file.patch", "terminal.run"},
		[]string{"site.status", "site.create", "site.build", "artifact.review", "site.publish", "calendar.add", "task.add"},
	)
	request := requestWithStepWorkingSetTools(AgentTurnRequest{
		ToolSet: toolSet,
		RequiredEvidenceTools: []string{
			"file.read", "file.write", "file.edit", "file.patch", "terminal.run",
			"site.status", "site.create", "site.build", "artifact.review", "site.publish",
		},
	}, nil)

	for _, toolName := range []string{"file.read", "file.write", "file.edit", "file.patch", "terminal.run"} {
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
				Action:    "inspect_or_edit_text",
				ToolNames: []string{"file.read", "file.edit", "file.patch", "file.write"},
			}},
		},
		ToolInputKey: "file.edit\x00{\"path\":\"App.tsx\"}",
	}, {
		ObservationID: "obs-002",
		Action:        "policy",
		Tool:          "file.edit",
		Output:        ToolOutput{Content: "The recovery budget for corrected_retry is exhausted."},
		RecoveryStep:  recoveryStepCorrectedRetry,
		Summary:       "The recovery budget for corrected_retry is exhausted.",
	}}

	toolNames := recoveryPinnedToolNames(InstructionBundle{}, AgentRequest{PinnedToolNames: []string{"file.edit", "file.write"}}, observations)

	if stringSliceContains(toolNames, "file.edit") {
		t.Fatalf("expected exhausted file.edit to be removed, got %+v", toolNames)
	}
	if !stringSliceContains(toolNames, "file.write") || !stringSliceContains(toolNames, "file.patch") {
		t.Fatalf("expected alternate edit tools to remain, got %+v", toolNames)
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

func TestFileToolCardsSeparateWriteEditAndPatchRoles(t *testing.T) {
	toolSet := NewToolSet([]string{"file.write", "file.edit", "file.patch"})
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
			Does:      "Replaces one exact oldText occurrence with newText.",
			UseWhen:   "A small targeted source change is needed.",
			AvoidWhen: "The oldText is missing or ambiguous.",
		},
	}, handler)
	toolSet.RegisterTool(ToolDefinition{
		Name: "file.patch",
		RecoveryCard: ToolRecoveryCard{
			Does:      "Applies structured exact replacements across files.",
			UseWhen:   "Several targeted edits should be applied together.",
			AvoidWhen: "A broad file rewrite is needed.",
		},
	}, handler)

	cards := renderCompactToolCards(toolSet, []toolExposureGroup{{Name: "file operations", ToolIDs: []string{"file.write", "file.edit", "file.patch"}}})

	for _, expectedText := range []string{"file.write", "full rewrite", "file.edit", "oldText", "file.patch", "Several targeted edits"} {
		if !strings.Contains(cards, expectedText) {
			t.Fatalf("expected file tool card text %q in %s", expectedText, cards)
		}
	}
}
