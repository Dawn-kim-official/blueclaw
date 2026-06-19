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

func TestTaskModelTierKeepsSimpleAndNormalCheap(t *testing.T) {
	cases := []struct {
		complexity TaskComplexity
		effort     EffortLevel
		want       modelTier
	}{
		{TaskComplexitySimple, EffortLevelStandard, modelTierLow},
		{TaskComplexityNormal, EffortLevelStandard, modelTierLow},
		{TaskComplexitySimple, EffortLevelQuick, modelTierXLow},
		{TaskComplexityComplex, EffortLevelQuick, modelTierXLow},
		{TaskComplexityComplex, EffortLevelStandard, modelTierMedium},
		{TaskComplexityComplex, EffortLevelDeep, modelTierHigh},
		{TaskComplexitySimple, EffortLevelDeep, modelTierHigh},
		{TaskComplexityNormal, EffortLevelExtended, modelTierHigh},
	}
	for _, testCase := range cases {
		if tier := taskModelTier(testCase.complexity, testCase.effort); tier != testCase.want {
			t.Fatalf("complexity %q effort %q: expected %q, got %q", testCase.complexity, testCase.effort, testCase.want, tier)
		}
	}
}

func TestResolvedTaskModelTierRoutesCodingHighToCoding(t *testing.T) {
	codingKinds := []string{WorkKindCoding}
	cases := []struct {
		complexity TaskComplexity
		effort     EffortLevel
		workKinds  []string
		want       modelTier
	}{
		{TaskComplexityComplex, EffortLevelDeep, codingKinds, modelTierCoding},
		{TaskComplexitySimple, EffortLevelDeep, codingKinds, modelTierCoding},
		{TaskComplexityComplex, EffortLevelDeep, nil, modelTierHigh},
		{TaskComplexityComplex, EffortLevelStandard, codingKinds, modelTierMedium},
		{TaskComplexitySimple, EffortLevelQuick, codingKinds, modelTierXLow},
	}
	for _, testCase := range cases {
		if tier := resolvedTaskModelTier(testCase.complexity, testCase.effort, testCase.workKinds); tier != testCase.want {
			t.Fatalf("complexity %q effort %q kinds %v: expected %q, got %q", testCase.complexity, testCase.effort, testCase.workKinds, testCase.want, tier)
		}
	}
}

func TestTaskLanguageModelForTierSelectsClient(t *testing.T) {
	kernel := &AgentKernel{
		languageModel:           labeledLanguageModel{label: "low"},
		mediumTaskLanguageModel: labeledLanguageModel{label: "medium"},
		highTaskLanguageModel:   labeledLanguageModel{label: "high"},
		xLowTaskLanguageModel:   labeledLanguageModel{label: "xlow"},
		codingTaskLanguageModel: labeledLanguageModel{label: "coding"},
	}
	cases := map[modelTier]string{
		modelTierLow:    "low",
		modelTierMedium: "medium",
		modelTierHigh:   "high",
		modelTierXLow:   "xlow",
		modelTierCoding: "coding",
	}
	for tier, expectedLabel := range cases {
		selected := kernel.taskLanguageModelForTier(tier)
		response, _ := selected.GenerateResponse(context.Background(), "")
		if response != expectedLabel {
			t.Fatalf("tier %q: expected %q client, got %q", tier, expectedLabel, response)
		}
	}
}

func TestTaskLanguageModelFallsBackToBaseWhenTierUnset(t *testing.T) {
	kernel := &AgentKernel{languageModel: labeledLanguageModel{label: "low"}}
	for _, tier := range []modelTier{modelTierMedium, modelTierHigh} {
		selected := kernel.taskLanguageModelForTier(tier)
		response, _ := selected.GenerateResponse(context.Background(), "")
		if response != "low" {
			t.Fatalf("tier %q: expected base client fallback, got %q", tier, response)
		}
	}
}
