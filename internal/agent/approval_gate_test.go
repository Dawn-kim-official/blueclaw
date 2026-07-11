package agent

import (
	"context"
	"testing"
)

func TestToolRequiresRuntimeApprovalForNonKernelTool(t *testing.T) {
	toolSet := NewToolSet([]string{"custom.publish"})
	toolSet.RegisterTool(ToolDefinition{Name: "custom.publish", RequiresApproval: true}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("ok"), nil
	})

	if !toolRequiresRuntimeApproval(toolSet, "custom.publish") {
		t.Fatal("expected non-kernel tool approval to remain enforced by the runtime")
	}
}

func TestToolSkipsRuntimeApprovalWhenToolHandlesApproval(t *testing.T) {
	toolSet := NewToolSet([]string{"message.send"})
	toolSet.RegisterTool(ToolDefinition{Name: "message.send", RequiresApproval: true, ApprovalHandledByTool: true}, func(context.Context, ToolInvocation) (ToolResult, error) {
		return ToolSuccess("ok"), nil
	})

	if toolRequiresRuntimeApproval(toolSet, "message.send") {
		t.Fatal("expected tool-handled approval not to be duplicated by the runtime")
	}
}

func TestApprovalRequiredObservationUsesCanonicalProtocolFields(t *testing.T) {
	observation := newFailureObservation("obs-001", "continue", "message.send", "approval is pending", FailurePolicyBlocked, FailureCodes.ApprovalRequired, "authorization")

	if !isApprovalRequiredObservation(observation) {
		t.Fatal("expected exact approval_required authorization failure to pause for approval")
	}
}

func TestApprovalRequiredObservationIgnoresUserFacingPhrase(t *testing.T) {
	observation := newFailureObservation("obs-001", "continue", "message.send", "requires approval", FailurePolicyBlocked, FailureCodes.OperationFailed, "authorization")

	if isApprovalRequiredObservation(observation) {
		t.Fatal("user-facing failure text must not activate the approval protocol")
	}
}
