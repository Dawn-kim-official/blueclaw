package agent

import "testing"

func TestToolExposureUsesFixedKernelOnly(t *testing.T) {
	toolSet := testToolSet(append(KernelToolNames(),
		"site.app.create",
		"site.app.publish",
		"file.write",
		"platform.message.send",
	))

	filteredToolSet, event := toolSetForAgentTurnWithExposure(
		toolSet,
		InstructionBundle{},
		AgentRequest{Prompt: "create and publish a site"},
		ExecutionPlan{},
		false,
		OutcomeContract{},
		ToolSelectionDecision{SelectedToolIDs: []string{"site.app.create"}},
		ToolExposureEvent{},
	)

	if got := filteredToolSet.ListToolNames(); !sameStringSet(got, KernelToolNames()) {
		t.Fatalf("expected fixed kernel tools, got %+v", got)
	}
	for _, hiddenToolName := range []string{"site.app.create", "site.app.publish", "file.write", "platform.message.send"} {
		if filteredToolSet.IsAllowed(hiddenToolName) {
			t.Fatalf("expected non-kernel tool %s to be hidden, got %+v", hiddenToolName, filteredToolSet.ListToolNames())
		}
	}
	if event.SelectionSource != "fixed_kernel" || event.UsedFallbackGroups {
		t.Fatalf("expected fixed kernel exposure event, got %+v", event)
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
