package agent

import "testing"

func TestFinishMessageClaimsAttachmentDeliveryUsesPositiveClaims(t *testing.T) {
	claimingReplies := []string{
		"요청하신 파일을 생성해 첨부했습니다.",
		"아래 파일을 확인해 주세요.",
		"I attached the file.",
		"The PDF has been delivered.",
	}
	for _, reply := range claimingReplies {
		if !FinishMessageClaimsAttachmentDelivery(reply) {
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
		if FinishMessageClaimsAttachmentDelivery(reply) {
			t.Fatalf("expected no attachment delivery claim for %q", reply)
		}
	}
}

func TestValidateUserNoticeDeliveryAllowsFailedArtifactNotice(t *testing.T) {
	if errorValue := ValidateUserNoticeDelivery("PPTX를 만들지 못했습니다. 다시 시도해 주세요."); errorValue != nil {
		t.Fatalf("expected failed artifact notice to be allowed: %v", errorValue)
	}
}

func TestValidateUserNoticeDeliveryRejectsUnavailableAttachmentClaim(t *testing.T) {
	if errorValue := ValidateUserNoticeDelivery("요청하신 파일을 생성해 첨부했습니다."); errorValue == nil {
		t.Fatal("expected unavailable attachment claim to be rejected")
	}
}

func TestValidateUserNoticeDeliveryRejectsNonDeliverableLocator(t *testing.T) {
	if errorValue := ValidateUserNoticeDelivery("작업 결과는 sandbox:/mnt/data/deck.pptx에 있습니다."); errorValue == nil {
		t.Fatal("expected non-deliverable locator to be rejected")
	}
}
