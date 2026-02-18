package tool

import (
	"context"
	"testing"
	"time"

	"github.com/blueclaw/blueclaw/internal/memory"
)

func newTestGraphStore(t *testing.T) *memory.GraphStore {
	t.Helper()
	store, err := memory.NewGraphStore(t.TempDir() + "/memory.db")
	if err != nil {
		t.Fatalf("NewGraphStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestRememberTool_MissingTitle(t *testing.T) {
	tool := NewRememberTool(newTestGraphStore(t), nil)
	result, err := tool.Execute(context.Background(), map[string]any{"content": "c", "type": "fact"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected error for missing title")
	}
}

func TestRememberTool_MissingContent(t *testing.T) {
	tool := NewRememberTool(newTestGraphStore(t), nil)
	result, err := tool.Execute(context.Background(), map[string]any{"title": "t", "type": "fact"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected error for missing content")
	}
}

func TestRememberTool_MissingType(t *testing.T) {
	tool := NewRememberTool(newTestGraphStore(t), nil)
	result, err := tool.Execute(context.Background(), map[string]any{"title": "t", "content": "c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected error for missing type")
	}
}

func TestRememberTool_InvalidType(t *testing.T) {
	tool := NewRememberTool(newTestGraphStore(t), nil)
	result, err := tool.Execute(context.Background(), map[string]any{"title": "t", "content": "c", "type": "bogus"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Error("expected error for invalid type")
	}
}

func TestRememberTool_FactHasNoExpiration(t *testing.T) {
	graphStore := newTestGraphStore(t)
	tool := NewRememberTool(graphStore, nil)
	_, err := tool.Execute(context.Background(), map[string]any{"title": "user name", "content": "Lee", "type": "fact"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	m, err := graphStore.Load("user name")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.ExpiresAt != nil {
		t.Errorf("fact should have nil expires_at, got %v", m.ExpiresAt)
	}
	if m.Type != memory.MemoryTypeFact {
		t.Errorf("type: got %q, want %q", m.Type, memory.MemoryTypeFact)
	}
}

func TestRememberTool_EpisodeHasExpiration(t *testing.T) {
	graphStore := newTestGraphStore(t)
	tool := NewRememberTool(graphStore, nil)
	before := time.Now()
	_, err := tool.Execute(context.Background(), map[string]any{"title": "meeting", "content": "Q1 planning", "type": "episode"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	m, err := graphStore.Load("meeting")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.ExpiresAt == nil {
		t.Fatal("episode should have non-nil expires_at")
	}
	expectedMin := before.Add(memory.DefaultEpisodeTTL - time.Minute)
	if m.ExpiresAt.Before(expectedMin) {
		t.Errorf("expires_at %v is earlier than expected minimum %v", m.ExpiresAt, expectedMin)
	}
}

func TestRememberTool_PreferenceHasNoExpiration(t *testing.T) {
	graphStore := newTestGraphStore(t)
	tool := NewRememberTool(graphStore, nil)
	_, err := tool.Execute(context.Background(), map[string]any{"title": "prefers brevity", "content": "keep it short", "type": "preference"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	m, _ := graphStore.Load("prefers brevity")
	if m.ExpiresAt != nil {
		t.Errorf("preference should have nil expires_at, got %v", m.ExpiresAt)
	}
}

func TestRememberTool_DuplicateTitleUpdatesInPlace(t *testing.T) {
	graphStore := newTestGraphStore(t)
	tool := NewRememberTool(graphStore, nil)
	tool.Execute(context.Background(), map[string]any{"title": "name", "content": "first", "type": "fact"})
	tool.Execute(context.Background(), map[string]any{"title": "name", "content": "updated", "type": "fact"})
	m, _ := graphStore.Load("name")
	if m.Content != "updated" {
		t.Errorf("content: got %q, want %q", m.Content, "updated")
	}
}

func TestRememberTool_ReturnsSuccessOutput(t *testing.T) {
	tool := NewRememberTool(newTestGraphStore(t), nil)
	result, err := tool.Execute(context.Background(), map[string]any{"title": "hello", "content": "world", "type": "fact"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
	if result.Output == "" {
		t.Error("expected non-empty output")
	}
}
