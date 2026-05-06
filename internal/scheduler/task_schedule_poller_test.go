package scheduler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/agentruntime"
	"blueclaw/internal/connectors"
	"blueclaw/internal/llm"
	"blueclaw/internal/policy"
	"blueclaw/internal/task"
)

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
	if repository.succeeded == nil || repository.succeeded.LastTaskRunID == "" {
		t.Fatalf("expected schedule success with task run id, got %+v", repository.succeeded)
	}
	if repository.succeeded.NextRunAt == nil || !repository.succeeded.NextRunAt.After(runAt) {
		t.Fatalf("expected schedule to advance, got %+v", repository.succeeded.NextRunAt)
	}
}

func TestTaskSchedulePollerRetriesFailedDeliveryWithoutAdvancing(t *testing.T) {
	runAt := time.Date(2026, 5, 6, 7, 0, 0, 0, time.UTC)
	nextRunAt := runAt
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
		NextRunAt:        &nextRunAt,
	}}}
	poller := TaskSchedulePoller{
		TaskScheduleRepository: repository,
		DeliveryRepository:     &pollerDeliveryRepository{errorValue: errors.New("outbox unavailable")},
		TaskScheduleRunner:     testTaskScheduleRunner("오늘의 조사 결과입니다."),
		PersonAccessResolver:   staticPersonAccessResolver{},
	}

	runCount, errorValue := poller.RunDue(context.Background(), runAt, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if runCount != 0 {
		t.Fatalf("expected failed delivery not to count as run, got %d", runCount)
	}
	if repository.succeeded != nil {
		t.Fatalf("expected schedule not to advance, got %+v", repository.succeeded)
	}
	if len(repository.failed) != 1 || !strings.Contains(repository.failed[0], "outbox unavailable") {
		t.Fatalf("expected delivery failure to be recorded, got %+v", repository.failed)
	}
}

func TestTaskSchedulePollerEnqueuesWaitingTaskReply(t *testing.T) {
	repository := &pollerScheduleRepository{taskSchedules: []task.TaskSchedule{waitingTaskSchedule(time.Now().UTC())}}
	deliveryRepository := &pollerDeliveryRepository{}
	poller := TaskSchedulePoller{
		TaskScheduleRepository: repository,
		DeliveryRepository:     deliveryRepository,
		TaskScheduleRunner:     testTaskScheduleRunner(`{"action":"call_tool","toolName":"approval.request","toolInput":{"message":"확인이 필요해요."}}`),
		PersonAccessResolver:   staticPersonAccessResolver{},
	}

	_, errorValue := poller.RunDue(context.Background(), *repository.taskSchedules[0].NextRunAt, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(deliveryRepository.replies) != 1 || !strings.Contains(deliveryRepository.replies[0].Message, "확인이 필요해요") {
		t.Fatalf("expected waiting task reply, got %+v", deliveryRepository.replies)
	}
	if repository.succeeded == nil {
		t.Fatal("expected waiting scheduled task to advance after notifying the user")
	}
}

type pollerScheduleRepository struct {
	taskSchedules []task.TaskSchedule
	succeeded     *task.TaskSchedule
	failed        []string
}

func (repository *pollerScheduleRepository) UpsertTaskSchedule(taskSchedule task.TaskSchedule) error {
	repository.taskSchedules = append(repository.taskSchedules, taskSchedule)
	return nil
}

func (repository *pollerScheduleRepository) ClaimDueTaskSchedules(limit int, _ time.Duration, _ time.Time, _ string) ([]task.TaskSchedule, error) {
	if limit <= 0 || limit > len(repository.taskSchedules) {
		limit = len(repository.taskSchedules)
	}
	return append([]task.TaskSchedule{}, repository.taskSchedules[:limit]...), nil
}

func (repository *pollerScheduleRepository) MarkTaskScheduleSucceeded(taskSchedule task.TaskSchedule) error {
	repository.succeeded = &taskSchedule
	return nil
}

func (repository *pollerScheduleRepository) MarkTaskScheduleFailed(_ task.TaskSchedule, errorMessage string, _ time.Time) error {
	repository.failed = append(repository.failed, errorMessage)
	return nil
}

type pollerDeliveryRepository struct {
	replies    []connectors.OutboundReply
	errorValue error
}

func (repository *pollerDeliveryRepository) EnqueueScheduledConnectorReply(_ task.TaskSchedule, _ string, reply connectors.OutboundReply) (string, error) {
	if repository.errorValue != nil {
		return "", repository.errorValue
	}
	repository.replies = append(repository.replies, reply)
	return "outbox-1", nil
}

type staticPersonAccessResolver struct{}

func (staticPersonAccessResolver) ResolvePersonAccess(personID string) policy.PersonAccess {
	return policy.PersonAccess{PersonID: personID, SecurityLevelRank: 100, GrantedClasses: []string{"internal"}}
}

func testTaskScheduleRunner(content string) agentruntime.TaskScheduleRunner {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := agent.NewAgentKernel(taskRunService, task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(staticPollerLanguageModel{content: content})
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"approval.request"})
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
	content string
}

func (languageModel staticPollerLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel staticPollerLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if strings.HasPrefix(languageModel.content, "{") {
		return llm.StructuredResponse{Content: languageModel.content}, nil
	}
	return llm.StructuredResponse{Content: `{"action":"final_reply","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[],"finalReply":"` + languageModel.content + `"}`}, nil
}
