package runtime

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"blueclaw/internal/config"
)

type PersistentLogger struct {
	Logger        *slog.Logger
	directoryPath string
	retentionDays int
	writer        *dailyLogWriter
}

func NewPersistentLogger(runtimeConfiguration config.RuntimeConfiguration, now time.Time) (*PersistentLogger, error) {
	directoryPath := deriveLogDirectoryPath(runtimeConfiguration)
	retentionDays := runtimeConfiguration.Logging.RetentionDays
	if retentionDays <= 0 {
		retentionDays = 7
	}

	errorValue := os.MkdirAll(directoryPath, 0o700)
	if errorValue != nil {
		return nil, errorValue
	}

	writer := &dailyLogWriter{
		directoryPath: directoryPath,
		now:           time.Now,
	}
	writer.now = func() time.Time {
		return now
	}
	errorValue = writer.rotateIfNeeded()
	if errorValue != nil {
		return nil, errorValue
	}
	writer.now = time.Now

	logger := slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelDebug}))
	persistentLogger := &PersistentLogger{
		Logger:        logger,
		directoryPath: directoryPath,
		retentionDays: retentionDays,
		writer:        writer,
	}
	return persistentLogger, nil
}

func NewDiscardLogger() *PersistentLogger {
	return &PersistentLogger{
		Logger: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})),
	}
}

func (persistentLogger *PersistentLogger) DirectoryPath() string {
	if persistentLogger == nil {
		return ""
	}
	return persistentLogger.directoryPath
}

func (persistentLogger *PersistentLogger) Retain(now time.Time) error {
	if persistentLogger == nil || persistentLogger.directoryPath == "" {
		return nil
	}

	entries, errorValue := os.ReadDir(persistentLogger.directoryPath)
	if errorValue != nil {
		return errorValue
	}

	cutoffDate := midnight(now).AddDate(0, 0, -persistentLogger.retentionDays)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		logDate, isLogFile := parseLogDate(entry)
		if !isLogFile || !logDate.Before(cutoffDate) {
			continue
		}
		errorValue = os.Remove(filepath.Join(persistentLogger.directoryPath, entry.Name()))
		if errorValue != nil && !errors.Is(errorValue, fs.ErrNotExist) {
			return errorValue
		}
	}

	return nil
}

func (persistentLogger *PersistentLogger) StartRetentionLoop(ctx context.Context) {
	if persistentLogger == nil {
		return
	}

	_ = persistentLogger.Retain(time.Now())
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case retainedAt := <-ticker.C:
			errorValue := persistentLogger.Retain(retainedAt)
			if errorValue != nil {
				persistentLogger.Logger.Warn("log.retention.failed", slog.String("error", errorValue.Error()))
			}
		}
	}
}

func (persistentLogger *PersistentLogger) Close() error {
	if persistentLogger == nil || persistentLogger.writer == nil {
		return nil
	}
	return persistentLogger.writer.Close()
}

func deriveLogDirectoryPath(runtimeConfiguration config.RuntimeConfiguration) string {
	if strings.TrimSpace(runtimeConfiguration.Logging.DirectoryPath) != "" {
		return strings.TrimSpace(runtimeConfiguration.Logging.DirectoryPath)
	}
	if strings.TrimSpace(runtimeConfiguration.Terminal.WorkspaceRootPath) != "" {
		return filepath.Join(strings.TrimSpace(runtimeConfiguration.Terminal.WorkspaceRootPath), ".blueclaw", "logs")
	}
	return "/workspace/.blueclaw/logs"
}

type dailyLogWriter struct {
	mutex         sync.Mutex
	directoryPath string
	currentDate   string
	file          *os.File
	now           func() time.Time
}

func (writer *dailyLogWriter) Write(document []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()

	errorValue := writer.rotateIfNeeded()
	if errorValue != nil {
		return 0, errorValue
	}
	return writer.file.Write(document)
}

func (writer *dailyLogWriter) Close() error {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()

	if writer.file == nil {
		return nil
	}
	errorValue := writer.file.Close()
	writer.file = nil
	return errorValue
}

func (writer *dailyLogWriter) rotateIfNeeded() error {
	currentDate := writer.now().UTC().Format("2006-01-02")
	if writer.file != nil && writer.currentDate == currentDate {
		return nil
	}

	if writer.file != nil {
		errorValue := writer.file.Close()
		if errorValue != nil {
			return errorValue
		}
	}

	filePath := filepath.Join(writer.directoryPath, "blueclaw-"+currentDate+".jsonl")
	file, errorValue := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if errorValue != nil {
		return errorValue
	}
	writer.file = file
	writer.currentDate = currentDate
	return nil
}

func parseLogDate(entry os.DirEntry) (time.Time, bool) {
	name := entry.Name()
	if !strings.HasPrefix(name, "blueclaw-") || !strings.HasSuffix(name, ".jsonl") {
		return time.Time{}, false
	}

	value := strings.TrimSuffix(strings.TrimPrefix(name, "blueclaw-"), ".jsonl")
	parsedTime, errorValue := time.Parse("2006-01-02", value)
	if errorValue != nil {
		return time.Time{}, false
	}
	return parsedTime, true
}

func midnight(value time.Time) time.Time {
	year, month, day := value.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
