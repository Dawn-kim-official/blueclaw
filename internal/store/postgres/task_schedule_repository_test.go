package postgres

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/task"
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

func TestNormalizeTaskScheduleListPagination(t *testing.T) {
	if normalizedTaskScheduleListPage(0) != 1 {
		t.Fatalf("expected default page")
	}
	if normalizedTaskScheduleListPageSize(0) != 25 {
		t.Fatalf("expected default page size")
	}
	if normalizedTaskScheduleListPageSize(999) != 100 {
		t.Fatalf("expected capped page size")
	}
}

func TestBuildTaskScheduleListFilterSharesListAndCountConditions(t *testing.T) {
	filter := buildTaskScheduleListFilter(task.TaskScheduleListRequest{
		ConversationID:  "channel-1",
		CreatorPersonID: "person-1",
		UnboundedOnly:   true,
	}, time.Date(2026, 6, 9, 0, 0, 0, 0, time.UTC))
	conditionText := strings.Join(filter.conditions, " AND ")
	for _, expectedCondition := range []string{
		"next_run_at IS NOT NULL",
		"delivery_conversation_id = $2",
		"creator_person_id = $3",
		"expires_at IS NULL",
		"max_run_count IS NULL",
	} {
		if !strings.Contains(conditionText, expectedCondition) {
			t.Fatalf("expected condition %q in %q", expectedCondition, conditionText)
		}
	}
	if len(filter.arguments) != 3 {
		t.Fatalf("expected filter arguments to exclude pagination values, got %+v", filter.arguments)
	}
}
