package approvalgate

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/mcpserver"
	"github.com/Dawn-kim-official/blueclaw/taskstate"
)

type heldCallRecord struct {
	ToolName       string                   `json:"toolName"`
	ToolInput      json.RawMessage          `json:"toolInput"`
	ApprovalScope  string                   `json:"approvalScope,omitempty"`
	Confirmation   string                   `json:"confirmation"`
	HarnessSession mcpserver.HarnessSession `json:"harnessSession"`
}

type DecisionSource interface {
	AwaitDecision(context.Context, string) (mcpserver.ApprovalDecision, bool)
}

type Gate struct {
	taskRunService taskstate.TaskRunStore
	decisionSource DecisionSource
	inlineWait     time.Duration
}

func New(taskRunService taskstate.TaskRunStore) *Gate {
	return &Gate{taskRunService: taskRunService}
}

func (gate *Gate) UseInlineWait(decisionSource DecisionSource, inlineWait time.Duration) {
	gate.decisionSource = decisionSource
	gate.inlineWait = inlineWait
}

func (gate *Gate) AwaitApproval(ctx context.Context, approvalRequest mcpserver.ApprovalRequest) (mcpserver.ApprovalOutcome, error) {
	taskRunID := strings.TrimSpace(approvalRequest.TaskRunID)
	if taskRunID == "" {
		return mcpserver.ApprovalOutcome{
			Decision: mcpserver.ApprovalDecisionHeld,
			Notice:   "This call needs the requester's approval, but it is not attached to a task run they can answer on, so it will not run.",
		}, nil
	}
	confirmation := confirmationWording(approvalRequest)
	gate.recordHeldCall(taskRunID, approvalRequest, confirmation)
	if decision, isDecided := gate.awaitInlineDecision(ctx, taskRunID); isDecided {
		return mcpserver.ApprovalOutcome{Decision: decision, Notice: confirmation}, nil
	}
	if _, errorValue := gate.taskRunService.PauseTaskRun(taskRunID, taskstate.TaskStatusWaitingApproval, confirmation); errorValue != nil {
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionHeld, Notice: errorValue.Error()}, nil
	}
	return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionHeld, Notice: confirmation}, nil
}

func (gate *Gate) awaitInlineDecision(ctx context.Context, taskRunID string) (mcpserver.ApprovalDecision, bool) {
	if gate.decisionSource == nil || gate.inlineWait <= 0 {
		return "", false
	}
	waitContext, cancel := context.WithTimeout(ctx, gate.inlineWait)
	defer cancel()
	return gate.decisionSource.AwaitDecision(waitContext, taskRunID)
}

func (gate *Gate) recordHeldCall(taskRunID string, approvalRequest mcpserver.ApprovalRequest, confirmation string) {
	gate.taskRunService.AppendTaskEvent(taskRunID, "approval.pending_call", marshalEventBody(heldCallRecord{
		ToolName:       approvalRequest.ToolName,
		ToolInput:      approvalRequest.ToolInput,
		ApprovalScope:  approvalRequest.ApprovalScope,
		Confirmation:   confirmation,
		HarnessSession: approvalRequest.HarnessSession,
	}))
	gate.taskRunService.AppendTaskEvent(taskRunID, "confirmation.requested", marshalEventBody(map[string]string{
		"toolName":     approvalRequest.ToolName,
		"confirmation": confirmation,
		"source":       "tool_catalog",
	}))
}

func confirmationWording(approvalRequest mcpserver.ApprovalRequest) string {
	toolName := strings.TrimSpace(approvalRequest.ToolName)
	if scope := strings.TrimSpace(approvalRequest.ApprovalScope); scope != "" {
		return toolName + " needs your approval (" + scope + ")."
	}
	return toolName + " needs your approval."
}

func marshalEventBody(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return ""
	}
	return string(document)
}
