package task

import (
	"testing"
	"time"
)

func TestInitializeTaskScheduleForOneTimeRun(t *testing.T) {
	taskScheduler := TaskScheduler{}
	runAt := time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC)

	taskSchedule, errorValue := taskScheduler.InitializeTaskSchedule(TaskSchedule{
		TaskScheduleID: "schedule-1",
		Name:           "one-time",
		Kind:           TaskScheduleKindOnce,
		RunAt:          &runAt,
	}, time.Date(2026, 4, 23, 9, 0, 0, 0, time.UTC))
	if errorValue != nil {
		t.Fatalf("expected one-time task schedule to initialize: %v", errorValue)
	}
	if taskSchedule.NextRunAt == nil {
		t.Fatal("expected one-time task schedule to include a next run time")
	}
	if !taskSchedule.NextRunAt.Equal(runAt) {
		t.Fatalf("expected next run time to match scheduled time, got %s", taskSchedule.NextRunAt.Format(time.RFC3339))
	}
}

func TestInitializeTaskScheduleForIntervalRun(t *testing.T) {
	taskScheduler := TaskScheduler{}
	runAt := time.Date(2026, 4, 23, 9, 0, 0, 0, time.UTC)

	taskSchedule, errorValue := taskScheduler.InitializeTaskSchedule(TaskSchedule{
		TaskScheduleID: "schedule-2",
		Name:           "interval",
		Kind:           TaskScheduleKindInterval,
		RunAt:          &runAt,
		IntervalSecond: 900,
	}, time.Date(2026, 4, 23, 9, 7, 0, 0, time.UTC))
	if errorValue != nil {
		t.Fatalf("expected interval task schedule to initialize: %v", errorValue)
	}
	if taskSchedule.NextRunAt == nil {
		t.Fatal("expected interval task schedule to include a next run time")
	}

	expectedNextRunAt := time.Date(2026, 4, 23, 9, 15, 0, 0, time.UTC)
	if !taskSchedule.NextRunAt.Equal(expectedNextRunAt) {
		t.Fatalf("expected interval next run time to be %s, got %s", expectedNextRunAt.Format(time.RFC3339), taskSchedule.NextRunAt.Format(time.RFC3339))
	}
}

func TestInitializeTaskScheduleForIntervalRunWithoutRunAtStartsNow(t *testing.T) {
	taskScheduler := TaskScheduler{}
	referenceTime := time.Date(2026, 4, 23, 9, 7, 0, 0, time.UTC)

	taskSchedule, errorValue := taskScheduler.InitializeTaskSchedule(TaskSchedule{
		TaskScheduleID: "schedule-immediate",
		Name:           "interval",
		Kind:           TaskScheduleKindInterval,
		IntervalSecond: 60,
	}, referenceTime)
	if errorValue != nil {
		t.Fatalf("expected interval task schedule to initialize: %v", errorValue)
	}
	if taskSchedule.NextRunAt == nil || !taskSchedule.NextRunAt.Equal(referenceTime) {
		t.Fatalf("expected interval next run time to start at %s, got %+v", referenceTime.Format(time.RFC3339), taskSchedule.NextRunAt)
	}
}

func TestAdvanceTaskScheduleStopsAtMaxRunCount(t *testing.T) {
	taskScheduler := TaskScheduler{}
	runAt := time.Date(2026, 4, 23, 9, 0, 0, 0, time.UTC)

	taskSchedule, errorValue := taskScheduler.AdvanceTaskSchedule(TaskSchedule{
		TaskScheduleID:    "schedule-limited",
		Name:              "limited interval",
		Kind:              TaskScheduleKindInterval,
		RunAt:             &runAt,
		IntervalSecond:    60,
		MaxRunCount:       10,
		CompletedRunCount: 9,
	}, time.Date(2026, 4, 23, 9, 10, 0, 0, time.UTC))
	if errorValue != nil {
		t.Fatalf("expected limited interval schedule to advance: %v", errorValue)
	}
	if taskSchedule.CompletedRunCount != 10 {
		t.Fatalf("expected completed run count to reach limit, got %+v", taskSchedule)
	}
	if taskSchedule.NextRunAt != nil {
		t.Fatalf("expected limited interval schedule to stop, got %+v", taskSchedule.NextRunAt)
	}
}

func TestAdvanceTaskScheduleForCronRun(t *testing.T) {
	taskScheduler := TaskScheduler{}
	executedAt := time.Date(2026, 4, 23, 10, 15, 0, 0, time.UTC)

	taskSchedule, errorValue := taskScheduler.AdvanceTaskSchedule(TaskSchedule{
		TaskScheduleID: "schedule-3",
		Name:           "cron",
		Kind:           TaskScheduleKindCron,
		CronExpression: "*/15 * * * *",
	}, executedAt)
	if errorValue != nil {
		t.Fatalf("expected cron task schedule to advance: %v", errorValue)
	}
	if taskSchedule.NextRunAt == nil {
		t.Fatal("expected cron task schedule to include a next run time")
	}

	expectedNextRunAt := time.Date(2026, 4, 23, 10, 30, 0, 0, time.UTC)
	if !taskSchedule.NextRunAt.Equal(expectedNextRunAt) {
		t.Fatalf("expected cron next run time to be %s, got %s", expectedNextRunAt.Format(time.RFC3339), taskSchedule.NextRunAt.Format(time.RFC3339))
	}
	if taskSchedule.LastRunAt == nil {
		t.Fatal("expected cron task schedule to include a last run time")
	}
	if !taskSchedule.LastRunAt.Equal(executedAt) {
		t.Fatalf("expected last run time to be %s, got %s", executedAt.Format(time.RFC3339), taskSchedule.LastRunAt.Format(time.RFC3339))
	}
}

func TestAdvanceTaskScheduleForCronRunUsesScheduleTimeZone(t *testing.T) {
	taskScheduler := TaskScheduler{}
	executedAt := time.Date(2026, 5, 5, 22, 0, 0, 0, time.UTC)

	taskSchedule, errorValue := taskScheduler.AdvanceTaskSchedule(TaskSchedule{
		TaskScheduleID: "schedule-seoul-morning",
		Name:           "morning research",
		Kind:           TaskScheduleKindCron,
		CronExpression: "0 7 * * *",
		TimeZone:       "Asia/Seoul",
	}, executedAt)
	if errorValue != nil {
		t.Fatalf("expected cron task schedule to advance: %v", errorValue)
	}

	expectedNextRunAt := time.Date(2026, 5, 6, 22, 0, 0, 0, time.UTC)
	if taskSchedule.NextRunAt == nil || !taskSchedule.NextRunAt.Equal(expectedNextRunAt) {
		t.Fatalf("expected next run time to be %s, got %+v", expectedNextRunAt.Format(time.RFC3339), taskSchedule.NextRunAt)
	}
}

func TestIsTaskScheduleDue(t *testing.T) {
	taskScheduler := TaskScheduler{}
	nextRunAt := time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC)

	isDue := taskScheduler.IsTaskScheduleDue(TaskSchedule{
		TaskScheduleID: "schedule-4",
		Name:           "due",
		Kind:           TaskScheduleKindCron,
		NextRunAt:      &nextRunAt,
	}, time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC))
	if !isDue {
		t.Fatal("expected task schedule to be due at its next run time")
	}
}
