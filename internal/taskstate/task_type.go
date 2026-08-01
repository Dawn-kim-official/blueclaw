package taskstate

import "time"

type TaskStatus string

const (
	TaskStatusPlanned          TaskStatus = "planned"
	TaskStatusRunning          TaskStatus = "running"
	TaskStatusWaitingUserInput TaskStatus = "waiting_user_input"
	TaskStatusWaitingApproval  TaskStatus = "waiting_approval"
	TaskStatusBlocked          TaskStatus = "blocked"
	TaskStatusInterrupted      TaskStatus = "interrupted"
	TaskStatusCompleted        TaskStatus = "completed"
	TaskStatusFailed           TaskStatus = "failed"
	TaskStatusCancelled        TaskStatus = "cancelled"
)

const TaskInterruptReasonPlannedShutdown = "planned_shutdown"
const TaskInterruptReasonRuntimeRestart = "runtime restarted before task completed"

func TaskRunWasInterruptedByRuntimeRestart(taskRun TaskRun) bool {
	if taskRun.Status != TaskStatusInterrupted {
		return false
	}
	return taskRun.FailureReason == TaskInterruptReasonRuntimeRestart || taskRun.FailureReason == TaskInterruptReasonPlannedShutdown
}

type TaskAttemptStatus string

const (
	TaskAttemptStatusStarting    TaskAttemptStatus = "starting"
	TaskAttemptStatusRunning     TaskAttemptStatus = "running"
	TaskAttemptStatusCompleted   TaskAttemptStatus = "completed"
	TaskAttemptStatusFailed      TaskAttemptStatus = "failed"
	TaskAttemptStatusCancelled   TaskAttemptStatus = "cancelled"
	TaskAttemptStatusInterrupted TaskAttemptStatus = "interrupted"
)

type TaskScheduleKind string

const (
	TaskScheduleKindOnce     TaskScheduleKind = "once"
	TaskScheduleKindInterval TaskScheduleKind = "interval"
	TaskScheduleKindCron     TaskScheduleKind = "cron"
)

type TaskScheduleExecutionMode string

const (
	TaskScheduleExecutionModeAgent   TaskScheduleExecutionMode = "agent"
	TaskScheduleExecutionModeMessage TaskScheduleExecutionMode = "message"
)

type TaskRun struct {
	TaskRunID               string     `json:"taskRunID"`
	RequesterPersonID       string     `json:"requesterPersonID"`
	OriginConversationID    string     `json:"originConversationID"`
	OriginReplyTargetID     string     `json:"originReplyTargetID,omitempty"`
	OriginIsThread          bool       `json:"originIsThread,omitempty"`
	CurrentAttemptID        string     `json:"currentAttemptID,omitempty"`
	CurrentAgentProfileName string     `json:"currentAgentProfileName"`
	Status                  TaskStatus `json:"status"`
	Prompt                  string     `json:"prompt"`
	Result                  string     `json:"result"`
	FailureReason           string     `json:"failureReason"`
	CreatedAt               time.Time  `json:"createdAt"`
	UpdatedAt               time.Time  `json:"updatedAt"`
}

type TaskAttempt struct {
	TaskAttemptID string            `json:"taskAttemptID"`
	TaskRunID     string            `json:"taskRunID"`
	RunnerID      string            `json:"runnerID"`
	Status        TaskAttemptStatus `json:"status"`
	StartedAt     time.Time         `json:"startedAt"`
	FinishedAt    *time.Time        `json:"finishedAt,omitempty"`
	FailureReason string            `json:"failureReason,omitempty"`
}

type TaskStep struct {
	TaskStepID               string     `json:"taskStepID"`
	TaskRunID                string     `json:"taskRunID"`
	ParentTaskStepID         string     `json:"parentTaskStepID"`
	AssignedAgentProfileName string     `json:"assignedAgentProfileName"`
	Instruction              string     `json:"instruction"`
	Status                   TaskStatus `json:"status"`
	Output                   string     `json:"output"`
}

type TaskEvent struct {
	TaskEventID string    `json:"taskEventID"`
	TaskRunID   string    `json:"taskRunID"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	CreatedAt   time.Time `json:"createdAt"`
}

type TaskArtifact struct {
	TaskArtifactID string `json:"taskArtifactID"`
	TaskRunID      string `json:"taskRunID"`
	Name           string `json:"name"`
	Body           string `json:"body"`
}

type TaskWaitToken struct {
	WaitID         string     `json:"waitID"`
	TaskRunID      string     `json:"taskRunID"`
	PersonID       string     `json:"personID"`
	Platform       string     `json:"platform"`
	ConversationID string     `json:"conversationID"`
	ReplyTargetID  string     `json:"replyTargetID"`
	ThreadRootID   string     `json:"threadRootID"`
	DispatchID     string     `json:"dispatchID"`
	InteractionID  string     `json:"interactionID"`
	Kind           string     `json:"kind"`
	State          string     `json:"state"`
	ExpiresAt      time.Time  `json:"expiresAt"`
	CreatedAt      time.Time  `json:"createdAt"`
	ResolvedAt     *time.Time `json:"resolvedAt,omitempty"`
}

type TaskSession struct {
	TaskSessionID string    `json:"taskSessionID"`
	PersonID      string    `json:"personID"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

type TaskSchedule struct {
	TaskScheduleID    string                    `json:"taskScheduleID"`
	CreatorPersonID   string                    `json:"creatorPersonID"`
	Name              string                    `json:"name"`
	Prompt            string                    `json:"prompt"`
	ExecutionMode     TaskScheduleExecutionMode `json:"executionMode"`
	AgentProfileName  string                    `json:"agentProfileName"`
	Platform          string                    `json:"platform"`
	ConversationID    string                    `json:"conversationID"`
	ReplyTargetID     string                    `json:"replyTargetID"`
	TimeZone          string                    `json:"timeZone"`
	Kind              TaskScheduleKind          `json:"kind"`
	RunAt             *time.Time                `json:"runAt"`
	IntervalSecond    int                       `json:"intervalSecond"`
	CronExpression    string                    `json:"cronExpression"`
	MaxRunCount       int                       `json:"maxRunCount,omitempty"`
	CompletedRunCount int                       `json:"completedRunCount"`
	ExpiresAt         *time.Time                `json:"expiresAt"`
	NextRunAt         *time.Time                `json:"nextRunAt"`
	LastRunAt         *time.Time                `json:"lastRunAt"`
	LastTaskRunID     string                    `json:"lastTaskRunID"`
	LeaseOwner        string                    `json:"leaseOwner"`
	LeasedUntil       *time.Time                `json:"leasedUntil"`
	FailureCount      int                       `json:"failureCount"`
	LastError         string                    `json:"lastError"`
	NextAttemptAt     *time.Time                `json:"nextAttemptAt"`
	CreatedAt         time.Time                 `json:"createdAt"`
	UpdatedAt         time.Time                 `json:"updatedAt"`
}
