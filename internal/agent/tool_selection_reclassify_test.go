package agent

import "testing"

func TestSelectToolsReclassifiesSkillNameThatIsRegisteredTool(t *testing.T) {
	request := AgentTurnRequest{ToolSet: testToolSet([]string{"platform.message.send"})}
	requestArguments := requestToolsArguments{
		ToolNames:  []string{"platform.message.send"},
		SkillNames: []string{"platform.message.send"},
	}

	nextRequest, result := applyToolRequest(request, requestArguments)

	if toolRequestResultFailed(result) {
		t.Fatalf("expected a registered tool listed under skillNames not to fail the selection, got %+v", result)
	}
	if len(result.UnknownSkillNames) != 0 {
		t.Fatalf("expected no unknown skills after reclassification, got %+v", result.UnknownSkillNames)
	}
	if !containsString(result.ReclassifiedSkillsAsTools, "platform.message.send") {
		t.Fatalf("expected platform.message.send to be recorded as reclassified, got %+v", result.ReclassifiedSkillsAsTools)
	}
	if !containsString(nextRequest.PinnedToolNames, "platform.message.send") {
		t.Fatalf("expected platform.message.send to be pinned as a tool, got %+v", nextRequest.PinnedToolNames)
	}
}

func TestExternalSendPreExposesSendToolsWithoutSelection(t *testing.T) {
	toolSet := testToolSet([]string{"skill.search", "ask.confirm", "platform.message.send", "mail.message.send"})
	request := AgentRequest{
		Prompt:    "이동하님께 DM 보내줘",
		WorkKinds: []string{WorkKindExternalSend},
		ToolSet:   toolSet,
	}

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(toolSet, InstructionBundle{}, request, ExecutionPlan{}, false, OutcomeContract{}, ToolSelectionDecision{}, ToolExposureEvent{})

	for _, toolName := range []string{"platform.message.send", "mail.message.send"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s pre-exposed for an external_send task so it can be called without request_tools, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestSelectToolsKeepsGenuinelyUnknownSkillFailing(t *testing.T) {
	request := AgentTurnRequest{ToolSet: testToolSet([]string{"platform.message.send"})}
	requestArguments := requestToolsArguments{SkillNames: []string{"made-up-skill"}}

	_, result := applyToolRequest(request, requestArguments)

	if !toolRequestResultFailed(result) {
		t.Fatalf("expected an unregistered skill name to still fail the selection, got %+v", result)
	}
	if !containsString(result.UnknownSkillNames, "made-up-skill") {
		t.Fatalf("expected made-up-skill to remain an unknown skill, got %+v", result.UnknownSkillNames)
	}
}
