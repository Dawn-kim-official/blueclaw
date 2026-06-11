package postgres

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateConnectorMigrationDirectoryRequiresConnectorQueueMigration(t *testing.T) {
	migrationDirectoryPath := t.TempDir()
	if errorValue := os.WriteFile(filepath.Join(migrationDirectoryPath, "001_base.sql"), []byte("select 1;"), 0o600); errorValue != nil {
		t.Fatalf("expected migration file: %v", errorValue)
	}

	errorValue := ValidateConnectorMigrationDirectory(MigrationRunner{MigrationDirectoryPath: migrationDirectoryPath})
	if errorValue == nil || !strings.Contains(errorValue.Error(), RequiredConnectorMigrationFileName) {
		t.Fatalf("expected connector migration error, got %v", errorValue)
	}
}

func TestValidateConnectorMigrationDirectoryRequiresTaskAttemptMigration(t *testing.T) {
	migrationDirectoryPath := t.TempDir()
	if errorValue := os.WriteFile(filepath.Join(migrationDirectoryPath, RequiredConnectorMigrationFileName), []byte("select 1;"), 0o600); errorValue != nil {
		t.Fatalf("expected migration file: %v", errorValue)
	}

	errorValue := ValidateConnectorMigrationDirectory(MigrationRunner{MigrationDirectoryPath: migrationDirectoryPath})
	if errorValue == nil || !strings.Contains(errorValue.Error(), "024_task_attempt.sql") {
		t.Fatalf("expected task attempt migration error, got %v", errorValue)
	}
}

func TestValidateConnectorMigrationDirectoryAcceptsRequiredMigrations(t *testing.T) {
	migrationDirectoryPath := t.TempDir()
	for _, fileName := range requiredMigrationFileNames {
		if errorValue := os.WriteFile(filepath.Join(migrationDirectoryPath, fileName), []byte("select 1;"), 0o600); errorValue != nil {
			t.Fatalf("expected migration file: %v", errorValue)
		}
	}

	if errorValue := ValidateConnectorMigrationDirectory(MigrationRunner{MigrationDirectoryPath: migrationDirectoryPath}); errorValue != nil {
		t.Fatalf("expected connector migration directory to pass: %v", errorValue)
	}
}
