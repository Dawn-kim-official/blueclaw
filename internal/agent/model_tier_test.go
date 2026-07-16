package agent

import (
	"context"
	"testing"

	"blueclaw/internal/llm"
)

type labeledLanguageModel struct {
	label string
}

func (model labeledLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return model.label, nil
}

func (model labeledLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{ModelName: model.label}, nil
}

func TestNormalizeTaskLevelMapsLegacyAndCanonicalValues(t *testing.T) {
	cases := map[string]TaskLevel{
		"quick":    TaskLevelXLow,
		"simple":   TaskLevelXLow,
		"standard": TaskLevelLow,
		"normal":   TaskLevelLow,
		"deep":     TaskLevelMedium,
		"complex":  TaskLevelMedium,
		"extended": TaskLevelHigh,
		"xlow":     TaskLevelXLow,
		"low":      TaskLevelLow,
		"medium":   TaskLevelMedium,
		"high":     TaskLevelHigh,
		"xhigh":    TaskLevelXHigh,
		"max":      TaskLevelMax,
		"unknown":  TaskLevel(""),
		"":         TaskLevel(""),
	}
	for input, want := range cases {
		if got := NormalizeTaskLevel(input); got != want {
			t.Fatalf("NormalizeTaskLevel(%q): expected %q, got %q", input, want, got)
		}
	}
}

func TestLargerTaskLevelPicksHigherRank(t *testing.T) {
	cases := []struct {
		first  TaskLevel
		second TaskLevel
		want   TaskLevel
	}{
		{TaskLevelXLow, TaskLevelLow, TaskLevelLow},
		{TaskLevelHigh, TaskLevelMedium, TaskLevelHigh},
		{TaskLevelMax, TaskLevelHigh, TaskLevelMax},
		{TaskLevel(""), TaskLevelXLow, TaskLevelXLow},
		{TaskLevelMedium, TaskLevelMedium, TaskLevelMedium},
	}
	for _, testCase := range cases {
		if got := LargerTaskLevel(testCase.first, testCase.second); got != testCase.want {
			t.Fatalf("LargerTaskLevel(%q, %q): expected %q, got %q", testCase.first, testCase.second, testCase.want, got)
		}
	}
}

func TestNextTaskLevelWalksLadderAndStopsAtMax(t *testing.T) {
	cases := []struct {
		current TaskLevel
		want    TaskLevel
		canNext bool
	}{
		{TaskLevelXLow, TaskLevelLow, true},
		{TaskLevelLow, TaskLevelMedium, true},
		{TaskLevelMedium, TaskLevelHigh, true},
		{TaskLevelHigh, TaskLevelXHigh, true},
		{TaskLevelXHigh, TaskLevelMax, true},
		{TaskLevelMax, TaskLevel(""), false},
	}
	for _, testCase := range cases {
		next, canNext := nextTaskLevel(testCase.current)
		if canNext != testCase.canNext || next != testCase.want {
			t.Fatalf("nextTaskLevel(%q): expected (%q, %v), got (%q, %v)", testCase.current, testCase.want, testCase.canNext, next, canNext)
		}
	}
}

func TestTaskLevelProfileForLevelMapsLimits(t *testing.T) {
	mediumProfile := TaskLevelProfileForLevel(TaskLevelMedium)
	if mediumProfile.MaxIterationCount != 180 || mediumProfile.MaxToolCallCount != 100 || mediumProfile.Duration.Minutes() != 40 {
		t.Fatalf("expected medium profile limits, got %+v", mediumProfile)
	}

	fallbackProfile := TaskLevelProfileForLevel(TaskLevel(""))
	if fallbackProfile.TaskLevel != TaskLevelLow {
		t.Fatalf("expected empty task level to fall back to low profile, got %+v", fallbackProfile)
	}
}

func TestArtifactTaskLevelFloorRaisesSiteAndSlidesToXHigh(t *testing.T) {
	siteFloor := artifactTaskLevelFloor(AgentRequest{}, IntakeDecision{RequiredEvidenceTools: []string{"site.publish"}})
	if siteFloor != TaskLevelXHigh {
		t.Fatalf("expected site request to floor at xhigh, got %q", siteFloor)
	}

	slidesRequest := AgentRequest{ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{RequiredAttachmentSuffixes: []string{".pptx"}}}}
	slidesFloor := artifactTaskLevelFloor(slidesRequest, IntakeDecision{})
	if slidesFloor != TaskLevelXHigh {
		t.Fatalf("expected slides request to floor at xhigh, got %q", slidesFloor)
	}

	plainFloor := artifactTaskLevelFloor(AgentRequest{}, IntakeDecision{})
	if plainFloor != TaskLevelXLow {
		t.Fatalf("expected plain request to keep xlow floor, got %q", plainFloor)
	}
}

// A fresh visual-deliverable task carries its output-format signal in the intake
// decision's requested formats (populated from the selected skill / classifier),
// not yet in the request's outcome contract, which is built later. The xhigh
// floor must read that signal, or new pptx/html deck tasks silently run below
// xhigh.
func TestArtifactTaskLevelFloorRaisesVisualDeliverableFromIntakeDecisionOutputFormats(t *testing.T) {
	for _, format := range []string{"pptx", "ppt", "html"} {
		floor := artifactTaskLevelFloor(AgentRequest{}, IntakeDecision{RequestedOutputFormats: []string{format}})
		if floor != TaskLevelXHigh {
			t.Fatalf("expected %q output format to floor at xhigh, got %q", format, floor)
		}
	}
}

