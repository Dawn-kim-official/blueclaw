package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/task"
)

type scheduleCreateToolInput struct {
	Name             string `json:"name"`
	Prompt           string `json:"prompt"`
	TaskInstruction  string `json:"taskInstruction"`
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

type scheduleListToolInput struct {
	Status string `json:"status"`
	Limit  int    `json:"limit"`
}

type scheduleUpdateToolInput struct {
	TaskScheduleID   string  `json:"scheduleID"`
	Name             *string `json:"name"`
	Prompt           *string `json:"prompt"`
	TaskInstruction  *string `json:"taskInstruction"`
	AgentProfileName *string `json:"agentProfileName"`
	Kind             *string `json:"kind"`
	RunAt            *string `json:"runAt"`
	ExpiresAt        *string `json:"expiresAt"`
	IntervalSecond   *int    `json:"intervalSecond"`
	CronExpression   *string `json:"cronExpression"`
	TimeZone         *string `json:"timeZone"`
	MaxRunCount      *int    `json:"maxRunCount"`
	RepeatPolicy     *string `json:"repeatPolicy"`
}

type scheduleCancelOperationResult struct {
	CancelledScheduleCount     int                 `json:"cancelledScheduleCount"`
	CancelledTaskRunCount      int                 `json:"cancelledTaskRunCount"`
	CancelledWaitCount         int                 `json:"cancelledWaitCount"`
	EffectiveCancellationCount int                 `json:"effectiveCancellationCount"`
	TaskSchedules              []task.TaskSchedule `json:"taskSchedules"`
}

type scheduleCreateToolResult struct {
	TaskScheduleID   string     `json:"taskScheduleID"`
	Name             string     `json:"name"`
	TaskInstruction  string     `json:"taskInstruction"`
	TimeZone         string     `json:"timeZone"`
	Kind             string     `json:"kind"`
	RunAt            *time.Time `json:"runAt,omitempty"`
	IntervalSecond   int        `json:"intervalSecond,omitempty"`
	CronExpression   string     `json:"cronExpression,omitempty"`
	MaxRunCount      int        `json:"maxRunCount,omitempty"`
	ExpiresAt        *time.Time `json:"expiresAt,omitempty"`
	NextRunAt        *time.Time `json:"nextRunAt,omitempty"`
	ConversationID   string     `json:"conversationID"`
	ReplyTargetID    string     `json:"replyTargetID"`
	AgentProfileName string     `json:"agentProfileName"`
}

type scheduleListToolOutput struct {
	Schedules []scheduleListToolItem `json:"schedules"`
}

type scheduleListToolItem struct {
	ScheduleID     string     `json:"scheduleID"`
	Prompt         string     `json:"prompt"`
	Description    string     `json:"description,omitempty"`
	Cadence        string     `json:"cadence"`
	CronExpression string     `json:"cronExpression,omitempty"`
	RunAt          *time.Time `json:"runAt,omitempty"`
	Status         string     `json:"status"`
	NextRunAt      *time.Time `json:"nextRunAt,omitempty"`
	LastRunAt      *time.Time `json:"lastRunAt,omitempty"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerScheduleTools(toolRegistry *agent.ToolSet, handlerContext toolHandlerContext) {
	if toolCatalogBuilder.taskScheduleRepository != nil && strings.TrimSpace(handlerContext.request.RequesterPersonID) != "" {
		agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[scheduleListToolInput, scheduleListToolOutput]{
			Definition: agent.ToolDefinition{
				Name:        "schedule.list",
				Description: "List active scheduled tasks created by the current requester. Use it to answer what reminders or recurring tasks are currently scheduled.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"status":{"type":"string"},"limit":{"type":"integer"}}}`),
			},
			Handler: func(toolContext context.Context, input scheduleListToolInput) (scheduleListToolOutput, error) {
				return toolCatalogBuilder.listScheduleTool(input, handlerContext)
			},
		})
	}
	if !handlerContext.request.IsScheduledRun {
		agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[scheduleCreateToolInput, agent.ToolResult]{
			Definition: agent.ToolDefinition{
				Name:        "schedule.create",
				Description: "Create a scheduled task for the current requester and reply target. Put only the work to perform at run time in taskInstruction. Do not copy the original scheduling request into taskInstruction. Cadence and stop conditions must be represented only by kind, runAt, intervalSecond, cronExpression, expiresAt, and maxRunCount. For interval or cron schedules, set repeatPolicy to finite when the user gave an end condition and include expiresAt or maxRunCount; set repeatPolicy to unbounded only when the user explicitly wants no end.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"taskInstruction":{"type":"string"},"agentProfileName":{"type":"string"},"kind":{"type":"string","enum":["once","interval","cron"]},"runAt":{"type":"string"},"expiresAt":{"type":"string"},"intervalSecond":{"type":"number"},"cronExpression":{"type":"string"},"timeZone":{"type":"string"},"maxRunCount":{"type":"number"},"repeatPolicy":{"type":"string","enum":["finite","unbounded"]}},"required":["taskInstruction","kind"]}`),
			},
			Handler: func(toolContext context.Context, input scheduleCreateToolInput) (agent.ToolResult, error) {
				return toolCatalogBuilder.createScheduleTool(toolContext, input, handlerContext)
			},
			Result: agent.IdentityToolResult,
		})
		agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[scheduleUpdateToolInput, agent.ToolResult]{
			Definition: agent.ToolDefinition{
				Name:        "schedule.update",
				Description: "Update an active scheduled task created by the current requester. Provide scheduleID and only the scalar fields that should change. Keep only the work to perform at run time in taskInstruction; represent cadence and stop conditions only with kind, runAt, intervalSecond, cronExpression, expiresAt, maxRunCount, and repeatPolicy.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"scheduleID":{"type":"string"},"name":{"type":"string"},"taskInstruction":{"type":"string"},"agentProfileName":{"type":"string"},"kind":{"type":"string","enum":["once","interval","cron"]},"runAt":{"type":"string"},"expiresAt":{"type":"string"},"intervalSecond":{"type":"number"},"cronExpression":{"type":"string"},"timeZone":{"type":"string"},"maxRunCount":{"type":"number"},"repeatPolicy":{"type":"string","enum":["finite","unbounded"]}},"required":["scheduleID"]}`),
			},
			Handler: func(toolContext context.Context, input scheduleUpdateToolInput) (agent.ToolResult, error) {
				return toolCatalogBuilder.updateScheduleTool(toolContext, input, handlerContext)
			},
			Result: agent.IdentityToolResult,
		})
	}
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

