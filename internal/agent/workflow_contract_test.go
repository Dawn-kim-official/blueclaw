package agent

import "testing"

func TestWorkflowContractDoesNotDeriveEffectsFromPrompt(t *testing.T) {
	requirements := requiredWorkflowEffectRequirementsForRequest(AgentRequest{
		Prompt:  "the tangerine website looks far too rough, make it prettier.",
		ToolSet: newTestToolSet([]string{"site.list", "file.edit", "site.serve"}),
	})

	if len(requirements) != 0 {
		t.Fatalf("expected no prompt-derived workflow effects, got %+v", requirements)
	}
}
