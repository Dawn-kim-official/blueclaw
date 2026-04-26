package runtime

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/config"
)

func TestPersistentLoggerWritesJSONLogFile(t *testing.T) {
	logDirectoryPath := t.TempDir()
	persistentLogger, errorValue := NewPersistentLogger(config.RuntimeConfiguration{
		Logging: config.LoggingConfiguration{
			DirectoryPath: logDirectoryPath,
			RetentionDays: 7,
		},
	}, time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC))
	if errorValue != nil {
		t.Fatalf("expected logger creation: %v", errorValue)
	}
	defer persistentLogger.Close()

	persistentLogger.Logger.Info("connector.mattermost.ingress.received", slog.String("token", "redacted"))
	if errorValue := persistentLogger.Close(); errorValue != nil {
		t.Fatalf("expected logger close: %v", errorValue)
	}

	matches, errorValue := filepath.Glob(filepath.Join(logDirectoryPath, "blueclaw-*.jsonl"))
	if errorValue != nil {
		t.Fatalf("expected log glob: %v", errorValue)
	}
	document := readNonEmptyLogDocument(t, matches)

	var logRecord map[string]any
	errorValue = json.Unmarshal(document, &logRecord)
	if errorValue != nil {
		t.Fatalf("expected json log record: %v", errorValue)
	}
	if logRecord["msg"] != "connector.mattermost.ingress.received" {
		t.Fatalf("expected log message, got %v", logRecord["msg"])
	}
	if strings.Contains(string(document), "bot-token") {
		t.Fatal("expected log not to include secret values")
	}
}

func readNonEmptyLogDocument(t *testing.T, matches []string) []byte {
	t.Helper()
	for _, match := range matches {
		document, errorValue := os.ReadFile(match)
		if errorValue != nil {
			t.Fatalf("expected log file: %v", errorValue)
		}
		if len(strings.TrimSpace(string(document))) > 0 {
			return document
		}
	}
	t.Fatalf("expected non-empty log file")
	return nil
}

func TestPersistentLoggerDeletesFilesOlderThanRetention(t *testing.T) {
	logDirectoryPath := t.TempDir()
	createLogFile(t, logDirectoryPath, "blueclaw-2026-04-10.jsonl")
	createLogFile(t, logDirectoryPath, "blueclaw-2026-04-20.jsonl")

	persistentLogger, errorValue := NewPersistentLogger(config.RuntimeConfiguration{
		Logging: config.LoggingConfiguration{
			DirectoryPath: logDirectoryPath,
			RetentionDays: 7,
		},
	}, time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC))
	if errorValue != nil {
		t.Fatalf("expected logger creation: %v", errorValue)
	}
	defer persistentLogger.Close()

	errorValue = persistentLogger.Retain(time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC))
	if errorValue != nil {
		t.Fatalf("expected retention to succeed: %v", errorValue)
	}

	if _, errorValue = os.Stat(filepath.Join(logDirectoryPath, "blueclaw-2026-04-10.jsonl")); !os.IsNotExist(errorValue) {
		t.Fatal("expected old log file to be deleted")
	}
	if _, errorValue = os.Stat(filepath.Join(logDirectoryPath, "blueclaw-2026-04-20.jsonl")); errorValue != nil {
		t.Fatal("expected recent log file to be kept")
	}
}

func createLogFile(t *testing.T, directoryPath string, fileName string) {
	t.Helper()

	errorValue := os.WriteFile(filepath.Join(directoryPath, fileName), []byte("{}\n"), 0o600)
	if errorValue != nil {
		t.Fatalf("expected log file to be created: %v", errorValue)
	}
}
