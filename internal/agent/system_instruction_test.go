package agent

import (
	"strings"
	"testing"
)

func TestCapabilityDomainPhraseDerivesFriendlyLabelsFromSkillTools(t *testing.T) {
	skills := []SkillInstruction{
		{Name: "direct-message", AllowedTools: []string{"message.send", "message.context"}},
		{Name: "flow", AllowedTools: []string{"task.list", "task.add"}},
		{Name: "scheduling", AllowedTools: []string{"schedule.create"}},
		{Name: "future", AllowedTools: []string{"hologram.project"}},
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
