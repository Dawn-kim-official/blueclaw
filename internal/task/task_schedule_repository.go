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

type TaskScheduleSummary struct {
	ActiveCount       int        `json:"activeCount"`
	UnboundedCount    int        `json:"unboundedCount"`
	IntervalCount     int        `json:"intervalCount"`
	CronCount         int        `json:"cronCount"`
	OnceCount         int        `json:"onceCount"`
	EarliestNextRunAt *time.Time `json:"earliestNextRunAt,omitempty"`
	LatestNextRunAt   *time.Time `json:"latestNextRunAt,omitempty"`
	CheckedAt         time.Time  `json:"checkedAt"`
}

type TaskScheduleListRequest struct {
	ConversationID  string
	CreatorPersonID string
	UnboundedOnly   bool
	Limit           int
	ReferenceTime   time.Time
}

type TaskScheduleDeliveryGroupRequest struct {
	UnboundedOnly bool
	Limit         int
	ReferenceTime time.Time
}

type TaskScheduleDeliveryGroup struct {
	ConversationID  string     `json:"deliveryConversationID"`
	ActiveCount     int        `json:"activeCount"`
	UnboundedCount  int        `json:"unboundedCount"`
	LatestCreatedAt *time.Time `json:"latestCreatedAt,omitempty"`
	LatestNextRunAt *time.Time `json:"latestNextRunAt,omitempty"`
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
