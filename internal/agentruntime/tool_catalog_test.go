package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"blueclaw/internal/agent"
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

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if errorValue := os.WriteFile(path, []byte(content), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
}
