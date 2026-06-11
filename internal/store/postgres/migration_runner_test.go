package postgres

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMigrationRunnerListsOnlySQLMigrationFiles(t *testing.T) {
	directoryPath := t.TempDir()
	for name, content := range map[string]string{
		"001_create_table.sql":     "select 1;",
		"002_add_column.sql":       "select 2;",
		"._001_create_table.sql":   "appledouble",
		".DS_Store":                "metadata",
		"README.md":                "notes",
		"003_nested_directory.sql": "",
	} {
		path := filepath.Join(directoryPath, name)
		if name == "003_nested_directory.sql" {
			if errorValue := os.Mkdir(path, 0o700); errorValue != nil {
				t.Fatal(errorValue)
			}
			continue
		}
		if errorValue := os.WriteFile(path, []byte(content), 0o600); errorValue != nil {
			t.Fatal(errorValue)
		}
	}

	paths, errorValue := (MigrationRunner{MigrationDirectoryPath: directoryPath}).ListMigrationPath()
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	names := []string{}
	for _, path := range paths {
		names = append(names, filepath.Base(path))
	}
	expectedNames := []string{"001_create_table.sql", "002_add_column.sql"}
	if !reflect.DeepEqual(names, expectedNames) {
		t.Fatalf("expected migration files %v, got %v", expectedNames, names)
	}
}

func TestCanonicalPersonReferenceMigrationIgnoresDuplicateConstraints(t *testing.T) {
	document, errorValue := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "023_canonical_person_references.sql"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	migrationText := string(document)
	requiredFragments := []string{
		"task_schedule_creator_person_id_present",
		"task_run_requester_person_id_fkey",
		"WHEN duplicate_object THEN NULL",
	}
	for _, requiredFragment := range requiredFragments {
		if !strings.Contains(migrationText, requiredFragment) {
			t.Fatalf("expected migration to contain %q", requiredFragment)
		}
	}
}
