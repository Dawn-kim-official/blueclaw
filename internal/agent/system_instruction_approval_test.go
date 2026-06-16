package agent

import (
	"strings"
	"testing"
)

func TestSystemInstructionAddsApprovalContinuationDirective(t *testing.T) {
	withContinuation := buildAgentSystemInstruction(AgentTurnRequest{IsApprovalContinuation: true})
	if !strings.Contains(withContinuation, "already-approved action now") {
		t.Fatalf("expected approval-continuation directive in the instruction")
	}

	withoutContinuation := buildAgentSystemInstruction(AgentTurnRequest{IsApprovalContinuation: false})
	if strings.Contains(withoutContinuation, "already-approved action now") {
		t.Fatal("did not expect the approval-continuation directive without a continuation")
	}
}
