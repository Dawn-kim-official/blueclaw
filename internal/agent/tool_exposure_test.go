package agent

import (
	"context"
	"strings"
	"testing"
)

func TestToolExposureKeepsSelectedToolsBeforeCore(t *testing.T) {
	toolSet := testToolSet([]string{"skill.search", "tool.describe", "ask.confirm", "ask.choice", "ask.input", "memory.search", "conversation.history", "memory.remember", "site.app.status", "site.app.publish"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:         "site-prototype",
			AllowedTools: []string{"site.app.status", "site.app.publish"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "site-prototype", Status: "selected"}},
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{Prompt: "사이트 고쳐줘"}, ExecutionPlan{}, false, OutcomeContract{}, ToolSelectionDecision{SelectedToolIDs: []string{"site.app.status"}}, ToolExposureEvent{})

	if !filteredToolSet.IsAllowed("site.app.status") {
		t.Fatalf("expected selected tool to be exposed, got %+v", filteredToolSet.ListToolNames())
	}
	if filteredToolSet.IsAllowed("site.app.publish") {
		t.Fatalf("expected unselected fallback skill tool to be hidden when selection is valid, got %+v", filteredToolSet.ListToolNames())
	}
	for _, toolID := range []string{"skill.search", "tool.describe", "ask.confirm", "ask.choice", "ask.input", "memory.search", "conversation.history", "memory.remember"} {
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected core tool %s to remain exposed, got %+v", toolID, filteredToolSet.ListToolNames())
		}
	}
	if event.UsedFallbackGroups {
		t.Fatalf("expected valid model selection to avoid fallback groups: %+v", event)
	}
}

func TestToolExposureFallsBackWhenSelectionIsEmptyOrInvalid(t *testing.T) {
	toolSet := testToolSet([]string{"skill.search", "tool.describe", "ask.confirm", "terminal.run", "file.write", "mail.message.search"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{
			{Name: "slides", AllowedTools: []string{"terminal.run", "file.write"}},
			{Name: "mail", AllowedTools: []string{"mail.message.search"}},
		},
		SkillDecisions: []SkillSelectionDecision{{Name: "slides", Status: "selected"}},
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{Prompt: "발표자료 만들어줘"}, ExecutionPlan{}, false, OutcomeContract{}, ToolSelectionDecision{SelectedToolIDs: []string{"unknown.tool"}}, ToolExposureEvent{})

	for _, toolID := range []string{"skill.search", "tool.describe", "ask.confirm", "terminal.run", "file.write"} {
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected fallback to expose %s, got %+v", toolID, filteredToolSet.ListToolNames())
		}
	}
	if filteredToolSet.IsAllowed("mail.message.search") {
		t.Fatalf("expected unselected skill tool to stay hidden, got %+v", filteredToolSet.ListToolNames())
	}
	if !event.UsedFallbackGroups {
		t.Fatalf("expected invalid selection to use fallback groups: %+v", event)
	}
}

func TestToolExposureCapTruncatesByGroupOrder(t *testing.T) {
	toolIDs := []string{}
	for index := 0; index < 25; index++ {
		toolIDs = append(toolIDs, "custom.tool"+string(rune('a'+index)))
	}
	toolSet := testToolSet(append([]string{"skill.search", "tool.describe", "ask.confirm"}, toolIDs...))
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:         "custom",
			AllowedTools: toolIDs,
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "custom", Status: "selected"}},
	}

	filteredToolSet, event := toolSetForAgentTurnWithExposure(toolSet, instructionBundle, AgentRequest{Prompt: "run custom workflow"}, ExecutionPlan{}, false, OutcomeContract{}, ToolSelectionDecision{SelectedToolIDs: toolIDs}, ToolExposureEvent{})

	exposedToolIDs := filteredToolSet.ListToolNames()
	if len(exposedToolIDs) != maxSchemaCallableToolCount {
		t.Fatalf("expected exactly %d exposed tools, got %d: %+v", maxSchemaCallableToolCount, len(exposedToolIDs), exposedToolIDs)
	}
	for _, toolID := range toolIDs[:maxSchemaCallableToolCount] {
		if !filteredToolSet.IsAllowed(toolID) {
			t.Fatalf("expected selected tool %s to survive cap, got %+v", toolID, exposedToolIDs)
		}
	}
	if filteredToolSet.IsAllowed("skill.search") {
		t.Fatalf("expected core tools to be dropped after selected group consumes cap, got %+v", exposedToolIDs)
	}
	if len(event.DroppedGroups) == 0 || !event.DroppedGroups[0].IsPartial {
		t.Fatalf("expected partial selected group drop event, got %+v", event.DroppedGroups)
	}
}

func TestToolSelectionContextUsesCompactCards(t *testing.T) {
	toolSet := testToolSet([]string{"site.app.status"})
	cards := renderCompactToolCards(toolSet, []toolExposureGroup{{Name: "G5 selected-skill candidates", ToolIDs: []string{"site.app.status"}}})
	summary := renderCoreGroupSummary(collectCoreGroups(testToolSet([]string{"skill.search", "tool.describe", "ask.confirm"})))

	if !strings.Contains(cards, "- site.app.status:") {
		t.Fatalf("expected compact card to include tool id, got %s", cards)
	}
	if strings.Contains(cards, "inputSchema") || strings.Contains(cards, "properties") {
		t.Fatalf("expected compact card to omit full schema, got %s", cards)
	}
	if !strings.Contains(summary, "G1 control-core: skill.search, tool.describe, ask.confirm") {
		t.Fatalf("expected compact core summary, got %s", summary)
	}
}

func TestToolSelectionRouterReturnsEmptyOnModelFailure(t *testing.T) {
	router := NewToolSelectionRouter(failingRecoveryLanguageModel{})
	decision, event := router.Select(context.Background(), toolSelectionRequest{
		Prompt:          "사이트 고쳐줘",
		ToolSet:         testToolSet([]string{"site.app.status"}),
		CandidateGroups: []toolExposureGroup{{Name: "G5 selected-skill candidates", ToolIDs: []string{"site.app.status"}}},
	})

	if len(decision.SelectedToolIDs) != 0 {
		t.Fatalf("expected failed selection to return empty decision, got %+v", decision)
	}
	if !event.SelectionFailed {
		t.Fatalf("expected failed selection event, got %+v", event)
	}
}
