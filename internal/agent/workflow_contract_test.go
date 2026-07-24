package agent

import "testing"

func TestWorkflowContractDoesNotDeriveEffectsFromPrompt(t *testing.T) {
	requirements := requiredWorkflowEffectRequirementsForRequest(AgentRequest{
		Prompt:  "예쁜 귤 웹사이트 퀄리티가 너무 낮잖아. 더 예쁘게 해줘.",
		ToolSet: newTestToolSet([]string{"site.list", "file.edit", "site.serve"}),
	})

	if len(requirements) != 0 {
		t.Fatalf("expected no prompt-derived workflow effects, got %+v", requirements)
	}
}
