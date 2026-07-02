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
		{TaskComplexitySimple, EffortLevelStandard, modelTierXLow},
		{TaskComplexityNormal, EffortLevelStandard, modelTierLow},
		{TaskComplexitySimple, EffortLevelQuick, modelTierXLow},
		{TaskComplexityComplex, EffortLevelQuick, modelTierMedium},
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

func TestResolvedTaskModelTierUsesComplexityAndEffort(t *testing.T) {
	cases := []struct {
		complexity TaskComplexity
		effort     EffortLevel
		want       modelTier
	}{
		{TaskComplexityComplex, EffortLevelDeep, modelTierHigh},
		{TaskComplexitySimple, EffortLevelDeep, modelTierHigh},
		{TaskComplexityComplex, EffortLevelStandard, modelTierMedium},
		{TaskComplexitySimple, EffortLevelQuick, modelTierXLow},
	}
	for _, testCase := range cases {
		if tier := resolvedTaskModelTier(testCase.complexity, testCase.effort); tier != testCase.want {
			t.Fatalf("complexity %q effort %q: expected %q, got %q", testCase.complexity, testCase.effort, testCase.want, tier)
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
