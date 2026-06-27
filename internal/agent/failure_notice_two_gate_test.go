package agent

import (
	"context"
	"strings"
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

func TestFailureNoticeReviewRewritesFalseDeliveryClaim(t *testing.T) {
	generator := FailureNoticeGenerator{LanguageModel: reviewingReplyLanguageModel{
		reply:           "슬라이드 덱을 첨부했습니다. 확인해 주세요.",
		structuredReply: `{"decision":"rewrite","message":"슬라이드 덱을 첨부하지 못했습니다. 다시 시도해 주세요.","reason":"candidate falsely claimed artifact delivery without attachments"}`,
	}}

	notice, status := generator.Generate(context.Background(), FailureReport{
		Phase:            "stall",
		StopReason:       "required artifact completion lacked file.attach evidence",
		ResponseLanguage: "ko",
		ArtifactRequired: true,
		HasAttachments:   false,
	})

	if status.Source != "generated_review" || notice.Source != "generated_review" {
		t.Fatalf("expected structured review rewrite, got notice=%+v status=%+v", notice, status)
	}
	if notice.SendableMessage() == "슬라이드 덱을 첨부했습니다. 확인해 주세요." {
		t.Fatal("expected false delivery claim to be rewritten through structured review")
	}
	if !strings.Contains(notice.SendableMessage(), "첨부하지 못했습니다") {
		t.Fatalf("expected reviewed failure notice, got %q", notice.SendableMessage())
	}
}

func TestValidateUserNoticeDoesNotClassifyAttachmentWording(t *testing.T) {
	if ValidateUserNoticeDelivery("slides.html을 완성하지 못했어요.") != nil {
		t.Fatal("expected naming an unbuilt artifact to be allowed")
	}
	if errorValue := ValidateUserNoticeDelivery("slides.html을 첨부했습니다."); errorValue != nil {
		t.Fatalf("expected delivery wording not to be classified by transport validation: %v", errorValue)
	}
}
