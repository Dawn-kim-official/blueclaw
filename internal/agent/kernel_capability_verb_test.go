package agent

import (
	"encoding/json"
	"testing"
)

func TestCanonicalEvidenceToolNameLeavesNeutralOperationsUntouched(t *testing.T) {
	cases := map[string]string{
		"task.add":         "task.add",
		"message.send":     "message.send",
		"site.create":      "site.create",
		"schedule.create":  "schedule.create",
		"ask.choice":       "ask.input",
		"artifact.deliver": "file.deliver",
		"terminal.session": "terminal.run",
	}
	for inputName, expected := range cases {
		if got := CanonicalEvidenceToolName(inputName); got != expected {
			t.Fatalf("CanonicalEvidenceToolName(%q) = %q, want %q", inputName, got, expected)
		}
	}
}

func TestEffectiveObservationToolNamePreservesDirectToolNames(t *testing.T) {
	if got := effectiveObservationToolName("site.publish", json.RawMessage(`{"siteID":"s1"}`)); got != "site.publish" {
		t.Fatalf("expected direct tool name unchanged, got %q", got)
	}
	if got := effectiveObservationToolName(TerminalRunToolName, json.RawMessage(`{"command":"ls"}`)); got != TerminalRunToolName {
		t.Fatalf("expected terminal tool name unchanged, got %q", got)
	}
}