func (toolCatalogBuilder *ToolCatalogBuilder) listScheduleTool(input scheduleListToolInput, handlerContext toolHandlerContext) (scheduleListToolOutput, error) {
	limit := normalizedScheduleListLimit(input.Limit)
	referenceTime := time.Now().UTC()
	result, errorValue := toolCatalogBuilder.taskScheduleRepository.ListTaskSchedules(task.TaskScheduleListRequest{
		CreatorPersonID: strings.TrimSpace(handlerContext.request.RequesterPersonID),
		Page:            1,
		PageSize:        20,
		ReferenceTime:   referenceTime,
	})
	if errorValue != nil {
		return scheduleListToolOutput{}, errorValue
	}
	return scheduleListToolOutput{
		Schedules: filteredScheduleListItems(result.TaskSchedules, input.Status, limit, referenceTime),
	}, nil
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
	resultDocument := scheduleCreateResultDocument(initializedTaskSchedule)
	if taskRunID := agent.TaskRunIDFromContext(toolContext); taskRunID != "" && toolCatalogBuilder.taskRunService != nil {
		toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "schedule.created", string(resultDocument))
	}
	return agent.ToolSuccessData(string(resultDocument), resultDocument), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) updateScheduleTool(toolContext context.Context, input scheduleUpdateToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	if toolCatalogBuilder.taskScheduleRepository == nil {
		return agent.ToolFailureResult(agent.FailureDependencyUnavailable, agent.FailureCodes.Unavailable, "schedule_update", "task schedule repository is unavailable"), nil
	}
	updateRequest := task.TaskScheduleUpdateRequest{
		TaskScheduleID:    strings.TrimSpace(input.TaskScheduleID),
		RequesterPersonID: strings.TrimSpace(handlerContext.request.RequesterPersonID),
		UpdateTaskSchedule: func(existingTaskSchedule task.TaskSchedule) (task.TaskSchedule, error) {
			return toolCatalogBuilder.buildUpdatedTaskSchedule(existingTaskSchedule, input)
		},
	}
	result, errorValue := toolCatalogBuilder.taskScheduleRepository.UpdateTaskSchedule(updateRequest)
	if errorValue != nil {
		if isScheduleToolValidationError(errorValue) {
			return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "schedule_update", errorValue.Error()), nil
		}
		return agent.ToolResult{}, errorValue
	}
	if !result.IsFound {
		return agent.ToolFailureResult(agent.FailureNotFound, agent.FailureCodes.NotFound, "schedule_update", "active schedule was not found for the current requester"), nil
	}
	resultDocument := scheduleCreateResultDocument(result.TaskSchedule)
	if taskRunID := agent.TaskRunIDFromContext(toolContext); taskRunID != "" && toolCatalogBuilder.taskRunService != nil {
		toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "schedule.updated", string(resultDocument))
	}
	return agent.ToolSuccessData(string(resultDocument), resultDocument), nil
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
	result, errorValue := toolCatalogBuilder.cancelMatchingSchedules(cancelRequest, cancelledAt)
	if errorValue != nil {
		return agent.ToolResult{}, errorValue
	}
	resultDocument := json.RawMessage(marshalToolResult(result))
	if result.EffectiveCancellationCount == 0 {
		return agent.ToolFailureData(agent.FailureNotFound, agent.FailureCodes.NotFound, "schedule_cancel", "no active schedules or pending scheduled work matched the cancellation request", resultDocument), nil
	}
	if taskRunID := agent.TaskRunIDFromContext(toolContext); taskRunID != "" && toolCatalogBuilder.taskRunService != nil {
		toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "schedule.cancelled", string(resultDocument))
	}
	return agent.ToolSuccessData(string(resultDocument), resultDocument), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) cancelMatchingSchedules(cancelRequest task.TaskScheduleCancelRequest, cancelledAt time.Time) (scheduleCancelOperationResult, error) {
	result, errorValue := toolCatalogBuilder.taskScheduleRepository.CancelTaskSchedules(cancelRequest)
	if errorValue != nil {
		return scheduleCancelOperationResult{}, errorValue
	}
	cancelledTaskRunCount := toolCatalogBuilder.cancelScheduledTaskRuns(cancelRequest, result)
	cancelledWaitCount := toolCatalogBuilder.cancelPendingWaits(cancelRequest, cancelledAt)
	effectiveCancellationCount := len(result.TaskSchedules) + cancelledTaskRunCount + cancelledWaitCount
	return scheduleCancelOperationResult{
		CancelledScheduleCount:     len(result.TaskSchedules),
		CancelledTaskRunCount:      cancelledTaskRunCount,
		CancelledWaitCount:         cancelledWaitCount,
		EffectiveCancellationCount: effectiveCancellationCount,
		TaskSchedules:              result.TaskSchedules,
	}, nil
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
	taskInstruction := firstNonEmptyString(input.TaskInstruction, input.Prompt)
	if taskInstruction == "" {
		return task.TaskSchedule{}, errScheduleTaskInstructionRequired
	}
	timeZone, errorValue := normalizeScheduleTimeZone(input.TimeZone)
	if errorValue != nil {
		return task.TaskSchedule{}, errorValue
	}
	now := time.Now().UTC()
	taskSchedule := task.TaskSchedule{
		TaskScheduleID:   task.NewIdentifier(),
		CreatorPersonID:  strings.TrimSpace(handlerContext.request.RequesterPersonID),
		Name:             firstNonEmptyString(input.Name, taskInstruction),
		Prompt:           taskInstruction,
		ExecutionMode:    task.TaskScheduleExecutionModeAgent,
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

func (toolCatalogBuilder *ToolCatalogBuilder) buildUpdatedTaskSchedule(taskSchedule task.TaskSchedule, input scheduleUpdateToolInput) (task.TaskSchedule, error) {
	now := time.Now().UTC()
	if input.Name != nil {
		taskSchedule.Name = strings.TrimSpace(*input.Name)
	}
	if taskInstruction := scheduleUpdateTaskInstruction(input); taskInstruction != nil {
		if strings.TrimSpace(*taskInstruction) == "" {
			return task.TaskSchedule{}, errScheduleTaskInstructionRequired
		}
		taskSchedule.Prompt = strings.TrimSpace(*taskInstruction)
	}
	if input.AgentProfileName != nil {
		taskSchedule.AgentProfileName = strings.TrimSpace(*input.AgentProfileName)
	}
	if input.TimeZone != nil {
		timeZone, errorValue := normalizeScheduleTimeZone(*input.TimeZone)
		if errorValue != nil {
			return task.TaskSchedule{}, errorValue
		}
		taskSchedule.TimeZone = timeZone
	}
	updatedTaskSchedule, errorValue := applyScheduleUpdateTiming(taskSchedule, input, now)
	if errorValue != nil {
		return task.TaskSchedule{}, errorValue
	}
	updatedTaskSchedule.UpdatedAt = now
	updatedTaskSchedule.NextAttemptAt = &now
	initializedTaskSchedule, errorValue := (task.TaskScheduler{}).InitializeTaskSchedule(updatedTaskSchedule, now)
	if errorValue != nil {
		return task.TaskSchedule{}, errorValue
	}
	if initializedTaskSchedule.NextRunAt == nil {
		return task.TaskSchedule{}, errScheduleNoFutureRun
	}
	return initializedTaskSchedule, nil
}

func normalizedScheduleListLimit(limit int) int {
	if limit <= 0 {
		return 10
	}
	if limit > 20 {
		return 20
	}
	return limit
}

func filteredScheduleListItems(taskSchedules []task.TaskSchedule, statusFilter string, limit int, referenceTime time.Time) []scheduleListToolItem {
	filter := strings.TrimSpace(statusFilter)
	items := []scheduleListToolItem{}
	for _, taskSchedule := range taskSchedules {
		item := scheduleListToolItemFromSchedule(taskSchedule, referenceTime)
		if filter != "" && item.Status != filter {
			continue
		}
		items = append(items, item)
		if len(items) == limit {
			break
		}
	}
	return items
}

func scheduleListToolItemFromSchedule(taskSchedule task.TaskSchedule, referenceTime time.Time) scheduleListToolItem {
	return scheduleListToolItem{
		ScheduleID:     taskSchedule.TaskScheduleID,
		Prompt:         taskSchedule.Prompt,
		Description:    taskSchedule.Name,
		Cadence:        taskScheduleCadence(taskSchedule),
		CronExpression: taskSchedule.CronExpression,
		RunAt:          taskSchedule.RunAt,
		Status:         taskScheduleStatus(taskSchedule, referenceTime),
		NextRunAt:      taskSchedule.NextRunAt,
		LastRunAt:      taskSchedule.LastRunAt,
	}
}

func taskScheduleCadence(taskSchedule task.TaskSchedule) string {
	switch taskSchedule.Kind {
	case task.TaskScheduleKindInterval:
		return "every " + strconv.Itoa(taskSchedule.IntervalSecond) + " seconds"
	case task.TaskScheduleKindCron:
		return "cron"
	default:
		return "once"
	}
}

func taskScheduleStatus(taskSchedule task.TaskSchedule, referenceTime time.Time) string {
	if taskSchedule.NextRunAt == nil || taskSchedule.ExpiresAt != nil && !taskSchedule.ExpiresAt.After(referenceTime) {
		return "expired"
	}
	if strings.TrimSpace(taskSchedule.LastError) != "" {
		return "failed"
	}
	return "active"
}

func scheduleCreateResultDocument(taskSchedule task.TaskSchedule) json.RawMessage {
	return json.RawMessage(marshalToolResult(scheduleCreateToolResult{
		TaskScheduleID:   taskSchedule.TaskScheduleID,
		Name:             taskSchedule.Name,
		TaskInstruction:  taskSchedule.Prompt,
		TimeZone:         taskSchedule.TimeZone,
		Kind:             string(taskSchedule.Kind),
		RunAt:            taskSchedule.RunAt,
		IntervalSecond:   taskSchedule.IntervalSecond,
		CronExpression:   taskSchedule.CronExpression,
		MaxRunCount:      taskSchedule.MaxRunCount,
		ExpiresAt:        taskSchedule.ExpiresAt,
		NextRunAt:        taskSchedule.NextRunAt,
		ConversationID:   taskSchedule.ConversationID,
		ReplyTargetID:    taskSchedule.ReplyTargetID,
		AgentProfileName: taskSchedule.AgentProfileName,
	}))
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

func applyScheduleUpdateTiming(taskSchedule task.TaskSchedule, input scheduleUpdateToolInput, now time.Time) (task.TaskSchedule, error) {
	if input.Kind != nil {
		taskSchedule.Kind = normalizeTaskScheduleKind(scheduleCreateToolInput{Kind: *input.Kind})
	}
	if input.RunAt != nil {
		if errorValue := applyScheduleRunAtPointer(&taskSchedule, *input.RunAt); errorValue != nil {
			return task.TaskSchedule{}, errorValue
		}
	}
	if input.ExpiresAt != nil {
		if errorValue := applyScheduleExpiresAtPointer(&taskSchedule, *input.ExpiresAt, now); errorValue != nil {
			return task.TaskSchedule{}, errorValue
		}
	}
	if input.IntervalSecond != nil {
		taskSchedule.IntervalSecond = *input.IntervalSecond
	}
	if input.CronExpression != nil {
		taskSchedule.CronExpression = strings.TrimSpace(*input.CronExpression)
	}
	if input.MaxRunCount != nil {
		taskSchedule.MaxRunCount = *input.MaxRunCount
	}
	normalizeUpdatedTaskScheduleKindFields(&taskSchedule)
	if errorValue := validateScheduleRepeatPolicy(scheduleUpdateAsCreateInput(input), taskSchedule); errorValue != nil {
		return task.TaskSchedule{}, errorValue
	}
	return taskSchedule, nil
}

func normalizeUpdatedTaskScheduleKindFields(taskSchedule *task.TaskSchedule) {
	switch taskSchedule.Kind {
	case task.TaskScheduleKindOnce:
		taskSchedule.IntervalSecond = 0
		taskSchedule.CronExpression = ""
		taskSchedule.MaxRunCount = 0
	case task.TaskScheduleKindInterval:
		taskSchedule.CronExpression = ""
	case task.TaskScheduleKindCron:
		taskSchedule.IntervalSecond = 0
	}
}

func scheduleUpdateTaskInstruction(input scheduleUpdateToolInput) *string {
	if input.TaskInstruction != nil {
		return input.TaskInstruction
	}
	return input.Prompt
}

func scheduleUpdateAsCreateInput(input scheduleUpdateToolInput) scheduleCreateToolInput {
	createInput := scheduleCreateToolInput{}
	if input.RepeatPolicy != nil {
		createInput.RepeatPolicy = *input.RepeatPolicy
	}
	return createInput
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

func applyScheduleExpiresAtPointer(taskSchedule *task.TaskSchedule, value string, referenceTime time.Time) error {
	if strings.TrimSpace(value) == "" {
		taskSchedule.ExpiresAt = nil
		return nil
	}
	return applyScheduleExpiresAt(taskSchedule, value, referenceTime)
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
	if request.IsScheduledRun {
		return errScheduleCreateInScheduledRun
	}
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

func applyScheduleRunAtPointer(taskSchedule *task.TaskSchedule, value string) error {
	if strings.TrimSpace(value) == "" {
		taskSchedule.RunAt = nil
		return nil
	}
	return applyScheduleRunAt(taskSchedule, value)
}

func isScheduleToolValidationError(errorValue error) bool {
	return errors.Is(errorValue, errScheduleTaskInstructionRequired) ||
		errors.Is(errorValue, errScheduleTimeZoneInvalid) ||
		errors.Is(errorValue, errScheduleRunAtInvalid) ||
		errors.Is(errorValue, errScheduleInvalidExpiresAt) ||
		errors.Is(errorValue, errScheduleRepeatPolicyRequired) ||
		errors.Is(errorValue, errScheduleFiniteBoundRequired) ||
		errors.Is(errorValue, errScheduleNoFutureRun)
}
