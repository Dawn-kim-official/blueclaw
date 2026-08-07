package agentruntime

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestTheHostSuppliesTheConventionsOfItsOwnMessenger(t *testing.T) {
	instruction := hostInstructionForRequest(agentcontract.AgentTurnRequest{
		AgentIdentity: agentcontract.AgentIdentity{Name: "Ada", Handle: "ada"},
	})

	for _, section := range []string{"Checkpoint messages:", "Bare mentions and banter:", "Approvals and user input:", "Recipients:", "Delivery and artifacts:", "Privacy boundary"} {
		if !strings.Contains(instruction, section) {
			t.Fatalf("%q describes tools and policy this host owns, so this host is where it has to come from", section)
		}
	}
}

func TestAnApprovalContinuationIsNamedToTheAgent(t *testing.T) {
	withContinuation := hostInstructionForRequest(agentcontract.AgentTurnRequest{IsApprovalContinuation: true})
	withoutContinuation := hostInstructionForRequest(agentcontract.AgentTurnRequest{})

	if !strings.Contains(withContinuation, "just approved") {
		t.Fatal("an agent resuming after an approval has to be told the approval already happened")
	}
	if strings.Contains(withoutContinuation, "just approved") {
		t.Fatal("and told nothing of the sort when it did not")
	}
}

func TestTheCheckpointAdviceFollowsTheTaskLevel(t *testing.T) {
	short := hostInstructionForRequest(agentcontract.AgentTurnRequest{TaskLevel: agentcontract.TaskLevelXLow})
	long := hostInstructionForRequest(agentcontract.AgentTurnRequest{TaskLevel: agentcontract.TaskLevelHigh})

	if !strings.Contains(short, "short task") || !strings.Contains(long, "multi-step task") {
		t.Fatalf("how often to speak up depends on how long the work runs, and the level is what says so:\nshort=%q\nlong=%q", short[:200], long[:200])
	}
}
