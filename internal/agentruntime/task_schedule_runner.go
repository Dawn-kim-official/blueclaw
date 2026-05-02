package agentruntime

import (
	"context"
	"time"

	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
	"blueclaw/internal/task"
)

type TaskScheduleRunner struct {
	taskLauncher  *TaskLauncher
	taskScheduler task.TaskScheduler
	workspaceID   string
}

type TaskScheduleRunRequest struct {
	TaskSchedule  task.TaskSchedule
	ReferenceTime time.Time
	PersonAccess  policy.PersonAccess
	WorkspaceID   string
}

type TaskScheduleRunResult struct {
	TaskSchedule task.TaskSchedule
	LaunchResult TaskLaunchResult
	DidRun       bool
}

func NewTaskScheduleRunner(taskLauncher *TaskLauncher) TaskScheduleRunner {
	return TaskScheduleRunner{
		taskLauncher:  taskLauncher,
		taskScheduler: task.TaskScheduler{},
	}
}

func (taskScheduleRunner TaskScheduleRunner) RunIfDue(ctx context.Context, request TaskScheduleRunRequest) (TaskScheduleRunResult, error) {
	referenceTime := request.ReferenceTime
	if referenceTime.IsZero() {
		referenceTime = time.Now().UTC()
	}
	taskSchedule := request.TaskSchedule
	if !taskScheduleRunner.taskScheduler.IsTaskScheduleDue(taskSchedule, referenceTime) {
		return TaskScheduleRunResult{TaskSchedule: taskSchedule}, nil
	}
	workspaceID := request.WorkspaceID
	if workspaceID == "" {
		workspaceID = taskScheduleRunner.workspaceID
	}
	launchResult, errorValue := taskScheduleRunner.taskLauncher.Launch(ctx, TaskLaunchRequest{
		Source:                    TaskLaunchSourceScheduled,
		SourceReference:           taskSchedule.TaskScheduleID,
		RequesterPersonID:         taskSchedule.CreatorPersonID,
		ProfileName:               taskSchedule.AgentProfileName,
		ConversationID:            "schedule:" + taskSchedule.TaskScheduleID,
		Prompt:                    taskSchedule.Prompt,
		PersonAccess:              request.PersonAccess,
		MemoryNamespaces:          scheduledMemoryNamespaces(taskSchedule, request.PersonAccess, workspaceID),
		AccessibleConversationIDs: []string{"schedule:" + taskSchedule.TaskScheduleID},
	})
	if errorValue != nil {
		return TaskScheduleRunResult{}, errorValue
	}
	advancedTaskSchedule, errorValue := taskScheduleRunner.taskScheduler.AdvanceTaskSchedule(taskSchedule, referenceTime)
	if errorValue != nil {
		return TaskScheduleRunResult{}, errorValue
	}
	advancedTaskSchedule.LastTaskRunID = launchResult.TurnResult.TaskRun.TaskRunID
	return TaskScheduleRunResult{TaskSchedule: advancedTaskSchedule, LaunchResult: launchResult, DidRun: true}, nil
}

func scheduledMemoryNamespaces(taskSchedule task.TaskSchedule, personAccess policy.PersonAccess, workspaceID string) []memory.MemoryNamespace {
	conversationID := "schedule:" + taskSchedule.TaskScheduleID
	return []memory.MemoryNamespace{
		memory.UserNamespace(taskSchedule.CreatorPersonID),
		memory.WorkspaceNamespace(workspaceID, personAccess.SecurityLevelRank, personAccess.GrantedClasses),
		memory.ConversationNamespace(conversationID, personAccess.SecurityLevelRank, personAccess.GrantedClasses),
	}
}
