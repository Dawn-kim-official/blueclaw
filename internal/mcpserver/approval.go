package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/toolcontract"
)

type ApprovalDecision string

const (
	ApprovalDecisionApproved ApprovalDecision = "approved"
	ApprovalDecisionHeld     ApprovalDecision = "held"
	ApprovalDecisionRejected ApprovalDecision = "rejected"
)

type ApprovalRequest struct {
	RequesterPersonID string
	ToolName          string
	ToolInput         json.RawMessage
	ApprovalScope     string
	SideEffectClass   string
}

type ApprovalOutcome struct {
	Decision ApprovalDecision
	Notice   string
}

type ApprovalGate interface {
	AwaitApproval(context.Context, ApprovalRequest) (ApprovalOutcome, error)
}

var errApprovalGateMissing = errors.New("this tool needs approval and the catalog has no approval gate configured, so it will not run")

func approvalRequestForTool(requesterPersonID string, toolDescriptor toolcontract.ToolDescriptor, toolInput json.RawMessage) ApprovalRequest {
	return ApprovalRequest{
		RequesterPersonID: requesterPersonID,
		ToolName:          toolDescriptor.Name,
		ToolInput:         toolInput,
		ApprovalScope:     strings.TrimSpace(toolDescriptor.ApprovalScope),
		SideEffectClass:   strings.TrimSpace(toolDescriptor.SideEffectClass),
	}
}

func heldCallResult(notice string) toolcontract.ToolResult {
	heldNotice := strings.TrimSpace(notice)
	if heldNotice == "" {
		heldNotice = "This call is waiting for the requester's approval. It has been recorded and will run once approved; do not retry it."
	}
	return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.InteractionRequired, "approval", heldNotice)
}

func rejectedCallResult(notice string) toolcontract.ToolResult {
	rejectedNotice := strings.TrimSpace(notice)
	if rejectedNotice == "" {
		rejectedNotice = "The requester declined this call. Do not retry it; choose another way or stop."
	}
	return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.PolicyBlocked, "approval", rejectedNotice)
}
