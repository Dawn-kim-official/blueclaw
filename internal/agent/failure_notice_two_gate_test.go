package agent

import (
	"context"
	"testing"

	"blueclaw/internal/llm"
)

type fixedReplyLanguageModel struct {
	reply string
}

func (model fixedReplyLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return model.reply, nil
}

func (model fixedReplyLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, nil
}

func TestFailureNoticeFallsBackWhenReviewIsUnavailable(t *testing.T) {
	report := FailureReport{
		Phase:            "stall",
		StopReason:       "recovery_tool_budget_exhausted",
		ResponseLanguage: "ko",
		ArtifactRequired: true,
		HasAttachments:   false,
	}
	generator := FailureNoticeGenerator{LanguageModel: fixedReplyLanguageModel{reply: "슬라이드 덱을 완성하지 못했어요. 원하시면 텍스트로 정리해 드릴까요?"}}

	notice, status := generator.Generate(context.Background(), report)

	if status.Source != "raw_error" {
		t.Fatalf("expected raw_error without structured review, got %q (reason %q)", status.Source, status.Reason)
	}
	if notice.SendableMessage() == "" {
		t.Fatal("expected a sendable raw failure notice")
	}
	if notice.SendableMessage() == "슬라이드 덱을 완성하지 못했어요. 원하시면 텍스트로 정리해 드릴까요?" {
		t.Fatal("expected unreviewed freeform draft not to be delivered")
	}
}

func TestFailureNoticeFallsBackToRawErrorOnlyWhenDraftLeaks(t *testing.T) {
	report := FailureReport{
		Phase:            "stall",
		StopReason:       "recovery_tool_budget_exhausted",
		ResponseLanguage: "ko",
	}
	generator := FailureNoticeGenerator{LanguageModel: fixedReplyLanguageModel{reply: "작업이 실패했습니다: context deadline exceeded at /workspace/.blueclaw/run"}}

	_, status := generator.Generate(context.Background(), report)

	if status.Source != "raw_error" {
		t.Fatalf("expected raw_error for a leaking draft, got %q", status.Source)
	}
}

func TestFailureNoticeRejectsFalseDeliveryClaimAsUnsafe(t *testing.T) {
	report := FailureReport{ResponseLanguage: "ko", ArtifactRequired: true, HasAttachments: false}
	if failureNoticeMessagePassesSafety("슬라이드 덱을 첨부했습니다. 확인해 주세요.", report) {
		t.Fatal("expected a false attachment-delivery claim to fail the safety gate")
	}
	if !failureNoticeMessagePassesSafety("슬라이드 덱(slides.html)을 완성하지 못했어요. 다시 시도해 주세요.", report) {
		t.Fatal("expected naming the unbuilt artifact to pass the safety gate")
	}
}

func TestValidateUserNoticeAllowsNamingUnbuiltArtifact(t *testing.T) {
	if ValidateUserNoticeDelivery("slides.html을 완성하지 못했어요.") != nil {
		t.Fatal("expected naming an unbuilt artifact to be allowed")
	}
	if ValidateUserNoticeDelivery("slides.html을 첨부했습니다.") == nil {
		t.Fatal("expected a false delivery claim to be rejected")
	}
}
