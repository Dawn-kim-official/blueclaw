package task

import "github.com/yeomyeonggeori/bluecollar/taskstate"

type (
	ErrIllegalTransition           = taskstate.ErrIllegalTransition
	InterruptedTaskResumeSelection = taskstate.InterruptedTaskResumeSelection
	RawTurnEvent                   = taskstate.RawTurnEvent
	TaskArtifact                   = taskstate.TaskArtifact
	TaskArtifactRepository         = taskstate.TaskArtifactRepository
	TaskArtifactService            = taskstate.TaskArtifactService
	TaskArtifactStore              = taskstate.TaskArtifactStore
	TaskAttempt                    = taskstate.TaskAttempt
	TaskAttemptStatus              = taskstate.TaskAttemptStatus
	TaskEvent                      = taskstate.TaskEvent
	TaskEventRepository            = taskstate.TaskEventRepository
	TaskEventService               = taskstate.TaskEventService
	TaskRun                        = taskstate.TaskRun
	TaskRunCancelRequest           = taskstate.TaskRunCancelRequest
	TaskRunOrigin                  = taskstate.TaskRunOrigin
	TaskRunRepository              = taskstate.TaskRunRepository
	TaskRunService                 = taskstate.TaskRunService
	TaskRunStore                   = taskstate.TaskRunStore
	TaskRunTransition              = taskstate.TaskRunTransition
	TaskSchedule                   = taskstate.TaskSchedule
	TaskScheduleExecutionMode      = taskstate.TaskScheduleExecutionMode
	TaskScheduleKind               = taskstate.TaskScheduleKind
	TaskSession                    = taskstate.TaskSession
	TaskStatus                     = taskstate.TaskStatus
	TaskStep                       = taskstate.TaskStep
	TaskStepRepository             = taskstate.TaskStepRepository
	TaskStepService                = taskstate.TaskStepService
	TaskStepStore                  = taskstate.TaskStepStore
	TaskWaitToken                  = taskstate.TaskWaitToken
)

const (
	TaskAttemptStatusCancelled         = taskstate.TaskAttemptStatusCancelled
	TaskAttemptStatusCompleted         = taskstate.TaskAttemptStatusCompleted
	TaskAttemptStatusFailed            = taskstate.TaskAttemptStatusFailed
	TaskAttemptStatusInterrupted       = taskstate.TaskAttemptStatusInterrupted
	TaskAttemptStatusRunning           = taskstate.TaskAttemptStatusRunning
	TaskAttemptStatusStarting          = taskstate.TaskAttemptStatusStarting
	TaskInterruptReasonPlannedShutdown = taskstate.TaskInterruptReasonPlannedShutdown
	TaskInterruptReasonRuntimeRestart  = taskstate.TaskInterruptReasonRuntimeRestart
	TaskScheduleExecutionModeAgent     = taskstate.TaskScheduleExecutionModeAgent
	TaskScheduleExecutionModeMessage   = taskstate.TaskScheduleExecutionModeMessage
	TaskScheduleKindCron               = taskstate.TaskScheduleKindCron
	TaskScheduleKindInterval           = taskstate.TaskScheduleKindInterval
	TaskScheduleKindOnce               = taskstate.TaskScheduleKindOnce
	TaskStatusBlocked                  = taskstate.TaskStatusBlocked
	TaskStatusCancelled                = taskstate.TaskStatusCancelled
	TaskStatusCompleted                = taskstate.TaskStatusCompleted
	TaskStatusFailed                   = taskstate.TaskStatusFailed
	TaskStatusInterrupted              = taskstate.TaskStatusInterrupted
	TaskStatusPlanned                  = taskstate.TaskStatusPlanned
	TaskStatusRunning                  = taskstate.TaskStatusRunning
	TaskStatusWaitingApproval          = taskstate.TaskStatusWaitingApproval
	TaskStatusWaitingUserInput         = taskstate.TaskStatusWaitingUserInput
)

var (
	ErrTaskRunAccessDenied                = taskstate.ErrTaskRunAccessDenied
	ErrTaskRunNotDeletable                = taskstate.ErrTaskRunNotDeletable
	ErrTaskRunNotFound                    = taskstate.ErrTaskRunNotFound
	NewIdentifier                         = taskstate.NewIdentifier
	NewTaskArtifactService                = taskstate.NewTaskArtifactService
	NewTaskEventService                   = taskstate.NewTaskEventService
	NewTaskRunService                     = taskstate.NewTaskRunService
	NewTaskStepService                    = taskstate.NewTaskStepService
	StaleUnattendedTaskRunReason          = taskstate.StaleUnattendedTaskRunReason
	TaskRunWasInterruptedByRuntimeRestart = taskstate.TaskRunWasInterruptedByRuntimeRestart
)
