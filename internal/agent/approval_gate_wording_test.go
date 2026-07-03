package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHeldCallConfirmationWordingFramesDeclarativeModelMessageAsQuestion(t *testing.T) {
	request := AgentTurnRequest{ResponseLanguage: "ko"}
	actionDocument := turnActionDocument{
		Message:   "'Local Fleet Studio' 테스트 웹사이트 삭제를 시작합니다.",
		ToolName:  CapabilityInvokeToolName,
		ToolInput: json.RawMessage(`{"operation":"site.delete","input":{"siteID":"site-1"}}`),
	}

	confirmation := heldCallConfirmationWording(request, actionDocument)
	if !strings.HasPrefix(confirmation, "'Local Fleet Studio' 테스트 웹사이트 삭제를 시작합니다.") {
		t.Fatalf("expected the model wording to lead the confirmation, got %q", confirmation)
	}
	if !strings.HasSuffix(confirmation, "진행할까요?") {
		t.Fatalf("expected a question frame on the declarative wording, got %q", confirmation)
	}
}

func TestHeldCallConfirmationWordingKeepsQuestionModelMessage(t *testing.T) {
	request := AgentTurnRequest{ResponseLanguage: "ko"}
	actionDocument := turnActionDocument{
		Message:  "테스트 웹사이트를 삭제할까요?",
		ToolName: CapabilityInvokeToolName,
	}

	confirmation := heldCallConfirmationWording(request, actionDocument)
	if confirmation != "테스트 웹사이트를 삭제할까요?" {
		t.Fatalf("expected question wording to stay untouched, got %q", confirmation)
	}
}

func TestHeldCallConfirmationWordingUsesModelReasonWhenMessageIsEmpty(t *testing.T) {
	request := AgentTurnRequest{ResponseLanguage: "ko"}
	actionDocument := turnActionDocument{
		ToolName:  CapabilityInvokeToolName,
		ToolInput: json.RawMessage(`{"operation":"site.delete","input":{"siteID":"site-1","slug":"demo","confirm":"DELETE","reason":"사용자의 웹사이트 삭제 요청"}}`),
	}

	confirmation := heldCallConfirmationWording(request, actionDocument)
	if !strings.Contains(confirmation, "사용자의 웹사이트 삭제 요청") {
		t.Fatalf("expected the model reason inside the confirmation, got %q", confirmation)
	}
	if !strings.Contains(confirmation, "?") {
		t.Fatalf("expected a question, got %q", confirmation)
	}
}

func TestHeldCallConfirmationWordingNeverReturnsEmpty(t *testing.T) {
	request := AgentTurnRequest{ResponseLanguage: "ko"}
	actionDocument := turnActionDocument{
		ToolName:  CapabilityInvokeToolName,
		ToolInput: json.RawMessage(`{"operation":"site.delete","input":{"siteID":"site-1","confirm":"DELETE"}}`),
	}

	confirmation := heldCallConfirmationWording(request, actionDocument)
	if confirmation != "site.delete 작업을 진행할까요?" {
		t.Fatalf("expected the operation fallback question, got %q", confirmation)
	}
}

func TestHeldCallConfirmationWordingFramesEnglishDeclarativeMessage(t *testing.T) {
	request := AgentTurnRequest{ResponseLanguage: "en"}
	actionDocument := turnActionDocument{
		Message:  "Deleting the Local Fleet Studio test website.",
		ToolName: CapabilityInvokeToolName,
	}

	confirmation := heldCallConfirmationWording(request, actionDocument)
	if !strings.HasSuffix(confirmation, "Proceed?") {
		t.Fatalf("expected an English question frame, got %q", confirmation)
	}
}
