package memory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSlugifyBasic(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"project deadlines", "project-deadlines"},
		{"Hello World!", "hello-world"},
		{"CamelCase Test", "camelcase-test"},
		{"special @#$ chars", "special-chars"},
		{"  spaces  ", "spaces"},
		{"unicode café résumé", "unicode-caf-r-sum"},
		{"", "untitled"},
		{"---", "untitled"},
		{"a", "a"},
		{"UPPER CASE", "upper-case"},
	}
	for _, testCase := range tests {
		result := Slugify(testCase.input)
		if result != testCase.expected {
			t.Errorf("Slugify(%q) = %q, expected %q", testCase.input, result, testCase.expected)
		}
	}
}

func TestSaveAndLoadMemory(t *testing.T) {
	temporaryDirectory := t.TempDir()
	store := NewStore(temporaryDirectory)
	filePath, err := store.Save("project deadlines", "MVP due March 15")
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if filePath != "short-term-memory/project-deadlines.md" {
		t.Errorf("expected path %q, got %q", "short-term-memory/project-deadlines.md", filePath)
	}
	fullPath := filepath.Join(temporaryDirectory, "short-term-memory", "project-deadlines.md")
	if _, err := os.Stat(fullPath); err != nil {
		t.Fatalf("file not created: %v", err)
	}
	loaded, err := store.Load("short-term", "project-deadlines")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Subject != "project deadlines" {
		t.Errorf("expected subject %q, got %q", "project deadlines", loaded.Subject)
	}
	if loaded.Content != "MVP due March 15" {
		t.Errorf("expected content %q, got %q", "MVP due March 15", loaded.Content)
	}
	if loaded.Storage != "short-term" {
		t.Errorf("expected storage %q, got %q", "short-term", loaded.Storage)
	}
}

func TestSaveUpsertPreservesMetadata(t *testing.T) {
	temporaryDirectory := t.TempDir()
	store := NewStore(temporaryDirectory)
	store.Save("test subject", "original content")
	original, _ := store.Load("short-term", "test-subject")
	originalCreatedAt := original.CreatedAt
	store.Save("test subject", "updated content")
	updated, _ := store.Load("short-term", "test-subject")
	if updated.Content != "updated content" {
		t.Errorf("expected updated content, got %q", updated.Content)
	}
	if !updated.CreatedAt.Equal(originalCreatedAt) {
		t.Error("CreatedAt should be preserved on upsert")
	}
}

func TestLoadNonexistentMemory(t *testing.T) {
	temporaryDirectory := t.TempDir()
	store := NewStore(temporaryDirectory)
	_, err := store.Load("short-term", "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent memory")
	}
}

func TestIncrementRecallCount(t *testing.T) {
	temporaryDirectory := t.TempDir()
	store := NewStore(temporaryDirectory)
	store.Save("test recall", "some content")
	loaded, _ := store.Load("short-term", "test-recall")
	if loaded.RecallCount != 0 {
		t.Fatalf("expected initial recallCount 0, got %d", loaded.RecallCount)
	}
	store.IncrementRecallCount(&loaded)
	reloaded, _ := store.Load("short-term", "test-recall")
	if reloaded.RecallCount != 1 {
		t.Errorf("expected recallCount 1, got %d", reloaded.RecallCount)
	}
	if reloaded.LastRecalledAt.IsZero() {
		t.Error("expected LastRecalledAt to be set after increment")
	}
}

func TestListMemories(t *testing.T) {
	temporaryDirectory := t.TempDir()
	store := NewStore(temporaryDirectory)
	store.Save("first memory", "content one")
	store.Save("second memory", "content two")
	memories, err := store.ListMemories("short-term")
	if err != nil {
		t.Fatalf("ListMemories failed: %v", err)
	}
	if len(memories) != 2 {
		t.Errorf("expected 2 memories, got %d", len(memories))
	}
}

func TestListMemoriesEmptyDirectory(t *testing.T) {
	temporaryDirectory := t.TempDir()
	store := NewStore(temporaryDirectory)
	memories, err := store.ListMemories("short-term")
	if err != nil {
		t.Fatalf("ListMemories failed: %v", err)
	}
	if len(memories) != 0 {
		t.Errorf("expected 0 memories, got %d", len(memories))
	}
}

func TestParseMemoryFileInvalidFrontmatter(t *testing.T) {
	_, err := parseMemoryFile("no frontmatter here")
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
	_, err = parseMemoryFile("---\nno end marker")
	if err == nil {
		t.Fatal("expected error for missing end marker")
	}
}
