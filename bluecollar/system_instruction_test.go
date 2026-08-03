package bluecollar

import (
	"strings"
	"testing"
)

func TestCapabilityDomainPhraseDerivesFriendlyLabelsFromSkillTools(t *testing.T) {
	skills := []SkillInstruction{
		{Name: "direct-message", ToolReferences: []string{"message_send", "message_context"}},
		{Name: "flow", ToolReferences: []string{"task_list", "task_add"}},
		{Name: "scheduling", ToolReferences: []string{"schedule_create"}},
		{Name: "future", ToolReferences: []string{"hologram.project"}},
	}

	phrase := capabilityDomainPhrase(skills)

	for _, expected := range []string{"messages", "tasks", "schedules", "hologram"} {
		if !strings.Contains(phrase, expected) {
			t.Fatalf("expected phrase to include %q, got %q", expected, phrase)
		}
	}
}

func TestCapabilityDomainPhraseEmptyWhenNoSkills(t *testing.T) {
	if phrase := capabilityDomainPhrase(nil); phrase != "" {
		t.Fatalf("expected empty phrase, got %q", phrase)
	}
}
