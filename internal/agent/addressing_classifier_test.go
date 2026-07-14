package agent

import (
	"context"
	"strings"
	"testing"

	"blueclaw/internal/llm"
)

func TestAddressingClassificationSchemaOmitsReasonByDefault(t *testing.T) {
	schema := addressingClassificationSchema(false)

	if strings.Contains(schema.Document, "reason") || strings.Contains(schema.Document, "addressingClass") {
		t.Fatalf("expected compact addressing schema without reason or legacy field, got %s", schema.Document)
	}
	for _, fragment := range []string{"target", "shouldRespond", "reactionEmoji", "dutyMatch", "dutyName", "dutyConfidence", "bot", "human", "anyone", "none", "unclear"} {
		if !strings.Contains(schema.Document, fragment) {
			t.Fatalf("expected addressing schema to contain %q, got %s", fragment, schema.Document)
		}
	}
}

func TestAddressingClassificationSchemaIncludesReasonOnlyForDebug(t *testing.T) {
	schema := addressingClassificationSchema(true)

	for _, fragment := range []string{"target", "shouldRespond", "reactionEmoji", "dutyMatch", "dutyName", "dutyConfidence", "reason"} {
		if !strings.Contains(schema.Document, fragment) {
			t.Fatalf("expected debug addressing schema to contain %q, got %s", fragment, schema.Document)
		}
	}
}

func TestAddressingClassificationPromptGuidesTheFourOutcomes(t *testing.T) {
	prompt := addressingClassificationPrompt(AddressingClassificationRequest{Prompt: "네 확인해볼게요"})

	for _, fragment := range []string{"shouldRespond", "reactionEmoji", "react only", "이거 봐주세요", "고마워", "dutyMatch"} {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("expected addressing prompt to contain %q, got %s", fragment, prompt)
		}
	}
}

func TestAddressingClassificationPromptRespondsToBanterAndReactsToSharing(t *testing.T) {
	prompt := addressingClassificationPrompt(AddressingClassificationRequest{Prompt: "@김인턴", BotMentioned: true})

	for _, fragment := range []string{"botMentioned: true", "역시 김인턴", "respond briefly and in kind", "공유합니다"} {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("expected addressing prompt to contain %q, got %s", fragment, prompt)
		}
	}
}

func TestAddressingClassificationOverridesHumanShouldRespond(t *testing.T) {
	agentKernel := NewAgentKernel(nil, nil)
	agentKernel.UseIntakeLanguageModelProvider(addressingStaticLanguageModel{
		content: `{"target":"human","shouldRespond":true,"reactionEmoji":"","dutyMatch":false,"dutyName":"","dutyConfidence":0}`,
	})

	decision, errorValue := agentKernel.ClassifyAddressing(context.Background(), AddressingClassificationRequest{Prompt: "네 확인해볼게요"})
	if errorValue != nil {
		t.Fatalf("expected addressing classification: %v", errorValue)
	}
	if decision.Target != AddressingTargetHuman {
		t.Fatalf("expected human target, got %+v", decision)
	}
	if decision.ShouldRespond {
		t.Fatalf("expected human target to override shouldRespond=false, got %+v", decision)
	}
}

func TestAddressingClassificationConstrainsReactionEmojiToAllowedSet(t *testing.T) {
	agentKernel := NewAgentKernel(nil, nil)
	agentKernel.UseIntakeLanguageModelProvider(addressingStaticLanguageModel{
		content: `{"target":"bot","shouldRespond":false,"reactionEmoji":"eyes","dutyMatch":false,"dutyName":"","dutyConfidence":0}`,
	})
	decision, errorValue := agentKernel.ClassifyAddressing(context.Background(), AddressingClassificationRequest{Prompt: "이거 봐주세요"})
	if errorValue != nil {
		t.Fatalf("expected addressing classification: %v", errorValue)
	}
	if decision.ReactionEmoji != "eyes" {
		t.Fatalf("expected allowed emoji to pass through, got %q", decision.ReactionEmoji)
	}

	agentKernel.UseIntakeLanguageModelProvider(addressingStaticLanguageModel{
		content: `{"target":"bot","shouldRespond":false,"reactionEmoji":"sparkles","dutyMatch":false,"dutyName":"","dutyConfidence":0}`,
	})
	offListDecision, errorValue := agentKernel.ClassifyAddressing(context.Background(), AddressingClassificationRequest{Prompt: "이거 봐주세요"})
	if errorValue != nil {
		t.Fatalf("expected addressing classification: %v", errorValue)
	}
	if offListDecision.ReactionEmoji != "" {
		t.Fatalf("expected an off-list emoji to be dropped (no reaction), got %q", offListDecision.ReactionEmoji)
	}
}

func TestAddressingClassificationReturnsAmbientDutyFields(t *testing.T) {
	agentKernel := NewAgentKernel(nil, nil)
	agentKernel.UseIntakeLanguageModelProvider(addressingStaticLanguageModel{
		content: `{"target":"anyone","shouldRespond":false,"reactionEmoji":"","dutyMatch":true,"dutyName":"calendar_upkeep","dutyConfidence":1.2}`,
	})

	decision, errorValue := agentKernel.ClassifyAddressing(context.Background(), AddressingClassificationRequest{Prompt: "오늘 5시 회의"})
	if errorValue != nil {
		t.Fatalf("expected addressing classification: %v", errorValue)
	}
	if !decision.DutyMatch || decision.DutyName != "calendar_upkeep" || decision.DutyConfidence != 1 {
		t.Fatalf("expected normalized duty fields, got %+v", decision)
	}
}

type addressingStaticLanguageModel struct {
	content string
}

func (languageModel addressingStaticLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel addressingStaticLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{Content: languageModel.content}, nil
}
