package agent

import (
	"context"
	"testing"
)

func TestConnectorProfileExposureAlwaysIncludesRegisteredKernelAndSelectedSkillTools(t *testing.T) {
	toolSet := NewToolSet([]string{ConversationHistoryToolName, "memory.search"})
	registeredKernelToolNames := []string{
		FileReadToolName,
		FileWriteToolName,
		FileEditToolName,
		TerminalRunToolName,
		FileDeliverToolName,
		SkillSearchToolName,
	}
	registerConnectorExposureTestTools(toolSet, registeredKernelToolNames)
	registerConnectorExposureTestTools(toolSet, []string{"calendar.add", "site.status"})

	instructionBundle := InstructionBundle{
		Skills:         []SkillInstruction{{Name: "calendar", AllowedTools: []string{"calendar.add"}}},
		SkillDecisions: []SkillSelectionDecision{{Name: "calendar", Status: "selected"}},
	}
	exposedToolSet, exposureEvent := toolSetForAgentTurnWithExposure(toolSet, instructionBundle)

	for _, kernelToolName := range registeredKernelToolNames {
		if !exposedToolSet.IsAllowed(kernelToolName) {
			t.Errorf("connector exposure omitted registered kernel tool %q; exposed tools: %v", kernelToolName, exposureEvent.ExposedToolIDs)
		}
	}
	if !exposedToolSet.IsAllowed("calendar.add") {
		t.Errorf("connector exposure omitted selected skill tool %q; exposed tools: %v", "calendar.add", exposureEvent.ExposedToolIDs)
	}
	if exposedToolSet.IsAllowed("site.status") {
		t.Errorf("connector exposure included unselected capability tool %q; exposed tools: %v", "site.status", exposureEvent.ExposedToolIDs)
	}
}

func TestIntakeInitialToolsReachStepPalette(t *testing.T) {
	toolSet := NewToolSet([]string{ConversationHistoryToolName})
	registerConnectorExposureTestTools(toolSet, []string{"calendar.add"})
	intakeInitialToolNames := []string{"calendar.add"}
	request := AgentTurnRequest{
		ToolSet:               toolSet,
		PinnedToolNames:       intakeInitialToolNames,
		RequiredEvidenceTools: intakeInitialToolNames,
	}

	iterationRequest := (&AgentTurnRunner{}).requestForStep(context.Background(), request, agentTaskState{})

	if !iterationRequest.ToolSet.IsAllowed("calendar.add") {
		t.Fatalf("intake initial tool %q was pruned before the step palette; pinned tools: %v, exposed tools: %v", "calendar.add", iterationRequest.PinnedToolNames, iterationRequest.ToolExposure.ExposedToolIDs)
	}
}

func registerConnectorExposureTestTools(toolSet *ToolSet, toolNames []string) {
	for _, toolName := range toolNames {
		toolSet.RegisterTool(ToolDefinition{Name: toolName}, func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolSuccess("ok"), nil
		})
	}
}
