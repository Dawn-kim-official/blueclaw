package firecracker

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

func TestWorkspaceSyncArgumentsForceSymlinkReplacement(t *testing.T) {
	arguments := workspaceSyncArguments("/source/workspace", "/mounted/workspace", false)

	if !slices.Contains(arguments, "--force") {
		t.Fatalf("expected rsync arguments to force symlink replacement, got %+v", arguments)
	}
	if arguments[len(arguments)-2] != "/source/workspace/" {
		t.Fatalf("expected source directory to include trailing slash, got %+v", arguments)
	}
	if arguments[len(arguments)-1] != "/mounted/workspace/" {
		t.Fatalf("expected mount directory to include trailing slash, got %+v", arguments)
	}
}

func TestWorkspaceSyncArgumentsPreserveGuestConfig(t *testing.T) {
	arguments := workspaceSyncArguments("/source/workspace", "/mounted/workspace", true)

	if !slices.Contains(arguments, "--exclude") || !slices.Contains(arguments, "/.blueclaw/config") {
		t.Fatalf("expected guest config exclude, got %+v", arguments)
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

func TestSeedGuestConfigRefreshesRuntimeButPreservesPolicy(t *testing.T) {
	if _, errorValue := exec.LookPath("rsync"); errorValue != nil {
		t.Skip("rsync not available")
	}
	hostWorkspace := t.TempDir()
	hostConfig := filepath.Join(hostWorkspace, ".blueclaw", "config")
	if errorValue := os.MkdirAll(hostConfig, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeConfigFile(t, hostConfig, "runtime.json", `{"op":"task.add"}`)
	writeConfigFile(t, hostConfig, "policy.json", `{"people":["host-stale"]}`)

	guestMount := t.TempDir()
	guestConfig := filepath.Join(guestMount, ".blueclaw", "config")
	if errorValue := os.MkdirAll(guestConfig, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeConfigFile(t, guestConfig, "runtime.json", `{"op":"flow.task.add"}`)
	writeConfigFile(t, guestConfig, "policy.json", `{"people":["guest-live"]}`)

	if errorValue := seedGuestConfigDirectory("rsync", hostWorkspace, guestMount); errorValue != nil {
		t.Fatal(errorValue)
	}

	if got := readConfigFile(t, guestConfig, "runtime.json"); got != `{"op":"task.add"}` {
		t.Fatalf("runtime.json should be refreshed from host, got %q", got)
	}
	if got := readConfigFile(t, guestConfig, "policy.json"); got != `{"people":["guest-live"]}` {
		t.Fatalf("policy.json should be preserved (runtime-managed), got %q", got)
	}
}

func writeConfigFile(t *testing.T, directory string, name string, content string) {
	t.Helper()
	if errorValue := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func readConfigFile(t *testing.T, directory string, name string) string {
	t.Helper()
	content, errorValue := os.ReadFile(filepath.Join(directory, name))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return string(content)
}
