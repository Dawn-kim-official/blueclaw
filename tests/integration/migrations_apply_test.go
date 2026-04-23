package integration

import (
	"testing"

	"blueclaw/internal/store/postgres"
)

func TestMigrationsApplyList(t *testing.T) {
	migrationRunner := postgres.MigrationRunner{MigrationDirectoryPath: "../../migrations"}
	migrationPaths, errorValue := migrationRunner.ListMigrationPath()
	if errorValue != nil {
		t.Fatalf("expected migrations to load: %v", errorValue)
	}
	if len(migrationPaths) != 8 {
		t.Fatalf("expected 8 migration files, got %d", len(migrationPaths))
	}
}
