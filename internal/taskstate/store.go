package taskstate

import "context"

// The harness records what a task did; the host decides where that record lives.

type TaskRunStore interface {
	AdvanceTaskRun(taskRunID string, currentAgentProfileName string) (TaskRun, error)
	AppendTaskEvent(taskRunID string, name string, body string)
	CancelActiveTaskRuns(request TaskRunCancelRequest) []TaskRun
	CancelTaskRunWithReason(taskRunID string, requesterPersonID string, reason string) (TaskRun, error)
	CompleteTaskRun(taskRunID string, result string) (TaskRun, error)
	CreateTaskRunWithOrigin(requesterPersonID string, origin TaskRunOrigin, prompt string) TaskRun
	CreateTaskRunWithOriginAndError(requesterPersonID string, origin TaskRunOrigin, prompt string) (TaskRun, error)
	FailTaskRun(taskRunID string, reason string) (TaskRun, error)
	FindTaskRun(taskRunID string) (TaskRun, bool)
	InterruptInactiveTaskRun(taskRunID string, reason string) (TaskRun, bool)
	IsTaskRunActuallyRunning(taskRun TaskRun) bool
	ListTaskEvent(taskRunID string) []TaskEvent
	ListTaskRun() []TaskRun
	ListTaskRunByPersonID(personID string) []TaskRun
	PauseTaskRun(taskRunID string, status TaskStatus, reason string) (TaskRun, error)
	RecordTaskRunResult(taskRunID string, result string) (TaskRun, error)
	RegisterTaskRunCancel(taskRunID string, cancelFunction context.CancelFunc) func()
	RegisterTaskRunObserver(taskRunID string, observer func(RawTurnEvent)) func()
	RegisterTaskRunTool(taskRunID string, observationID string, toolName string) func()
	ResumeTaskRun(taskRunID string) (TaskRun, error)
}

type TaskStepStore interface {
	AddTaskStep(taskStep TaskStep)
}

type TaskArtifactStore interface {
	AddTaskArtifactBody(taskRunID string, name string, body string) TaskArtifact
	ListTaskArtifact(taskRunID string) []TaskArtifact
}
