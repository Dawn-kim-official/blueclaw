package agentruntime

import (
	"path/filepath"
	"testing"
)

func TestConversationScopeGivesAStandaloneRunTheWorkspaceItWasHanded(t *testing.T) {
	workspaceRootPath := t.TempDir()

	scope := ConversationScopeForRequest(workspaceRootPath, ToolCatalogRequest{
		Prompt:            "count the lines in notes.txt",
		RequesterPersonID: "bluecollar",
	})

	if scope.Kind != "workspace" {
		t.Fatalf("expected a requester without a conversation to stay in the workspace, got %q", scope.Kind)
	}
	if scope.DefaultDirectoryPath != workspaceRootPath {
		t.Fatalf("expected the handed workspace as the default directory, got %q", scope.DefaultDirectoryPath)
	}
}

func TestConversationScopeKeepsAPrivateConversationInThePersonHome(t *testing.T) {
	workspaceRootPath := t.TempDir()

	scope := ConversationScopeForRequest(workspaceRootPath, ToolCatalogRequest{
		Prompt:            "count the lines in notes.txt",
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		ConversationType:  "D",
	})

	if scope.Kind != "private" || scope.PersonID != "person-1" {
		t.Fatalf("expected a direct conversation to stay private, got %+v", scope)
	}
	expectedPath := filepath.Join(workspaceRootPath, "private", "people", "person-1")
	if scope.DefaultDirectoryPath != expectedPath {
		t.Fatalf("expected the person home as the default directory, got %q", scope.DefaultDirectoryPath)
	}
}
