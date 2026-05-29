package integration

import (
	"context"
	"testing"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/agentruntime"
	"blueclaw/internal/llm"
	"blueclaw/internal/policy"
	"blueclaw/internal/task"
)

func TestCronScheduleRunsDailyResearchPromptAndAdvancesToNextDay(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := agent.NewAgentKernel(taskRunService, task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(staticScheduleLanguageModel{content: scheduleFinishMessage("오늘 조사한 핵심 변화는 세 가지입니다.")})
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"memory.search"},
	}, nil)
	runAt := time.Date(2026, 5, 6, 9, 0, 0, 0, time.UTC)
	nextRunAt := runAt

	result, errorValue := agentruntime.NewTaskScheduleRunner(agentruntime.NewTaskLauncher(agentKernel, toolCatalogBuilder)).RunIfDue(context.Background(), agentruntime.TaskScheduleRunRequest{
		TaskSchedule: task.TaskSchedule{
			TaskScheduleID:   "schedule-daily-research",
			CreatorPersonID:  "person-1",
			Name:             "daily research brief",
			Prompt:           "매일 업계 뉴스를 조사해서 아침 9시에 핵심만 알려줘.",
			AgentProfileName: "default",
			Kind:             task.TaskScheduleKindCron,
			CronExpression:   "0 9 * * *",
			NextRunAt:        &nextRunAt,
		},
		ReferenceTime: runAt,
		PersonAccess:  policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100, GrantedClasses: []string{"internal"}},
		WorkspaceID:   "workspace-1",
	})
	if errorValue != nil {
		t.Fatalf("expected cron schedule run to succeed: %v", errorValue)
	}
	if !result.DidRun {
		t.Fatal("expected due daily research schedule to run")
	}
	if result.LaunchResult.TurnResult.FinishMessage != "오늘 조사한 핵심 변화는 세 가지입니다." {
		t.Fatalf("expected daily research reply, got %q", result.LaunchResult.TurnResult.FinishMessage)
	}
	if result.TaskSchedule.LastTaskRunID == "" {
		t.Fatalf("expected launched task run id, got %+v", result.TaskSchedule)
	}
	if result.TaskSchedule.LastRunAt == nil || !result.TaskSchedule.LastRunAt.Equal(runAt) {
		t.Fatalf("expected last run time %s, got %+v", runAt.Format(time.RFC3339), result.TaskSchedule.LastRunAt)
	}
	expectedNextRunAt := time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)
	if result.TaskSchedule.NextRunAt == nil || !result.TaskSchedule.NextRunAt.Equal(expectedNextRunAt) {
		t.Fatalf("expected next run time %s, got %+v", expectedNextRunAt.Format(time.RFC3339), result.TaskSchedule.NextRunAt)
	}
}

type staticScheduleLanguageModel struct {
	content string
}

func (languageModel staticScheduleLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel staticScheduleLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{Content: languageModel.content}, nil
}

func scheduleFinishMessage(reply string) string {
	return `{"action":"finish","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[],"finishMessage":"` + reply + `"}`
}