func TestTimeBudgetSecondsForIntakeUsesShorterOfTierAndEstimate(t *testing.T) {
	xhighProfile := TaskLevelProfileForLevel(TaskLevelXHigh)
	tierCeiling := int(xhighProfile.Duration.Seconds())

	if seconds := timeBudgetSecondsForIntake(xhighProfile, 3); seconds != 270 {
		t.Fatalf("expected 3min estimate to give 270s (x1.5), got %d", seconds)
	}
	if seconds := timeBudgetSecondsForIntake(xhighProfile, 0); seconds != tierCeiling {
		t.Fatalf("expected tier ceiling with no estimate, got %d", seconds)
	}
	if seconds := timeBudgetSecondsForIntake(xhighProfile, 100000); seconds != tierCeiling {
		t.Fatalf("expected an oversized estimate to be capped at the tier ceiling, got %d", seconds)
	}
}

func TestTaskLanguageModelForLevelSelectsClient(t *testing.T) {
	kernel := &AgentKernel{
		languageModel:           labeledLanguageModel{label: "low"},
		maxTaskLanguageModel:    labeledLanguageModel{label: "max"},
		xHighTaskLanguageModel:  labeledLanguageModel{label: "xhigh"},
		highTaskLanguageModel:   labeledLanguageModel{label: "high"},
		mediumTaskLanguageModel: labeledLanguageModel{label: "medium"},
		xLowTaskLanguageModel:   labeledLanguageModel{label: "xlow"},
		codingTaskLanguageModel: labeledLanguageModel{label: "coding"},
	}
	cases := map[TaskLevel]string{
		TaskLevelMax:    "max",
		TaskLevelXHigh:  "xhigh",
		TaskLevelHigh:   "high",
		TaskLevelMedium: "medium",
		TaskLevelXLow:   "xlow",
		TaskLevelLow:    "low",
	}
	for taskLevel, expectedLabel := range cases {
		selected := kernel.taskLanguageModelForLevel(taskLevel)
		response, _ := selected.GenerateResponse(context.Background(), "")
		if response != expectedLabel {
			t.Fatalf("task level %q: expected %q client, got %q", taskLevel, expectedLabel, response)
		}
	}
}

func TestTaskLanguageModelForLevelFallsBackToBaseWhenUnset(t *testing.T) {
	kernel := &AgentKernel{languageModel: labeledLanguageModel{label: "low"}}
	for _, taskLevel := range []TaskLevel{TaskLevelMedium, TaskLevelHigh, TaskLevelXHigh, TaskLevelMax} {
		selected := kernel.taskLanguageModelForLevel(taskLevel)
		response, _ := selected.GenerateResponse(context.Background(), "")
		if response != "low" {
			t.Fatalf("task level %q: expected base client fallback, got %q", taskLevel, response)
		}
	}
}

func TestClassificationLanguageModelPrefersXLow(t *testing.T) {
	kernel := &AgentKernel{
		languageModel:         labeledLanguageModel{label: "low"},
		intakeLanguageModel:   labeledLanguageModel{label: "intake"},
		xLowTaskLanguageModel: labeledLanguageModel{label: "xlow"},
	}

	selected := kernel.classificationLanguageModel()
	response, _ := selected.GenerateResponse(context.Background(), "")
	if response != "xlow" {
		t.Fatalf("expected xlow classification model, got %q", response)
	}
}

func TestClassificationLanguageModelFallsBackToIntakeThenBase(t *testing.T) {
	kernel := &AgentKernel{
		languageModel:       labeledLanguageModel{label: "low"},
		intakeLanguageModel: labeledLanguageModel{label: "intake"},
	}
	selected := kernel.classificationLanguageModel()
	response, _ := selected.GenerateResponse(context.Background(), "")
	if response != "intake" {
		t.Fatalf("expected intake classification fallback, got %q", response)
	}

	kernel.intakeLanguageModel = nil
	selected = kernel.classificationLanguageModel()
	response, _ = selected.GenerateResponse(context.Background(), "")
	if response != "low" {
		t.Fatalf("expected base classification fallback, got %q", response)
	}
}

func TestTurnRouterLanguageModelPrefersIntake(t *testing.T) {
	kernel := &AgentKernel{
		languageModel:         labeledLanguageModel{label: "low"},
		intakeLanguageModel:   labeledLanguageModel{label: "intake"},
		xLowTaskLanguageModel: labeledLanguageModel{label: "xlow"},
	}

	selected := kernel.turnRouterLanguageModel()
	response, _ := selected.GenerateResponse(context.Background(), "")
	if response != "intake" {
		t.Fatalf("expected intake turn router model, got %q", response)
	}

	kernel.intakeLanguageModel = nil
	selected = kernel.turnRouterLanguageModel()
	response, _ = selected.GenerateResponse(context.Background(), "")
	if response != "xlow" {
		t.Fatalf("expected classification fallback, got %q", response)
	}
}
