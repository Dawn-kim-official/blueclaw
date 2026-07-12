package agentruntime

import (
	"context"
	"strings"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/policy"
	"blueclaw/internal/task"
)

func TestAmbientCaptureAllowedToolNamesClampsToTaskAndCalendar(t *testing.T) {
	allowed := map[string]bool{}
	for _, toolName := range ambientCaptureAllowedToolNames() {
		allowed[toolName] = true
	}

	for _, requiredToolName := range []string{"task.add", "task.update", "calendar.add", "ask.input"} {
		if !allowed[requiredToolName] {
			t.Fatalf("ambient capture palette must allow %q", requiredToolName)
		}
	}

	for _, forbiddenToolName := range []string{"terminal.run", "web.fetch", "file.write", "task.delete", "calendar.delete", "message.send"} {
		if allowed[forbiddenToolName] {
			t.Fatalf("ambient capture palette must not allow %q", forbiddenToolName)
		}
	}
}

func TestTaskLauncherKeepsFullToolSetForAddressedLaunchWithAmbientDutyMatch(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	launchResult := launchWithAmbientDutyMatch(t, taskEventService, true)

	for _, requiredToolName := range []string{"file.write", "terminal.run"} {
		if !containsString(launchResult.ToolNames, requiredToolName) {
			t.Fatalf("addressed launch must keep %q despite ambient duty match, got %+v", requiredToolName, launchResult.ToolNames)
		}
	}
	ambientDutyEvent := findTaskEvent(taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID), "agent.ambient_duty_launch")
	if ambientDutyEvent.Name == "" || !strings.Contains(ambientDutyEvent.Body, `"isAddressedToBot":true`) {
		t.Fatalf("expected ambient duty launch event to remain for observability, got %+v", ambientDutyEvent)
	}
}

func TestTaskLauncherCapsUnaddressedAmbientCaptureLaunch(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	launchResult := launchWithAmbientDutyMatch(t, taskEventService, false)

	allowed := map[string]bool{}
	for _, toolName := range ambientCaptureAllowedToolNames() {
		allowed[toolName] = true
	}
	for _, toolName := range launchResult.ToolNames {
		if !allowed[toolName] {
			t.Fatalf("ambient capture launch must stay within the capture palette, got %+v", launchResult.ToolNames)
		}
	}
	if !containsString(launchResult.ToolNames, "ask.input") {
		t.Fatalf("expected capture palette tools, got %+v", launchResult.ToolNames)
	}
	for _, forbiddenToolName := range []string{"file.write", "terminal.run"} {
		if containsString(launchResult.ToolNames, forbiddenToolName) {
			t.Fatalf("ambient capture launch must not expose %q, got %+v", forbiddenToolName, launchResult.ToolNames)
		}
	}
}

func TestAmbientCaptureDutyForLaunchClearsDutyForAddressedRequests(t *testing.T) {
	dutyMatch := agent.AmbientDutyContext{IsMatch: true, Name: "team_flow_update", Confidence: 0.8}

	if ambientCaptureDutyForLaunch(TaskLaunchRequest{AmbientDuty: dutyMatch, IsAddressedToBot: true}).IsMatch {
		t.Fatal("addressed launch must not carry the ambient capture duty into the turn")
	}
	if !ambientCaptureDutyForLaunch(TaskLaunchRequest{AmbientDuty: dutyMatch}).IsMatch {
		t.Fatal("unaddressed launch must keep the ambient capture duty")
	}
}

func launchWithAmbientDutyMatch(t *testing.T, taskEventService *task.TaskEventService, isAddressedToBot bool) TaskLaunchResult {
	t.Helper()
	agentKernel := agent.NewAgentKernel(task.NewTaskRunService(taskEventService), task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(staticRuntimeLanguageModel{content: runtimeFinishMessage("done")})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"file.write", "terminal.run", "ask.input", "conversation.history"},
	}, nil)

	launchResult, errorValue := NewTaskLauncher(agentKernel, toolCatalogBuilder).Launch(context.Background(), TaskLaunchRequest{
		Source:                    TaskLaunchSourceConnector,
		SourceReference:           "mattermost:post-1",
		RequesterPersonID:         "person-1",
		ProfileName:               "default",
		ConversationID:            "channel-1",
		Prompt:                    "회사 소개 페이지를 html 파일로 만들어서 첨부해줘",
		HistoryProvider:           staticHistoryProvider{},
		AmbientDuty:               agent.AmbientDutyContext{IsMatch: true, Name: "team_flow_update", Confidence: 0.8},
		IsAddressedToBot:          isAddressedToBot,
		PersonAccess:              policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
		AccessibleConversationIDs: []string{"channel-1"},
	})
	if errorValue != nil {
		t.Fatalf("expected launch to succeed: %v", errorValue)
	}
	return launchResult
}
