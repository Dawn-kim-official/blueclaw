package agent

import "testing"

func TestFinalReplyClaimsAttachmentDeliveryUsesPositiveClaims(t *testing.T) {
	claimingReplies := []string{
		"요청하신 파일을 생성해 첨부했습니다.",
		"아래 파일을 확인해 주세요.",
		"I attached the file.",
		"The PDF has been delivered.",
	}
	for _, reply := range claimingReplies {
		if !FinalReplyClaimsAttachmentDelivery(reply) {
			t.Fatalf("expected attachment delivery claim for %q", reply)
		}
	}

	nonClaimingReplies := []string{
		"파일을 보냈다고 말할 수는 없어요.",
		"첨부 근거가 없어서 완료됐다고 말할 수는 없어요.",
		"I cannot honestly say it was delivered.",
		"The file creation needs to be tried again.",
	}
	for _, reply := range nonClaimingReplies {
		if FinalReplyClaimsAttachmentDelivery(reply) {
			t.Fatalf("expected no attachment delivery claim for %q", reply)
		}
	}
}
