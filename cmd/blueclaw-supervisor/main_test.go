package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncWorkspaceRefusesMalformedExistingImage(t *testing.T) {
	workspacePath := t.TempDir()
	workspaceImagePath := filepath.Join(workspacePath, "workspace.ext4")
	originalDocument := []byte("postgres-history-must-survive")
	if errorValue := os.WriteFile(workspaceImagePath, originalDocument, 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	sourceDirectoryPath := filepath.Join(workspacePath, "skills")
	if errorValue := os.MkdirAll(sourceDirectoryPath, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}

	errorValue := syncWorkspace([]string{
		"--atomic",
		"--workspace-image", workspaceImagePath,
		"--source", sourceDirectoryPath,
		"--relative-target", "skills",
	})
	if errorValue == nil || !strings.Contains(errorValue.Error(), "refusing to format") {
		t.Fatalf("expected malformed image to fail closed, got %v", errorValue)
	}
	document, readError := os.ReadFile(workspaceImagePath)
	if readError != nil {
		t.Fatal(readError)
	}
	if string(document) != string(originalDocument) {
		t.Fatalf("workspace image changed from %q to %q", string(originalDocument), string(document))
	}
}

func TestSyncWorkspaceRequiresAtomicMode(t *testing.T) {
	errorValue := syncWorkspace([]string{
		"--workspace-image", "/state/workspace.ext4",
		"--source", "/state/workspace",
	})
	if errorValue == nil || !strings.Contains(errorValue.Error(), "requires --atomic") {
		t.Fatalf("expected explicit atomic mode requirement, got %v", errorValue)
	}
}
