package postgres

import (
	"os"
	"path/filepath"
	"sort"
)

type MigrationRunner struct {
	MigrationDirectoryPath string
}

func (migrationRunner MigrationRunner) ListMigrationPath() ([]string, error) {
	entries, errorValue := os.ReadDir(migrationRunner.MigrationDirectoryPath)
	if errorValue != nil {
		return nil, errorValue
	}

	migrationPaths := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		migrationPaths = append(migrationPaths, filepath.Join(migrationRunner.MigrationDirectoryPath, entry.Name()))
	}

	sort.Strings(migrationPaths)
	return migrationPaths, nil
}
