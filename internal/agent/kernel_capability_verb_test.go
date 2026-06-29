package agent

import (
	"encoding/json"
	"testing"
)

func TestKernelToolNamesIncludeCapabilityInvoke(t *testing.T) {
	found := false
	for _, name := range KernelToolNames() {
		if name == CapabilityInvokeToolName {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected capability.invoke in kernel palette, got %v", KernelToolNames())
	}
}

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

func TestEffectiveObservationToolNameReturnsCapabilityInvokeOperationVerbatim(t *testing.T) {
	resolved := effectiveObservationToolName(CapabilityInvokeToolName, json.RawMessage(`{"operation":"site.publish","input":{"siteID":"s1"}}`))
	if resolved != "site.publish" {
		t.Fatalf("expected neutral operation site.publish, got %q", resolved)
	}
	if got := effectiveObservationToolName(TerminalRunToolName, json.RawMessage(`{"command":"ls"}`)); got != TerminalRunToolName {
		t.Fatalf("expected non-verb tool name unchanged, got %q", got)
	}
	if got := effectiveObservationToolName(CapabilityInvokeToolName, json.RawMessage(`{}`)); got != CapabilityInvokeToolName {
		t.Fatalf("expected verb name kept when operation missing, got %q", got)
	}
}
