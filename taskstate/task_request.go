package taskstate

import "time"

type TaskRunOrigin struct {
	ConversationID string
	ReplyTargetID  string
	IsThread       bool
}

type TaskRunTransition struct {
	TaskRunID               string
	FromStates              []TaskStatus
	ToState                 TaskStatus
	CurrentAgentProfileName string
	Result                  string
	FailureReason           string
	StartedAttempt          *TaskAttempt
	FinishCurrentAttempt    bool
	FinishedAttemptStatus   TaskAttemptStatus
	RunnerID                string
	Event                   *TaskEvent
	UpdatedAt               time.Time
}

type TaskRunCancelRequest struct {
	TaskRunIDs                 []string
	RequesterPersonID          string
	OriginConversationIDs      []string
	OriginConversationIDPrefix string
	ScheduleOnly               bool
	StaleBefore                *time.Time
	Reason                     string
}

type RawTurnEvent struct {
	TaskRunID string
	Name      string
	Body      string
}
