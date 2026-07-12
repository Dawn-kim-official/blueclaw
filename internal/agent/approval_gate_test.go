package agent

import (
	"context"
	"encoding/json"
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

func TestApprovalRequiredObservationDetectsRequiresApprovalAuthorizationText(t *testing.T) {
	observation := newFailureObservation("obs-001", "continue", "message.send", "requires approval", FailurePolicyBlocked, FailureCodes.OperationFailed, "authorization")

	if !isApprovalRequiredObservation(observation) {
		t.Fatal("expected requires-approval authorization failure to pause for approval")
	}
}

func TestApprovalRequiredObservationDetectsMarkerInFailureData(t *testing.T) {
	observation := newFailureObservation("obs-001", "continue", "message.send", "blocked", FailurePolicyBlocked, FailureCodes.OperationFailed, "capability")
	observation.Output.Data = json.RawMessage(`{"failure":{"code":"approval_required"}}`)

	if !isApprovalRequiredObservation(observation) {
		t.Fatal("expected legacy approval_required marker in data to pause for approval")
	}
}

func TestApprovalRequiredObservationDetectsMarkerInContent(t *testing.T) {
	observation := newFailureObservation("obs-001", "continue", "message.send", "capability returned approval_required", FailurePolicyBlocked, FailureCodes.OperationFailed, "capability")

	if !isApprovalRequiredObservation(observation) {
		t.Fatal("expected approval_required marker in content to pause for approval")
	}
}

func TestApprovalRequiredObservationIgnoresUnrelatedFailure(t *testing.T) {
	observation := newFailureObservation("obs-001", "continue", "message.send", "network timeout", FailureExternalService, FailureCodes.OperationFailed, "invoke")

	if isApprovalRequiredObservation(observation) {
		t.Fatal("unrelated failure must not activate the approval protocol")
	}
}

func TestApprovalRequiredObservationIgnoresRequiresApprovalTextOutsideAuthorization(t *testing.T) {
	observation := newFailureObservation("obs-001", "continue", "message.send", "this operation requires approval", FailurePolicyBlocked, FailureCodes.OperationFailed, "invoke")

	if isApprovalRequiredObservation(observation) {
		t.Fatal("requires-approval wording outside the authorization stage must not activate the approval protocol")
	}
}
