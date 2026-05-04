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
	if len(migrationPaths) != 13 {
		t.Fatalf("expected 13 migration files, got %d", len(migrationPaths))
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

func TestScopedMemoryMigrationStoresSourceMetadata(t *testing.T) {
	migrationDocument, errorValue := os.ReadFile(filepath.Join("../../migrations", "011_scoped_memory_source_metadata.sql"))
	if errorValue != nil {
		t.Fatalf("expected scoped memory migration to load: %v", errorValue)
	}

	migrationText := string(migrationDocument)
	requiredFields := []string{
		"source_platform text",
		"source_message_id text",
		"memory_record_scope_lookup_idx",
	}
	for _, requiredField := range requiredFields {
		if !strings.Contains(migrationText, requiredField) {
			t.Fatalf("expected migration to include %q", requiredField)
		}
	}
}

func TestGraphitiMemoryMigrationStoresMirrorMetadata(t *testing.T) {
	migrationDocument, errorValue := os.ReadFile(filepath.Join("../../migrations", "012_graphiti_memory_metadata.sql"))
	if errorValue != nil {
		t.Fatalf("expected graphiti memory migration to load: %v", errorValue)
	}

	migrationText := string(migrationDocument)
	requiredFields := []string{
		"graphiti_namespace",
		"graphiti_episode",
		"namespace_document jsonb",
		"UNIQUE (source_platform, source_message_id)",
	}
	for _, requiredField := range requiredFields {
		if !strings.Contains(migrationText, requiredField) {
			t.Fatalf("expected migration to include %q", requiredField)
		}
	}
}

func TestConnectorQueueMigrationStoresInboxAndOutboxState(t *testing.T) {
	migrationDocument, errorValue := os.ReadFile(filepath.Join("../../migrations", "013_connector_queue.sql"))
	if errorValue != nil {
		t.Fatalf("expected connector queue migration to load: %v", errorValue)
	}

	migrationText := string(migrationDocument)
	requiredFields := []string{
		"connector_event_json jsonb",
		"connector_status text",
		"connector_attempt_count integer",
		"CREATE TABLE IF NOT EXISTS connector_outbox",
		"reply_target_json jsonb",
		"reply_json jsonb",
		"UNIQUE (raw_event_id)",
	}
	for _, requiredField := range requiredFields {
		if !strings.Contains(migrationText, requiredField) {
			t.Fatalf("expected migration to include %q", requiredField)
		}
	}
}
