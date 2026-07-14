package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSkillLoaderParsesFrontmatterMetadata(t *testing.T) {
	directoryPath := t.TempDir()
	documentPath := filepath.Join(directoryPath, "SKILL.md")
	document := `---
name: presentation
description: Create presentation decks.
license: Apache-2.0
compatibility: Requires a POSIX shell.
metadata:
  author: InternKim
allowed-tools:
  - terminal.run
  - file.write
---
# Simple Slides

Build slides.
`
	if errorValue := os.WriteFile(documentPath, []byte(document), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}

	skillBundle, errorValue := (SkillLoader{}).LoadSkillBundle(directoryPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if skillBundle.Name != "presentation" {
		t.Fatalf("expected name from frontmatter, got %q", skillBundle.Name)
	}
	if skillBundle.Description != "Create presentation decks." {
		t.Fatalf("expected description from frontmatter, got %q", skillBundle.Description)
	}
	if !containsString(skillBundle.AllowedTools, "terminal.run") || !containsString(skillBundle.AllowedTools, "file.write") {
		t.Fatalf("expected allowed tools, got %+v", skillBundle.AllowedTools)
	}
	if skillBundle.WhenToUse != "" || skillBundle.RecommendedMinutes != 0 || len(skillBundle.TriggerHints) != 0 {
		t.Fatalf("expected no nonstandard runtime metadata, got %+v", skillBundle)
	}
	if skillBundle.Instruction != "# Simple Slides\n\nBuild slides." {
		t.Fatalf("expected frontmatter to be stripped, got %q", skillBundle.Instruction)
	}
}

func TestSkillLoaderFallsBackForLegacySkillDocument(t *testing.T) {
	directoryPath := t.TempDir()
	documentPath := filepath.Join(directoryPath, "SKILL.md")
	if errorValue := os.WriteFile(documentPath, []byte("Use this legacy skill."), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}

	skillBundle, errorValue := (SkillLoader{}).LoadSkillBundle(directoryPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if skillBundle.Name != filepath.Base(directoryPath) {
		t.Fatalf("expected directory name fallback, got %q", skillBundle.Name)
	}
	if skillBundle.Instruction != "Use this legacy skill." {
		t.Fatalf("expected legacy body as instruction, got %q", skillBundle.Instruction)
	}
	if skillBundle.Description != "Use this legacy skill." {
		t.Fatalf("expected first paragraph description fallback, got %q", skillBundle.Description)
	}
	if len(skillBundle.TriggerHints) != 0 || len(skillBundle.AllowedTools) != 0 {
		t.Fatalf("expected empty metadata fallback, got %+v", skillBundle)
	}
}

func TestSkillLoaderParsesSpaceSeparatedAllowedTools(t *testing.T) {
	directoryPath := t.TempDir()
	documentPath := filepath.Join(directoryPath, "SKILL.md")
	document := `---
name: file-work
description: Work with files.
allowed-tools: file.read file.write
---
Use files.
`
	if errorValue := os.WriteFile(documentPath, []byte(document), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}

	skillBundle, errorValue := (SkillLoader{}).LoadSkillBundle(directoryPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if !containsString(skillBundle.AllowedTools, "file.read") || !containsString(skillBundle.AllowedTools, "file.write") {
		t.Fatalf("expected space separated allowed tools, got %+v", skillBundle.AllowedTools)
	}
}

func TestSkillLoaderKeepsYamlAllowedToolItemsWhole(t *testing.T) {
	directoryPath := t.TempDir()
	documentPath := filepath.Join(directoryPath, "SKILL.md")
	document := `---
name: git-work
description: Work with git.
allowed-tools:
  - Bash(git *)
---
Use git.
`
	if errorValue := os.WriteFile(documentPath, []byte(document), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}

	skillBundle, errorValue := (SkillLoader{}).LoadSkillBundle(directoryPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if !containsString(skillBundle.AllowedTools, "Bash(git *)") {
		t.Fatalf("expected YAML allowed tool item to stay whole, got %+v", skillBundle.AllowedTools)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
