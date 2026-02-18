package tool

import (
	"context"
	"testing"
)

type mockTool struct {
	name        string
	description string
}

func (mock *mockTool) Name() string                    { return mock.name }
func (mock *mockTool) Description() string             { return mock.description }
func (mock *mockTool) ParameterSchema() map[string]any { return map[string]any{"type": "object"} }
func (mock *mockTool) Execute(_ context.Context, _ map[string]any) (Result, error) {
	return Result{Output: "executed"}, nil
}

func TestRegistryRegisterAndGet(t *testing.T) {
	registry := NewRegistry()
	tool := &mockTool{name: "remember", description: "Save a memory"}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("unexpected error registering tool: %v", err)
	}
	retrieved, err := registry.Get("remember")
	if err != nil {
		t.Fatalf("unexpected error getting tool: %v", err)
	}
	if retrieved.Name() != "remember" {
		t.Errorf("expected tool name %q, got %q", "remember", retrieved.Name())
	}
}

func TestRegistryDuplicateNameReturnsError(t *testing.T) {
	registry := NewRegistry()
	tool := &mockTool{name: "remember", description: "Save a memory"}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("unexpected error on first register: %v", err)
	}
	duplicate := &mockTool{name: "remember", description: "Duplicate"}
	if err := registry.Register(duplicate); err == nil {
		t.Fatal("expected error for duplicate registration, got nil")
	}
}

func TestRegistryGetUnknownReturnsError(t *testing.T) {
	registry := NewRegistry()
	_, err := registry.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown tool, got nil")
	}
}

func TestRegistryListDefinitions(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(&mockTool{name: "remember", description: "Save"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&mockTool{name: "recall", description: "Search"}); err != nil {
		t.Fatal(err)
	}
	definitions := registry.ListDefinitions()
	if len(definitions) != 2 {
		t.Errorf("expected 2 definitions, got %d", len(definitions))
	}
	names := make(map[string]bool)
	for _, definition := range definitions {
		names[definition.Name] = true
	}
	if !names["remember"] || !names["recall"] {
		t.Errorf("expected remember and recall in definitions, got %v", names)
	}
}

func TestRegistryListDefinitionsEmpty(t *testing.T) {
	registry := NewRegistry()
	definitions := registry.ListDefinitions()
	if len(definitions) != 0 {
		t.Errorf("expected 0 definitions for empty registry, got %d", len(definitions))
	}
}
