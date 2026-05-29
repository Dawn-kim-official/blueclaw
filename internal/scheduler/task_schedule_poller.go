package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/agentruntime"
	"blueclaw/internal/connectors"
	"blueclaw/internal/policy"
	"blueclaw/internal/task"
)

const taskScheduleLeaseDuration = 15 * time.Minute
const defaultStaleScheduledTaskRunTimeout = 30 * time.Minute

type TaskScheduleDeliveryRepository interface {
	EnqueueScheduledConnectorReply(task.TaskSchedule, string, connectors.OutboundReply) (string, error)
}

type PersonAccessResolver interface {
	ResolvePersonAccess(string) policy.PersonAccess
}

type TaskSchedulePoller struct {
	TaskScheduleRepository task.TaskScheduleRepository
	DeliveryRepository     TaskScheduleDeliveryRepository
	TaskScheduleRunner     agentruntime.TaskScheduleRunner
	TaskRunService         *task.TaskRunService
	PersonAccessResolver   PersonAccessResolver
	WorkspaceID            string
	WorkerID               string
	Logger                 *slog.Logger
	StaleTaskRunTimeout    time.Duration
}

type taskScheduleExecutionResult struct {
	TaskSchedule task.TaskSchedule
	TaskRunID    string
	Reply        connectors.OutboundReply
	DidRun       bool
}

