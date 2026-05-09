package agentruntime

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
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
	MaxRunCount      int    `json:"maxRunCount"`
}

type scheduleIntervalPattern struct {
	expression *regexp.Regexp
	multiplier int
}

var scheduleIntervalPatterns = []scheduleIntervalPattern{
	{expression: regexp.MustCompile(`(?i)(\d+)\s*분\s*마다`), multiplier: 60},
	{expression: regexp.MustCompile(`(?i)(\d+)\s*분\s*(에|간격으로)\s*(한\s*)?번씩`), multiplier: 60},
	{expression: regexp.MustCompile(`(?i)(\d+)\s*분\s*간격`), multiplier: 60},
	{expression: regexp.MustCompile(`(?i)(\d+)\s*시간\s*마다`), multiplier: 60 * 60},
	{expression: regexp.MustCompile(`(?i)(\d+)\s*시간\s*(에|간격으로)\s*(한\s*)?번씩`), multiplier: 60 * 60},
	{expression: regexp.MustCompile(`(?i)(\d+)\s*시간\s*간격`), multiplier: 60 * 60},
	{expression: regexp.MustCompile(`(?i)(\d+)\s*일\s*마다`), multiplier: 24 * 60 * 60},
	{expression: regexp.MustCompile(`(?i)(\d+)\s*일\s*(에|간격으로)\s*(한\s*)?번씩`), multiplier: 24 * 60 * 60},
	{expression: regexp.MustCompile(`(?i)(\d+)\s*일\s*간격`), multiplier: 24 * 60 * 60},
	{expression: regexp.MustCompile(`(?i)every\s+(\d+)\s+minute`), multiplier: 60},
	{expression: regexp.MustCompile(`(?i)every\s+(\d+)\s+hour`), multiplier: 60 * 60},
	{expression: regexp.MustCompile(`(?i)every\s+(\d+)\s+day`), multiplier: 24 * 60 * 60},
}

var scheduleRunCountPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(\d+)\s*(번|회)\s*(만|해|반복|보내|전송|말|알려)?`),
	regexp.MustCompile(`(?i)(\d+)\s+times`),
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerScheduleTools(toolRegistry *agent.ToolSet, handlerContext toolHandlerContext) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[scheduleCreateToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "schedule.create",
			Description: "Create a scheduled agent task for the current requester and reply target. Use this for recurring, finite repeated, or future work such as daily research, calendar briefings, reminders, reports, messages, or follow-up tasks. The input prompt is the task to run at schedule time, not a confirmation message. Set maxRunCount for finite repeats.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"prompt":{"type":"string"},"agentProfileName":{"type":"string"},"kind":{"type":"string","enum":["once","interval","cron"]},"runAt":{"type":"string"},"intervalSecond":{"type":"integer"},"cronExpression":{"type":"string"},"timeZone":{"type":"string"},"maxRunCount":{"type":"integer"}},"required":["prompt","kind"],"additionalProperties":false}`),
		},
		Handler: func(toolContext context.Context, input scheduleCreateToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.createScheduleTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) createScheduleTool(toolContext context.Context, input scheduleCreateToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	if toolCatalogBuilder.taskScheduleRepository == nil {
		return agent.ToolResult{Content: "task schedule repository is unavailable", IsError: true}, nil
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
		IntervalSecond:   normalizeScheduleIntervalSecond(input, handlerContext.request.Prompt),
		CronExpression:   strings.TrimSpace(input.CronExpression),
		MaxRunCount:      normalizeScheduleMaxRunCount(input, handlerContext.request.Prompt),
		CreatedAt:        now,
		UpdatedAt:        now,
		NextAttemptAt:    &now,
	}
	if errorValue := applyScheduleRunAt(&taskSchedule, input.RunAt); errorValue != nil {
		return task.TaskSchedule{}, errorValue
	}
	return taskSchedule, nil
}

func normalizeScheduleMaxRunCount(input scheduleCreateToolInput, requestPrompt string) int {
	if input.MaxRunCount > 0 {
		return input.MaxRunCount
	}
	return inferScheduleMaxRunCount(requestPrompt)
}

func normalizeScheduleIntervalSecond(input scheduleCreateToolInput, requestPrompt string) int {
	if input.IntervalSecond > 0 {
		return input.IntervalSecond
	}
	if normalizeTaskScheduleKind(input) != task.TaskScheduleKindInterval {
		return 0
	}
	return inferScheduleIntervalSecond(requestPrompt)
}

func inferScheduleIntervalSecond(prompt string) int {
	normalizedPrompt := strings.TrimSpace(prompt)
	if normalizedPrompt == "" {
		return 0
	}
	if strings.Contains(normalizedPrompt, "매분") || strings.Contains(strings.ToLower(normalizedPrompt), "every minute") {
		return 60
	}
	for _, pattern := range scheduleIntervalPatterns {
		match := pattern.expression.FindStringSubmatch(normalizedPrompt)
		if len(match) < 2 {
			continue
		}
		count, errorValue := strconv.Atoi(match[1])
		if errorValue == nil && count > 0 {
			return count * pattern.multiplier
		}
	}
	return 0
}

func inferScheduleMaxRunCount(prompt string) int {
	normalizedPrompt := strings.TrimSpace(prompt)
	if normalizedPrompt == "" {
		return 0
	}
	for _, pattern := range scheduleRunCountPatterns {
		match := pattern.FindStringSubmatch(normalizedPrompt)
		if len(match) < 2 {
			continue
		}
		count, errorValue := strconv.Atoi(match[1])
		if errorValue == nil && count > 0 {
			return count
		}
	}
	return 0
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
