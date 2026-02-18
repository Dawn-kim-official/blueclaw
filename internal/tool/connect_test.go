package tool

import (
	"context"
	"testing"

	"github.com/blueclaw/blueclaw/internal/memory"
)

func TestConnectTool_HappyPath(t *testing.T) {
	graphStore := newTestGraphStore(t)
	graphStore.Save("A", "content A", memory.MemoryTypeFact, nil)
	graphStore.Save("B", "content B", memory.MemoryTypeFact, nil)
	tool := NewConnectTool(graphStore)
	result, err := tool.Execute(context.Background(), map[string]any{
		"from_title": "A",
		"to_title":   "B",
		"relation":   "extends",
	})
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

func TestConnectTool_SelfLoop(t *testing.T) {
	graphStore := newTestGraphStore(t)
	graphStore.Save("A", "content", memory.MemoryTypeFact, nil)
	tool := NewConnectTool(graphStore)
	result, err := tool.Execute(context.Background(), map[string]any{
		"from_title": "A",
		"to_title":   "A",
		"relation":   "extends",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Error == "" {
		t.Error("expected error for self-loop")
	}
}

func TestConnectTool_MissingFromTitle(t *testing.T) {
	tool := NewConnectTool(newTestGraphStore(t))
	result, _ := tool.Execute(context.Background(), map[string]any{"to_title": "B", "relation": "extends"})
	if result.Error == "" {
		t.Error("expected error for missing from_title")
	}
}

func TestConnectTool_MissingToTitle(t *testing.T) {
	tool := NewConnectTool(newTestGraphStore(t))
	result, _ := tool.Execute(context.Background(), map[string]any{"from_title": "A", "relation": "extends"})
	if result.Error == "" {
		t.Error("expected error for missing to_title")
	}
}

func TestConnectTool_EmptyRelation(t *testing.T) {
	graphStore := newTestGraphStore(t)
	graphStore.Save("A", "a", memory.MemoryTypeFact, nil)
	graphStore.Save("B", "b", memory.MemoryTypeFact, nil)
	tool := NewConnectTool(graphStore)
	result, _ := tool.Execute(context.Background(), map[string]any{"from_title": "A", "to_title": "B", "relation": ""})
	if result.Error == "" {
		t.Error("expected error for empty relation")
	}
}

func TestConnectTool_FromTitleNotFound(t *testing.T) {
	graphStore := newTestGraphStore(t)
	graphStore.Save("B", "b", memory.MemoryTypeFact, nil)
	tool := NewConnectTool(graphStore)
	result, _ := tool.Execute(context.Background(), map[string]any{"from_title": "nonexistent", "to_title": "B", "relation": "extends"})
	if result.Error == "" {
		t.Error("expected error for missing from_title memory")
	}
}

func TestConnectTool_ToTitleNotFound(t *testing.T) {
	graphStore := newTestGraphStore(t)
	graphStore.Save("A", "a", memory.MemoryTypeFact, nil)
	tool := NewConnectTool(graphStore)
	result, _ := tool.Execute(context.Background(), map[string]any{"from_title": "A", "to_title": "nonexistent", "relation": "extends"})
	if result.Error == "" {
		t.Error("expected error for missing to_title memory")
	}
}

func TestConnectTool_ReplaceDuplicatePair(t *testing.T) {
	graphStore := newTestGraphStore(t)
	graphStore.Save("A", "a", memory.MemoryTypeFact, nil)
	graphStore.Save("B", "b", memory.MemoryTypeFact, nil)
	tool := NewConnectTool(graphStore)
	tool.Execute(context.Background(), map[string]any{"from_title": "A", "to_title": "B", "relation": "extends"})
	result, err := tool.Execute(context.Background(), map[string]any{"from_title": "A", "to_title": "B", "relation": "updates"})
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	if result.Error != "" {
		t.Errorf("unexpected error on replacement: %s", result.Error)
	}
}
