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

func TestTaskModelTierForEffortLevel(t *testing.T) {
	cases := map[EffortLevel]modelTier{
		EffortLevelQuick:    modelTierLow,
		EffortLevelStandard: modelTierMedium,
		EffortLevelDeep:     modelTierHigh,
		EffortLevelExtended: modelTierHigh,
		EffortLevel(""):     modelTierLow,
	}
	for effortLevel, expectedTier := range cases {
		if tier := taskModelTierForEffortLevel(effortLevel); tier != expectedTier {
			t.Fatalf("effort %q: expected tier %q, got %q", effortLevel, expectedTier, tier)
		}
	}
}

func TestTaskLanguageModelForEffortLevelSelectsTierClient(t *testing.T) {
	kernel := &AgentKernel{
		languageModel:           labeledLanguageModel{label: "low"},
		mediumTaskLanguageModel: labeledLanguageModel{label: "medium"},
		highTaskLanguageModel:   labeledLanguageModel{label: "high"},
	}
	cases := map[EffortLevel]string{
		EffortLevelQuick:    "low",
		EffortLevelStandard: "medium",
		EffortLevelDeep:     "high",
		EffortLevelExtended: "high",
	}
	for effortLevel, expectedLabel := range cases {
		selected := kernel.taskLanguageModelForEffortLevel(effortLevel)
		response, _ := selected.GenerateResponse(context.Background(), "")
		if response != expectedLabel {
			t.Fatalf("effort %q: expected %q client, got %q", effortLevel, expectedLabel, response)
		}
	}
}

func TestTaskLanguageModelFallsBackToBaseWhenTierUnset(t *testing.T) {
	kernel := &AgentKernel{languageModel: labeledLanguageModel{label: "low"}}
	for _, effortLevel := range []EffortLevel{EffortLevelStandard, EffortLevelDeep, EffortLevelExtended} {
		selected := kernel.taskLanguageModelForEffortLevel(effortLevel)
		response, _ := selected.GenerateResponse(context.Background(), "")
		if response != "low" {
			t.Fatalf("effort %q: expected base client fallback, got %q", effortLevel, response)
		}
	}
}
