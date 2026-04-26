package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"blueclaw/internal/store/postgres"
)

func TestMigrationsApplyList(t *testing.T) {
	migrationRunner := postgres.MigrationRunner{MigrationDirectoryPath: "../../migrations"}
	migrationPaths, errorValue := migrationRunner.ListMigrationPath()
	if errorValue != nil {
		t.Fatalf("expected migrations to load: %v", errorValue)
	}
	if len(migrationPaths) != 10 {
		t.Fatalf("expected 10 migration files, got %d", len(migrationPaths))
	}
}

func TestMinimalConversationContractMigrationStoresReplayFields(t *testing.T) {
	migrationDocument, errorValue := os.ReadFile(filepath.Join("../../migrations", "009_minimal_conversation_contract.sql"))
	if errorValue != nil {
		t.Fatalf("expected minimal conversation migration to load: %v", errorValue)
	}

	migrationText := string(migrationDocument)
	requiredFields := []string{
		"reply_target_id text",
		"visible_context_ciphertext bytea",
		"visible_context_sha256 bytea",
		"has_more_before boolean NOT NULL DEFAULT false",
		"history_cursor text",
	}
	for _, requiredField := range requiredFields {
		if !strings.Contains(migrationText, requiredField) {
			t.Fatalf("expected migration to include %q", requiredField)
		}
	}
}
