package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type ApprovalDecision string

const (
	ApprovalDecisionApproved ApprovalDecision = "approved"
	ApprovalDecisionHeld     ApprovalDecision = "held"
	ApprovalDecisionRejected ApprovalDecision = "rejected"
)

type ApprovalRequest struct {
	RequesterPersonID string
	TaskRunID         string
	ToolName          string
	ToolInput         json.RawMessage
	ApprovalScope     string
	SideEffectClass   string
	ResponseLanguage  string
	HarnessSession    HarnessSession
}

// HarnessSession is the handle that lets a held call be resumed inside the
// conversation that asked for it, rather than restarting the agent's
// reasoning from nothing. A harness that cannot resume leaves it empty.
type HarnessSession struct {
	HarnessName string `json:"harnessName,omitempty"`
	SessionID   string `json:"sessionID,omitempty"`
	IsResumable bool   `json:"isResumable"`
}

type ApprovalOutcome struct {
	Decision ApprovalDecision
	Notice   string
}

type ApprovalGate interface {
	AwaitApproval(context.Context, ApprovalRequest) (ApprovalOutcome, error)
}

var errApprovalGateMissing = errors.New("this tool needs approval and the catalog has no approval gate configured, so it will not run")

func approvalRequestForTool(requesterToolSet RequesterToolSet, toolDescriptor toolcontract.ToolDescriptor, toolInput json.RawMessage) ApprovalRequest {
	return ApprovalRequest{
		RequesterPersonID: requesterToolSet.RequesterPersonID,
		TaskRunID:         requesterToolSet.TaskRunID,
		HarnessSession:    requesterToolSet.HarnessSession,
		ResponseLanguage:  requesterToolSet.ResponseLanguage,
		ToolName:          toolDescriptor.Name,
		ToolInput:         toolInput,
		ApprovalScope:     strings.TrimSpace(toolDescriptor.ApprovalScope),
		SideEffectClass:   strings.TrimSpace(toolDescriptor.SideEffectClass),
	}
}

func heldCallResult(notice string) toolcontract.ToolResult {
	heldNotice := strings.TrimSpace(notice)
	if heldNotice == "" {
		heldNotice = "This call is waiting for the requester's approval and has been recorded. Do not retry it now; call it again unchanged once you are told the approval arrived, and it will run."
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
