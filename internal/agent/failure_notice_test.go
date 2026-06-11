package agent

import (
	"context"
	"strings"
	"testing"
)

func TestFailureNoticeSendabilityAllowsPublicURLAndNaturalEllipsis(t *testing.T) {
	message := "공개 문서 https://example.com/guide 를 확인했지만 요청한 결과를 끝내지 못했습니다..."

	if !failureNoticeMessageIsSendable(message) {
		t.Fatalf("expected public URL and natural ellipsis to be sendable")
	}
}

func TestFailureNoticeSendabilityRejectsInternalDiagnostics(t *testing.T) {
	messages := []string{
		"replyStatus: source=suppressed; text_recovery_error=context deadline exceeded",
		"내부 요청 http://internkim-capability/v1/llm/text 가 실패했습니다.",
		"작업 디렉터리 /home/site/draft 권한을 확인하지 못했습니다.",
	}

	for _, message := range messages {
		if failureNoticeMessageIsSendable(message) {
			t.Fatalf("expected internal diagnostic to be rejected: %q", message)
		}
	}
}

func TestFailureNoticePromptUsesCompactContextOnly(t *testing.T) {
	report := FailureReport{
		Phase:              "failure",
		StopReason:         "tool failed",
		FailedOperation:    "site.build",
		SafeFailureSummary: "build tool could not write the requested output",
		OriginalRequest:    "발표자료 만들어줘",
		ResponseLanguage:   ResponseLanguageKorean,
		DiagnosticEventID:  "task-1:failure",
	}

	prompt := buildFailureNoticePrompt(report)

	if !strings.Contains(prompt, "Compact failure context") || !strings.Contains(prompt, "발표자료 만들어줘") {
		t.Fatalf("expected compact failure context, got %q", prompt)
	}
	if strings.Contains(prompt, "VisibleContext") || strings.Contains(prompt, "Messages") {
		t.Fatalf("expected prompt to avoid full visible history references, got %q", prompt)
	}
}

func TestFailureNoticeGeneratorFallsBackToRedactedRawError(t *testing.T) {
	notice, status := (FailureNoticeGenerator{LanguageModel: failingLanguageModel{}}).Generate(context.Background(), FailureReport{
		Phase:             "launch",
		StepName:          "audit_tool_registry",
		RawError:          "runtime_registry_mismatch token=secret-token Authorization: Bearer sk-testsecret123",
		ResponseLanguage:  "ko",
		DiagnosticEventID: "task-1:launch",
	})

	if status.Source != "raw_error" || notice.Source != "raw_error" {
		t.Fatalf("expected raw error source, got status=%+v notice=%+v", status, notice)
	}
	if !strings.Contains(notice.Message, "runtime_registry_mismatch") {
		t.Fatalf("expected raw failure detail, got %q", notice.Message)
	}
	if strings.Contains(notice.Message, "secret-token") || strings.Contains(notice.Message, "sk-testsecret123") {
		t.Fatalf("expected secrets to be redacted, got %q", notice.Message)
	}
	if notice.SendableMessage() == "" {
		t.Fatalf("expected raw error notice to be sendable")
	}
}

func TestFinishMessageCompressionPromptUsesMattermostBudget(t *testing.T) {
	prompt := buildFinishMessageCompressionPrompt("긴 결과입니다.", ResponseLanguageKorean, finishMessageMaximumCharacters)

	if !strings.Contains(prompt, "Maximum characters: 1200") {
		t.Fatalf("expected Mattermost finish budget, got %q", prompt)
	}
}
