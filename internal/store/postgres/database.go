package postgres

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type Database struct {
	ConnectionString string
	SQL              *sql.DB
}

func OpenDatabase(connectionString string) (Database, error) {
	if strings.TrimSpace(connectionString) == "" {
		return Database{}, errors.New("postgres connection string is required")
	}
	sqlDatabase, errorValue := sql.Open("pgx", connectionString)
	if errorValue != nil {
		return Database{}, errorValue
	}
	if errorValue := sqlDatabase.Ping(); errorValue != nil {
		_ = sqlDatabase.Close()
		return Database{}, errorValue
	}
	return Database{ConnectionString: connectionString, SQL: sqlDatabase}, nil
}

func (database Database) Close() error {
	if database.SQL == nil {
		return nil
	}
	return database.SQL.Close()
}

func (database Database) Exec(ctx context.Context, query string, arguments ...any) error {
	if database.SQL == nil {
		return errors.New("postgres database is not open")
	}
	_, errorValue := database.SQL.ExecContext(ctx, query, arguments...)
	return errorValue
}

func (migrationRunner MigrationRunner) ApplyMigrations(ctx context.Context, database Database) error {
	migrationPaths, errorValue := migrationRunner.ListMigrationPath()
	if errorValue != nil {
		return errorValue
	}
	for _, migrationPath := range migrationPaths {
		document, errorValue := os.ReadFile(migrationPath)
		if errorValue != nil {
			return errorValue
		}
		if strings.TrimSpace(string(document)) == "" {
			continue
		}
		if errorValue := database.Exec(ctx, string(document)); errorValue != nil {
			return errorValue
		}
	}
	return nil
}
