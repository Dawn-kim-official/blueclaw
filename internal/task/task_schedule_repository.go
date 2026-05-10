package task

import "time"

type TaskScheduleCancelScope string

const (
	TaskScheduleCancelScopeCurrentConversation TaskScheduleCancelScope = "currentConversation"
	TaskScheduleCancelScopeMine                TaskScheduleCancelScope = "mine"
	TaskScheduleCancelScopeScheduleIDs         TaskScheduleCancelScope = "scheduleIDs"
)

type TaskScheduleCancelRequest struct {
	Scope             TaskScheduleCancelScope
	RequesterPersonID string
	ConversationID    string
	TaskScheduleIDs   []string
	CancelledAt       time.Time
}

type TaskScheduleCancelResult struct {
	TaskSchedules []TaskSchedule `json:"taskSchedules"`
}

type TaskScheduleRepository interface {
	UpsertTaskSchedule(TaskSchedule) error
	ClaimDueTaskSchedules(int, time.Duration, time.Time, string) ([]TaskSchedule, error)
	MarkTaskScheduleSucceeded(TaskSchedule) error
	MarkTaskScheduleFailed(TaskSchedule, string, time.Time) error
	CancelTaskSchedules(TaskScheduleCancelRequest) (TaskScheduleCancelResult, error)
}

type TaskWaitTokenRepository interface {
	ExpireTaskWaitTokensForPerson(string, time.Time) ([]string, error)
}
