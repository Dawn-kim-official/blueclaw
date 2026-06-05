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
	ExecutionMode    string `json:"executionMode"`
	AgentProfileName string `json:"agentProfileName"`
	Kind             string `json:"kind"`
	RunAt            string `json:"runAt"`
	ExpiresAt        string `json:"expiresAt"`
	IntervalSecond   int    `json:"intervalSecond"`
	CronExpression   string `json:"cronExpression"`
	TimeZone         string `json:"timeZone"`
	MaxRunCount      int    `json:"maxRunCount"`
	RepeatPolicy     string `json:"repeatPolicy"`
}

type scheduleCancelToolInput struct {
	Scope           string   `json:"scope"`
	TaskScheduleIDs []string `json:"scheduleIDs"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerScheduleTools(toolRegistry *agent.ToolSet, handlerContext toolHandlerContext) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[scheduleCreateToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "schedule.create",
			Description: "Create a scheduled task for the current requester and reply target. Use executionMode message when the schedule should send the prompt verbatim, such as reminders, repeated messages, or \"say this\" requests. Use executionMode agent only when the schedule must perform reasoning, research, checks, summaries, or tool work at run time. For interval or cron schedules, set repeatPolicy to finite when the user gave an end condition and include expiresAt or maxRunCount; set repeatPolicy to unbounded only when the user explicitly wants no end. Do not rely on the prompt text for cadence or stop conditions: fill intervalSecond, cronExpression, expiresAt, and maxRunCount explicitly.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"prompt":{"type":"string"},"executionMode":{"type":"string","enum":["message","agent"]},"agentProfileName":{"type":"string"},"kind":{"type":"string","enum":["once","interval","cron"]},"runAt":{"type":"string"},"expiresAt":{"type":"string"},"intervalSecond":{"type":"number"},"cronExpression":{"type":"string"},"timeZone":{"type":"string"},"maxRunCount":{"type":"number"},"repeatPolicy":{"type":"string","enum":["finite","unbounded"]}},"required":["prompt","executionMode","kind"]}`),
		},
		Handler: func(toolContext context.Context, input scheduleCreateToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.createScheduleTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[scheduleCancelToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "schedule.cancel",
			Description: "Cancel active scheduled tasks and pending approval or user-input waits. Use scope mine for schedules created by the current requester. Use scope currentConversation when the user wants messages or reminders delivered to this conversation to stop, even if another person created that delivery schedule. Use scope scheduleIDs for explicit schedule IDs visible from prior tool results. Cancellation expires records instead of deleting audit history.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"scope":{"type":"string","enum":["currentConversation","mine","scheduleIDs"]},"scheduleIDs":{"type":"array","items":{"type":"string"}}},"required":["scope"]}`),
		},
		Handler: func(toolContext context.Context, input scheduleCancelToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.cancelScheduleTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) createScheduleTool(toolContext context.Context, input scheduleCreateToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	if toolCatalogBuilder.taskScheduleRepository == nil {
		return agent.ToolFailureResult(agent.FailureDependencyUnavailable, agent.FailureCodes.Unavailable, "schedule_create", "task schedule repository is unavailable"), nil
	}
	taskSchedule, errorValue := toolCatalogBuilder.buildTaskSchedule(input, handlerContext)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "schedule_create", errorValue.Error()), nil
	}
	initializedTaskSchedule, errorValue := (task.TaskScheduler{}).InitializeTaskSchedule(taskSchedule, time.Now().UTC())
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "schedule_create", "invalid task schedule"), nil
	}
	if initializedTaskSchedule.NextRunAt == nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "schedule_create", "task schedule has no future run"), nil
	}
	if errorValue := toolCatalogBuilder.taskScheduleRepository.UpsertTaskSchedule(initializedTaskSchedule); errorValue != nil {
		return agent.ToolResult{}, errorValue
	}
	if taskRunID := agent.TaskRunIDFromContext(toolContext); taskRunID != "" && toolCatalogBuilder.taskRunService != nil {
		toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "schedule.created", marshalToolResult(initializedTaskSchedule))
	}
	return agent.ToolSuccess(marshalToolResult(initializedTaskSchedule)), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) cancelScheduleTool(toolContext context.Context, input scheduleCancelToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	if toolCatalogBuilder.taskScheduleRepository == nil {
		return agent.ToolFailureResult(agent.FailureDependencyUnavailable, agent.FailureCodes.Unavailable, "schedule_cancel", "task schedule repository is unavailable"), nil
	}
	cancelledAt := time.Now().UTC()
	cancelRequest := task.TaskScheduleCancelRequest{
		Scope:             normalizeScheduleCancelScope(input.Scope),
		RequesterPersonID: strings.TrimSpace(handlerContext.request.RequesterPersonID),
		ConversationID:    strings.TrimSpace(handlerContext.request.ConversationID),
		TaskScheduleIDs:   trimNonEmptyStrings(input.TaskScheduleIDs),
		CancelledAt:       cancelledAt,
	}
	result, errorValue := toolCatalogBuilder.taskScheduleRepository.CancelTaskSchedules(cancelRequest)
	if errorValue != nil {
		return agent.ToolResult{}, errorValue
	}
	cancelledTaskRunCount := toolCatalogBuilder.cancelScheduledTaskRuns(cancelRequest, result)
	cancelledWaitCount := toolCatalogBuilder.cancelPendingWaits(cancelRequest, cancelledAt)
	response := map[string]any{
		"cancelledScheduleCount": len(result.TaskSchedules),
		"cancelledTaskRunCount":  cancelledTaskRunCount,
		"cancelledWaitCount":     cancelledWaitCount,
		"taskSchedules":          result.TaskSchedules,
	}
	if len(result.TaskSchedules)+cancelledTaskRunCount+cancelledWaitCount == 0 {
		return agent.ToolFailureWithOutput(agent.FailureNotFound, agent.FailureCodes.NotFound, "schedule_cancel", "no active schedules or pending scheduled work matched the cancellation request", json.RawMessage(marshalToolResult(response))), nil
	}
	if taskRunID := agent.TaskRunIDFromContext(toolContext); taskRunID != "" && toolCatalogBuilder.taskRunService != nil {
		toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "schedule.cancelled", marshalToolResult(response))
	}
	return agent.ToolSuccess(marshalToolResult(response)), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) cancelScheduledTaskRuns(cancelRequest task.TaskScheduleCancelRequest, result task.TaskScheduleCancelResult) int {
	if toolCatalogBuilder.taskRunService == nil {
		return 0
	}
	taskRunCancelRequest := task.TaskRunCancelRequest{
		ScheduleOnly: true,
		Reason:       "schedule.cancel",
	}
	if cancelRequest.Scope == task.TaskScheduleCancelScopeMine {
		taskRunCancelRequest.RequesterPersonID = strings.TrimSpace(cancelRequest.RequesterPersonID)
		taskRunCancelRequest.OriginConversationIDPrefix = "schedule:"
	} else {
		taskRunCancelRequest.OriginConversationIDs = scheduleOriginConversationIDs(result.TaskSchedules)
	}
	return len(toolCatalogBuilder.taskRunService.CancelActiveTaskRuns(taskRunCancelRequest))
}

