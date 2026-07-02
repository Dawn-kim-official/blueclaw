package agent

import (
	"context"
	"testing"
)

func TestWorkflowContractDerivesFlowTaskWorkKindAndTools(t *testing.T) {
	toolSet := newTestCapabilityToolSet([]string{"task.add", "task.list", "task.update"})
	request := AgentRequest{
		Prompt:  "업무 등록해줘",
		ToolSet: toolSet,
	}

	workKinds := deterministicWorkflowWorkKindsForRequest(request)
	if !workKindsContain(workKinds, WorkKindFlowTask) {
		t.Fatalf("expected flow task work kind, got %+v", workKinds)
	}

	toolNames := workflowToolNamesForWorkKinds(toolSet, workKinds)
	if !stringSliceContains(toolNames, CapabilityInvokeToolName) {
		t.Fatalf("expected workflow to prepare capability.invoke, got %+v", toolNames)
	}
	for _, toolName := range []string{"task.add", "task.list", "task.update"} {
		if stringSliceContains(toolNames, toolName) {
			t.Fatalf("expected capability operation %s to stay hidden from direct workflow tools, got %+v", toolName, toolNames)
		}
	}
}

func TestWorkflowContractDerivesSitePrototypeWorkKindAndTools(t *testing.T) {
	toolSet := newTestCapabilityToolSet([]string{
		"site.status",
		"site.create",
		"site.repair",
		"artifact.review",
		"site.preview",
		"browser.open",
		"browser.snapshot",
		"browser.screenshot",
		"site.publish",
	})
	for _, toolName := range []string{
		"file.read",
		"file.write",
		"file.edit",
		"file.patch",
		"terminal.run",
	} {
		currentToolName := toolName
		toolSet.RegisterTool(ToolDefinition{Name: currentToolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolSuccess("ok"), nil
		})
	}
	toolSet = toolSet.WithAdditionalAllowedToolNames([]string{"file.read", "file.write", "file.edit", "file.patch", "terminal.run"})
	request := AgentRequest{
		Prompt:  "사이트 버튼 기능 수정하고 다시 배포해줘",
		ToolSet: toolSet,
	}

	workKinds := deterministicWorkflowWorkKindsForRequest(request)
	if !workKindsContain(workKinds, WorkKindSitePrototype) {
		t.Fatalf("expected site prototype work kind, got %+v", workKinds)
	}

	toolNames := workflowToolNamesForWorkKinds(toolSet, workKinds)
	for _, toolName := range []string{CapabilityInvokeToolName, "file.read", "file.write", "file.edit", "file.patch", "terminal.run"} {
		if !stringSliceContains(toolNames, toolName) {
			t.Fatalf("expected workflow tool %s, got %+v", toolName, toolNames)
		}
	}
	for _, toolName := range []string{"site.status", "site.repair", "artifact.review", "site.publish"} {
		if stringSliceContains(toolNames, toolName) {
			t.Fatalf("expected capability operation %s to stay hidden from direct workflow tools, got %+v", toolName, toolNames)
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

	requirements := requiredWorkflowEffectRequirementsForRequest(request, nil)

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

	requirements := requiredWorkflowEffectRequirementsForRequest(request, nil)

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
