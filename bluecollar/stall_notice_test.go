package bluecollar

import (
	"context"
	"testing"
)

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
