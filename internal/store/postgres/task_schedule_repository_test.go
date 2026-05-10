package postgres

import (
	"database/sql"
	"testing"
	"time"
)

type taskScheduleScannerStub struct {
	values []any
}

func (scanner taskScheduleScannerStub) Scan(targets ...any) error {
	for index, value := range scanner.values {
		switch target := targets[index].(type) {
		case *string:
			*target = value.(string)
		case *bool:
			*target = value.(bool)
		case *int:
			*target = value.(int)
		case *time.Time:
			*target = value.(time.Time)
		case *sql.NullInt64:
			*target = value.(sql.NullInt64)
		case *sql.NullString:
			*target = value.(sql.NullString)
		case *sql.NullTime:
			*target = value.(sql.NullTime)
		}
	}
	return nil
}

func TestScanTaskScheduleIncludesRunLimit(t *testing.T) {
	createdAt := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	taskSchedule, errorValue := scanTaskSchedule(taskScheduleScannerStub{values: []any{
		"schedule-1",
		"person-1",
		"limited reminder",
		"죄송합니다라고 말해줘.",
		"default",
		"message",
		"interval",
		sql.NullTime{},
		sql.NullInt64{Int64: 60, Valid: true},
		sql.NullString{},
		sql.NullTime{},
		sql.NullTime{},
		"",
		false,
		createdAt,
		createdAt,
		"mattermost",
		"channel-1",
		"reply-1",
		"Asia/Seoul",
		"",
		sql.NullTime{},
		0,
		"",
		sql.NullTime{},
		sql.NullInt64{Int64: 10, Valid: true},
		4,
	}})
	if errorValue != nil {
		t.Fatalf("expected task schedule scan to succeed: %v", errorValue)
	}
	if taskSchedule.MaxRunCount != 10 || taskSchedule.CompletedRunCount != 4 {
		t.Fatalf("expected run limit fields to scan, got %+v", taskSchedule)
	}
	if taskSchedule.ExecutionMode != "message" {
		t.Fatalf("expected execution mode to scan, got %+v", taskSchedule)
	}
	if taskSchedule.CronExpression != "" {
		t.Fatalf("expected nullable cron expression to scan as empty, got %q", taskSchedule.CronExpression)
	}
}
