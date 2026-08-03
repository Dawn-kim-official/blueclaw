package mcpserver

import (
	"context"
	"testing"
)

type fixedDecisionGate struct {
	decision ApprovalDecision
}

func (gate fixedDecisionGate) AwaitApproval(context.Context, ApprovalRequest) (ApprovalOutcome, error) {
	return ApprovalOutcome{Decision: gate.decision}, nil
}

func executedToolsUnderDecision(t *testing.T, decision ApprovalDecision) []string {
	t.Helper()
	executed := []string{}
	callThroughCatalog(t, RequesterToolSet{
		RequesterPersonID: "person-1",
		TaskRunID:         "task-run-1",
		ToolSet:           approvalToolSet(t, &executed),
		ApprovalGate:      fixedDecisionGate{decision: decision},
	}, "file_delete")
	return executed
}

func TestAHeldCallLeavesNoTraceBecauseTheAgentWillIssueItAgain(t *testing.T) {
	if executed := executedToolsUnderDecision(t, ApprovalDecisionHeld); len(executed) != 0 {
		t.Fatalf("expected a held call to run nothing, because every effect before the hold happens twice when the approved call is reissued, got %v", executed)
	}
}

func TestADeclinedCallLeavesNoTrace(t *testing.T) {
	if executed := executedToolsUnderDecision(t, ApprovalDecisionRejected); len(executed) != 0 {
		t.Fatalf("expected a declined call to run nothing, got %v", executed)
	}
}

func TestAnApprovedCallRunsExactlyOnce(t *testing.T) {
	if executed := executedToolsUnderDecision(t, ApprovalDecisionApproved); len(executed) != 1 {
		t.Fatalf("expected an approved call to run once, got %v", executed)
	}
}
