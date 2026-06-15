package agentruntime

import (
	"context"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/llm"
	"blueclaw/internal/policy"
	"blueclaw/internal/task"
)

func TestTaskScheduleRunnerLaunchesDueSchedule(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := agent.NewAgentKernel(taskRunService, task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(staticRuntimeLanguageModel{content: runtimeFinishMessage("scheduled done")})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"memory.search"},
	}, nil)
	provisioner := &recordingRequesterWorkspaceProvisioner{}
	taskLauncher := NewTaskLauncher(agentKernel, toolCatalogBuilder)
	taskLauncher.UseRequesterWorkspaceProvisioner(provisioner)
	runAt := time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC)

	result, errorValue := NewTaskScheduleRunner(taskLauncher).RunIfDue(context.Background(), TaskScheduleRunRequest{
		TaskSchedule: task.TaskSchedule{
			TaskScheduleID:  "schedule-1",
			CreatorPersonID: "person-1",
			Prompt:          "daily brief",
			Kind:            task.TaskScheduleKindOnce,
			RunAt:           &runAt,
			NextRunAt:       &runAt,
		},
		ReferenceTime: runAt,
		PersonAccess:  policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
		WorkspaceID:   "workspace-1",
	})
	if errorValue != nil {
		t.Fatalf("expected schedule run to succeed: %v", errorValue)
	}
	if !result.DidRun {
		t.Fatal("expected due schedule to run")
	}
	if result.TaskSchedule.LastTaskRunID == "" {
		t.Fatalf("expected last task run id, got %+v", result.TaskSchedule)
	}
	if provisioner.callCount != 1 {
		t.Fatalf("expected scheduled launch to provision requester workspace, got %d calls", provisioner.callCount)
	}
	if result.TaskSchedule.LastRunAt == nil || !result.TaskSchedule.LastRunAt.Equal(runAt) {
		t.Fatalf("expected last run time, got %+v", result.TaskSchedule.LastRunAt)
	}
	if result.TaskSchedule.NextRunAt != nil {
		t.Fatalf("expected one-time schedule to complete, got next run %+v", result.TaskSchedule.NextRunAt)
	}

	taskEvents := taskEventService.ListTaskEvent(result.LaunchResult.TurnResult.TaskRun.TaskRunID)
	if !containsTaskEvent(taskEvents, "agent.task_launched") {
		t.Fatalf("expected scheduled launch event, got %+v", taskEvents)
	}
}

func TestTaskScheduleRunnerAddsCronContextToLaunch(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := agent.NewAgentKernel(taskRunService, task.NewTaskStepService())
	languageModel := &capturingScheduleRuntimeLanguageModel{content: runtimeFinishMessage("scheduled done")}
	agentKernel.UseLanguageModelProvider(languageModel)
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"memory.search"},
	}, nil)
	taskLauncher := NewTaskLauncher(agentKernel, toolCatalogBuilder)
	nextRunAt := time.Date(2026, 6, 15, 23, 0, 0, 0, time.UTC)

	result, errorValue := NewTaskScheduleRunner(taskLauncher).RunIfDue(context.Background(), TaskScheduleRunRequest{
		TaskSchedule: task.TaskSchedule{
			TaskScheduleID:    "schedule-briefing",
			CreatorPersonID:   "person-1",
			Name:              "일일 브리핑",
			Prompt:            "오늘의 주요 일정, 날씨, 할 일을 브리핑한다.",
			Kind:              task.TaskScheduleKindCron,
			CronExpression:    "0 8 * * *",
			TimeZone:          "Asia/Seoul",
			NextRunAt:         &nextRunAt,
			CompletedRunCount: 3,
		},
		ReferenceTime: nextRunAt.Add(29 * time.Second),
		PersonAccess:  policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
		WorkspaceID:   "workspace-1",
	})
	if errorValue != nil {
		t.Fatalf("expected schedule run to succeed: %v", errorValue)
	}
	if !result.DidRun {
		t.Fatal("expected due schedule to run")
	}

	requestText := structuredRequestMessagesText(languageModel.requests)
	for _, expected := range []string{
		"Scheduled run:",
		"Scheduled task instruction:",
		`"scheduleID":"schedule-briefing"`,
		`"kind":"cron"`,
		`"cadence":"daily at 08:00 Asia/Seoul"`,
		`"cronExpression":"0 8 * * *"`,
		`"occurrenceAt":"2026-06-16T08:00:00+09:00"`,
		`"completedRunCount":3`,
	} {
		if !strings.Contains(requestText, expected) {
			t.Fatalf("expected launch request to include %q, got %s", expected, requestText)
		}
	}

	taskLaunchEvent := findTaskEvent(taskEventService.ListTaskEvent(result.LaunchResult.TurnResult.TaskRun.TaskRunID), "agent.task_launched")
	if !strings.Contains(taskLaunchEvent.Body, `"scheduledRun"`) || !strings.Contains(taskLaunchEvent.Body, `"scheduleID":"schedule-briefing"`) {
		t.Fatalf("expected task launch event to include scheduled run context, got %s", taskLaunchEvent.Body)
	}
}

type capturingScheduleRuntimeLanguageModel struct {
	content  string
	requests []llm.StructuredResponseRequest
}

func (languageModel *capturingScheduleRuntimeLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *capturingScheduleRuntimeLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	languageModel.requests = append(languageModel.requests, request)
	return llm.StructuredResponse{Content: languageModel.content}, nil
}

func structuredRequestMessagesText(requests []llm.StructuredResponseRequest) string {
	lines := []string{}
	for _, request := range requests {
		for _, message := range request.Messages {
			lines = append(lines, message.Content)
			for _, part := range message.Parts {
				lines = append(lines, part.Text)
			}
		}
	}
	return strings.Join(lines, "\n")
}
