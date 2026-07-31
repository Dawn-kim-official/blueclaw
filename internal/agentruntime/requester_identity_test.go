package agentruntime

import (
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/policy"
	"github.com/Dawn-kim-official/blueclaw/internal/security"
)

// Two people sharing one Blueclaw must never share one POSIX identity: this is
// the boundary every file write and command lands on.
func TestEachRequesterGetsTheirOwnPOSIXIdentity(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath("/workspace")

	firstIdentity := toolCatalogBuilder.executionIdentityForRequester(ToolCatalogRequest{
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
	})
	secondIdentity := toolCatalogBuilder.executionIdentityForRequester(ToolCatalogRequest{
		RequesterPersonID: "person-2",
		PersonAccess:      policy.PersonAccess{PersonID: "person-2"},
	})

	if firstIdentity.UserName != security.LinuxPersonUserName("person-1") {
		t.Fatalf("expected the first requester to run as their own user, got %q", firstIdentity.UserName)
	}
	if secondIdentity.UserName != security.LinuxPersonUserName("person-2") {
		t.Fatalf("expected the second requester to run as their own user, got %q", secondIdentity.UserName)
	}
	if firstIdentity.UserName == secondIdentity.UserName {
		t.Fatal("expected two people to run as two different users")
	}
	if firstIdentity.HomeDirectoryPath == secondIdentity.HomeDirectoryPath {
		t.Fatalf("expected separate private workspaces, both were %q", firstIdentity.HomeDirectoryPath)
	}
}

func TestRequesterIdentityFallsBackToTheRequesterPersonID(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath("/workspace")

	identity := toolCatalogBuilder.executionIdentityForRequester(ToolCatalogRequest{RequesterPersonID: "person-3"})

	if identity.UserName != security.LinuxPersonUserName("person-3") {
		t.Fatalf("expected the requester person to supply the identity, got %q", identity.UserName)
	}
}

func TestAnonymousRequesterGetsNoIdentity(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath("/workspace")

	identity := toolCatalogBuilder.executionIdentityForRequester(ToolCatalogRequest{})

	if identity.UserName != "" {
		t.Fatalf("expected no POSIX identity without a person, got %q", identity.UserName)
	}
}
