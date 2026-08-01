package bluecollar

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestHeldCallConfirmationWordingRewritesQuestionDraftThroughModel(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"question":"테스트 웹사이트를 삭제할까요?"}`,
	}}
	agentTurnRunner := &AgentTurnRunner{languageModel: languageModel}
	request := AgentTurnRequest{ResponseLanguage: "ko"}
	actionDocument := turnActionDocument{
		Message:  "테스트 웹사이트를 삭제할까요?",
		ToolName: "site_unserve",
	}

	confirmation, errorValue := agentTurnRunner.heldCallConfirmationWording(context.Background(), request, actionDocument)

	if errorValue != nil {
		t.Fatalf("expected no error: %v", errorValue)
	}
	if confirmation != "테스트 웹사이트를 삭제할까요?" {
		t.Fatalf("expected model-generated question, got %q", confirmation)
	}
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected one approval-question model call, got %d", len(languageModel.requests))
	}
	if !strings.Contains(structuredRequestText(languageModel.requests[0]), "테스트 웹사이트를 삭제할까요?") {
		t.Fatalf("expected model draft in approval question prompt, got %s", structuredRequestText(languageModel.requests[0]))
	}
}

func TestHeldCallConfirmationWordingAsksModelForDeclarativeDraft(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"question":"'Local Fleet Studio' 테스트 웹사이트를 삭제할까요?"}`,
	}}
	agentTurnRunner := &AgentTurnRunner{languageModel: languageModel}
	request := AgentTurnRequest{
		Prompt:           "'Local Fleet Studio' 테스트 웹사이트 삭제해줘",
		ResponseLanguage: ResponseLanguageKorean,
	}
	actionDocument := turnActionDocument{
		Message:   "'Local Fleet Studio' 테스트 웹사이트 삭제를 시작합니다.",
		ToolName:  "site_unserve",
		ToolInput: json.RawMessage(`{"siteID":"site-1","slug":"local-fleet-studio"}`),
	}

	confirmation, errorValue := agentTurnRunner.heldCallConfirmationWording(context.Background(), request, actionDocument)

	if errorValue != nil {
		t.Fatalf("expected no error: %v", errorValue)
	}
	if confirmation != "'Local Fleet Studio' 테스트 웹사이트를 삭제할까요?" {
		t.Fatalf("expected model-generated question, got %q", confirmation)
	}
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected one approval-question model call, got %d", len(languageModel.requests))
	}
	if languageModel.requests[0].StructuredOutputSchema.Name != "blueclaw_approval_question" {
		t.Fatalf("expected approval question schema, got %q", languageModel.requests[0].StructuredOutputSchema.Name)
	}
}

func TestHeldCallConfirmationWordingUsesActionFactsAsModelInput(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"question":"테스트에게 다음 내용을 보낼까요?\n\n오늘 오후 3시에 확인하자"}`,
	}}
	agentTurnRunner := &AgentTurnRunner{languageModel: languageModel}
	request := AgentTurnRequest{
		Prompt:           "테스트에게 DM 보내줘",
		ResponseLanguage: ResponseLanguageKorean,
	}
	actionDocument := turnActionDocument{
		ToolName:  "message_send",
		ToolInput: json.RawMessage(`{"targetType":"directMessage","personHint":"테스트","message":"오늘 오후 3시에 확인하자"}`),
	}

	confirmation, errorValue := agentTurnRunner.heldCallConfirmationWording(context.Background(), request, actionDocument)

	if errorValue != nil {
		t.Fatalf("expected no error: %v", errorValue)
	}
	if confirmation != "테스트에게 다음 내용을 보낼까요?\n\n오늘 오후 3시에 확인하자" {
		t.Fatalf("expected model-generated question with message content, got %q", confirmation)
	}
	requestText := structuredRequestText(languageModel.requests[0])
	for _, expected := range []string{"테스트", "오늘 오후 3시에 확인하자", "Do not mention internal tool names"} {
		if !strings.Contains(requestText, expected) {
			t.Fatalf("expected approval question prompt to contain %q, got %s", expected, requestText)
		}
	}
}

func TestHeldCallConfirmationWordingRejectsEmptyModelQuestion(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"question":" "}`,
	}}
	agentTurnRunner := &AgentTurnRunner{languageModel: languageModel}
	request := AgentTurnRequest{ResponseLanguage: ResponseLanguageKorean}
	actionDocument := turnActionDocument{
		ToolName:  "site_unserve",
		ToolInput: json.RawMessage(`{"siteID":"site-1"}`),
	}

	confirmation, errorValue := agentTurnRunner.heldCallConfirmationWording(context.Background(), request, actionDocument)

	if errorValue == nil {
		t.Fatalf("expected empty generated question to fail, got confirmation %q", confirmation)
	}
}
