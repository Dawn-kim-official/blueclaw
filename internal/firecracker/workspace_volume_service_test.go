package firecracker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureWorkspaceImageCreatesSparseFileAndMetadata(t *testing.T) {
	workspacePath := t.TempDir()
	workspaceImagePath := filepath.Join(workspacePath, "workspace.ext4")
	workspaceVolumeService := WorkspaceVolumeService{
		ImageSizeByte: 1024 * 1024,
	}

	workspaceVolumeMetadata, errorValue := workspaceVolumeService.EnsureWorkspaceImage(workspaceImagePath)
	if errorValue != nil {
		t.Fatalf("expected workspace image to be ensured: %v", errorValue)
	}

	fileInformation, errorValue := os.Stat(workspaceImagePath)
	if errorValue != nil {
		t.Fatalf("expected workspace image to exist: %v", errorValue)
	}
	if fileInformation.Size() != 1024*1024 {
		t.Fatalf("expected sparse file size to match, got %d", fileInformation.Size())
	}
	if workspaceVolumeMetadata.GuestMountPath != "/workspace" {
		t.Fatalf("expected guest mount path to match, got %q", workspaceVolumeMetadata.GuestMountPath)
	}
	if workspaceVolumeMetadata.DataDirectoryPath != "/workspace/.blueclaw" {
		t.Fatalf("expected data directory path to match, got %q", workspaceVolumeMetadata.DataDirectoryPath)
	}
}