func (toolCatalogBuilder *ToolCatalogBuilder) cancelPendingWaits(cancelRequest task.TaskScheduleCancelRequest, cancelledAt time.Time) int {
	if toolCatalogBuilder.taskRunService == nil {
		return 0
	}
	if toolCatalogBuilder.taskWaitTokenRepository != nil && cancelRequest.Scope == task.TaskScheduleCancelScopeMine {
		_, _ = toolCatalogBuilder.taskWaitTokenRepository.ExpireTaskWaitTokensForPerson(cancelRequest.RequesterPersonID, cancelledAt)
	}
	originConversationID := ""
	if cancelRequest.Scope == task.TaskScheduleCancelScopeCurrentConversation {
		originConversationID = cancelRequest.ConversationID
	}
	cancelledTaskRuns := toolCatalogBuilder.taskRunService.CancelWaitingTaskRuns(cancelRequest.RequesterPersonID, originConversationID, "schedule.cancel")
	return len(cancelledTaskRuns)
}

func scheduleOriginConversationIDs(taskSchedules []task.TaskSchedule) []string {
	originConversationIDs := []string{}
	for _, taskSchedule := range taskSchedules {
		if strings.TrimSpace(taskSchedule.TaskScheduleID) == "" {
			continue
		}
		originConversationIDs = append(originConversationIDs, "schedule:"+taskSchedule.TaskScheduleID)
	}
	return originConversationIDs
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
		ExecutionMode:    normalizeTaskScheduleExecutionMode(input.ExecutionMode),
		AgentProfileName: firstNonEmptyString(input.AgentProfileName, handlerContext.request.ProfileName, "default"),
		Platform:         strings.TrimSpace(handlerContext.request.Platform),
		ConversationID:   strings.TrimSpace(handlerContext.request.ConversationID),
		ReplyTargetID:    strings.TrimSpace(handlerContext.request.ReplyTargetID),
		TimeZone:         timeZone,
		Kind:             normalizeTaskScheduleKind(input),
		IntervalSecond:   input.IntervalSecond,
		CronExpression:   strings.TrimSpace(input.CronExpression),
		MaxRunCount:      input.MaxRunCount,
		CreatedAt:        now,
		UpdatedAt:        now,
		NextAttemptAt:    &now,
	}
	if errorValue := applyScheduleRunAt(&taskSchedule, input.RunAt); errorValue != nil {
		return task.TaskSchedule{}, errorValue
	}
	if errorValue := applyScheduleExpiresAt(&taskSchedule, input.ExpiresAt, now); errorValue != nil {
		return task.TaskSchedule{}, errorValue
	}
	if errorValue := validateScheduleRepeatPolicy(input, taskSchedule); errorValue != nil {
		return task.TaskSchedule{}, errorValue
	}
	return taskSchedule, nil
}

