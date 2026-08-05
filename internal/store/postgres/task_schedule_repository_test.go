package postgres

import (
	"database/sql"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
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
		"say sorry.",
		"default",
		"message",
		"interval",
		sql.NullTime{},
		sql.NullInt64{Int64: 60, Valid: true},
		sql.NullString{},
		sql.NullTime{},
		sql.NullTime{},
		"",
		sql.NullTime{Time: createdAt.Add(time.Hour), Valid: true},
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

func TestTaskScheduleListFilterIncludesExpiredRowsWithoutNextRun(t *testing.T) {
	conditions, _ := taskScheduleListFilter(task.TaskScheduleListRequest{IncludeExpired: true})
	for _, condition := range conditions {
		if condition == "next_run_at IS NOT NULL" {
			t.Fatalf("expected includeExpired to allow schedules without next_run_at, got %+v", conditions)
		}
	}
}

func TestTaskScheduleListFilterExcludesExpiredRowsByDefault(t *testing.T) {
	conditions, _ := taskScheduleListFilter(task.TaskScheduleListRequest{})
	if !containsTaskScheduleListCondition(conditions, "next_run_at IS NOT NULL") || !containsTaskScheduleListCondition(conditions, "(expires_at IS NULL OR expires_at > $1)") {
		t.Fatalf("expected default list filter to require active schedules, got %+v", conditions)
	}
}

func containsTaskScheduleListCondition(conditions []string, expectedCondition string) bool {
	for _, condition := range conditions {
		if condition == expectedCondition {
			return true
		}
	}
	return false
}
