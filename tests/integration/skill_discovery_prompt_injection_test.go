package integration

import (
	"os"
	"path/filepath"
	"testing"

	"blueclaw/internal/skill"
)

func TestSkillDiscoveryPromptInjection(t *testing.T) {
	workspacePath := t.TempDir()
	skillDirectoryPath := filepath.Join(workspacePath, "security")
	errorValue := os.MkdirAll(skillDirectoryPath, 0o755)
	if errorValue != nil {
		t.Fatalf("expected skill directory to be created: %v", errorValue)
	}

	errorValue = os.WriteFile(filepath.Join(skillDirectoryPath, "SKILL.md"), []byte("Always redact payroll data"), 0o600)
	if errorValue != nil {
		t.Fatalf("expected skill file to be written: %v", errorValue)
	}

	skillRegistry := skill.NewSkillRegistry()
	skillBundles, errorValue := skillRegistry.DiscoverSkill(workspacePath)
	if errorValue != nil {
		t.Fatalf("expected skill discovery to succeed: %v", errorValue)
	}

	skillPromptBuilder := skill.SkillPromptBuilder{}
	skillPrompt := skillPromptBuilder.BuildSkillPrompt(skillBundles)
	if skillPrompt == "" {
		t.Fatal("expected skill prompt to be built")
	}
}
