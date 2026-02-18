package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	PromotionThreshold    = 3
	ShortTermTTL          = 7 * 24 * time.Hour
)

func PromoteIfEligible(store *Store, searchIndex *SearchIndex, memory *Memory) error {
	if memory.Storage != "short-term" || memory.RecallCount < PromotionThreshold {
		return nil
	}
	slug := Slugify(memory.Subject)
	sourceDirectory := filepath.Join(store.blueclawDirectory, "short-term-memory")
	destinationDirectory := filepath.Join(store.blueclawDirectory, "long-term-memory")
	if err := os.MkdirAll(destinationDirectory, 0755); err != nil {
		return fmt.Errorf("creating long-term memory directory: %w", err)
	}
	sourcePath := filepath.Join(sourceDirectory, slug+".md")
	destinationPath := filepath.Join(destinationDirectory, slug+".md")
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("reading short-term memory for promotion: %w", err)
	}
	if err := os.WriteFile(destinationPath, data, 0644); err != nil {
		return fmt.Errorf("writing long-term memory: %w", err)
	}
	if err := os.Remove(sourcePath); err != nil {
		return fmt.Errorf("removing short-term memory after promotion: %w", err)
	}
	memory.Storage = "long-term"
	newFilePath := filepath.Join("long-term-memory", slug+".md")
	if err := searchIndex.UpdateStorage(memory.Subject, "long-term", newFilePath); err != nil {
		return fmt.Errorf("updating search index after promotion: %w", err)
	}
	return store.writeMemoryFile(destinationPath, *memory)
}

func CleanupExpiredMemories(store *Store, searchIndex *SearchIndex) error {
	memories, err := store.ListMemories("short-term")
	if err != nil {
		return fmt.Errorf("listing short-term memories: %w", err)
	}
	now := time.Now()
	for _, memory := range memories {
		age := now.Sub(memory.CreatedAt)
		if age <= ShortTermTTL || memory.RecallCount >= PromotionThreshold {
			continue
		}
		slug := Slugify(memory.Subject)
		filePath := filepath.Join(store.blueclawDirectory, "short-term-memory", slug+".md")
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing expired memory %s: %w", memory.Subject, err)
		}
	}
	return nil
}
