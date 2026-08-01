package scheduler

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/agentruntime"
	"github.com/Dawn-kim-official/blueclaw/internal/bluecollar"
	"github.com/Dawn-kim-official/blueclaw/internal/connectors"
	"github.com/Dawn-kim-official/blueclaw/internal/llm"
	"github.com/Dawn-kim-official/blueclaw/internal/policy"
	"github.com/Dawn-kim-official/blueclaw/internal/task"
)

func TestScheduledTaskReplyPreservesModelWording(t *testing.T) {
	turnResult := bluecollar.AgentTurnResult{
		TaskRun:       task.TaskRun{TaskRunID: "task-1", Status: task.TaskStatusCompleted},
		FinishMessage: "완료했습니다: sandbox:/mnt/data/report.pdf",
		Attachments:   []bluecollar.FileAttachment{{Filename: "report.pdf", DevicePath: "/workspace/private/people/p1/artifacts/report.pdf"}},
	}

	reply, errorValue := scheduledTaskReply(agentruntime.TaskScheduleRunResult{
		LaunchResult: agentruntime.TaskLaunchResult{TurnResult: turnResult},
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if reply.Message != turnResult.FinishMessage || len(reply.Attachments) != 1 {
		t.Fatalf("expected model wording and typed attachments to pass through, got %+v", reply)
	}
}

func TestTaskSchedulePollerRunsDueScheduleAndEnqueuesReply(t *testing.T) {
	runAt := time.Date(2026, 5, 6, 7, 0, 0, 0, time.UTC)
	nextRunAt := runAt
	repository := &pollerScheduleRepository{taskSchedules: []task.TaskSchedule{{
		TaskScheduleID:   "schedule-1",
		CreatorPersonID:  "person-1",
		Name:             "daily research",
		Prompt:           "매일 업계 뉴스를 조사해서 알려줘.",
		AgentProfileName: "default",
		Platform:         "mattermost",
		ConversationID:   "channel-1",
		ReplyTargetID:    "reply-target-1",
		TimeZone:         "Asia/Seoul",
		Kind:             task.TaskScheduleKindCron,
		CronExpression:   "0 7 * * *",
		NextRunAt:        &nextRunAt,
	}}}
	deliveryRepository := &pollerDeliveryRepository{}
	poller := TaskSchedulePoller{
		TaskScheduleRepository: repository,
		DeliveryRepository:     deliveryRepository,
		TaskScheduleRunner:     testTaskScheduleRunner("오늘의 조사 결과입니다."),
		PersonAccessResolver:   staticPersonAccessResolver{},
		WorkspaceID:            "workspace-1",
	}

	runCount, errorValue := poller.RunDue(context.Background(), runAt, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if runCount != 1 {
		t.Fatalf("expected one run, got %d", runCount)
	}
	if len(deliveryRepository.replies) != 1 || deliveryRepository.replies[0].Message != "오늘의 조사 결과입니다." {
		t.Fatalf("expected scheduled reply delivery, got %+v", deliveryRepository.replies)
	}
	if deliveryRepository.replies[0].TaskRunID == "" || deliveryRepository.replies[0].ReplyKind != "success" {
		t.Fatalf("expected scheduled reply metadata, got %+v", deliveryRepository.replies[0])
	}
	if repository.succeeded == nil || repository.succeeded.LastTaskRunID == "" {
		t.Fatalf("expected schedule success with task run id, got %+v", repository.succeeded)
	}
	if repository.succeeded.NextRunAt == nil || !repository.succeeded.NextRunAt.After(runAt) {
		t.Fatalf("expected schedule to advance, got %+v", repository.succeeded.NextRunAt)
	}
}

func TestTaskSchedulePollerDoesNotClaimDueScheduleWhenQuiesced(t *testing.T) {
	runAt := time.Date(2026, 5, 6, 7, 0, 0, 0, time.UTC)
	nextRunAt := runAt
	claimCount := 0
	repository := &pollerScheduleRepository{
		taskSchedules: []task.TaskSchedule{{
			TaskScheduleID:   "schedule-1",
			CreatorPersonID:  "person-1",
			Prompt:           "매일 업계 뉴스를 조사해서 알려줘.",
			AgentProfileName: "default",
			Platform:         "mattermost",
			ConversationID:   "channel-1",
			ReplyTargetID:    "reply-target-1",
			TimeZone:         "Asia/Seoul",
			Kind:             task.TaskScheduleKindInterval,
			IntervalSecond:   60,
			NextRunAt:        &nextRunAt,
		}},
		claimCallback: func() {
			claimCount++
		},
	}
	poller := TaskSchedulePoller{
		TaskScheduleRepository: repository,
		DeliveryRepository:     &pollerDeliveryRepository{},
		TaskScheduleRunner:     testTaskScheduleRunner("오늘의 조사 결과입니다."),
		TaskIntakeGate:         pollerTaskIntakeGate{isQuiesced: true},
	}

	runCount, errorValue := poller.RunDue(context.Background(), runAt, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if runCount != 0 {
		t.Fatalf("expected no runs, got %d", runCount)
	}
	if claimCount != 0 {
		t.Fatalf("quiesced scheduler must not claim schedules, got %d claims", claimCount)
	}
	if repository.succeeded != nil || len(repository.failed) != 0 {
		t.Fatalf("quiesced scheduler must not advance schedules, succeeded=%+v failed=%+v", repository.succeeded, repository.failed)
	}
}

func TestTaskSchedulePollerDeliversMessageScheduleWithoutAgentRun(t *testing.T) {
	runAt := time.Date(2026, 5, 6, 7, 0, 0, 0, time.UTC)
	nextRunAt := runAt
	repository := &pollerScheduleRepository{taskSchedules: []task.TaskSchedule{{
		TaskScheduleID:  "schedule-1",
		CreatorPersonID: "person-1",
		Name:            "apology repeat",
		Prompt:          "죄송합니다.",
		ExecutionMode:   task.TaskScheduleExecutionModeMessage,
		Platform:        "mattermost",
		ConversationID:  "channel-1",
		ReplyTargetID:   "reply-target-1",
		TimeZone:        "Asia/Seoul",
		Kind:            task.TaskScheduleKindInterval,
		IntervalSecond:  60,
		MaxRunCount:     1,
		NextRunAt:       &nextRunAt,
	}}}
	deliveryRepository := &pollerDeliveryRepository{}
	poller := TaskSchedulePoller{
		TaskScheduleRepository: repository,
		DeliveryRepository:     deliveryRepository,
		TaskRunService:         task.NewTaskRunService(task.NewTaskEventService()),
	}

	runCount, errorValue := poller.RunDue(context.Background(), runAt, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if runCount != 1 {
		t.Fatalf("expected one run, got %d", runCount)
	}
	if len(deliveryRepository.replies) != 1 || deliveryRepository.replies[0].Message != "죄송합니다." {
		t.Fatalf("expected direct scheduled message delivery, got %+v", deliveryRepository.replies)
	}
	if deliveryRepository.replies[0].TaskRunID == "" || deliveryRepository.replies[0].ReplyKind != "success" {
		t.Fatalf("expected direct scheduled reply metadata, got %+v", deliveryRepository.replies[0])
	}
	if repository.succeeded == nil || repository.succeeded.LastTaskRunID == "" {
		t.Fatalf("expected audited schedule success, got %+v", repository.succeeded)
	}
	if repository.succeeded.NextRunAt != nil || repository.succeeded.CompletedRunCount != 1 {
		t.Fatalf("expected limited message schedule to complete, got %+v", repository.succeeded)
	}
}

func TestTaskSchedulePollerDoesNotAdvanceWhenDeliveryFails(t *testing.T) {
	runAt := time.Date(2026, 5, 6, 7, 0, 0, 0, time.UTC)
	nextRunAt := runAt
	generatedResponseCount := 0
	repository := &pollerScheduleRepository{taskSchedules: []task.TaskSchedule{{
		TaskScheduleID:   "schedule-1",
		CreatorPersonID:  "person-1",
		Prompt:           "매일 업계 뉴스를 조사해서 알려줘.",
		AgentProfileName: "default",
		Platform:         "mattermost",
		ConversationID:   "channel-1",
		ReplyTargetID:    "reply-target-1",
		TimeZone:         "Asia/Seoul",
		Kind:             task.TaskScheduleKindInterval,
		IntervalSecond:   60,
		MaxRunCount:      10,
		NextRunAt:        &nextRunAt,
	}}}
	poller := TaskSchedulePoller{
		TaskScheduleRepository: repository,
		DeliveryRepository:     &pollerDeliveryRepository{errorValue: errors.New("outbox unavailable")},
		TaskScheduleRunner:     testTaskScheduleRunnerWithResponseCount("오늘의 조사 결과입니다.", &generatedResponseCount),
		PersonAccessResolver:   staticPersonAccessResolver{},
	}

	errorValue := poller.runTaskSchedule(context.Background(), repository.taskSchedules[0], runAt)

	if errorValue == nil || !strings.Contains(errorValue.Error(), "outbox unavailable") {
		t.Fatalf("expected delivery error to surface, got %v", errorValue)
	}
	if repository.succeeded != nil {
		t.Fatalf("expected delivery failure not to advance schedule, got %+v", repository.succeeded)
	}
	if len(repository.failed) != 0 {
		t.Fatalf("expected direct run not to record poller failure, got %+v", repository.failed)
	}
	// The agent turn now legitimately makes more than one structured-response call per
	// run (completion-evidence/quality-criteria checks), so this only asserts the task
	// executor ran at all, not an exact call count.
	if generatedResponseCount == 0 {
		t.Fatalf("expected task executor to run, got %d calls", generatedResponseCount)
	}
}

func TestTaskSchedulePollerAtomicSuccessAdvancesOnceAndEnqueuesOnce(t *testing.T) {
	runAt := time.Date(2026, 5, 6, 7, 0, 0, 0, time.UTC)
	nextRunAt := runAt
	repository := &pollerAtomicScheduleRepository{
		pollerScheduleRepository: &pollerScheduleRepository{taskSchedules: []task.TaskSchedule{{
			TaskScheduleID:   "schedule-1",
			CreatorPersonID:  "person-1",
			Prompt:           "매일 업계 뉴스를 조사해서 알려줘.",
			AgentProfileName: "default",
			Platform:         "mattermost",
			ConversationID:   "channel-1",
			ReplyTargetID:    "reply-target-1",
			TimeZone:         "Asia/Seoul",
			Kind:             task.TaskScheduleKindInterval,
			IntervalSecond:   60,
			MaxRunCount:      10,
			NextRunAt:        &nextRunAt,
		}}},
	}
	poller := TaskSchedulePoller{
		TaskScheduleRepository: repository,
		TaskScheduleRunner:     testTaskScheduleRunner("오늘의 조사 결과입니다."),
		PersonAccessResolver:   staticPersonAccessResolver{},
	}

	errorValue := poller.runTaskSchedule(context.Background(), repository.taskSchedules[0], runAt)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if repository.succeeded == nil || repository.succeeded.CompletedRunCount != 1 {
		t.Fatalf("expected schedule to advance once, got %+v", repository.succeeded)
	}
	if repository.succeeded.NextRunAt == nil || !repository.succeeded.NextRunAt.Equal(runAt.Add(time.Minute)) {
		t.Fatalf("expected next run to advance by one minute, got %+v", repository.succeeded.NextRunAt)
	}
	if len(repository.deliveryDeduplicationKeys) != 1 {
		t.Fatalf("expected one delivery enqueue, got %+v", repository.deliveryDeduplicationKeys)
	}
	expectedDeliveryDeduplicationKey := "schedule:schedule-1:occurrence:" + runAt.Format(time.RFC3339Nano)
	if repository.deliveryDeduplicationKeys[0] != expectedDeliveryDeduplicationKey {
		t.Fatalf("expected occurrence deduplication key %q, got %+v", expectedDeliveryDeduplicationKey, repository.deliveryDeduplicationKeys)
	}
}

func TestTaskSchedulePollerRetriedOccurrenceDoesNotDoubleEnqueue(t *testing.T) {
	runAt := time.Date(2026, 5, 6, 7, 0, 0, 0, time.UTC)
	nextRunAt := runAt
	repository := &pollerAtomicScheduleRepository{
		pollerScheduleRepository: &pollerScheduleRepository{taskSchedules: []task.TaskSchedule{{
			TaskScheduleID:   "schedule-1",
			CreatorPersonID:  "person-1",
			Prompt:           "매일 업계 뉴스를 조사해서 알려줘.",
			AgentProfileName: "default",
			Platform:         "mattermost",
			ConversationID:   "channel-1",
			ReplyTargetID:    "reply-target-1",
			TimeZone:         "Asia/Seoul",
			Kind:             task.TaskScheduleKindInterval,
			IntervalSecond:   60,
			MaxRunCount:      10,
			NextRunAt:        &nextRunAt,
		}}},
	}
	poller := TaskSchedulePoller{
		TaskScheduleRepository: repository,
		TaskScheduleRunner:     testTaskScheduleRunner("오늘의 조사 결과입니다."),
		PersonAccessResolver:   staticPersonAccessResolver{},
	}
	claimedTaskSchedule := repository.taskSchedules[0]

	firstError := poller.runTaskSchedule(context.Background(), claimedTaskSchedule, runAt)
	secondError := poller.runTaskSchedule(context.Background(), claimedTaskSchedule, runAt)

	if firstError != nil || secondError != nil {
		t.Fatalf("expected retry to be idempotent, first=%v second=%v", firstError, secondError)
	}
	if len(repository.deliveryDeduplicationKeys) != 1 {
		t.Fatalf("expected retried occurrence not to double-enqueue, got %+v", repository.deliveryDeduplicationKeys)
	}
}

func TestTaskSchedulePollerDoesNotRunExpiredSchedule(t *testing.T) {
	runAt := time.Date(2026, 5, 6, 7, 0, 0, 0, time.UTC)
	expiredAt := runAt.Add(-time.Minute)
	repository := &pollerScheduleRepository{taskSchedules: []task.TaskSchedule{{
		TaskScheduleID:  "schedule-expired",
		CreatorPersonID: "person-1",
		Prompt:          "만료된 알림",
		ExecutionMode:   task.TaskScheduleExecutionModeMessage,
		Platform:        "mattermost",
		ConversationID:  "channel-1",
		ReplyTargetID:   "reply-target-1",
		TimeZone:        "Asia/Seoul",
		Kind:            task.TaskScheduleKindInterval,
		IntervalSecond:  60,
		NextRunAt:       &runAt,
		ExpiresAt:       &expiredAt,
	}}}
	deliveryRepository := &pollerDeliveryRepository{}
	poller := TaskSchedulePoller{
		TaskScheduleRepository: repository,
		DeliveryRepository:     deliveryRepository,
		TaskRunService:         task.NewTaskRunService(task.NewTaskEventService()),
	}

	runCount, errorValue := poller.RunDue(context.Background(), runAt, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if runCount != 0 || len(deliveryRepository.replies) != 0 {
		t.Fatalf("expected expired schedule not to run, count=%d replies=%+v", runCount, deliveryRepository.replies)
	}
}

func TestTaskSchedulePollerLogsClaimErrors(t *testing.T) {
	var logBuffer bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	poller := TaskSchedulePoller{
		TaskScheduleRepository: &pollerScheduleRepository{
			claimError:    errors.New("database unavailable"),
			claimCallback: cancel,
		},
		Logger: slog.New(slog.NewTextHandler(&logBuffer, nil)),
	}

	poller.Start(ctx, time.Nanosecond)

	logDocument := logBuffer.String()
	if !strings.Contains(logDocument, "task_schedule.poller.failed") || !strings.Contains(logDocument, "database unavailable") {
		t.Fatalf("expected poller claim error log, got %q", logDocument)
	}
}

func TestTaskSchedulePollerLogsDeliveryFailures(t *testing.T) {
	var logBuffer bytes.Buffer
	runAt := time.Date(2026, 5, 6, 7, 0, 0, 0, time.UTC)
	repository := &pollerScheduleRepository{taskSchedules: []task.TaskSchedule{{
		TaskScheduleID:   "schedule-1",
		CreatorPersonID:  "person-1",
		Prompt:           "매일 업계 뉴스를 조사해서 알려줘.",
		AgentProfileName: "default",
		Platform:         "mattermost",
		ConversationID:   "channel-1",
		ReplyTargetID:    "reply-target-1",
		TimeZone:         "Asia/Seoul",
		Kind:             task.TaskScheduleKindCron,
		CronExpression:   "0 7 * * *",
		NextRunAt:        &runAt,
	}}}
	poller := TaskSchedulePoller{
		TaskScheduleRepository: repository,
		DeliveryRepository:     &pollerDeliveryRepository{errorValue: errors.New("outbox unavailable")},
		TaskScheduleRunner:     testTaskScheduleRunner("오늘의 조사 결과입니다."),
		Logger:                 slog.New(slog.NewTextHandler(&logBuffer, nil)),
	}

	runCount, errorValue := poller.RunDue(context.Background(), runAt, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if runCount != 0 {
		t.Fatalf("expected delivery failure not to count as completed run, got %d", runCount)
	}
	if repository.succeeded != nil {
		t.Fatalf("expected delivery failure not to advance schedule, got %+v", repository.succeeded)
	}
	if len(repository.failed) != 1 || !strings.Contains(repository.failed[0], "outbox unavailable") {
		t.Fatalf("expected delivery failure to mark schedule failed, got %+v", repository.failed)
	}
	logDocument := logBuffer.String()
	if !strings.Contains(logDocument, "task_schedule.reply.enqueue_failed") || !strings.Contains(logDocument, "schedule-1") || !strings.Contains(logDocument, "outbox unavailable") {
		t.Fatalf("expected delivery failure log, got %q", logDocument)
	}
}

func TestTaskSchedulePollerRejectsScheduledInteractionWithoutWaiting(t *testing.T) {
	repository := &pollerScheduleRepository{taskSchedules: []task.TaskSchedule{waitingTaskSchedule(time.Now().UTC())}}
	deliveryRepository := &pollerDeliveryRepository{}
	poller := TaskSchedulePoller{
		TaskScheduleRepository: repository,
		DeliveryRepository:     deliveryRepository,
		TaskScheduleRunner:     testTaskScheduleRunner(`{"action":"continue","toolName":"ask.confirm","toolInput":{"message":"확인이 필요해요."}}`),
		PersonAccessResolver:   staticPersonAccessResolver{},
	}

	_, errorValue := poller.RunDue(context.Background(), *repository.taskSchedules[0].NextRunAt, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(deliveryRepository.replies) != 0 {
		t.Fatalf("expected no waiting task reply, got %+v", deliveryRepository.replies)
	}
	if repository.succeeded != nil {
		t.Fatalf("expected interactive scheduled task not to advance, got %+v", repository.succeeded)
	}
	if len(repository.failed) != 1 {
		t.Fatalf("expected scheduled interaction to retry with backoff instead of waiting, got expired=%+v failed=%+v", repository.expired, repository.failed)
	}
	if len(repository.expired) != 0 {
		t.Fatalf("expected the schedule to survive a single blocked run, got %+v", repository.expired)
	}
}

func TestTaskSchedulePollerSkipsActiveRunForSameSchedule(t *testing.T) {
	runAt := time.Now().UTC()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "schedule:schedule-waiting", "already running")
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	repository := &pollerScheduleRepository{taskSchedules: []task.TaskSchedule{waitingTaskSchedule(runAt)}}
	poller := TaskSchedulePoller{
		TaskScheduleRepository: repository,
		DeliveryRepository:     &pollerDeliveryRepository{},
		TaskScheduleRunner:     testTaskScheduleRunner("should not run"),
		TaskRunService:         taskRunService,
		PersonAccessResolver:   staticPersonAccessResolver{},
	}

	runCount, errorValue := poller.RunDue(context.Background(), runAt, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if runCount != 0 {
		t.Fatalf("expected active schedule run to be skipped, got %d", runCount)
	}
	if repository.succeeded != nil || len(repository.failed) != 0 {
		t.Fatalf("expected no schedule state change, succeeded=%+v failed=%+v", repository.succeeded, repository.failed)
	}
}

func TestTaskSchedulePollerDoesNotDeliverFailedTaskReply(t *testing.T) {
	repository := &pollerScheduleRepository{taskSchedules: []task.TaskSchedule{waitingTaskSchedule(time.Now().UTC())}}
	deliveryRepository := &pollerDeliveryRepository{}
	poller := TaskSchedulePoller{
		TaskScheduleRepository: repository,
		DeliveryRepository:     deliveryRepository,
		TaskScheduleRunner:     testTaskScheduleRunner(`{"action":"fail","reason":"calendar unavailable"}`),
		PersonAccessResolver:   staticPersonAccessResolver{},
	}

	_, errorValue := poller.RunDue(context.Background(), *repository.taskSchedules[0].NextRunAt, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(deliveryRepository.replies) != 0 {
		t.Fatalf("expected no failed task reply, got %+v", deliveryRepository.replies)
	}
	if len(repository.failed) != 1 || !strings.Contains(repository.failed[0], "failed") {
		t.Fatalf("expected failed task status to retry with backoff, got expired=%+v failed=%+v", repository.expired, repository.failed)
	}
	if len(repository.expired) != 0 {
		t.Fatalf("expected transient task failure not to expire the schedule, got %+v", repository.expired)
	}
}

func TestTaskSchedulePollerExpiresOneTimeMessageWithInvalidDeliveryTarget(t *testing.T) {
	runAt := time.Now().UTC().Add(-time.Minute)
	repository := &pollerScheduleRepository{taskSchedules: []task.TaskSchedule{{
		TaskScheduleID:   "schedule-once",
		CreatorPersonID:  "person-1",
		Prompt:           "알림입니다.",
		ExecutionMode:    task.TaskScheduleExecutionModeMessage,
		AgentProfileName: "default",
		Platform:         "mattermost",
		ConversationID:   "channel-1",
		TimeZone:         "Asia/Seoul",
		Kind:             task.TaskScheduleKindOnce,
		RunAt:            &runAt,
		NextRunAt:        &runAt,
	}}}
	poller := TaskSchedulePoller{
		TaskScheduleRepository: repository,
		DeliveryRepository:     &pollerDeliveryRepository{},
		TaskRunService:         task.NewTaskRunService(task.NewTaskEventService()),
		PersonAccessResolver:   staticPersonAccessResolver{},
	}

	_, errorValue := poller.RunDue(context.Background(), runAt, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(repository.expired) != 1 || repository.expired[0].NextRunAt != nil {
		t.Fatalf("expected invalid one-time message schedule to expire, got %+v", repository.expired)
	}
	if len(repository.failed) != 0 {
		t.Fatalf("expected invalid one-time message schedule not to retry, got %+v", repository.failed)
	}
}

func TestTaskSchedulePollerCancelsStaleScheduledTaskRuns(t *testing.T) {
	referenceTime := time.Now().UTC().Add(time.Hour)
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "schedule:schedule-1", "stale schedule")
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	poller := TaskSchedulePoller{
		TaskScheduleRepository: &pollerScheduleRepository{},
		TaskRunService:         taskRunService,
		StaleTaskRunTimeout:    time.Minute,
	}

	_, errorValue := poller.RunDue(context.Background(), referenceTime, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	cancelledTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if !isFound || cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected stale scheduled task run to be cancelled, got found=%v run=%+v", isFound, cancelledTaskRun)
	}
}

type pollerScheduleRepository struct {
	taskSchedules []task.TaskSchedule
	succeeded     *task.TaskSchedule
	failed        []string
	expired       []task.TaskSchedule
	claimError    error
	claimCallback func()
}

func (repository *pollerScheduleRepository) UpsertTaskSchedule(taskSchedule task.TaskSchedule) error {
	repository.taskSchedules = append(repository.taskSchedules, taskSchedule)
	return nil
}

func (repository *pollerScheduleRepository) UpdateTaskSchedule(request task.TaskScheduleUpdateRequest) (task.TaskScheduleUpdateResult, error) {
	for index, taskSchedule := range repository.taskSchedules {
		if taskSchedule.TaskScheduleID != request.TaskScheduleID || taskSchedule.CreatorPersonID != request.RequesterPersonID || taskSchedule.NextRunAt == nil {
			continue
		}
		updatedTaskSchedule := taskSchedule
		var errorValue error
		if request.UpdateTaskSchedule != nil {
			updatedTaskSchedule, errorValue = request.UpdateTaskSchedule(taskSchedule)
			if errorValue != nil {
				return task.TaskScheduleUpdateResult{}, errorValue
			}
		}
		repository.taskSchedules[index] = updatedTaskSchedule
		return task.TaskScheduleUpdateResult{TaskSchedule: updatedTaskSchedule, IsFound: true}, nil
	}
	return task.TaskScheduleUpdateResult{}, nil
}

func (repository *pollerScheduleRepository) ClaimDueTaskSchedules(limit int, _ time.Duration, referenceTime time.Time, _ string) ([]task.TaskSchedule, error) {
	if repository.claimCallback != nil {
		repository.claimCallback()
	}
	if repository.claimError != nil {
		return nil, repository.claimError
	}
	dueTaskSchedules := []task.TaskSchedule{}
	for _, taskSchedule := range repository.taskSchedules {
		if (task.TaskScheduler{}).IsTaskScheduleDue(taskSchedule, referenceTime) {
			dueTaskSchedules = append(dueTaskSchedules, taskSchedule)
		}
	}
	if limit <= 0 || limit > len(dueTaskSchedules) {
		limit = len(dueTaskSchedules)
	}
	return append([]task.TaskSchedule{}, dueTaskSchedules[:limit]...), nil
}

func (repository *pollerScheduleRepository) ListTaskSchedules(task.TaskScheduleListRequest) (task.TaskScheduleListResult, error) {
	return task.TaskScheduleListResult{}, nil
}

func (repository *pollerScheduleRepository) MarkTaskScheduleSucceeded(taskSchedule task.TaskSchedule) error {
	repository.succeeded = &taskSchedule
	for index, existingTaskSchedule := range repository.taskSchedules {
		if existingTaskSchedule.TaskScheduleID == taskSchedule.TaskScheduleID {
			repository.taskSchedules[index] = taskSchedule
			break
		}
	}
	return nil
}

func (repository *pollerScheduleRepository) MarkTaskScheduleFailed(_ task.TaskSchedule, errorMessage string, _ time.Time) error {
	repository.failed = append(repository.failed, errorMessage)
	return nil
}

func (repository *pollerScheduleRepository) ExpireTaskSchedule(taskSchedule task.TaskSchedule, errorMessage string, referenceTime time.Time) error {
	taskSchedule.ExpiresAt = &referenceTime
	taskSchedule.NextRunAt = nil
	taskSchedule.LastError = errorMessage
	repository.expired = append(repository.expired, taskSchedule)
	return nil
}

func (repository *pollerScheduleRepository) CancelTaskSchedules(task.TaskScheduleCancelRequest) (task.TaskScheduleCancelResult, error) {
	return task.TaskScheduleCancelResult{}, nil
}

type pollerDeliveryRepository struct {
	replies    []connectors.OutboundReply
	errorValue error
}

type pollerTaskIntakeGate struct {
	isQuiesced bool
}

func (gate pollerTaskIntakeGate) IsQuiesced() bool {
	return gate.isQuiesced
}

func (repository *pollerDeliveryRepository) EnqueueScheduledConnectorReply(_ task.TaskSchedule, _ string, reply connectors.OutboundReply) (string, error) {
	if repository.errorValue != nil {
		return "", repository.errorValue
	}
	repository.replies = append(repository.replies, reply)
	return "outbox-1", nil
}

type pollerAtomicScheduleRepository struct {
	*pollerScheduleRepository
	deliveryDeduplicationKeys []string
	deliveryError             error
}

func (repository *pollerAtomicScheduleRepository) MarkTaskScheduleSucceededAndEnqueueDelivery(taskSchedule task.TaskSchedule, _ string, deliveryDeduplicationKey string, _ connectors.OutboundReply) (string, error) {
	if repository.deliveryError != nil {
		return "", repository.deliveryError
	}
	if !repository.hasDeliveryDeduplicationKey(deliveryDeduplicationKey) {
		repository.deliveryDeduplicationKeys = append(repository.deliveryDeduplicationKeys, deliveryDeduplicationKey)
	}
	if errorValue := repository.MarkTaskScheduleSucceeded(taskSchedule); errorValue != nil {
		return "", errorValue
	}
	return deliveryDeduplicationKey, nil
}

func (repository *pollerAtomicScheduleRepository) hasDeliveryDeduplicationKey(deliveryDeduplicationKey string) bool {
	for _, existingDeliveryDeduplicationKey := range repository.deliveryDeduplicationKeys {
		if existingDeliveryDeduplicationKey == deliveryDeduplicationKey {
			return true
		}
	}
	return false
}

type staticPersonAccessResolver struct{}

func (staticPersonAccessResolver) ResolvePersonAccess(personID string) policy.PersonAccess {
	return policy.PersonAccess{PersonID: personID, SecurityLevelRank: 100, GrantedClasses: []string{"internal"}}
}

func testTaskScheduleRunner(content string) agentruntime.TaskScheduleRunner {
	return testTaskScheduleRunnerWithResponseCount(content, nil)
}

func testTaskScheduleRunnerWithResponseCount(content string, generatedResponseCount *int) agentruntime.TaskScheduleRunner {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := bluecollar.NewAgentKernel(taskRunService, task.NewTaskStepService())
	languageModel := staticPollerLanguageModel{content: content, generatedResponseCount: generatedResponseCount}
	agentKernel.UseLanguageModelProvider(languageModel)
	agentKernel.UseIntakeLanguageModelProvider(languageModel)
	agentKernel.UseIntakeOptions(bluecollar.IntakeOptions{IsEnabled: true})
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"ask.confirm"})
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	return agentruntime.NewTaskScheduleRunner(agentruntime.NewTaskLauncher(agentKernel, toolCatalogBuilder))
}

func waitingTaskSchedule(runAt time.Time) task.TaskSchedule {
	return task.TaskSchedule{
		TaskScheduleID:   "schedule-waiting",
		CreatorPersonID:  "person-1",
		Prompt:           "내 스케줄을 보고 확인이 필요한 일을 알려줘.",
		AgentProfileName: "default",
		Platform:         "mattermost",
		ConversationID:   "channel-1",
		ReplyTargetID:    "reply-target-1",
		TimeZone:         "Asia/Seoul",
		Kind:             task.TaskScheduleKindOnce,
		RunAt:            &runAt,
		NextRunAt:        &runAt,
	}
}

type staticPollerLanguageModel struct {
	content                string
	generatedResponseCount *int
}

func (languageModel staticPollerLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel staticPollerLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if languageModel.generatedResponseCount != nil {
		*languageModel.generatedResponseCount++
	}
	if request.StructuredOutputSchema.Name == "blueclaw_turn_router" {
		return llm.StructuredResponse{Content: `{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"medium","estimatedMinutes":10,"requestedOutputFormats":null,"expectedResults":[],"requiredEvidence":[],"siteRequestEvidence":"","responseLanguage":"ko","reason":"scheduled objective","userFacingReply":"","initialToolNames":[],"priorTaskReference":"none"}`}, nil
	}
	if strings.HasPrefix(languageModel.content, "{") {
		return llm.StructuredResponse{Content: languageModel.content}, nil
	}
	return llm.StructuredResponse{Content: `{"action":"finish","message":"` + languageModel.content + `","replyParts":[{"type":"text","text":"` + languageModel.content + `"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[],"qualityReview":[]}`}, nil
}

func TestRecordTaskScheduleFailureExpiresAfterRepeatedFailures(t *testing.T) {
	taskSchedule := waitingTaskSchedule(time.Now().UTC())
	taskSchedule.FailureCount = maxTaskScheduleFailureCount - 1
	if !taskScheduleFailureIsTerminal(taskSchedule, errors.New("transient"), time.Now()) {
		t.Fatal("expected the failure cap to expire the schedule")
	}
	taskSchedule.FailureCount = 0
	if taskScheduleFailureIsTerminal(taskSchedule, errors.New("transient"), time.Now()) {
		t.Fatal("expected a first transient failure to stay retryable")
	}
}
