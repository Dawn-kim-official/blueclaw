package agent

import "testing"

func TestSelectToolsReclassifiesSkillNameThatIsRegisteredTool(t *testing.T) {
	request := AgentTurnRequest{ToolSet: testToolSet([]string{"message.send"})}
	requestArguments := requestToolsArguments{
		ToolNames:  []string{"message.send"},
		SkillNames: []string{"message.send"},
	}

	nextRequest, result := applyToolRequest(request, requestArguments)

	if toolRequestResultFailed(result) {
		t.Fatalf("expected a registered tool listed under skillNames not to fail the selection, got %+v", result)
	}
	if len(result.UnknownSkillNames) != 0 {
		t.Fatalf("expected no unknown skills after reclassification, got %+v", result.UnknownSkillNames)
	}
	if !containsString(result.ReclassifiedSkillsAsTools, "message.send") {
		t.Fatalf("expected message.send to be recorded as reclassified, got %+v", result.ReclassifiedSkillsAsTools)
	}
	if !containsString(nextRequest.PinnedToolNames, "message.send") {
		t.Fatalf("expected message.send to be pinned as a tool, got %+v", nextRequest.PinnedToolNames)
	}
}

func TestExternalSendReachesPinnedSendOperationsOnlyThroughCapabilityInvoke(t *testing.T) {
	toolSet := newTestCapabilityToolSet([]string{"message.send", "mail.message.send"})
	request := AgentRequest{
		Prompt:          "이동하님께 DM 보내줘",
		PinnedToolNames: []string{"message.send", "mail.message.send"},
		ToolSet:         toolSet,
	}

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(toolSet, InstructionBundle{}, request, ExecutionPlan{}, false, OutcomeContract{}, ToolSelectionDecision{}, ToolExposureEvent{})

	if !filteredToolSet.IsAllowed(CapabilityInvokeToolName) {
		t.Fatalf("expected capability.invoke to stay exposed as the only path to pinned send operations, got %+v", filteredToolSet.ListToolNames())
	}
	for _, operationName := range []string{"message.send", "mail.message.send"} {
		if filteredToolSet.IsAllowed(operationName) {
			t.Fatalf("expected pinned domain operation %s to stay reachable only through capability.invoke, got %+v", operationName, filteredToolSet.ListToolNames())
		}
	}
}

func TestSelectToolsKeepsGenuinelyUnknownSkillFailing(t *testing.T) {
	request := AgentTurnRequest{ToolSet: testToolSet([]string{"message.send"})}
	requestArguments := requestToolsArguments{SkillNames: []string{"made-up-skill"}}

	_, result := applyToolRequest(request, requestArguments)

	if !toolRequestResultFailed(result) {
		t.Fatalf("expected an unregistered skill name to still fail the selection, got %+v", result)
	}
	if !containsString(result.UnknownSkillNames, "made-up-skill") {
		t.Fatalf("expected made-up-skill to remain an unknown skill, got %+v", result.UnknownSkillNames)
	}
}
