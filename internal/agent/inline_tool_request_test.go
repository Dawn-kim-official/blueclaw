package agent

import "testing"

func TestApplyInlineToolRequestRejectsHiddenToolExpansion(t *testing.T) {
	services := newTurnRunnerTestServices(&sequenceLanguageModel{}, TurnOptions{})
	request := AgentTurnRequest{ToolSet: testToolSet([]string{"skill.search", "web.search", "mail.message.send"})}
	state := agentTaskState{Request: request}
	actionDocument := turnActionDocument{
		Action:       "continue",
		ToolName:     "skill.search",
		RequestTools: []string{"web.search", "mail.message.send"},
	}

	updatedRequest := services.runner.applyInlineToolRequest("task-1", request, &state, actionDocument)

	if len(updatedRequest.PinnedToolNames) != 0 || len(state.Request.PinnedToolNames) != 0 {
		t.Fatalf("expected hidden inline fields not to expand the palette, request=%+v state=%+v", updatedRequest.PinnedToolNames, state.Request.PinnedToolNames)
	}
}

func TestApplyInlineToolRequestRejectsEncodedHiddenToolExpansion(t *testing.T) {
	services := newTurnRunnerTestServices(&sequenceLanguageModel{}, TurnOptions{})
	request := AgentTurnRequest{ToolSet: testToolSet([]string{"skill.search", "file.deliver", "file.promote"})}
	state := agentTaskState{Request: request}
	actionDocument := turnActionDocument{
		Action:       "continue",
		ToolName:     "skill.search",
		RequestTools: []string{"continue__file_attach", "continue__file_promote"},
	}

	updatedRequest := services.runner.applyInlineToolRequest("task-1", request, &state, actionDocument)

	if len(updatedRequest.PinnedToolNames) != 0 || len(state.Request.PinnedToolNames) != 0 {
		t.Fatalf("expected encoded hidden fields not to expand the palette, request=%+v state=%+v", updatedRequest.PinnedToolNames, state.Request.PinnedToolNames)
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
