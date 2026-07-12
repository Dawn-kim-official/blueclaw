package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"blueclaw/internal/llm"
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

func TestFailureNoticeGeneratorRejectsUngroundedGeneratedReply(t *testing.T) {
	generator := FailureNoticeGenerator{LanguageModel: reviewingReplyLanguageModel{
		reply:           "4. I am a large language model, trained by Google DeepMind. I am an open weights model.",
		structuredReply: `{"decision":"rewrite","message":"사이트 빌드가 정체되어 요청하신 귤 웹사이트를 아직 게시하지 못했습니다. 현재 작업 상태를 다시 확인한 뒤 같은 프로젝트에서 이어가겠습니다.","reason":"candidate was unrelated to the failure context"}`,
	}}

	notice, status := generator.Generate(context.Background(), FailureReport{
		Phase:              "stall",
		StopReason:         "stopped after repeated model actions without workspace progress",
		FailedOperation:    "site.build",
		SafeFailureSummary: "site.build could not create the build scaffold",
		OriginalRequest:    "더 해 괜찮아",
		ResponseLanguage:   ResponseLanguageKorean,
		DiagnosticEventID:  "task-1:stall",
	})

	if status.Source != "generated_review" || notice.Source != "generated_review" {
		t.Fatalf("expected structured review rewrite, got notice=%+v status=%+v", notice, status)
	}
	if strings.Contains(notice.SendableMessage(), "large language model") {
		t.Fatalf("expected unrelated generated text not to be sent, got %q", notice.SendableMessage())
	}
	if !strings.Contains(notice.SendableMessage(), "게시하지 못했습니다") {
		t.Fatalf("expected reviewed user notice, got %q", notice.SendableMessage())
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

func TestFailureNoticePromptForcesCompletedSummaryOnMaxElapsedLimit(t *testing.T) {
	report := FailureReport{
		Phase:            "limit",
		StopReason:       "max_elapsed",
		CompletedSummary: "- message.search: found 3 candidate messages about the Q3 launch\n- skill.search: matched \"weekly-report\" skill",
		OriginalRequest:  "이번 주 업무 관련 메시지 찾아줘",
		ResponseLanguage: ResponseLanguageKorean,
	}

	prompt := buildFailureNoticePrompt(report)

	if !strings.Contains(prompt, "concrete findings") {
		t.Fatalf("expected prompt to require concrete findings from completedSummary, got %q", prompt)
	}
	if !strings.Contains(prompt, "found 3 candidate messages") {
		t.Fatalf("expected prompt to carry completedSummary content, got %q", prompt)
	}
}

func TestFailureNoticePromptDoesNotForceCompletedSummaryWithoutData(t *testing.T) {
	report := FailureReport{
		Phase:            "limit",
		StopReason:       "max_elapsed",
		OriginalRequest:  "이번 주 업무 관련 메시지 찾아줘",
		ResponseLanguage: ResponseLanguageKorean,
	}

	prompt := buildFailureNoticePrompt(report)

	if strings.Contains(prompt, "concrete findings") {
		t.Fatalf("expected no completedSummary instruction without data, got %q", prompt)
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

func TestStallControlPhraseNotDeliveredWhenModelFails(t *testing.T) {
	notice, status := (FailureNoticeGenerator{LanguageModel: failingLanguageModel{}}).Generate(context.Background(), FailureReport{
		Phase:             "stall",
		StopReason:        "stopped after repeated model actions without workspace, tool, artifact, attachment, or new failure progress, including after stall guidance",
		ResponseLanguage:  ResponseLanguageKorean,
		DiagnosticEventID: "task-1:stall",
	})

	if status.Source != "raw_error" {
		t.Fatalf("expected raw error fallback for stall, got %+v", status)
	}
	if stallNoticeCanReachUser(notice, status.Source) {
		t.Fatalf("expected stall control phrase not to reach the user, got %q", notice.SendableMessage())
	}
}

func TestFailureNoticeRejectsChatTextSubstituteForRequiredArtifact(t *testing.T) {
	message := "파일 생성 단계가 중단되었습니다. 준비된 발표 자료의 전체 기획안을 텍스트로 이곳에 바로 정리해 드릴까요?"

	substituteNotice := buildFailureNotice(message, "generated", FailureReport{ArtifactRequired: true, ResponseLanguage: ResponseLanguageKorean})
	if substituteNotice.IsSendable {
		t.Fatalf("expected chat text substitute for required artifact to be rejected, got %q", substituteNotice.SendableMessage())
	}

	plainNotice := buildFailureNotice(message, "generated", FailureReport{ResponseLanguage: ResponseLanguageKorean})
	if !plainNotice.IsSendable {
		t.Fatalf("expected notice without required artifact to stay sendable, got %+v", plainNotice)
	}
}

func TestFinishMessageCompressionPromptUsesMattermostBudget(t *testing.T) {
	prompt := buildFinishMessageCompressionPrompt("긴 결과입니다.", ResponseLanguageKorean, finishMessageMaximumCharacters)

	if !strings.Contains(prompt, "Maximum characters: 1200") {
		t.Fatalf("expected Mattermost finish budget, got %q", prompt)
	}
}

type staticReplyLanguageModel struct {
	reply string
}

func (model staticReplyLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return model.reply, nil
}

func (model staticReplyLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, errors.New("structured response unsupported")
}

type reviewingReplyLanguageModel struct {
	reply           string
	structuredReply string
}

func (model reviewingReplyLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return model.reply, nil
}

func (model reviewingReplyLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{Content: model.structuredReply}, nil
}

func TestIntakeNoticeGeneratorUsesGeneratedReply(t *testing.T) {
	generator := FailureNoticeGenerator{LanguageModel: staticReplyLanguageModel{reply: "요청 범위가 한 번에 처리하기에 커서 더 좁혀주시면 진행할게요."}}

	notice := generator.GenerateIntakeNotice(context.Background(), IntakeReport{
		Classification:   IntakeClassificationNeedsConfirmation,
		Reason:           "request appears too large for one bounded execution",
		OriginalRequest:  "회사 전체 데이터를 다 정리해줘",
		ResponseLanguage: ResponseLanguageKorean,
	})

	if notice.Source != "generated" || !notice.IsSendable {
		t.Fatalf("expected generated sendable notice, got %+v", notice)
	}
	if !strings.Contains(notice.Message, "좁혀주시면") {
		t.Fatalf("expected model wording, got %q", notice.Message)
	}
}

func TestIntakeNoticeGeneratorFallsBackToReasonWhenModelFails(t *testing.T) {
	generator := FailureNoticeGenerator{LanguageModel: failingLanguageModel{}}

	notice := generator.GenerateIntakeNotice(context.Background(), IntakeReport{
		Classification:   IntakeClassificationUnsupported,
		Reason:           "request is outside the available execution boundary",
		ResponseLanguage: ResponseLanguageKorean,
	})

	if notice.Source != "raw_error" {
		t.Fatalf("expected raw error fallback, got %+v", notice)
	}
	if !strings.Contains(notice.Message, "execution boundary") {
		t.Fatalf("expected compact reason summary, got %q", notice.Message)
	}
	if notice.SendableMessage() == "" {
		t.Fatalf("expected fallback notice to be sendable")
	}
}

func TestIntakeNoticePromptCarriesClassificationIntent(t *testing.T) {
	report := normalizeFailureReport(FailureReport{
		Phase:            "task_intake",
		StopReason:       "request appears too large for one bounded execution",
		ResponseLanguage: ResponseLanguageKorean,
	})

	prompt := buildIntakeNoticePrompt(IntakeClassificationNeedsConfirmation, report)

	if !strings.Contains(prompt, "confirm a narrower scope") {
		t.Fatalf("expected needs-confirmation intent, got %q", prompt)
	}
	if !strings.Contains(prompt, "Compact intake context") {
		t.Fatalf("expected compact intake context, got %q", prompt)
	}
}
