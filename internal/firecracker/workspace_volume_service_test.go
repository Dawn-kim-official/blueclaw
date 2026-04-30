package firecracker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureWorkspaceImageCreatesSparseFileAndMetadata(t *testing.T) {
	workspacePath := t.TempDir()
	workspaceImagePath := filepath.Join(workspacePath, "workspace.ext4")
	formatterPath := writeFakeExt4Formatter(t, workspacePath)
	workspaceVolumeService := WorkspaceVolumeService{
		ImageSizeByte: 1024 * 1024,
		FormatterPath: formatterPath,
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
	if !workspaceImageIsExt4(workspaceImagePath) {
		t.Fatal("expected workspace image to be formatted as ext4")
	}
}

func writeFakeExt4Formatter(t *testing.T, workspacePath string) string {
	t.Helper()
	formatterPath := filepath.Join(workspacePath, "mkfs.ext4")
	formatterDocument := `#!/usr/bin/env bash
set -euo pipefail
target="${@: -1}"
python3 - "$target" <<'PY'
import sys
path = sys.argv[1]
with open(path, "r+b") as file:
    file.seek(1080)
    file.write(bytes([0x53, 0xef]))
PY
`
	if errorValue := os.WriteFile(formatterPath, []byte(formatterDocument), 0o755); errorValue != nil {
		t.Fatalf("expected fake formatter to be written: %v", errorValue)
	}
	return formatterPath
}
