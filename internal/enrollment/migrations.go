package enrollment

import (
	"os"
	"path/filepath"

	"github.com/Dawn-kim-official/blueclaw/migrations"
)

func writeMigrations(directoryPath string) error {
	if errorValue := os.MkdirAll(directoryPath, 0o755); errorValue != nil {
		return errorValue
	}
	migrationEntries, errorValue := migrations.Files.ReadDir(".")
	if errorValue != nil {
		return errorValue
	}
	for _, migrationEntry := range migrationEntries {
		migrationBytes, errorValue := migrations.Files.ReadFile(migrationEntry.Name())
		if errorValue != nil {
			return errorValue
		}
		if errorValue := os.WriteFile(filepath.Join(directoryPath, migrationEntry.Name()), migrationBytes, 0o644); errorValue != nil {
			return errorValue
		}
	}
	return nil
}
