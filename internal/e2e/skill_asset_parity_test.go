package e2e

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/skill"
)

func TestScenarioSkillAllowedToolsAreBackedByBundledAssets(t *testing.T) {
	skillAssetsRootPath := parentInternKimSkillAssetsRootPath(t)
	skillLoader := skill.SkillLoader{}
	for _, scenarioSkill := range scenarioSkillFixtures() {
		t.Run(scenarioSkill.Source.SkillName, func(t *testing.T) {
			skillDirectoryPath := filepath.Join(skillAssetsRootPath, scenarioSkill.Source.SkillName)
			assetSkill, errorValue := skillLoader.LoadSkillBundle(skillDirectoryPath)
			if errorValue != nil {
				t.Fatalf("expected bundled skill asset for simulator skill %q at %s: %v", scenarioSkill.Source.SkillName, skillDirectoryPath, errorValue)
			}
			missingToolNames := missingAllowedToolNames(assetSkill.AllowedTools, scenarioSkill.AllowedTools)
			if len(missingToolNames) > 0 {
				t.Errorf("bundled skill %q allowed-tools missing simulator tools %v; asset declares %v", scenarioSkill.Source.SkillName, missingToolNames, assetSkill.AllowedTools)
			}
		})
	}
}

func TestScenarioSkillActivationTermsAreBackedByBundledAssets(t *testing.T) {
	skillAssetsRootPath := parentInternKimSkillAssetsRootPath(t)
	skillLoader := skill.SkillLoader{}
	for _, scenarioSkill := range scenarioSkillFixtures() {
		t.Run(scenarioSkill.Source.SkillName, func(t *testing.T) {
			skillDirectoryPath := filepath.Join(skillAssetsRootPath, scenarioSkill.Source.SkillName)
			assetSkill, errorValue := skillLoader.LoadSkillBundle(skillDirectoryPath)
			if errorValue != nil {
				t.Fatalf("expected bundled skill asset for simulator skill %q at %s: %v", scenarioSkill.Source.SkillName, skillDirectoryPath, errorValue)
			}
			activationSurface := skillActivationSurface(assetSkill)
			fixtureActivationTerms := append(append([]string{}, scenarioSkill.Activation.Keywords...), scenarioSkill.TriggerHints...)
			missingTerms := missingActivationTerms(activationSurface, fixtureActivationTerms)
			if len(missingTerms) > 0 {
				t.Errorf("bundled skill %q activation surface missing simulator activation terms %v", scenarioSkill.Source.SkillName, missingTerms)
			}
		})
	}
}

func skillActivationSurface(assetSkill skill.SkillBundle) string {
	parts := []string{
		assetSkill.Name,
		assetSkill.Description,
		assetSkill.WhenToUse,
		assetSkill.Category,
		strings.Join(assetSkill.Tags, " "),
		strings.Join(assetSkill.Activation.Keywords, " "),
		strings.Join(assetSkill.Activation.ToolNames, " "),
		strings.Join(assetSkill.Activation.ToolPrefixes, " "),
		strings.Join(assetSkill.TriggerHints, " "),
		strings.Join(assetSkill.AllowedTools, " "),
		strings.Join(assetSkill.Completion.RequiredEvidenceTools, " "),
	}
	return strings.ToLower(strings.Join(parts, " "))
}

func missingActivationTerms(activationSurface string, terms []string) []string {
	missingTerms := []string{}
	for _, term := range terms {
		normalizedTerm := strings.ToLower(strings.TrimSpace(term))
		if normalizedTerm == "" || strings.Contains(activationSurface, normalizedTerm) {
			continue
		}
		missingTerms = append(missingTerms, term)
	}
	return missingTerms
}

func scenarioSkillFixtures() []agent.SkillInstruction {
	return []agent.SkillInstruction{
		flowTaskSkill(),
		calendarSkill(),
		scheduledTaskSkill(),
		directMessageSkill(),
		mattermostSkill(),
		presentationSkill(),
		websiteSkill(),
		websiteManagementSkill(),
	}
}

func missingAllowedToolNames(assetToolNames []string, scenarioToolNames []string) []string {
	assetToolNameByName := map[string]bool{}
	for _, assetToolName := range assetToolNames {
		assetToolNameByName[assetToolName] = true
	}
	missingToolNames := []string{}
	for _, scenarioToolName := range scenarioToolNames {
		if !assetToolNameByName[scenarioToolName] {
			missingToolNames = append(missingToolNames, scenarioToolName)
		}
	}
	return missingToolNames
}

func parentInternKimSkillAssetsRootPath(t *testing.T) string {
	t.Helper()
	_, sourceFilename, _, isAvailable := runtime.Caller(0)
	if !isAvailable {
		t.Fatal("expected runtime caller for bundled asset path")
	}
	internKimRootPath := filepath.Clean(filepath.Join(filepath.Dir(sourceFilename), "..", "..", "..", ".."))
	skillAssetsRootPath := filepath.Join(internKimRootPath, "assets", "blueclaw-workspace", "skills")
	if _, errorValue := os.Stat(skillAssetsRootPath); errorValue != nil {
		t.Fatalf("parity requires the parent InternKim checkout; expected bundled skill assets at %s: %v", skillAssetsRootPath, errorValue)
	}
	return skillAssetsRootPath
}
