package agentruntime

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/task"
)

type scheduleCreateToolInput struct {
	Name             string `json:"name"`
	Prompt           string `json:"prompt"`
	AgentProfileName string `json:"agentProfileName"`
	Kind             string `json:"kind"`
	RunAt            string `json:"runAt"`
	IntervalSecond   int    `json:"intervalSecond"`
	CronExpression   string `json:"cronExpression"`
	TimeZone         string `json:"timeZone"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerScheduleTools(toolRegistry *agent.ToolRegistry, handlerContext toolHandlerContext) {
	toolRegistry.RegisterTool(agent.ToolDefinition{
		Name:        "schedule.create",
		Description: "Create a scheduled agent task for the current requester and reply target. Use this for recurring or future work such as daily research, calendar briefings, reminders, reports, or follow-up tasks. The input prompt is the task to run at schedule time, not a confirmation message.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"prompt":{"type":"string"},"agentProfileName":{"type":"string"},"kind":{"type":"string","enum":["once","interval","cron"]},"runAt":{"type":"string"},"intervalSecond":{"type":"integer"},"cronExpression":{"type":"string"},"timeZone":{"type":"string"}},"required":["prompt","kind"],"additionalProperties":false}`),
	}, func(toolContext context.Context, toolInvocation agent.ToolInvocation) (agent.ToolResult, error) {
		return toolCatalogBuilder.createScheduleTool(toolContext, toolInvocation, handlerContext)
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) createScheduleTool(toolContext context.Context, toolInvocation agent.ToolInvocation, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	if toolCatalogBuilder.taskScheduleRepository == nil {
		return agent.ToolResult{Content: "task schedule repository is unavailable", IsError: true}, nil
	}
	var input scheduleCreateToolInput
	if errorValue := agent.UnmarshalToolInput(toolInvocation.Input, &input); errorValue != nil {
		return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
	}
	taskSchedule, errorValue := toolCatalogBuilder.buildTaskSchedule(input, handlerContext)
	if errorValue != nil {
		return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
	}
	initializedTaskSchedule, errorValue := (task.TaskScheduler{}).InitializeTaskSchedule(taskSchedule, time.Now().UTC())
	if errorValue != nil {
		return agent.ToolResult{Content: "invalid task schedule", IsError: true}, nil
	}
	if initializedTaskSchedule.NextRunAt == nil {
		return agent.ToolResult{Content: "task schedule has no future run", IsError: true}, nil
	}
	if errorValue := toolCatalogBuilder.taskScheduleRepository.UpsertTaskSchedule(initializedTaskSchedule); errorValue != nil {
		return agent.ToolResult{}, errorValue
	}
	if taskRunID := agent.TaskRunIDFromContext(toolContext); taskRunID != "" && toolCatalogBuilder.taskRunService != nil {
		toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "schedule.created", marshalToolResult(initializedTaskSchedule))
	}
	return agent.ToolResult{Content: marshalToolResult(initializedTaskSchedule)}, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) buildTaskSchedule(input scheduleCreateToolInput, handlerContext toolHandlerContext) (task.TaskSchedule, error) {
	if errorValue := validateScheduleCreateContext(handlerContext.request); errorValue != nil {
		return task.TaskSchedule{}, errorValue
	}
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		return task.TaskSchedule{}, errSchedulePromptRequired
	}
	timeZone, errorValue := normalizeScheduleTimeZone(input.TimeZone)
	if errorValue != nil {
		return task.TaskSchedule{}, errorValue
	}
	now := time.Now().UTC()
	taskSchedule := task.TaskSchedule{
		TaskScheduleID:   task.NewIdentifier(),
		CreatorPersonID:  strings.TrimSpace(handlerContext.request.RequesterPersonID),
		Name:             firstNonEmptyString(input.Name, prompt),
		Prompt:           prompt,
		AgentProfileName: firstNonEmptyString(input.AgentProfileName, handlerContext.request.ProfileName, "default"),
		Platform:         strings.TrimSpace(handlerContext.request.Platform),
		ConversationID:   strings.TrimSpace(handlerContext.request.ConversationID),
		ReplyTargetID:    strings.TrimSpace(handlerContext.request.ReplyTargetID),
		TimeZone:         timeZone,
		Kind:             normalizeTaskScheduleKind(input),
		IntervalSecond:   input.IntervalSecond,
		CronExpression:   strings.TrimSpace(input.CronExpression),
		CreatedAt:        now,
		UpdatedAt:        now,
		NextAttemptAt:    &now,
	}
	if errorValue := applyScheduleRunAt(&taskSchedule, input.RunAt); errorValue != nil {
		return task.TaskSchedule{}, errorValue
	}
	return taskSchedule, nil
}

func validateScheduleCreateContext(request ToolCatalogRequest) error {
	if strings.TrimSpace(request.RequesterPersonID) == "" {
		return errScheduleRequesterRequired
	}
	if strings.TrimSpace(request.Platform) == "" || strings.TrimSpace(request.ConversationID) == "" {
		return errScheduleConversationRequired
	}
	if strings.TrimSpace(request.ReplyTargetID) == "" {
		return errScheduleReplyTargetRequired
	}
	return nil
}

func normalizeTaskScheduleKind(input scheduleCreateToolInput) task.TaskScheduleKind {
	switch strings.TrimSpace(input.Kind) {
	case string(task.TaskScheduleKindOnce):
		return task.TaskScheduleKindOnce
	case string(task.TaskScheduleKindInterval):
		return task.TaskScheduleKindInterval
	case string(task.TaskScheduleKindCron):
		return task.TaskScheduleKindCron
	default:
		if strings.TrimSpace(input.CronExpression) != "" {
			return task.TaskScheduleKindCron
		}
		if input.IntervalSecond > 0 {
			return task.TaskScheduleKindInterval
		}
		return task.TaskScheduleKindOnce
	}
}

func normalizeScheduleTimeZone(value string) (string, error) {
	timeZone := firstNonEmptyString(value, "Asia/Seoul")
	if _, errorValue := time.LoadLocation(timeZone); errorValue != nil {
		return "", errScheduleTimeZoneInvalid
	}
	return timeZone, nil
}

func applyScheduleRunAt(taskSchedule *task.TaskSchedule, value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	runAt, errorValue := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if errorValue != nil {
		return errScheduleRunAtInvalid
	}
	taskSchedule.RunAt = &runAt
	return nil
}
