package agent

import "testing"

func TestWorkflowToolNamesForTurnRequestUsesExactCapabilityEvidence(t *testing.T) {
	toolSet := newTestCapabilityToolSet([]string{"task.add"})
	request := AgentTurnRequest{
		ToolSet:               toolSet,
		RequiredEvidenceTools: []string{"task.add"},
	}

	toolNames := workflowToolNamesForTurnRequest(request)

	if len(toolNames) != 1 || toolNames[0] != "task.add" {
		t.Fatalf("expected exact capability evidence, got %+v", toolNames)
	}
}

func TestWorkflowContractDoesNotDeriveEffectsFromPrompt(t *testing.T) {
	requirements := requiredWorkflowEffectRequirementsForRequest(AgentRequest{
		Prompt:  "예쁜 귤 웹사이트 퀄리티가 너무 낮잖아. 더 예쁘게 해줘.",
		ToolSet: newTestToolSet([]string{"site.status", "file.edit", "site.publish"}),
	})

	if len(requirements) != 0 {
		t.Fatalf("expected no prompt-derived workflow effects, got %+v", requirements)
	}
}
