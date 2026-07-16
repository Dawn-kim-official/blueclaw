package agent

import "testing"

func TestToolExposureUsesKernelWithoutSelectedSkills(t *testing.T) {
	toolSet := testToolSet(append(KernelToolNames(),
		"site.create",
		"site.publish",
		"message.send",
	))

	filteredToolSet, event := toolSetForAgentTurnWithExposure(
		toolSet,
		InstructionBundle{},
		AgentRequest{Prompt: "create and publish a site"},
		ExecutionPlan{},
		false,
		OutcomeContract{},
		ToolExposureEvent{},
	)

	if got := filteredToolSet.ListToolNames(); !sameStringSet(got, KernelToolNames()) {
		t.Fatalf("expected fixed kernel tools, got %+v", got)
	}
	for _, hiddenToolName := range []string{"site.create", "site.publish", "message.send"} {
		if filteredToolSet.IsAllowed(hiddenToolName) {
			t.Fatalf("expected non-kernel tool %s to be hidden, got %+v", hiddenToolName, filteredToolSet.ListToolNames())
		}
	}
	for _, kernelToolName := range []string{"file.read", "file.write", "file.edit", "file.preview", "image.read"} {
		if !filteredToolSet.IsAllowed(kernelToolName) {
			t.Fatalf("expected coding kernel tool %s to be exposed, got %+v", kernelToolName, filteredToolSet.ListToolNames())
		}
	}
	if event.SelectionSource != "fixed_kernel" || event.UsedFallbackGroups {
		t.Fatalf("expected fixed kernel exposure event, got %+v", event)
	}
}

func TestToolExposureAddsAskInputOnlyForTypedInteraction(t *testing.T) {
	toolSet := testToolSet(append(KernelToolNames(), AskInputToolName))
	outcomeContract := OutcomeContract{ExpectedResults: []ExpectedResult{{
		ID:              "interactive-choice",
		Type:            ExpectedResultTypeMessage,
		Description:     "The user can choose one of the presented options.",
		Required:        true,
		AcceptanceHints: []string{AskInputToolName},
	}}}

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(
		toolSet,
		InstructionBundle{},
		AgentRequest{},
		ExecutionPlan{},
		false,
		outcomeContract,
		ToolExposureEvent{},
	)

	if !filteredToolSet.IsAllowed(AskInputToolName) {
		t.Fatalf("expected typed interactive outcome to expose ask.input, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestToolExposureRequiresExplicitSkillSearchForImmediateReply(t *testing.T) {
	toolSet := testToolSet(KernelToolNames())
	request := AgentRequest{TaskShape: TaskShapeImmediateReply}

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(
		toolSet,
		InstructionBundle{},
		request,
		ExecutionPlan{},
		false,
		OutcomeContract{},
		ToolExposureEvent{},
	)
	if filteredToolSet.IsAllowed(SkillSearchToolName) {
		t.Fatalf("expected immediate reply to hide unrequested skill.search, got %+v", filteredToolSet.ListToolNames())
	}

	request.PinnedToolNames = []string{SkillSearchToolName}
	filteredToolSet, _ = toolSetForAgentTurnWithExposure(
		toolSet,
		InstructionBundle{},
		request,
		ExecutionPlan{},
		false,
		OutcomeContract{},
		ToolExposureEvent{},
	)
	if !filteredToolSet.IsAllowed(SkillSearchToolName) {
		t.Fatalf("expected typed initial tool to expose skill.search, got %+v", filteredToolSet.ListToolNames())
	}
}

func sameStringSet(leftValues []string, rightValues []string) bool {
	if len(leftValues) != len(rightValues) {
		return false
	}
	rightValueByValue := map[string]bool{}
	for _, rightValue := range rightValues {
		rightValueByValue[rightValue] = true
	}
	for _, leftValue := range leftValues {
		if !rightValueByValue[leftValue] {
			return false
		}
	}
	return true
}
