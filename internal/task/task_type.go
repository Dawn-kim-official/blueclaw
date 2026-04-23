package task

import "time"

type TaskStatus string

const (
	TaskStatusPlanned          TaskStatus = "planned"
	TaskStatusRunning          TaskStatus = "running"
	TaskStatusWaitingUserInput TaskStatus = "waiting_user_input"
	TaskStatusWaitingApproval  TaskStatus = "waiting_approval"
	TaskStatusBlocked          TaskStatus = "blocked"
	TaskStatusCompleted        TaskStatus = "completed"
	TaskStatusFailed           TaskStatus = "failed"
	TaskStatusCancelled        TaskStatus = "cancelled"
)

type TaskRun struct {
	TaskRunID               string     `json:"taskRunID"`
	RequesterPersonID       string     `json:"requesterPersonID"`
	OriginConversationID    string     `json:"originConversationID"`
	CurrentAgentProfileName string     `json:"currentAgentProfileName"`
	Status                  TaskStatus `json:"status"`
	Prompt                  string     `json:"prompt"`
	Result                  string     `json:"result"`
	FailureReason           string     `json:"failureReason"`
	CreatedAt               time.Time  `json:"createdAt"`
	UpdatedAt               time.Time  `json:"updatedAt"`
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
	TaskWaitTokenID string    `json:"taskWaitTokenID"`
	PersonID        string    `json:"personID"`
	TaskRunID       string    `json:"taskRunID"`
	TokenHash       string    `json:"tokenHash"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

type TaskSession struct {
	TaskSessionID string    `json:"taskSessionID"`
	PersonID      string    `json:"personID"`
	ExpiresAt     time.Time `json:"expiresAt"`
}
