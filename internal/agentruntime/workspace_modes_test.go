package agentruntime

import (
	"testing"

	"blueclaw/internal/workspacepath"
)

func TestWorkspaceCreateModesUsePrivateOwnershipByDefault(t *testing.T) {
	directory := workspacepath.Directory{Kind: workspacepath.KindWorkspace}
	path := workspacepath.Path{Kind: workspacepath.KindWorkspace}

	if workspaceDirectoryCreateMode(directory) != 0700 {
		t.Fatalf("expected private directory mode 0700, got %04o", workspaceDirectoryCreateMode(directory))
	}
	if workspaceFileCreateMode(path) != 0600 {
		t.Fatalf("expected private file mode 0600, got %04o", workspaceFileCreateMode(path))
	}
}

func TestWorkspaceCreateModesKeepGroupInheritanceForCircleAndSharedPaths(t *testing.T) {
	for _, kind := range []workspacepath.Kind{workspacepath.KindCircle, workspacepath.KindSharedPublic} {
		directory := workspacepath.Directory{Kind: kind}
		path := workspacepath.Path{Kind: kind}
		if workspaceDirectoryCreateMode(directory) != 02770 {
			t.Fatalf("expected %s directory mode 2770, got %04o", kind, workspaceDirectoryCreateMode(directory))
		}
		if workspaceFileCreateMode(path) != 0660 {
			t.Fatalf("expected %s file mode 0660, got %04o", kind, workspaceFileCreateMode(path))
		}
	}
}
