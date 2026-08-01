package bluecollar

import ()

import "testing"

func TestSelectToolsReclassifiesSkillNameThatIsRegisteredTool(t *testing.T) {
	request := AgentTurnRequest{ToolSet: testToolSet([]string{"message_send"})}
	requestArguments := requestToolsArguments{
		ToolNames:  []string{"message_send"},
		SkillNames: []string{"message_send"},
	}

	nextRequest, result := applyToolRequest(request, requestArguments)

	if toolRequestResultFailed(result) {
		t.Fatalf("expected a registered tool listed under skillNames not to fail the selection, got %+v", result)
	}
	if len(result.UnknownSkillNames) != 0 {
		t.Fatalf("expected no unknown skills after reclassification, got %+v", result.UnknownSkillNames)
	}
	if !containsString(result.ReclassifiedSkillsAsTools, "message_send") {
		t.Fatalf("expected message_send to be recorded as reclassified, got %+v", result.ReclassifiedSkillsAsTools)
	}
	if !containsString(nextRequest.PinnedToolNames, "message_send") {
		t.Fatalf("expected message_send to be pinned as a tool, got %+v", nextRequest.PinnedToolNames)
	}
}

func TestExternalSendReachesPinnedSendOperationsDirectlyOnceRequested(t *testing.T) {
	toolSet := newTestCapabilityToolSet([]string{"message_send", "mail_message_send"})
	request := AgentRequest{
		Prompt:          "send Dana Lee a DM",
		PinnedToolNames: []string{"message_send", "mail_message_send"},
		ToolSet:         toolSet,
	}

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(toolSet, InstructionBundle{}, request, ExecutionPlan{}, false, OutcomeContract{}, ToolExposureEvent{})

	for _, toolName := range []string{"message_send", "mail_message_send"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected pinned tool %s to stay exposed, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestSelectToolsKeepsGenuinelyUnknownSkillFailing(t *testing.T) {
	request := AgentTurnRequest{ToolSet: testToolSet([]string{"message_send"})}
	requestArguments := requestToolsArguments{SkillNames: []string{"made-up-skill"}}

	_, result := applyToolRequest(request, requestArguments)

	if !toolRequestResultFailed(result) {
		t.Fatalf("expected an unregistered skill name to still fail the selection, got %+v", result)
	}
	if !containsString(result.UnknownSkillNames, "made-up-skill") {
		t.Fatalf("expected made-up-skill to remain an unknown skill, got %+v", result.UnknownSkillNames)
	}
}
