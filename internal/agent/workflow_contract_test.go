package agent

import "testing"

func TestWorkflowContractDerivesFlowTaskWorkKindAndTools(t *testing.T) {
	toolSet := newTestToolSet([]string{"task.add", "task.list", "task.update"})
	request := AgentRequest{
		Prompt:  "업무 등록해줘",
		ToolSet: toolSet,
	}

	workKinds := deterministicWorkflowWorkKindsForRequest(request)
	if !workKindsContain(workKinds, WorkKindFlowTask) {
		t.Fatalf("expected flow task work kind, got %+v", workKinds)
	}

	toolNames := workflowToolNamesForWorkKinds(toolSet, workKinds)
	for _, toolName := range []string{"task.add", "task.list", "task.update"} {
		if !stringSliceContains(toolNames, toolName) {
			t.Fatalf("expected workflow tool %s, got %+v", toolName, toolNames)
		}
	}
}

func TestWorkflowContractDerivesSitePrototypeWorkKindAndTools(t *testing.T) {
	toolSet := newTestToolSet([]string{
		"site.status",
		"site.create",
		"site.repair",
		"file.read",
		"file.write",
		"file.edit",
		"file.patch",
		"terminal.run",
		"artifact.review",
		"site.preview",
		"browser.open",
		"browser.snapshot",
		"browser.screenshot",
		"site.publish",
	})
	request := AgentRequest{
		Prompt:  "사이트 버튼 기능 수정하고 다시 배포해줘",
		ToolSet: toolSet,
	}

	workKinds := deterministicWorkflowWorkKindsForRequest(request)
	if !workKindsContain(workKinds, WorkKindSitePrototype) {
		t.Fatalf("expected site prototype work kind, got %+v", workKinds)
	}

	toolNames := workflowToolNamesForWorkKinds(toolSet, workKinds)
	for _, toolName := range []string{"site.status", "site.repair", "file.read", "file.write", "file.edit", "file.patch", "artifact.review", "site.publish"} {
		if !stringSliceContains(toolNames, toolName) {
			t.Fatalf("expected workflow tool %s, got %+v", toolName, toolNames)
		}
	}
}

func TestWorkflowContractSelectsFlowTaskEvidenceByIntent(t *testing.T) {
	toolSet := newTestToolSet([]string{"task.add", "task.list", "task.update"})
	tests := []struct {
		prompt           string
		expectedToolName string
	}{
		{prompt: "업무 등록해줘", expectedToolName: "task.add"},
		{prompt: "업무 목록 보여줘", expectedToolName: "task.list"},
		{prompt: "업무 완료 처리해줘", expectedToolName: "task.update"},
	}

	for _, test := range tests {
		request := AgentRequest{
			Prompt:    test.prompt,
			ToolSet:   toolSet,
			WorkKinds: []string{WorkKindFlowTask},
		}
		toolNames := requiredWorkflowEvidenceToolsForRequest(request)
		if len(toolNames) != 1 || toolNames[0] != test.expectedToolName {
			t.Fatalf("expected evidence tool %s for %q, got %+v", test.expectedToolName, test.prompt, toolNames)
		}
	}
}

func TestWorkflowContractSelectsSitePrototypeEvidenceByIntent(t *testing.T) {
	toolSet := newTestToolSet([]string{"site.status", "site.publish"})
	tests := []struct {
		prompt           string
		expectedToolName string
	}{
		{prompt: "사이트 버튼 기능 수정하고 다시 배포해줘", expectedToolName: "site.publish"},
		{prompt: "사이트 상태 확인해줘", expectedToolName: "site.status"},
	}

	for _, test := range tests {
		request := AgentRequest{
			Prompt:    test.prompt,
			ToolSet:   toolSet,
			WorkKinds: []string{WorkKindSitePrototype},
		}
		toolNames := requiredWorkflowEvidenceToolsForRequest(request)
		if len(toolNames) != 1 || toolNames[0] != test.expectedToolName {
			t.Fatalf("expected evidence tool %s for %q, got %+v", test.expectedToolName, test.prompt, toolNames)
		}
	}
}

func TestWorkflowContractRequiresSiteModificationEffectsByDefault(t *testing.T) {
	toolSet := newTestToolSet([]string{"site.status", "file.edit", "file.patch", "file.write", "site.publish"})
	request := AgentRequest{
		Prompt:    "예쁜 귤 웹사이트 퀄리티가 너무 낮아. 더 예쁘게 해줘.",
		ToolSet:   toolSet,
		WorkKinds: []string{WorkKindSitePrototype},
	}

	requirements := requiredWorkflowEffectRequirementsForRequest(request)

	for _, expectedEffect := range []OutcomeEffect{
		{ObjectType: "workspace", Effect: "modified"},
		{ObjectType: "website", Effect: "published"},
	} {
		if !outcomeEffectsContain(requirements, expectedEffect.ObjectType, expectedEffect.Effect) {
			t.Fatalf("expected required effect %+v, got %+v", expectedEffect, requirements)
		}
	}
}

func TestWorkflowContractRequiresOnlySiteReadEffectForStatusIntent(t *testing.T) {
	toolSet := newTestToolSet([]string{"site.status", "site.publish"})
	request := AgentRequest{
		Prompt:    "예쁜 귤 웹사이트 주소 확인해줘",
		ToolSet:   toolSet,
		WorkKinds: []string{WorkKindSitePrototype},
	}

	requirements := requiredWorkflowEffectRequirementsForRequest(request)

	if len(requirements) != 1 || requirements[0].ObjectType != "website" || requirements[0].Effect != "read" {
		t.Fatalf("expected only website read effect, got %+v", requirements)
	}
}

func TestWorkflowContractDerivesSitePrototypeWorkKindForQualityRequest(t *testing.T) {
	toolSet := newTestToolSet([]string{"site.status", "file.edit", "site.publish"})
	request := AgentRequest{
		Prompt:  "예쁜 귤 웹사이트 퀄리티가 너무 낮잖아. 더 예쁘게 해줘.",
		ToolSet: toolSet,
	}

	workKinds := deterministicWorkflowWorkKindsForRequest(request)

	if !workKindsContain(workKinds, WorkKindSitePrototype) {
		t.Fatalf("expected site prototype work kind, got %+v", workKinds)
	}
}

func outcomeEffectsContain(effects []OutcomeEffect, objectType string, effect string) bool {
	for _, observedEffect := range effects {
		if observedEffect.ObjectType == objectType && observedEffect.Effect == effect {
			return true
		}
	}
	return false
}
