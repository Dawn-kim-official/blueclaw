package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueclaw/blueclaw/internal/provider"
	"github.com/blueclaw/blueclaw/internal/tool"
)

func TestBuildRequestWithSoulFile(t *testing.T) {
	temporaryDirectory := t.TempDir()
	soulContent := "You are a test assistant."
	os.WriteFile(filepath.Join(temporaryDirectory, "SOUL.md"), []byte(soulContent), 0644)
	session := NewSession("test-session")
	session.AddMessage(provider.Message{Role: "user", Content: "hello"})
	registry := tool.NewRegistry()
	promptContext := PromptContext{
		BlueclawDirectory: temporaryDirectory,
		ToolRegistry:      registry,
	}
	request, err := BuildRequest(promptContext, session)
	if err != nil {
		t.Fatalf("BuildRequest failed: %v", err)
	}
	if !strings.HasPrefix(request.SystemPrompt, soulContent) {
		t.Errorf("expected system prompt to start with %q, got %q", soulContent, request.SystemPrompt)
	}
	if !strings.Contains(request.SystemPrompt, "Current time:") {
		t.Errorf("expected system prompt to contain current time, got %q", request.SystemPrompt)
	}
	if len(request.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(request.Messages))
	}
}

func TestBuildRequestWithoutSoulFile(t *testing.T) {
	temporaryDirectory := t.TempDir()
	session := NewSession("test-session")
	registry := tool.NewRegistry()
	promptContext := PromptContext{
		BlueclawDirectory: temporaryDirectory,
		ToolRegistry:      registry,
	}
	request, err := BuildRequest(promptContext, session)
	if err != nil {
		t.Fatalf("BuildRequest failed: %v", err)
	}
	if !strings.Contains(request.SystemPrompt, "Blueclaw") {
		t.Error("default system prompt should contain 'Blueclaw'")
	}
}

func TestBuildRequestWithToolDefinitions(t *testing.T) {
	temporaryDirectory := t.TempDir()
	os.WriteFile(filepath.Join(temporaryDirectory, "SOUL.md"), []byte("test"), 0644)
	session := NewSession("test-session")
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "remember", description: "Save a memory"})
	registry.Register(&mockTool{name: "recall", description: "Search memories"})
	promptContext := PromptContext{
		BlueclawDirectory: temporaryDirectory,
		ToolRegistry:      registry,
	}
	request, err := BuildRequest(promptContext, session)
	if err != nil {
		t.Fatalf("BuildRequest failed: %v", err)
	}
	if len(request.ToolDefinitions) != 2 {
		t.Errorf("expected 2 tool definitions, got %d", len(request.ToolDefinitions))
	}
}

func TestBuildRequestEmptyHistory(t *testing.T) {
	temporaryDirectory := t.TempDir()
	os.WriteFile(filepath.Join(temporaryDirectory, "SOUL.md"), []byte("test"), 0644)
	session := NewSession("test-session")
	registry := tool.NewRegistry()
	promptContext := PromptContext{
		BlueclawDirectory: temporaryDirectory,
		ToolRegistry:      registry,
	}
	request, err := BuildRequest(promptContext, session)
	if err != nil {
		t.Fatalf("BuildRequest failed: %v", err)
	}
	if len(request.Messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(request.Messages))
	}
}

type mockTool struct {
	name        string
	description string
}

func (mock *mockTool) Name() string        { return mock.name }
func (mock *mockTool) Description() string  { return mock.description }
func (mock *mockTool) ParameterSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (mock *mockTool) Execute(_ context.Context, _ map[string]any) (tool.Result, error) {
	return tool.Result{Output: "ok"}, nil
}
