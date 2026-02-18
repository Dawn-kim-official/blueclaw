package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPromoteEligibleMemory(t *testing.T) {
	temporaryDirectory := t.TempDir()
	store := NewStore(temporaryDirectory)
	store.Save("promote me", "important content")
	databasePath := filepath.Join(temporaryDirectory, "test.db")
	index, err := NewSearchIndex(databasePath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()
	index.Upsert("promote me", "short-term-memory/promote-me.md", "short-term", makeTestEmbedding(0.5))
	loaded, _ := store.Load("short-term", "promote-me")
	loaded.RecallCount = 3
	if err := PromoteIfEligible(store, index, &loaded); err != nil {
		t.Fatalf("PromoteIfEligible failed: %v", err)
	}
	if loaded.Storage != "long-term" {
		t.Errorf("expected storage %q, got %q", "long-term", loaded.Storage)
	}
	longTermPath := filepath.Join(temporaryDirectory, "long-term-memory", "promote-me.md")
	if _, err := os.Stat(longTermPath); err != nil {
		t.Errorf("long-term file not created: %v", err)
	}
	shortTermPath := filepath.Join(temporaryDirectory, "short-term-memory", "promote-me.md")
	if _, err := os.Stat(shortTermPath); !os.IsNotExist(err) {
		t.Error("short-term file should be removed after promotion")
	}
}

func TestPromoteSkipsIneligible(t *testing.T) {
	temporaryDirectory := t.TempDir()
	store := NewStore(temporaryDirectory)
	store.Save("not ready", "some content")
	databasePath := filepath.Join(temporaryDirectory, "test.db")
	index, err := NewSearchIndex(databasePath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()
	loaded, _ := store.Load("short-term", "not-ready")
	loaded.RecallCount = 2
	if err := PromoteIfEligible(store, index, &loaded); err != nil {
		t.Fatalf("PromoteIfEligible failed: %v", err)
	}
	if loaded.Storage != "short-term" {
		t.Errorf("expected storage to remain %q, got %q", "short-term", loaded.Storage)
	}
}

func TestCleanupExpiredMemories(t *testing.T) {
	temporaryDirectory := t.TempDir()
	store := NewStore(temporaryDirectory)
	store.Save("old memory", "stale content")
	store.Save("new memory", "fresh content")
	oldSlug := Slugify("old memory")
	oldPath := filepath.Join(temporaryDirectory, "short-term-memory", oldSlug+".md")
	oldMemory, _ := store.Load("short-term", oldSlug)
	oldMemory.CreatedAt = time.Now().Add(-8 * 24 * time.Hour)
	store.writeMemoryFile(oldPath, oldMemory)
	databasePath := filepath.Join(temporaryDirectory, "test.db")
	index, err := NewSearchIndex(databasePath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()
	if err := CleanupExpiredMemories(store, index); err != nil {
		t.Fatalf("CleanupExpiredMemories failed: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Error("old memory should be cleaned up")
	}
	newPath := filepath.Join(temporaryDirectory, "short-term-memory", Slugify("new memory")+".md")
	if _, err := os.Stat(newPath); err != nil {
		t.Error("new memory should be preserved")
	}
}

func TestCleanupPreservesHighRecallCount(t *testing.T) {
	temporaryDirectory := t.TempDir()
	store := NewStore(temporaryDirectory)
	store.Save("recalled memory", "important content")
	slug := Slugify("recalled memory")
	filePath := filepath.Join(temporaryDirectory, "short-term-memory", slug+".md")
	loaded, _ := store.Load("short-term", slug)
	loaded.CreatedAt = time.Now().Add(-8 * 24 * time.Hour)
	loaded.RecallCount = 3
	store.writeMemoryFile(filePath, loaded)
	databasePath := filepath.Join(temporaryDirectory, "test.db")
	index, err := NewSearchIndex(databasePath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	defer index.Close()
	CleanupExpiredMemories(store, index)
	if _, err := os.Stat(filePath); err != nil {
		t.Error("high recall count memory should be preserved")
	}
}
