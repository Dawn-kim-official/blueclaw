package bluecollar

import "strings"

type ScheduledRunContext struct {
	ScheduleID        string `json:"scheduleID,omitempty"`
	Name              string `json:"name,omitempty"`
	Kind              string `json:"kind,omitempty"`
	Cadence           string `json:"cadence,omitempty"`
	CronExpression    string `json:"cronExpression,omitempty"`
	TimeZone          string `json:"timeZone,omitempty"`
	OccurrenceAt      string `json:"occurrenceAt,omitempty"`
	RunAt             string `json:"runAt,omitempty"`
	IntervalSecond    int    `json:"intervalSecond,omitempty"`
	CompletedRunCount int    `json:"completedRunCount,omitempty"`
	MaxRunCount       int    `json:"maxRunCount,omitempty"`
	LastRunAt         string `json:"lastRunAt,omitempty"`
	NextRunAt         string `json:"nextRunAt,omitempty"`
	ExpiresAt         string `json:"expiresAt,omitempty"`
}

func (context ScheduledRunContext) IsEmpty() bool {
	return strings.TrimSpace(context.ScheduleID) == "" &&
		strings.TrimSpace(context.Kind) == "" &&
		strings.TrimSpace(context.OccurrenceAt) == ""
}
