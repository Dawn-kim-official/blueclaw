package agentruntime

import (
	"os"

	"blueclaw/internal/workspacepath"
)

func workspaceDirectoryCreateMode(directory workspacepath.Directory) os.FileMode {
	if workspacePathKeepsGroupInheritance(directory.Kind) {
		return 02770
	}
	return 0700
}

func workspaceFileCreateMode(path workspacepath.Path) os.FileMode {
	if workspacePathKeepsGroupInheritance(path.Kind) {
		return 0660
	}
	return 0600
}

func workspacePathKeepsGroupInheritance(kind workspacepath.Kind) bool {
	return kind == workspacepath.KindCircle || kind == workspacepath.KindSharedPublic
}
