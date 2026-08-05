package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/backup"
	"github.com/yeomyeonggeori/blueclaw/internal/restore"
)

func TestSnapshotBackupRestore(t *testing.T) {
	workspacePath := t.TempDir()
	sourceFilePath := filepath.Join(workspacePath, "policy.yaml")
	errorValue := os.WriteFile(sourceFilePath, []byte("policy"), 0o600)
	if errorValue != nil {
		t.Fatalf("expected source file to be written: %v", errorValue)
	}

	bundlePath := filepath.Join(workspacePath, "bundle.tar.gz")
	backupService := backup.BackupService{}
	_, errorValue = backupService.CreateSnapshotBundle(bundlePath, []string{sourceFilePath})
	if errorValue != nil {
		t.Fatalf("expected bundle to be created: %v", errorValue)
	}

	restoreDirectoryPath := filepath.Join(workspacePath, "restore")
	restoreService := restore.RestoreService{}
	errorValue = restoreService.RestoreSnapshotBundle(bundlePath, restoreDirectoryPath)
	if errorValue != nil {
		t.Fatalf("expected bundle to be restored: %v", errorValue)
	}

	restoredDocument, errorValue := os.ReadFile(filepath.Join(restoreDirectoryPath, "policy.yaml"))
	if errorValue != nil {
		t.Fatalf("expected restored file to be readable: %v", errorValue)
	}
	if string(restoredDocument) != "policy" {
		t.Fatalf("expected restored content to match, got %q", string(restoredDocument))
	}
}
