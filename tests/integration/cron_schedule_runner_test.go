package integration

import (
	"context"
	"testing"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/agent"
	"github.com/Dawn-kim-official/blueclaw/internal/agentruntime"
	"github.com/Dawn-kim-official/blueclaw/internal/llm"
	"github.com/Dawn-kim-official/blueclaw/internal/policy"
	"github.com/Dawn-kim-official/blueclaw/internal/task"
)

func TestCronScheduleRunsDailyResearchPromptAndAdvancesToNextDay(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := agent.NewAgentKernel(taskRunService, task.NewTaskStepService())
	useScheduleTestLanguageModel(agentKernel, staticScheduleLanguageModel{content: scheduleFinishMessage("Today's research surfaced three key changes.")})
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
			Prompt:           "research the industry news every day and give me the highlights at 9am.",
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
	if result.LaunchResult.TurnResult.FinishMessage != "Today's research surfaced three key changes." {
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

func (languageModel staticScheduleLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == "blueclaw_turn_router" {
		return llm.StructuredResponse{Content: scheduleTurnRouterResponse()}, nil
	}
	return llm.StructuredResponse{Content: languageModel.content}, nil
}

func scheduleTurnRouterResponse() string {
	return `{"route":"answer_question","classification":"quick_reply","taskShape":"immediate_reply","level":"xlow","estimatedMinutes":1,"requestedOutputFormats":null,"responseLanguage":"ko","reason":"scheduled run","userFacingReply":""}`
}

func useScheduleTestLanguageModel(agentKernel *agent.AgentKernel, languageModel staticScheduleLanguageModel) {
	agentKernel.UseLanguageModelProvider(languageModel)
	agentKernel.UseIntakeLanguageModelProvider(languageModel)
	agentKernel.UseIntakeOptions(agent.IntakeOptions{IsEnabled: true})
}

func scheduleFinishMessage(reply string) string {
	return `{"action":"finish","message":"` + reply + `","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[],"qualityReview":[]}`
}
