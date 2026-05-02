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
name: simple-slides
description: Create presentation decks.
category: document-generation
tags: [slides, pptx]
activation:
  keywords:
    - 피피티
    - pptx
  toolNames: [file.write, file.attach]
requiredTools:
  - terminal.run
allowedProfiles: [default]
triggerHints:
  - slide deck
references:
  - references/design-system.md
scripts:
  - scripts/extract_notes.py
assets:
  - assets/template.md
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

	if skillBundle.Name != "simple-slides" {
		t.Fatalf("expected name from frontmatter, got %q", skillBundle.Name)
	}
	if skillBundle.Description != "Create presentation decks." {
		t.Fatalf("expected description from frontmatter, got %q", skillBundle.Description)
	}
	if skillBundle.Category != "document-generation" {
		t.Fatalf("expected category from frontmatter, got %q", skillBundle.Category)
	}
	if !containsString(skillBundle.Tags, "slides") || !containsString(skillBundle.Tags, "pptx") {
		t.Fatalf("expected tags, got %+v", skillBundle.Tags)
	}
	if !containsString(skillBundle.Activation.Keywords, "피피티") || !containsString(skillBundle.Activation.Keywords, "pptx") {
		t.Fatalf("expected activation keywords, got %+v", skillBundle.Activation.Keywords)
	}
	if !containsString(skillBundle.Activation.ToolNames, "file.write") || !containsString(skillBundle.Activation.ToolNames, "file.attach") {
		t.Fatalf("expected activation tool names, got %+v", skillBundle.Activation.ToolNames)
	}
	if !containsString(skillBundle.RequiredTools, "terminal.run") {
		t.Fatalf("expected required tools, got %+v", skillBundle.RequiredTools)
	}
	if !containsString(skillBundle.AllowedProfiles, "default") {
		t.Fatalf("expected allowed profiles, got %+v", skillBundle.AllowedProfiles)
	}
	if !containsString(skillBundle.TriggerHints, "slide deck") {
		t.Fatalf("expected trigger hints, got %+v", skillBundle.TriggerHints)
	}
	if !containsString(skillBundle.References, "references/design-system.md") {
		t.Fatalf("expected references, got %+v", skillBundle.References)
	}
	if !containsString(skillBundle.Scripts, "scripts/extract_notes.py") {
		t.Fatalf("expected scripts, got %+v", skillBundle.Scripts)
	}
	if !containsString(skillBundle.Assets, "assets/template.md") {
		t.Fatalf("expected assets, got %+v", skillBundle.Assets)
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
	if len(skillBundle.TriggerHints) != 0 || len(skillBundle.RequiredTools) != 0 {
		t.Fatalf("expected empty metadata fallback, got %+v", skillBundle)
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
