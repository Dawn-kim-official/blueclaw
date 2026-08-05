package turnbriefing

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func briefedRequest() agentcontract.AgentTurnRequest {
	return agentcontract.AgentTurnRequest{
		AgentIdentity:    agentcontract.AgentIdentity{Name: "인턴킴", Handle: "@internkim"},
		Company:          agentcontract.CompanyContext{Name: "여명거리"},
		RequesterName:    "Ada",
		ResponseLanguage: "ko",
		MemoryFacts:      []agentcontract.MemoryFact{{Content: "Ada는 금요일 오후에 회의를 잡지 않는다"}},
	}
}

func TestAnAgentIsToldWhoItIsAndWhoIsAsking(t *testing.T) {
	preamble := Preamble(briefedRequest(), "Answer from evidence you have gathered.")

	for _, expectedFragment := range []string{
		"Answer from evidence you have gathered.",
		"인턴킴",
		"@internkim",
		"여명거리",
		"Ada",
		"ko",
		"Ada는 금요일 오후에 회의를 잡지 않는다",
	} {
		if !strings.Contains(preamble, expectedFragment) {
			t.Fatalf("an agent that is told none of this answers as nobody, expected %q in:\n%s", expectedFragment, preamble)
		}
	}
}

func TestATurnWithNothingToSayCarriesNoPreamble(t *testing.T) {
	if preamble := Preamble(agentcontract.AgentTurnRequest{}, ""); preamble != "" {
		t.Fatalf("expected no preamble when there is nothing to brief, got %q", preamble)
	}
}
