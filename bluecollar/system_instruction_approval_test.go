package bluecollar

import (
	"strings"
	"testing"
)

func TestSystemInstructionAddsApprovalContinuationDirective(t *testing.T) {
	withContinuation := buildAgentSystemInstruction(AgentTurnRequest{IsApprovalContinuation: true})
	if !strings.Contains(withContinuation, "runtime has already performed") {
		t.Fatalf("expected approval-continuation directive in the instruction")
	}

	withoutContinuation := buildAgentSystemInstruction(AgentTurnRequest{IsApprovalContinuation: false})
	if strings.Contains(withoutContinuation, "runtime has already performed") {
		t.Fatal("did not expect the approval-continuation directive without a continuation")
	}
}

func TestSystemInstructionGuidesBareMentionAndPlayfulReplies(t *testing.T) {
	instruction := buildAgentSystemInstruction(AgentTurnRequest{})

	for _, expected := range []string{"only mentions you", "@김인턴", "recent visible conversation context", "Do not silently ignore", "good-humored coworker"} {
		if !strings.Contains(instruction, expected) {
			t.Fatalf("expected system instruction to contain %q, got %s", expected, instruction)
		}
	}
}

func TestSystemInstructionRestrictsCheckpointsAndRequiresRecovery(t *testing.T) {
	instruction := buildAgentSystemInstruction(AgentTurnRequest{})

	for _, expected := range []string{
		"Do not use continue.message as a pre-tool repeat-back",
		"meaningful intermediate progress",
		"finish is the permanent final messenger reply",
		"Do not give up after one failed attempt",
		"checking which stage failed",
		"delivery is genuinely blocked",
	} {
		if !strings.Contains(instruction, expected) {
			t.Fatalf("expected system instruction to contain %q, got %s", expected, instruction)
		}
	}
}

func TestSystemInstructionRequiresConcreteReadResults(t *testing.T) {
	instruction := buildAgentSystemInstruction(AgentTurnRequest{})
	for _, expected := range []string{"final reply must state the concrete result facts", "status-only reply"} {
		if !strings.Contains(instruction, expected) {
			t.Fatalf("expected system instruction to contain %q, got %s", expected, instruction)
		}
	}
}

func TestSystemInstructionGuidesApprovalMessageAsNaturalQuestion(t *testing.T) {
	instruction := buildAgentSystemInstruction(AgentTurnRequest{})

	for _, expected := range []string{
		"natural user-facing question",
		"테스트에게 다음 내용을 보낼까요?",
		"instead of naming internal tools or operations",
	} {
		if !strings.Contains(instruction, expected) {
			t.Fatalf("expected system instruction to contain %q, got %s", expected, instruction)
		}
	}
}
