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

func TestMaintenanceCancelQueryIncludesRecursiveChildren(t *testing.T) {
	request := taskScheduleMaintenanceCancelRequestFixture()
	conditions, arguments := maintenanceCancelConditions(request, request.CancelledAt)
	query, arguments := maintenanceCancelQuery(request, conditions, arguments)

	if !strings.Contains(query, "WITH RECURSIVE matched") || !strings.Contains(query, "child.delivery_conversation_id = 'schedule:' || parent.task_schedule_id") {
		t.Fatalf("expected recursive child query, got %s", query)
	}
	if !strings.Contains(query, "UPDATE task_schedule") {
		t.Fatalf("expected non-dry-run query to update schedules, got %s", query)
	}
	if strings.Contains(query, "FROM updated") || strings.Contains(query, "updated AS") {
		t.Fatalf("expected update query to return task schedule columns directly, got %s", query)
	}
	if len(arguments) < 4 {
		t.Fatalf("expected reference time and delivery filters, got %+v", arguments)
	}
}

func TestMaintenanceCancelDryRunQueryDoesNotUpdate(t *testing.T) {
	request := taskScheduleMaintenanceCancelRequestFixture()
	request.DryRun = true
	conditions, arguments := maintenanceCancelConditions(request, request.CancelledAt)
	query, _ := maintenanceCancelQuery(request, conditions, arguments)

	if strings.Contains(query, "UPDATE task_schedule") {
		t.Fatalf("expected dry-run query to avoid updates, got %s", query)
	}
	if !strings.Contains(query, "SELECT "+taskScheduleReturningColumns()) {
		t.Fatalf("expected dry-run query to return matching schedules, got %s", query)
	}
}

func taskScheduleMaintenanceCancelRequestFixture() task.TaskScheduleMaintenanceCancelRequest {
	return task.TaskScheduleMaintenanceCancelRequest{
		DryRun:                       false,
		DeliveryConversationIDs:      []string{"channel-1"},
		DeliveryConversationIDPrefix: "thread:channel-1:",
		IncludeScheduleChildren:      true,
		UnboundedOnly:                true,
		StaleFailedOnly:              true,
		CancelledAt:                  time.Date(2026, 6, 6, 13, 0, 0, 0, time.UTC),
	}
}
