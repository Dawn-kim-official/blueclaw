package postgres

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
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
		if !isMigrationFileName(entry.Name()) {
			continue
		}
		migrationPaths = append(migrationPaths, filepath.Join(migrationRunner.MigrationDirectoryPath, entry.Name()))
	}

	sort.Strings(migrationPaths)
	return migrationPaths, nil
}

func isMigrationFileName(name string) bool {
	return !strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".sql")
}
