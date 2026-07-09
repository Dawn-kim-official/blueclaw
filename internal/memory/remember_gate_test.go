package memory

import (
	"strings"
	"testing"
)

func TestRememberContentGateAcceptsDurableFact(t *testing.T) {
	if gateMessage := RememberContentGateMessage("The user prefers terse Korean release notes."); gateMessage != "" {
		t.Fatalf("expected durable fact to pass, got %q", gateMessage)
	}
}

func TestRememberContentGateRejectsEmptyContent(t *testing.T) {
	if gateMessage := RememberContentGateMessage("   "); gateMessage == "" {
		t.Fatal("expected empty content to be rejected")
	}
}

func TestRememberContentGateRejectsTransientChatter(t *testing.T) {
	for _, transientContent := range []string{"ok", "thanks", "고마워", "ㅋㅋ", "ㅇㅋ", "감사합니다", "안녕하세요"} {
		gateMessage := RememberContentGateMessage(transientContent)
		if !strings.Contains(gateMessage, "transient") {
			t.Fatalf("expected transient rejection for %q, got %q", transientContent, gateMessage)
		}
	}
}

func TestRememberContentGateRejectsOversizedContent(t *testing.T) {
	oversizedContent := strings.Repeat("가", RememberContentRuneLimit+1)
	gateMessage := RememberContentGateMessage(oversizedContent)
	if !strings.Contains(gateMessage, "compact standalone fact") {
		t.Fatalf("expected oversized rejection, got %q", gateMessage)
	}
}

func TestRememberContentGateAcceptsContentAtRuneLimit(t *testing.T) {
	limitContent := strings.Repeat("가", RememberContentRuneLimit)
	if gateMessage := RememberContentGateMessage(limitContent); gateMessage != "" {
		t.Fatalf("expected content at limit to pass, got %q", gateMessage)
	}
}
