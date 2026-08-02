package bluecollar

import (
	"context"
	"strings"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/model"
)

type fixedReplyLanguageModel struct {
	reply string
}

func (languageModel fixedReplyLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return languageModel.reply, nil
}

func (languageModel fixedReplyLanguageModel) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	return model.StructuredResponse{}, nil
}

func TestFailureNoticeFallsBackWhenChatReviewIsUnavailable(t *testing.T) {
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
		t.Fatalf("expected raw_error without Chat review, got %q (reason %q)", status.Source, status.Reason)
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

func TestFailureNoticeChatReviewRewritesFalseDeliveryClaim(t *testing.T) {
	generator := FailureNoticeGenerator{LanguageModel: &recoveryChatNoticeProvider{
		chatReplies: []string{
			"슬라이드 덱을 첨부했습니다. 확인해 주세요.",
			"슬라이드 덱을 첨부하지 못했습니다. 다시 시도해 주세요.",
		},
	}}

	notice, status := generator.Generate(context.Background(), FailureReport{
		Phase:            "stall",
		StopReason:       "required artifact completion lacked file_deliver evidence",
		ResponseLanguage: "ko",
		ArtifactRequired: true,
		HasAttachments:   false,
	})

	if status.Source != "generated_review" || notice.Source != "generated_review" {
		t.Fatalf("expected Chat review rewrite, got notice=%+v status=%+v", notice, status)
	}
	if notice.SendableMessage() == "슬라이드 덱을 첨부했습니다. 확인해 주세요." {
		t.Fatal("expected false delivery claim to be rewritten through Chat review")
	}
	if !strings.Contains(notice.SendableMessage(), "첨부하지 못했습니다") {
		t.Fatalf("expected reviewed failure notice, got %q", notice.SendableMessage())
	}
}
