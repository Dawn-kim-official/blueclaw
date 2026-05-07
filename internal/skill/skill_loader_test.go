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
when_to_use: Use for 피피티, PowerPoint, and slide deck requests.
category: document-generation
tags: [slides, pptx]
activation:
  keywords:
    - 피피티
    - pptx
  toolNames: [file.write, file.attach]
allowed-tools:
  - terminal.run
  - file.write
disable-model-invocation: true
paths:
  - "*.pptx"
completion:
  requiredEvidenceTools:
    - file.attach
  requiredAttachmentSuffixes:
    - .pptx
quality:
  acceptanceGuidance:
    - preserve original request
    - verify artifact evidence
  rubric:
    - pass declared criteria before final reply
allowedProfiles: [default]
hiddenFromCircles: [staff]
triggerHints:
  - slide deck
references:
  - references/example.md
scripts:
  - scripts/extract_notes.py
assets:
  - assets/design.md
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
	if skillBundle.WhenToUse != "Use for 피피티, PowerPoint, and slide deck requests." {
		t.Fatalf("expected when_to_use from frontmatter, got %q", skillBundle.WhenToUse)
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
	if !containsString(skillBundle.AllowedTools, "terminal.run") || !containsString(skillBundle.AllowedTools, "file.write") {
		t.Fatalf("expected allowed tools, got %+v", skillBundle.AllowedTools)
	}
	if !skillBundle.DisableModelInvocation {
		t.Fatal("expected disable model invocation")
	}
	if !containsString(skillBundle.Paths, "*.pptx") {
		t.Fatalf("expected paths, got %+v", skillBundle.Paths)
	}
	if !containsString(skillBundle.Completion.RequiredEvidenceTools, "file.attach") {
		t.Fatalf("expected completion evidence tools, got %+v", skillBundle.Completion.RequiredEvidenceTools)
	}
	if !containsString(skillBundle.Completion.RequiredAttachmentSuffixes, ".pptx") {
		t.Fatalf("expected completion attachment suffixes, got %+v", skillBundle.Completion.RequiredAttachmentSuffixes)
	}
	if !containsString(skillBundle.Quality.AcceptanceGuidance, "preserve original request") || !containsString(skillBundle.Quality.AcceptanceGuidance, "verify artifact evidence") {
		t.Fatalf("expected quality acceptance guidance, got %+v", skillBundle.Quality.AcceptanceGuidance)
	}
	if !containsString(skillBundle.Quality.Rubric, "pass declared criteria before final reply") {
		t.Fatalf("expected quality rubric, got %+v", skillBundle.Quality.Rubric)
	}
	if !containsString(skillBundle.AllowedProfiles, "default") {
		t.Fatalf("expected allowed profiles, got %+v", skillBundle.AllowedProfiles)
	}
	if !containsString(skillBundle.HiddenFromCircles, "staff") {
		t.Fatalf("expected hidden circles, got %+v", skillBundle.HiddenFromCircles)
	}
	if !containsString(skillBundle.TriggerHints, "slide deck") {
		t.Fatalf("expected trigger hints, got %+v", skillBundle.TriggerHints)
	}
	if !containsString(skillBundle.References, "references/example.md") {
		t.Fatalf("expected references, got %+v", skillBundle.References)
	}
	if !containsString(skillBundle.Scripts, "scripts/extract_notes.py") {
		t.Fatalf("expected scripts, got %+v", skillBundle.Scripts)
	}
	if !containsString(skillBundle.Assets, "assets/design.md") {
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
