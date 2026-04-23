package skill

import (
	"os"
	"path/filepath"
)

type SkillLoader struct{}

func (skillLoader SkillLoader) LoadSkillBundle(directoryPath string) (SkillBundle, error) {
	documentPath := filepath.Join(directoryPath, "SKILL.md")
	document, errorValue := os.ReadFile(documentPath)
	if errorValue != nil {
		return SkillBundle{}, errorValue
	}

	return SkillBundle{
		Name:          filepath.Base(directoryPath),
		Instruction:   string(document),
		DirectoryPath: directoryPath,
	}, nil
}
