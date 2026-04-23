package skill

import (
	"os"
	"path/filepath"
	"sync"
)

type SkillRegistry struct {
	mutex        sync.RWMutex
	skillBundles map[string]SkillBundle
	skillLoader  SkillLoader
}

func NewSkillRegistry() *SkillRegistry {
	return &SkillRegistry{
		skillBundles: map[string]SkillBundle{},
		skillLoader:  SkillLoader{},
	}
}

func (skillRegistry *SkillRegistry) DiscoverSkill(rootPath string) ([]SkillBundle, error) {
	entries, errorValue := os.ReadDir(rootPath)
	if errorValue != nil {
		return nil, errorValue
	}

	discoveredSkillBundles := []SkillBundle{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillBundle, innerErrorValue := skillRegistry.skillLoader.LoadSkillBundle(filepath.Join(rootPath, entry.Name()))
		if innerErrorValue != nil {
			continue
		}

		skillRegistry.mutex.Lock()
		skillRegistry.skillBundles[skillBundle.Name] = skillBundle
		skillRegistry.mutex.Unlock()

		discoveredSkillBundles = append(discoveredSkillBundles, skillBundle)
	}

	return discoveredSkillBundles, nil
}

func (skillRegistry *SkillRegistry) ListSkillBundle() []SkillBundle {
	skillRegistry.mutex.RLock()
	defer skillRegistry.mutex.RUnlock()

	skillBundles := make([]SkillBundle, 0, len(skillRegistry.skillBundles))
	for _, skillBundle := range skillRegistry.skillBundles {
		skillBundles = append(skillBundles, skillBundle)
	}

	return skillBundles
}
