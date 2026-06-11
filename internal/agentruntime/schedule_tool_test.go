package agentruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/capability"
	"blueclaw/internal/task"
)

func TestScheduleCreateToolStoresCurrentReplyTarget(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "schedule.create",
		Input: agent.MarshalToolInput(map[string]any{
			"name":            "daily research",
			"taskInstruction": "중요한 업계 뉴스를 조사해서 알려줘.",
			"kind":            "cron",
			"cronExpression":  "0 7 * * *",
			"repeatPolicy":    "unbounded",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule.create success, got %s", result.ContentText())
	}
	if len(repository.taskSchedules) != 1 {
		t.Fatalf("expected one schedule, got %+v", repository.taskSchedules)
	}
	taskSchedule := repository.taskSchedules[0]
	if taskSchedule.Platform != "mattermost" || taskSchedule.ConversationID != "channel-1" || taskSchedule.ReplyTargetID != "reply-target-1" {
		t.Fatalf("expected current reply target to be stored, got %+v", taskSchedule)
	}
	if taskSchedule.Prompt != "중요한 업계 뉴스를 조사해서 알려줘." {
		t.Fatalf("expected stored task instruction without cadence, got %q", taskSchedule.Prompt)
	}
	if taskSchedule.ExecutionMode != task.TaskScheduleExecutionModeAgent {
		t.Fatalf("expected agent execution mode, got %+v", taskSchedule)
	}
	if taskSchedule.TimeZone != "Asia/Seoul" || taskSchedule.NextRunAt == nil {
		t.Fatalf("expected default timezone and next run, got %+v", taskSchedule)
	}
}

func TestScheduleCreateSchemaUsesTaskInstruction(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	toolDefinition, isFound := findToolDefinition(toolRegistry.ListToolDefinitions(), "schedule.create")
	if !isFound {
		t.Fatal("expected schedule.create definition")
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if errorValue := json.Unmarshal(toolDefinition.InputSchema, &schema); errorValue != nil {
		t.Fatal(errorValue)
	}
	if !containsString(schema.Required, "taskInstruction") {
		t.Fatalf("expected taskInstruction to be required, got %+v", schema.Required)
	}
	for _, hiddenField := range []string{"prompt", "message", "schedule", "executionMode"} {
		if _, isFound := schema.Properties[hiddenField]; isFound {
			t.Fatalf("expected %s to stay out of schedule.create model-facing schema, got %+v", hiddenField, schema.Properties)
		}
	}
}

func TestScheduleCreateToolStoresTaskInstructionAsAgentTask(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "schedule.create",
		Input: agent.MarshalToolInput(map[string]any{
			"taskInstruction": "죄송합니다라고 말해줘.",
			"kind":            "interval",
			"intervalSecond":  60,
			"maxRunCount":     10,
			"repeatPolicy":    "finite",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule.create success, got %s", result.ContentText())
	}
	if len(repository.taskSchedules) != 1 {
		t.Fatalf("expected one schedule, got %+v", repository.taskSchedules)
	}
	if repository.taskSchedules[0].ExecutionMode != task.TaskScheduleExecutionModeAgent {
		t.Fatalf("expected agent execution mode, got %+v", repository.taskSchedules[0])
	}
	if repository.taskSchedules[0].Prompt != "죄송합니다라고 말해줘." {
		t.Fatalf("expected task instruction to be stored, got %+v", repository.taskSchedules[0])
	}
	if !strings.Contains(result.ContentText(), `"taskInstruction":"죄송합니다라고 말해줘."`) || strings.Contains(result.ContentText(), `"prompt"`) {
		t.Fatalf("expected tool result to expose taskInstruction without prompt, got %s", result.ContentText())
	}
	if repository.taskSchedules[0].ExpiresAt != nil {
		t.Fatalf("expected schedule expiration to default to nil, got %+v", repository.taskSchedules[0])
	}
}

func TestScheduleCreateToolRejectsBoundedRepeatWithoutFiniteBound(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		Prompt:            "한 시간마다 노래 가사 한 줄을 오늘 18시까지만 보내줘",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "schedule.create",
		Input: agent.MarshalToolInput(map[string]any{
			"taskInstruction": "노래 가사 한 줄을 지어서 DM으로 보내세요.",
			"kind":            "interval",
			"intervalSecond":  3600,
			"repeatPolicy":    "finite",
			"timeZone":        "Asia/Seoul",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || !strings.Contains(result.ContentText(), "expiresAt or maxRunCount") {
		t.Fatalf("expected finite bound failure, got %s", result.ContentText())
	}
	if len(repository.taskSchedules) != 0 {
		t.Fatalf("expected no schedule to be created, got %+v", repository.taskSchedules)
	}
}

func TestScheduleCreateToolStoresExpiresAtForBoundedRepeat(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	expiresAt := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		Prompt:            "한 시간마다 노래 가사 한 줄을 오늘 18시까지만 보내줘",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "schedule.create",
		Input: agent.MarshalToolInput(map[string]any{
			"taskInstruction": "노래 가사 한 줄을 지어서 DM으로 보내세요.",
			"kind":            "interval",
			"intervalSecond":  3600,
			"expiresAt":       expiresAt,
			"repeatPolicy":    "finite",
			"timeZone":        "Asia/Seoul",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule.create success, got %s", result.ContentText())
	}
	if len(repository.taskSchedules) != 1 || repository.taskSchedules[0].ExpiresAt == nil {
		t.Fatalf("expected one expiring schedule, got %+v", repository.taskSchedules)
	}
	if !strings.Contains(result.ContentText(), `"expiresAt"`) || !strings.Contains(result.ContentText(), `"nextRunAt"`) {
		t.Fatalf("expected schedule result to include timing fields, got %s", result.ContentText())
	}
}

func TestScheduledToolSetDoesNotExposeInteractiveTools(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{{
		Name:        "user.confirm",
		Description: "Ask the user to confirm",
	}})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"ask.confirm", "ask.choice", "ask.input", "user.confirm", "schedule.create"})

	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default", IsScheduledRun: true})

	for _, toolName := range []string{"ask.confirm", "ask.choice", "ask.input", "user.confirm", "schedule.create"} {
		if toolRegistry.IsRegistered(toolName) || toolRegistry.IsAllowed(toolName) {
			t.Fatalf("expected scheduled run not to register interactive tool %s", toolName)
		}
	}
}

func TestScheduleCancelToolCancelsRequesterSchedules(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Minute)
	repository := &memoryTaskScheduleRepository{taskSchedules: []task.TaskSchedule{{
		TaskScheduleID:   "schedule-owned",
		CreatorPersonID:  "person-1",
		ConversationID:   "channel-1",
		Prompt:           "owned",
		Kind:             task.TaskScheduleKindInterval,
		IntervalSecond:   60,
		NextRunAt:        &nextRunAt,
		ExpiresAt:        timePointer(nextRunAt.Add(time.Hour)),
		AgentProfileName: "default",
	}, {
		TaskScheduleID:   "schedule-other",
		CreatorPersonID:  "person-2",
		ConversationID:   "channel-1",
		Prompt:           "other",
		Kind:             task.TaskScheduleKindInterval,
		IntervalSecond:   60,
		NextRunAt:        &nextRunAt,
		ExpiresAt:        timePointer(nextRunAt.Add(time.Hour)),
		AgentProfileName: "default",
	}}}
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	waitingTaskRun := taskRunService.CreateTaskRun("person-1", "channel-1", "승인 필요")
	if _, errorValue := taskRunService.PauseTaskRun(waitingTaskRun.TaskRunID, task.TaskStatusWaitingApproval, "approval"); errorValue != nil {
		t.Fatal(errorValue)
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.cancel"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "channel-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "schedule.cancel",
		Input: agent.MarshalToolInput(map[string]any{
			"scope": "mine",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule.cancel success, got %s", result.ContentText())
	}
	if !strings.Contains(result.ContentText(), `"cancelledScheduleCount":1`) || !strings.Contains(result.ContentText(), `"cancelledWaitCount":1`) {
		t.Fatalf("expected one schedule and one wait cancelled, got %s", result.ContentText())
	}
	ownedSchedule := repository.taskSchedules[1]
	if ownedSchedule.TaskScheduleID != "schedule-owned" || ownedSchedule.NextRunAt != nil || ownedSchedule.ExpiresAt == nil || ownedSchedule.ExpiresAt.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("expected owned schedule to expire, got %+v", ownedSchedule)
	}
	otherSchedule := repository.taskSchedules[0]
	if otherSchedule.TaskScheduleID != "schedule-other" || otherSchedule.NextRunAt == nil {
		t.Fatalf("expected other schedule to remain active, got %+v", otherSchedule)
	}
	cancelledTaskRun, isFound := taskRunService.FindTaskRun(waitingTaskRun.TaskRunID)
	if !isFound || cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected waiting task run to be cancelled, got found=%v task=%+v", isFound, cancelledTaskRun)
	}
}

func TestScheduleCancelToolCancelsCurrentConversationDeliverySchedules(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Minute)
	repository := &memoryTaskScheduleRepository{taskSchedules: []task.TaskSchedule{{
		TaskScheduleID:   "schedule-delivered",
		CreatorPersonID:  "person-creator",
		ConversationID:   "dm-recipient",
		Prompt:           "spam",
		Kind:             task.TaskScheduleKindInterval,
		IntervalSecond:   60,
		NextRunAt:        &nextRunAt,
		ExpiresAt:        timePointer(nextRunAt.Add(time.Hour)),
		AgentProfileName: "default",
	}, {
		TaskScheduleID:   "schedule-other-conversation",
		CreatorPersonID:  "person-creator",
		ConversationID:   "dm-other",
		Prompt:           "other",
		Kind:             task.TaskScheduleKindInterval,
		IntervalSecond:   60,
		NextRunAt:        &nextRunAt,
		ExpiresAt:        timePointer(nextRunAt.Add(time.Hour)),
		AgentProfileName: "default",
	}}}
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-creator", "schedule:schedule-delivered", "spam")
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.cancel"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-recipient",
		ConversationID:    "dm-recipient",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "schedule.cancel",
		Input: agent.MarshalToolInput(map[string]any{
			"scope": "currentConversation",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule.cancel success, got %s", result.ContentText())
	}
	if !strings.Contains(result.ContentText(), `"cancelledScheduleCount":1`) || !strings.Contains(result.ContentText(), `"cancelledTaskRunCount":1`) || !strings.Contains(result.ContentText(), `"effectiveCancellationCount":2`) {
		t.Fatalf("expected delivered schedule and active run cancelled, got %s", result.ContentText())
	}
	deliveredSchedule := repository.taskSchedules[1]
	if deliveredSchedule.TaskScheduleID != "schedule-delivered" || deliveredSchedule.NextRunAt != nil {
		t.Fatalf("expected delivered schedule to expire, got %+v", deliveredSchedule)
	}
	otherSchedule := repository.taskSchedules[0]
	if otherSchedule.TaskScheduleID != "schedule-other-conversation" || otherSchedule.NextRunAt == nil {
		t.Fatalf("expected other conversation schedule to remain active, got %+v", otherSchedule)
	}
	cancelledTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if !isFound || cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected active scheduled task run to be cancelled, got found=%v task=%+v", isFound, cancelledTaskRun)
	}
}

func TestScheduleCancelToolFailsWhenNothingMatched(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "dm-1", "cancel request")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(&memoryTaskScheduleRepository{})
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.cancel"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm-1",
	})

	result, errorValue := toolRegistry.Invoke(agent.WithTaskRunID(context.Background(), taskRun.TaskRunID), agent.ToolInvocation{
		ToolName: "schedule.cancel",
		Input: agent.MarshalToolInput(map[string]any{
			"scope": "currentConversation",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureCode() != agent.FailureCodes.NotFound.String() {
		t.Fatalf("expected not found failure, got %s", result.ContentText())
	}
	if !strings.Contains(string(result.Output.Data), `"effectiveCancellationCount":0`) {
		t.Fatalf("expected failed result to expose zero effective cancellation count, got %s", string(result.Output.Data))
	}
	if containsTaskEvent(taskRunService.ListTaskEvent(taskRun.TaskRunID), "schedule.cancelled") {
		t.Fatalf("expected not found cancellation to avoid schedule.cancelled event")
	}
}

func TestScheduleCreateToolRejectsIntervalWithoutExplicitCadence(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		Prompt:            "1분마다 \"1분 지났습니다\"라고 보내줘",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "schedule.create",
		Input: agent.MarshalToolInput(map[string]any{
			"taskInstruction": "1분 지났습니다라고 말해줘.",
			"kind":            "interval",
			"timeZone":        "Asia/Seoul",
			"maxRunCount":     10,
			"repeatPolicy":    "finite",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected schedule.create to reject missing intervalSecond, got %s", result.ContentText())
	}
	if len(repository.taskSchedules) != 0 {
		t.Fatalf("expected no schedule to be created, got %+v", repository.taskSchedules)
	}
}

func TestScheduleCreateToolStoresMaxRunCount(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		Prompt:            `1분에 한 번씩 나한테 "죄송합니다" 10번 해봐`,
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "schedule.create",
		Input: agent.MarshalToolInput(map[string]any{
			"taskInstruction": "죄송합니다라고 말해줘.",
			"kind":            "interval",
			"intervalSecond":  60,
			"maxRunCount":     10,
			"repeatPolicy":    "finite",
			"timeZone":        "Asia/Seoul",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule.create success, got %s", result.ContentText())
	}
	if len(repository.taskSchedules) != 1 {
		t.Fatalf("expected one schedule, got %+v", repository.taskSchedules)
	}
	if repository.taskSchedules[0].MaxRunCount != 10 {
		t.Fatalf("expected max run count 10, got %+v", repository.taskSchedules[0])
	}
	if repository.taskSchedules[0].Prompt != "죄송합니다라고 말해줘." {
		t.Fatalf("expected task instruction without cadence or run count, got %+v", repository.taskSchedules[0])
	}
}

func TestScheduleCreateToolSeparatesRepeatFieldsFromTaskInstruction(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		Prompt:            `1분마다 세 번 안녕이라고 테스트이한테 보내`,
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "schedule.create",
		Input: agent.MarshalToolInput(map[string]any{
			"taskInstruction": "테스트이에게 \"안녕\"이라고 보낸다.",
			"kind":            "interval",
			"intervalSecond":  60,
			"maxRunCount":     3,
			"repeatPolicy":    "finite",
			"timeZone":        "Asia/Seoul",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule.create success, got %s", result.ContentText())
	}
	if len(repository.taskSchedules) != 1 {
		t.Fatalf("expected one schedule, got %+v", repository.taskSchedules)
	}
	taskSchedule := repository.taskSchedules[0]
	if taskSchedule.IntervalSecond != 60 || taskSchedule.MaxRunCount != 3 {
		t.Fatalf("expected structured repeat fields, got %+v", taskSchedule)
	}
	if taskSchedule.Prompt != "테스트이에게 \"안녕\"이라고 보낸다." {
		t.Fatalf("expected only executable action in task instruction, got %q", taskSchedule.Prompt)
	}
}

func TestScheduleCancelToolCancelsActiveScheduledTaskRuns(t *testing.T) {
	runAt := time.Now().UTC().Add(time.Hour)
	repository := &memoryTaskScheduleRepository{taskSchedules: []task.TaskSchedule{{
		TaskScheduleID:  "schedule-1",
		CreatorPersonID: "person-1",
		Prompt:          "테스트",
		Platform:        "mattermost",
		ConversationID:  "channel-1",
		ReplyTargetID:   "reply-target-1",
		TimeZone:        "Asia/Seoul",
		Kind:            task.TaskScheduleKindOnce,
		RunAt:           &runAt,
		NextRunAt:       &runAt,
		ExpiresAt:       timePointer(runAt.Add(time.Hour)),
	}}}
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "schedule:schedule-1", "테스트")
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.cancel"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "schedule.cancel",
		Input:    agent.MarshalToolInput(map[string]any{"scope": "mine"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule.cancel success, got %s", result.ContentText())
	}
	cancelledTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if !isFound || cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected scheduled task run to be cancelled, got found=%v run=%+v", isFound, cancelledTaskRun)
	}
}

func TestScheduleCreateToolRejectsMissingReplyTarget(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(&memoryTaskScheduleRepository{})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "schedule.create",
		Input: agent.MarshalToolInput(map[string]any{
			"taskInstruction": "오늘 일정을 보고 브리핑해줘.",
			"kind":            "cron",
			"cronExpression":  "0 7 * * *",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || !strings.Contains(result.ContentText(), "reply target") {
		t.Fatalf("expected reply target error, got %+v", result)
	}
}

func TestScheduleCreateExecutorRejectsScheduledRunContext(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(&memoryTaskScheduleRepository{})

	result, errorValue := toolCatalogBuilder.createScheduleTool(context.Background(), scheduleCreateToolInput{
		TaskInstruction: "새 예약을 만들어줘.",
		Kind:            "cron",
		CronExpression:  "* * * * *",
		RepeatPolicy:    "unbounded",
	}, toolHandlerContext{request: ToolCatalogRequest{
		IsScheduledRun:    true,
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	}})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || !strings.Contains(result.ContentText(), "scheduled task executions cannot create new schedules") {
		t.Fatalf("expected scheduled run schedule.create failure, got %+v", result)
	}
}
