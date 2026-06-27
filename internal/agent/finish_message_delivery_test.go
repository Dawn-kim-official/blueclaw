package agent

import "testing"

func TestValidateUserNoticeDeliveryAllowsFailedArtifactNotice(t *testing.T) {
	if errorValue := ValidateUserNoticeDelivery("PPTX를 만들지 못했습니다. 다시 시도해 주세요."); errorValue != nil {
		t.Fatalf("expected failed artifact notice to be allowed: %v", errorValue)
	}
}

func TestValidateUserNoticeDeliveryRejectsNonDeliverableLocator(t *testing.T) {
	if errorValue := ValidateUserNoticeDelivery("작업 결과는 sandbox:/mnt/data/deck.pptx에 있습니다."); errorValue == nil {
		t.Fatal("expected non-deliverable locator to be rejected")
	}
}
