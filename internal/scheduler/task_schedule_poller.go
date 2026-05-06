package scheduler

import (
	"context"
	"errors"
	"strings"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/agentruntime"
	"blueclaw/internal/connectors"
	"blueclaw/internal/policy"
	"blueclaw/internal/task"
)

const taskScheduleLeaseDuration = 15 * time.Minute

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
	PersonAccessResolver   PersonAccessResolver
	WorkspaceID            string
	WorkerID               string
}

func (taskSchedulePoller TaskSchedulePoller) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		_, _ = taskSchedulePoller.RunDue(ctx, time.Now().UTC(), 10)
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
			_ = taskSchedulePoller.TaskScheduleRepository.MarkTaskScheduleFailed(taskSchedule, errorValue.Error(), referenceTime)
			continue
		}
		runCount++
	}
	return runCount, nil
}

func (taskSchedulePoller TaskSchedulePoller) runTaskSchedule(ctx context.Context, taskSchedule task.TaskSchedule, referenceTime time.Time) error {
	if errorValue := validateTaskScheduleDeliveryTarget(taskSchedule); errorValue != nil {
		return errorValue
	}
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
		return errorValue
	}
	if !result.DidRun {
		return taskSchedulePoller.TaskScheduleRepository.MarkTaskScheduleSucceeded(result.TaskSchedule)
	}
	if errorValue := taskSchedulePoller.enqueueTaskScheduleReply(result); errorValue != nil {
		return errorValue
	}
	return taskSchedulePoller.TaskScheduleRepository.MarkTaskScheduleSucceeded(result.TaskSchedule)
}

func (taskSchedulePoller TaskSchedulePoller) enqueueTaskScheduleReply(result agentruntime.TaskScheduleRunResult) error {
	if taskSchedulePoller.DeliveryRepository == nil {
		return errors.New("task schedule delivery repository is unavailable")
	}
	reply, errorValue := scheduledTaskReply(result)
	if errorValue != nil {
		return errorValue
	}
	_, errorValue = taskSchedulePoller.DeliveryRepository.EnqueueScheduledConnectorReply(result.TaskSchedule, result.LaunchResult.TurnResult.TaskRun.TaskRunID, reply)
	return errorValue
}

func scheduledTaskReply(result agentruntime.TaskScheduleRunResult) (connectors.OutboundReply, error) {
	turnResult := result.LaunchResult.TurnResult
	reply := strings.TrimSpace(turnResult.FinalReply)
	if turnResult.TaskRun.Status != task.TaskStatusCompleted {
		if reply == "" {
			reply = strings.TrimSpace(turnResult.TaskRun.FailureReason)
		}
		if reply == "" {
			reply = agent.BuildIncompleteTaskRecoveryReply(result.TaskSchedule.Prompt, "task_not_completed")
		}
		return connectors.OutboundReply{Message: reply, Attachments: turnResult.Attachments}, nil
	}
	if reply == "" {
		return connectors.OutboundReply{}, errors.New("scheduled task completed without a reply")
	}
	if agent.FinalReplyContainsNonDeliverableArtifactLocator(reply) {
		return connectors.OutboundReply{}, errors.New("scheduled task reply exposes non-deliverable artifact locator")
	}
	if agent.FinalReplyClaimsAttachmentDelivery(reply) && len(turnResult.Attachments) == 0 {
		return connectors.OutboundReply{}, errors.New("scheduled task reply claims attachments without evidence")
	}
	return connectors.OutboundReply{Message: reply, Attachments: turnResult.Attachments}, nil
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

func (taskSchedulePoller TaskSchedulePoller) workerID() string {
	if strings.TrimSpace(taskSchedulePoller.WorkerID) != "" {
		return strings.TrimSpace(taskSchedulePoller.WorkerID)
	}
	return "blueclaw-task-schedule-poller"
}
