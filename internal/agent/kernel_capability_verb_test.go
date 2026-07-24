package agent

import (
	"encoding/json"
	"testing"
)

func TestToolNamesMatchRequiresExactCanonicalIdentity(t *testing.T) {
	if !ToolNamesMatch(" file.deliver ", FileDeliverToolName) {
		t.Fatal("expected surrounding whitespace to be ignored")
	}
	for _, legacyToolName := range []string{"ask.choice", "artifact.deliver", "file.attach", "site.promote", "terminal.session"} {
		if ToolNamesMatch(legacyToolName, normalizePersistedToolName(legacyToolName)) {
			t.Fatalf("expected legacy tool %q not to match its canonical replacement", legacyToolName)
		}
	}
}

func TestEffectiveObservationToolNamePreservesDirectToolNames(t *testing.T) {
	if got := effectiveObservationToolName("site.serve", json.RawMessage(`{"siteID":"s1"}`)); got != "site.serve" {
		t.Fatalf("expected direct tool name unchanged, got %q", got)
	}
	if got := effectiveObservationToolName(TerminalRunToolName, json.RawMessage(`{"command":"ls"}`)); got != TerminalRunToolName {
		t.Fatalf("expected terminal tool name unchanged, got %q", got)
	}
}
