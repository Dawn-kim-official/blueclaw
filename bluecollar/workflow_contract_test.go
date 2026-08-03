package bluecollar

import ()

import "testing"

func TestWorkflowContractDoesNotDeriveEffectsFromPrompt(t *testing.T) {
	requirements := requiredWorkflowEffectRequirementsForRequest(AgentRequest{
		Prompt:  "the tangerine website looks far too rough, make it prettier.",
		ToolSet: newTestToolSet([]string{"site_list", "file_edit", "site_serve"}),
	})

	if len(requirements) != 0 {
		t.Fatalf("expected no prompt-derived workflow effects, got %+v", requirements)
	}
}