func (taskSchedulePoller TaskSchedulePoller) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	taskSchedulePoller.logger().Info("task_schedule.poller.started", "intervalSecond", int(interval.Seconds()), "workerID", taskSchedulePoller.workerID())
	for ctx.Err() == nil {
		if runCount, errorValue := taskSchedulePoller.RunDue(ctx, time.Now().UTC(), 10); errorValue != nil {
			taskSchedulePoller.logger().Error("task_schedule.poller.failed", "error", errorValue.Error())
		} else if runCount > 0 {
			taskSchedulePoller.logger().Info("task_schedule.poller.completed", "runCount", runCount)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (taskSchedulePoller TaskSchedulePoller) RunDue(ctx context.Context, referenceTime time.Time, limit int) (int, error) {
	if taskSchedulePoller.TaskScheduleRepository == nil {
		return 0, errors.New("task schedule repository is unavailable")
	}
	taskSchedulePoller.cancelStaleScheduledTaskRuns(referenceTime)
	taskSchedules, errorValue := taskSchedulePoller.TaskScheduleRepository.ClaimDueTaskSchedules(limit, taskScheduleLeaseDuration, referenceTime, taskSchedulePoller.workerID())
	if errorValue != nil {
		return 0, errorValue
	}
	runCount := 0
	for _, taskSchedule := range taskSchedules {
		if ctx.Err() != nil {
			return runCount, ctx.Err()
		}
		if errorValue := taskSchedulePoller.runTaskSchedule(ctx, taskSchedule, referenceTime); errorValue != nil {
			taskSchedulePoller.logger().Error(
				"task_schedule.run.failed",
				"taskScheduleID",
				taskSchedule.TaskScheduleID,
				"nextRunAt",
				taskSchedule.NextRunAt,
				"error",
				errorValue.Error(),
			)
			_ = taskSchedulePoller.TaskScheduleRepository.MarkTaskScheduleFailed(taskSchedule, errorValue.Error(), referenceTime)
			continue
		}
		taskSchedulePoller.logger().Info("task_schedule.run.completed", "taskScheduleID", taskSchedule.TaskScheduleID)
		runCount++
	}
	return runCount, nil
}

func (taskSchedulePoller TaskSchedulePoller) runTaskSchedule(ctx context.Context, taskSchedule task.TaskSchedule, referenceTime time.Time) error {
	if errorValue := validateTaskScheduleDeliveryTarget(taskSchedule); errorValue != nil {
		return errorValue
	}
	result, errorValue := taskSchedulePoller.executeTaskSchedule(ctx, taskSchedule, referenceTime)
	if errorValue != nil {
		return errorValue
	}
	if !result.DidRun {
		return taskSchedulePoller.TaskScheduleRepository.MarkTaskScheduleSucceeded(result.TaskSchedule)
	}
	if errorValue := taskSchedulePoller.enqueueTaskScheduleReply(result); errorValue != nil {
		return errorValue
	}
	taskSchedulePoller.logger().Info(
		"task_schedule.reply.enqueued",
		"taskScheduleID",
		result.TaskSchedule.TaskScheduleID,
		"taskRunID",
		result.TaskRunID,
	)
	return taskSchedulePoller.TaskScheduleRepository.MarkTaskScheduleSucceeded(result.TaskSchedule)
}

func (taskSchedulePoller TaskSchedulePoller) executeTaskSchedule(ctx context.Context, taskSchedule task.TaskSchedule, referenceTime time.Time) (taskScheduleExecutionResult, error) {
	if taskSchedule.ExecutionMode == task.TaskScheduleExecutionModeMessage {
		return taskSchedulePoller.executeMessageTaskSchedule(taskSchedule, referenceTime)
	}
	return taskSchedulePoller.executeAgentTaskSchedule(ctx, taskSchedule, referenceTime)
}

func (taskSchedulePoller TaskSchedulePoller) executeAgentTaskSchedule(ctx context.Context, taskSchedule task.TaskSchedule, referenceTime time.Time) (taskScheduleExecutionResult, error) {
	personAccess := policy.PersonAccess{PersonID: taskSchedule.CreatorPersonID}
	if taskSchedulePoller.PersonAccessResolver != nil {
		personAccess = taskSchedulePoller.PersonAccessResolver.ResolvePersonAccess(taskSchedule.CreatorPersonID)
	}
	result, errorValue := taskSchedulePoller.TaskScheduleRunner.RunIfDue(ctx, agentruntime.TaskScheduleRunRequest{
		TaskSchedule:  taskSchedule,
		ReferenceTime: referenceTime,
		PersonAccess:  personAccess,
		WorkspaceID:   taskSchedulePoller.WorkspaceID,
	})
	if errorValue != nil {
		return taskScheduleExecutionResult{}, errorValue
	}
	if !result.DidRun {
		return taskScheduleExecutionResult{TaskSchedule: result.TaskSchedule}, nil
	}
	reply, errorValue := scheduledTaskReply(result)
	if errorValue != nil {
		return taskScheduleExecutionResult{}, errorValue
	}
	return taskScheduleExecutionResult{
		TaskSchedule: result.TaskSchedule,
		TaskRunID:    result.LaunchResult.TurnResult.TaskRun.TaskRunID,
		Reply:        reply,
		DidRun:       true,
	}, nil
}

func (taskSchedulePoller TaskSchedulePoller) executeMessageTaskSchedule(taskSchedule task.TaskSchedule, referenceTime time.Time) (taskScheduleExecutionResult, error) {
	if !(task.TaskScheduler{}).IsTaskScheduleDue(taskSchedule, referenceTime) {
		return taskScheduleExecutionResult{TaskSchedule: taskSchedule}, nil
	}
	if taskSchedulePoller.TaskRunService == nil {
		return taskScheduleExecutionResult{}, errors.New("task run service is unavailable")
	}
	taskRun := taskSchedulePoller.TaskRunService.CreateTaskRun(taskSchedule.CreatorPersonID, "schedule:"+taskSchedule.TaskScheduleID, taskSchedule.Prompt)
	if _, errorValue := taskSchedulePoller.TaskRunService.AdvanceTaskRun(taskRun.TaskRunID, firstNonEmptyString(taskSchedule.AgentProfileName, "default")); errorValue != nil {
		return taskScheduleExecutionResult{}, errorValue
	}
	completedTaskRun, errorValue := taskSchedulePoller.TaskRunService.CompleteTaskRun(taskRun.TaskRunID, taskSchedule.Prompt)
	if errorValue != nil {
		return taskScheduleExecutionResult{}, errorValue
	}
	advancedTaskSchedule, errorValue := (task.TaskScheduler{}).AdvanceTaskSchedule(taskSchedule, referenceTime)
	if errorValue != nil {
		return taskScheduleExecutionResult{}, errorValue
	}
	advancedTaskSchedule.LastTaskRunID = completedTaskRun.TaskRunID
	return taskScheduleExecutionResult{
		TaskSchedule: advancedTaskSchedule,
		TaskRunID:    completedTaskRun.TaskRunID,
		Reply:        connectors.OutboundReply{Message: taskSchedule.Prompt},
		DidRun:       true,
	}, nil
}

func (taskSchedulePoller TaskSchedulePoller) enqueueTaskScheduleReply(result taskScheduleExecutionResult) error {
	if taskSchedulePoller.DeliveryRepository == nil {
		return errors.New("task schedule delivery repository is unavailable")
	}
	_, errorValue := taskSchedulePoller.DeliveryRepository.EnqueueScheduledConnectorReply(result.TaskSchedule, result.TaskRunID, result.Reply)
	return errorValue
}

func scheduledTaskReply(result agentruntime.TaskScheduleRunResult) (connectors.OutboundReply, error) {
	turnResult := result.LaunchResult.TurnResult
	reply := strings.TrimSpace(turnResult.FinishMessage)
	if turnResult.TaskRun.Status != task.TaskStatusCompleted {
		return connectors.OutboundReply{}, errors.New("scheduled task did not complete: taskRunID=" + turnResult.TaskRun.TaskRunID + " status=" + string(turnResult.TaskRun.Status))
	}
	if reply == "" {
		return connectors.OutboundReply{}, errors.New("scheduled task completed without a reply")
	}
	if agent.FinishMessageContainsNonDeliverableArtifactLocator(reply) {
		return connectors.OutboundReply{}, errors.New("scheduled task reply exposes non-deliverable artifact locator")
	}
	if agent.FinishMessageClaimsAttachmentDelivery(reply) && len(turnResult.Attachments) == 0 {
		return connectors.OutboundReply{}, errors.New("scheduled task reply claims attachments without evidence")
	}
	return connectors.OutboundReply{Message: reply, Attachments: turnResult.Attachments}, nil
}

func (taskSchedulePoller TaskSchedulePoller) cancelStaleScheduledTaskRuns(referenceTime time.Time) {
	if taskSchedulePoller.TaskRunService == nil {
		return
	}
	if referenceTime.IsZero() {
		referenceTime = time.Now().UTC()
	}
	staleBefore := referenceTime.Add(-taskSchedulePoller.staleTaskRunTimeout())
	cancelledTaskRuns := taskSchedulePoller.TaskRunService.CancelActiveTaskRuns(task.TaskRunCancelRequest{
		OriginConversationIDPrefix: "schedule:",
		ScheduleOnly:               true,
		StaleBefore:                &staleBefore,
		Reason:                     "scheduled task stale timeout",
	})
	if len(cancelledTaskRuns) > 0 {
		taskSchedulePoller.logger().Warn("task_schedule.stale_runs_cancelled", "count", len(cancelledTaskRuns))
	}
}

func (taskSchedulePoller TaskSchedulePoller) staleTaskRunTimeout() time.Duration {
	if taskSchedulePoller.StaleTaskRunTimeout > 0 {
		return taskSchedulePoller.StaleTaskRunTimeout
	}
	return defaultStaleScheduledTaskRunTimeout
}

func validateTaskScheduleDeliveryTarget(taskSchedule task.TaskSchedule) error {
	if strings.TrimSpace(taskSchedule.Platform) == "" {
		return errors.New("scheduled task platform is required")
	}
	if strings.TrimSpace(taskSchedule.ConversationID) == "" {
		return errors.New("scheduled task conversation is required")
	}
	if strings.TrimSpace(taskSchedule.ReplyTargetID) == "" {
		return errors.New("scheduled task reply target is required")
	}
	return nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (taskSchedulePoller TaskSchedulePoller) workerID() string {
	if strings.TrimSpace(taskSchedulePoller.WorkerID) != "" {
		return strings.TrimSpace(taskSchedulePoller.WorkerID)
	}
	return "blueclaw-task-schedule-poller"
}

func (taskSchedulePoller TaskSchedulePoller) logger() *slog.Logger {
	if taskSchedulePoller.Logger != nil {
		return taskSchedulePoller.Logger
	}
	return slog.Default()
}
