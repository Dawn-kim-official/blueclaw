package e2e

import (
	"os"
	"path/filepath"
	"runtime"
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
	if _, errorValue := os.Stat(skillAssetsRootPath); os.IsNotExist(errorValue) {
		t.Skipf("parent InternKim checkout is absent; expected bundled skill assets at %s", skillAssetsRootPath)
	} else if errorValue != nil {
		t.Fatalf("expected bundled skill asset directory %s: %v", skillAssetsRootPath, errorValue)
	}
	return skillAssetsRootPath
}
