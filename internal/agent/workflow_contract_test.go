package agent

import "testing"

func TestWorkflowContractDerivesFlowTaskWorkKindAndTools(t *testing.T) {
	toolSet := newTestToolSet([]string{"flow.task.add", "flow.task.list", "flow.task.update"})
	request := AgentRequest{
		Prompt:  "업무 등록해줘",
		ToolSet: toolSet,
	}

	workKinds := deterministicWorkflowWorkKindsForRequest(request)
	if !workKindsContain(workKinds, WorkKindFlowTask) {
		t.Fatalf("expected flow task work kind, got %+v", workKinds)
	}

	toolNames := workflowToolNamesForWorkKinds(toolSet, workKinds)
	for _, toolName := range []string{"flow.task.add", "flow.task.list", "flow.task.update"} {
		if !stringSliceContains(toolNames, toolName) {
			t.Fatalf("expected workflow tool %s, got %+v", toolName, toolNames)
		}
	}
}

func TestWorkflowContractSelectsFlowTaskEvidenceByIntent(t *testing.T) {
	toolSet := newTestToolSet([]string{"flow.task.add", "flow.task.list", "flow.task.update"})
	tests := []struct {
		prompt           string
		expectedToolName string
	}{
		{prompt: "업무 등록해줘", expectedToolName: "flow.task.add"},
		{prompt: "업무 목록 보여줘", expectedToolName: "flow.task.list"},
		{prompt: "업무 완료 처리해줘", expectedToolName: "flow.task.update"},
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
