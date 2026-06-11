package postgres

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
)

const RequiredConnectorMigrationFileName = "013_connector_queue.sql"

var requiredMigrationFileNames = []string{
	RequiredConnectorMigrationFileName,
	"024_task_attempt.sql",
}

type ConnectorDeliveryBacklog struct {
	RawEventPendingCount int `json:"rawEventPendingCount"`
	RawEventRunningCount int `json:"rawEventRunningCount"`
	OutboxPendingCount   int `json:"outboxPendingCount"`
	OutboxRunningCount   int `json:"outboxRunningCount"`
}

func ValidateConnectorMigrationDirectory(migrationRunner MigrationRunner) error {
	migrationPaths, errorValue := migrationRunner.ListMigrationPath()
	if errorValue != nil {
		return errorValue
	}
	if len(migrationPaths) == 0 {
		return errors.New("blueclaw migration directory is empty")
	}
	foundMigrationFileNames := map[string]bool{}
	for _, migrationPath := range migrationPaths {
		foundMigrationFileNames[filepath.Base(migrationPath)] = true
	}
	missingMigrationFileNames := []string{}
	for _, fileName := range requiredMigrationFileNames {
		if !foundMigrationFileNames[fileName] {
			missingMigrationFileNames = append(missingMigrationFileNames, fileName)
		}
	}
	if len(missingMigrationFileNames) > 0 {
		return errors.New("blueclaw migrations are missing: " + strings.Join(missingMigrationFileNames, ", "))
	}
	return nil
}

func ValidateConnectorDeliverySchema(ctx context.Context, database Database) error {
	if database.SQL == nil {
		return errors.New("postgres database is not open")
	}
	if errorValue := database.SQL.PingContext(ctx); errorValue != nil {
		return errorValue
	}
	if errorValue := validateRawEventConnectorColumns(ctx, database.SQL); errorValue != nil {
		return errorValue
	}
	if errorValue := validateConnectorOutboxTable(ctx, database.SQL); errorValue != nil {
		return errorValue
	}
	return ValidateTaskAttemptSchema(ctx, database)
}

func ValidateTaskAttemptSchema(ctx context.Context, database Database) error {
	if database.SQL == nil {
		return errors.New("postgres database is not open")
	}
	if errorValue := validateTaskRunAttemptColumns(ctx, database.SQL); errorValue != nil {
		return errorValue
	}
	return validateTaskAttemptTable(ctx, database.SQL)
}

func QueryConnectorDeliveryBacklog(ctx context.Context, database Database) (ConnectorDeliveryBacklog, error) {
	if database.SQL == nil {
		return ConnectorDeliveryBacklog{}, errors.New("postgres database is not open")
	}
	var backlog ConnectorDeliveryBacklog
	errorValue := database.SQL.QueryRowContext(ctx, `
SELECT
  COUNT(*) FILTER (WHERE connector_status = 'pending'),
  COUNT(*) FILTER (WHERE connector_status = 'running')
FROM raw_event
WHERE connector_event_json != '{}'::jsonb`).Scan(&backlog.RawEventPendingCount, &backlog.RawEventRunningCount)
	if errorValue != nil {
		return ConnectorDeliveryBacklog{}, errorValue
	}
	errorValue = database.SQL.QueryRowContext(ctx, `
SELECT
  COUNT(*) FILTER (WHERE status = 'pending'),
  COUNT(*) FILTER (WHERE status = 'running')
FROM connector_outbox`).Scan(&backlog.OutboxPendingCount, &backlog.OutboxRunningCount)
	return backlog, errorValue
}

func validateRawEventConnectorColumns(ctx context.Context, database *sql.DB) error {
	requiredColumns := []string{
		"connector_event_json",
		"connector_status",
		"connector_attempt_count",
		"connector_error",
	}
	rows, errorValue := database.QueryContext(ctx, `
SELECT column_name
FROM information_schema.columns
WHERE table_schema = 'public'
  AND table_name = 'raw_event'
  AND column_name IN (
    'connector_event_json',
    'connector_status',
    'connector_attempt_count',
    'connector_error'
  )`)
	if errorValue != nil {
		return errorValue
	}
	defer rows.Close()

	foundColumns := map[string]bool{}
	for rows.Next() {
		var columnName string
		if errorValue := rows.Scan(&columnName); errorValue != nil {
			return errorValue
		}
		foundColumns[columnName] = true
	}
	if errorValue := rows.Err(); errorValue != nil {
		return errorValue
	}
	missingColumns := []string{}
	for _, columnName := range requiredColumns {
		if !foundColumns[columnName] {
			missingColumns = append(missingColumns, columnName)
		}
	}
	if len(missingColumns) > 0 {
		return errors.New("raw_event connector columns are missing: " + strings.Join(missingColumns, ", "))
	}
	return nil
}

func validateConnectorOutboxTable(ctx context.Context, database *sql.DB) error {
	var tableName sql.NullString
	errorValue := database.QueryRowContext(ctx, "SELECT to_regclass('public.connector_outbox')::text").Scan(&tableName)
	if errorValue != nil {
		return errorValue
	}
	if !tableName.Valid || strings.TrimSpace(tableName.String) == "" {
		return errors.New("connector_outbox table is missing")
	}
	return nil
}

func validateTaskRunAttemptColumns(ctx context.Context, database *sql.DB) error {
	requiredColumns := []string{"current_attempt_id"}
	rows, errorValue := database.QueryContext(ctx, `
SELECT column_name
FROM information_schema.columns
WHERE table_schema = 'public'
  AND table_name = 'task_run'
  AND column_name IN ('current_attempt_id')`)
	if errorValue != nil {
		return errorValue
	}
	defer rows.Close()

	foundColumns := map[string]bool{}
	for rows.Next() {
		var columnName string
		if errorValue := rows.Scan(&columnName); errorValue != nil {
			return errorValue
		}
		foundColumns[columnName] = true
	}
	if errorValue := rows.Err(); errorValue != nil {
		return errorValue
	}
	missingColumns := []string{}
	for _, columnName := range requiredColumns {
		if !foundColumns[columnName] {
			missingColumns = append(missingColumns, columnName)
		}
	}
	if len(missingColumns) > 0 {
		return errors.New("task_run attempt columns are missing: " + strings.Join(missingColumns, ", "))
	}
	return nil
}

func validateTaskAttemptTable(ctx context.Context, database *sql.DB) error {
	var tableName sql.NullString
	errorValue := database.QueryRowContext(ctx, "SELECT to_regclass('public.task_attempt')::text").Scan(&tableName)
	if errorValue != nil {
		return errorValue
	}
	if !tableName.Valid || strings.TrimSpace(tableName.String) == "" {
		return errors.New("task_attempt table is missing")
	}
	return nil
}
