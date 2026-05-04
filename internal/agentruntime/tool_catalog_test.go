package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/config"
	"blueclaw/internal/security"
)

func TestFileAttachToolAttachesMultiplePaths(t *testing.T) {
	workspacePath := t.TempDir()
	writeTestFile(t, filepath.Join(workspacePath, "deck.pptx"), "pptx")
	writeTestFile(t, filepath.Join(workspacePath, "deck.pdf"), "%PDF")
	writeTestFile(t, filepath.Join(workspacePath, "deck.html"), "<html></html>")
	writeTestFile(t, filepath.Join(workspacePath, "deck-notes.txt"), "notes")

	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
		ToolName: "file.attach",
		Input: agent.MarshalToolInput(map[string]any{
			"paths": []string{"deck.pptx", "deck.pdf", "deck.html", "deck-notes.txt"},
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.IsError {
		t.Fatalf("expected successful attachment result, got %s", result.Content)
	}
	if len(result.Attachments) != 4 {
		t.Fatalf("expected four attachments, got %+v", result.Attachments)
	}
	if result.Attachments[0].Filename != "deck.pptx" || result.Attachments[3].Filename != "deck-notes.txt" {
		t.Fatalf("expected attachment filenames to match paths, got %+v", result.Attachments)
	}
}

func TestFileToolsAcceptAgentWorkspacePathsWithoutLeakingHostPath(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{ProfileName: "default"})

	writeResult, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "/workspace/deck/presentation.md",
			"content": "# Deck",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.IsError {
		t.Fatalf("expected file.write success, got %s", writeResult.Content)
	}
	if strings.Contains(writeResult.Content, workspacePath) {
		t.Fatalf("expected file.write result not to expose host path, got %s", writeResult.Content)
	}
	if _, errorValue := os.Stat(filepath.Join(workspacePath, "deck", "presentation.md")); errorValue != nil {
		t.Fatal(errorValue)
	}

	attachResult, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
		ToolName: "file.attach",
		Input: agent.MarshalToolInput(map[string]string{
			"path": "/workspace/deck/presentation.md",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if attachResult.IsError {
		t.Fatalf("expected file.attach success, got %s", attachResult.Content)
	}
	if attachResult.Attachments[0].DevicePath != "/workspace/deck/presentation.md" {
		t.Fatalf("expected agent workspace device path, got %+v", attachResult.Attachments[0])
	}
}

func TestTerminalRunTranslatesAgentWorkspacePaths(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolCatalogBuilder.UseTerminalService(security.NewTerminalSessionService(config.TerminalConfiguration{
		WorkspaceRootPath: workspacePath,
		Mode:              "firecrackerGuest",
		TimeoutSecond:     5,
		OutputMaxBytes:    4096,
		SessionMaxCount:   2,
	}))
	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"command":              "mkdir -p /workspace/deck && printf ok > /workspace/deck/result.txt",
			"workingDirectoryPath": "/workspace",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.IsError {
		t.Fatalf("expected terminal.run success, got %s", result.Content)
	}
	content, errorValue := os.ReadFile(filepath.Join(workspacePath, "deck", "result.txt"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(content) != "ok" {
		t.Fatalf("expected translated workspace command to write file, got %q", string(content))
	}
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if errorValue := os.WriteFile(path, []byte(content), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
}
