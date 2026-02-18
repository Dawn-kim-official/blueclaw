package memory

import (
	"fmt"
	"sync"
)

var registerOnce sync.Once

func ensureVecRegistered() {
	registerOnce.Do(registerVecExtension)
}

func checkDatabaseIntegrity(store *GraphStore) error {
	var result string
	if err := store.database.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("running integrity check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("database integrity check failed: %s", result)
	}
	return nil
}
