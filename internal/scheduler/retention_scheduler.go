package scheduler

import "time"

type RetentionScheduler struct{}

func (retentionScheduler RetentionScheduler) NextRetentionCheck(intervalMinute int) time.Time {
	return time.Now().Add(time.Duration(intervalMinute) * time.Minute)
}
