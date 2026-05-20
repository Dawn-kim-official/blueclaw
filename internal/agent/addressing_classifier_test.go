package agent

import (
	"context"
	"strings"
	"testing"

	"blueclaw/internal/llm"
)

func TestAddressingClassificationSchemaOmitsReasonByDefault(t *testing.T) {
	schema := addressingClassificationSchema(false)

	if containsAny(schema.Document, []string{"reason", "addressingClass"}) {
		t.Fatalf("expected compact addressing schema without reason or legacy field, got %s", schema.Document)
	}
	for _, fragment := range []string{"target", "shouldReply", "bot", "human", "anyone", "none", "unclear"} {
		if !strings.Contains(schema.Document, fragment) {
			t.Fatalf("expected addressing schema to contain %q, got %s", fragment, schema.Document)
		}
	}
}

func TestAddressingClassificationSchemaIncludesReasonOnlyForDebug(t *testing.T) {
	schema := addressingClassificationSchema(true)

	for _, fragment := range []string{"target", "shouldReply", "reason"} {
		if !strings.Contains(schema.Document, fragment) {
			t.Fatalf("expected debug addressing schema to contain %q, got %s", fragment, schema.Document)
		}
	}
}

func TestAddressingClassificationPromptGuidesHumanDirectedAcknowledgements(t *testing.T) {
	prompt := addressingClassificationPrompt(AddressingClassificationRequest{Prompt: "네 확인해볼게요"})

	for _, fragment := range []string{"target=human", "shouldReply=false", "short acknowledgement", "human-directed message"} {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("expected addressing prompt to contain %q, got %s", fragment, prompt)
		}
	}
}

func TestAddressingClassificationOverridesHumanShouldReply(t *testing.T) {
	agentKernel := NewAgentKernel(nil, nil)
	agentKernel.UseIntakeLanguageModelProvider(addressingStaticLanguageModel{
		content: `{"target":"human","shouldReply":true}`,
	})

	decision, errorValue := agentKernel.ClassifyAddressing(context.Background(), AddressingClassificationRequest{Prompt: "네 확인해볼게요"})
	if errorValue != nil {
		t.Fatalf("expected addressing classification: %v", errorValue)
	}
	if decision.Target != AddressingTargetHuman {
		t.Fatalf("expected human target, got %+v", decision)
	}
	if decision.ShouldReply {
		t.Fatalf("expected human target to override shouldReply=false, got %+v", decision)
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
