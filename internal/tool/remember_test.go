package tool

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/blueclaw/blueclaw/internal/memory"
)

func setupMemoryTestDeps(t *testing.T) (*memory.Store, *memory.SearchIndex, *memory.EmbeddingClient) {
	t.Helper()
	temporaryDirectory := t.TempDir()
	store := memory.NewStore(temporaryDirectory)
	databasePath := filepath.Join(temporaryDirectory, "test.db")
	index, err := memory.NewSearchIndex(databasePath)
	if err != nil {
		t.Fatalf("NewSearchIndex failed: %v", err)
	}
	t.Cleanup(func() { index.Close() })
	embedding := memory.NewEmbeddingClient(0)
	return store, index, embedding
}

func TestRememberToolSavesMemory(t *testing.T) {
	store, index, embedding := setupMemoryTestDeps(t)
	rememberTool := NewRememberTool(store, index, embedding)
	result, err := rememberTool.Execute(context.Background(), map[string]any{
		"subject": "test topic",
		"content": "test content",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
	if result.Output == "" {
		t.Error("expected non-empty output")
	}
}

func TestRememberToolMissingSubject(t *testing.T) {
	store, index, embedding := setupMemoryTestDeps(t)
	rememberTool := NewRememberTool(store, index, embedding)
	result, err := rememberTool.Execute(context.Background(), map[string]any{
		"content": "test content",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Error == "" {
		t.Error("expected validation error for missing subject")
	}
}

func TestRememberToolMissingContent(t *testing.T) {
	store, index, embedding := setupMemoryTestDeps(t)
	rememberTool := NewRememberTool(store, index, embedding)
	result, err := rememberTool.Execute(context.Background(), map[string]any{
		"subject": "test",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Error == "" {
		t.Error("expected validation error for missing content")
	}
}

func TestRecallToolWithNoMatches(t *testing.T) {
	store, index, embedding := setupMemoryTestDeps(t)
	recallTool := NewRecallTool(store, index, embedding, 5)
	result, err := recallTool.Execute(context.Background(), map[string]any{
		"query": "something random",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
}

func TestRecallToolMissingQuery(t *testing.T) {
	store, index, embedding := setupMemoryTestDeps(t)
	recallTool := NewRecallTool(store, index, embedding, 5)
	result, err := recallTool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Error == "" {
		t.Error("expected validation error for missing query")
	}
}

func TestRememberAndRecallRoundTrip(t *testing.T) {
	store, index, embedding := setupMemoryTestDeps(t)
	rememberTool := NewRememberTool(store, index, embedding)
	recallTool := NewRecallTool(store, index, embedding, 5)
	_, err := rememberTool.Execute(context.Background(), map[string]any{
		"subject": "project deadline",
		"content": "MVP is due March 15",
	})
	if err != nil {
		t.Fatalf("Remember failed: %v", err)
	}
	result, err := recallTool.Execute(context.Background(), map[string]any{
		"query": "when is the MVP due",
	})
	if err != nil {
		t.Fatalf("Recall failed: %v", err)
	}
	if result.Output == "" {
		t.Error("expected recall to return results")
	}
}
