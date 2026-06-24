package agent

import "testing"

func TestApplyInlineToolRequestPinsToolsAndSkillsFromContinueAction(t *testing.T) {
	services := newTurnRunnerTestServices(&sequenceLanguageModel{}, TurnOptions{})
	request := AgentTurnRequest{ToolSet: testToolSet([]string{"skill.search", "web.search", "mail.message.send"})}
	state := agentTaskState{Request: request}
	actionDocument := turnActionDocument{
		Action:       "continue",
		ToolName:     "skill.search",
		RequestTools: []string{"web.search", "mail.message.send"},
	}

	updatedRequest := services.runner.applyInlineToolRequest("task-1", request, &state, actionDocument)

	for _, toolName := range []string{"web.search", "mail.message.send"} {
		if !containsString(updatedRequest.PinnedToolNames, toolName) {
			t.Fatalf("expected inline requestTools to pin %s, got %+v", toolName, updatedRequest.PinnedToolNames)
		}
		if !containsString(state.Request.PinnedToolNames, toolName) {
			t.Fatalf("expected state.Request to carry pinned %s into the next step, got %+v", toolName, state.Request.PinnedToolNames)
		}
	}
}

func TestApplyInlineToolRequestIsNoOpWithoutRequestedNames(t *testing.T) {
	services := newTurnRunnerTestServices(&sequenceLanguageModel{}, TurnOptions{})
	request := AgentTurnRequest{ToolSet: testToolSet([]string{"skill.search"})}
	state := agentTaskState{Request: request}
	actionDocument := turnActionDocument{Action: "continue", ToolName: "skill.search"}

	updatedRequest := services.runner.applyInlineToolRequest("task-1", request, &state, actionDocument)

	if len(updatedRequest.PinnedToolNames) != 0 {
		t.Fatalf("expected no pins for an empty inline request, got %+v", updatedRequest.PinnedToolNames)
	}
}
