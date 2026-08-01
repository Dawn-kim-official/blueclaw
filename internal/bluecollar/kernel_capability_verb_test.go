package bluecollar

import (
	"encoding/json"
	"github.com/Dawn-kim-official/blueclaw/internal/toolcontract"
	"testing"
)

func TestToolNamesMatchRequiresExactCanonicalIdentity(t *testing.T) {
	if !toolcontract.ToolNamesMatch(" file_deliver ", toolcontract.FileDeliverToolName) {
		t.Fatal("expected surrounding whitespace to be ignored")
	}
	for _, legacyToolName := range []string{"ask_choice", "artifact.deliver", "file.attach", "site.promote", "terminal.session"} {
		if toolcontract.ToolNamesMatch(legacyToolName, normalizePersistedToolName(legacyToolName)) {
			t.Fatalf("expected legacy tool %q not to match its canonical replacement", legacyToolName)
		}
	}
}

func TestEffectiveObservationToolNamePreservesDirectToolNames(t *testing.T) {
	if got := effectiveObservationToolName("site_serve", json.RawMessage(`{"siteID":"s1"}`)); got != "site_serve" {
		t.Fatalf("expected direct tool name unchanged, got %q", got)
	}
	if got := effectiveObservationToolName(toolcontract.TerminalRunToolName, json.RawMessage(`{"command":"ls"}`)); got != toolcontract.TerminalRunToolName {
		t.Fatalf("expected terminal tool name unchanged, got %q", got)
	}
}