func normalizeScheduleCancelScope(value string) task.TaskScheduleCancelScope {
	switch strings.TrimSpace(value) {
	case string(task.TaskScheduleCancelScopeCurrentConversation):
		return task.TaskScheduleCancelScopeCurrentConversation
	case string(task.TaskScheduleCancelScopeScheduleIDs):
		return task.TaskScheduleCancelScopeScheduleIDs
	default:
		return task.TaskScheduleCancelScopeMine
	}
}

func normalizeTaskScheduleExecutionMode(value string) task.TaskScheduleExecutionMode {
	switch strings.TrimSpace(value) {
	case string(task.TaskScheduleExecutionModeMessage):
		return task.TaskScheduleExecutionModeMessage
	default:
		return task.TaskScheduleExecutionModeAgent
	}
}

func applyScheduleExpiresAt(taskSchedule *task.TaskSchedule, value string, referenceTime time.Time) error {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return nil
	}
	expiresAt, errorValue := time.Parse(time.RFC3339, trimmedValue)
	if errorValue != nil {
		return errScheduleInvalidExpiresAt
	}
	expiresAt = expiresAt.UTC()
	if !expiresAt.After(referenceTime) {
		return errScheduleInvalidExpiresAt
	}
	taskSchedule.ExpiresAt = &expiresAt
	return nil
}

func validateScheduleRepeatPolicy(input scheduleCreateToolInput, taskSchedule task.TaskSchedule) error {
	if taskSchedule.Kind != task.TaskScheduleKindInterval && taskSchedule.Kind != task.TaskScheduleKindCron {
		return nil
	}
	if taskSchedule.MaxRunCount > 0 || taskSchedule.ExpiresAt != nil {
		return nil
	}
	switch strings.TrimSpace(input.RepeatPolicy) {
	case "unbounded":
		return nil
	case "finite":
		return errScheduleFiniteBoundRequired
	default:
		return errScheduleRepeatPolicyRequired
	}
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
