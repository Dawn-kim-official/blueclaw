package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/task"
)

const defaultRetentionDays = 14

type TaskRetentionSweeper struct {
	TaskRunService      *task.TaskRunService
	TaskEventService    *task.TaskEventService
	TaskStepService     *task.TaskStepService
	TaskArtifactService *task.TaskArtifactService
	Logger              *slog.Logger
	RetentionDays       int
}

func (sweeper TaskRetentionSweeper) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		sweeper.SweepOnce(time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (sweeper TaskRetentionSweeper) SweepOnce(now time.Time) int {
	retentionDays := sweeper.RetentionDays
	if retentionDays <= 0 {
		retentionDays = defaultRetentionDays
	}
	cutoff := now.AddDate(0, 0, -retentionDays)
	prunedIDs := sweeper.TaskRunService.PruneTerminalTaskRunsBefore(cutoff)
	for _, taskRunID := range prunedIDs {
		sweeper.TaskEventService.RemoveTaskRunEvents(taskRunID)
		sweeper.TaskStepService.RemoveTaskRunSteps(taskRunID)
		sweeper.TaskArtifactService.RemoveTaskRunArtifacts(taskRunID)
	}
	if len(prunedIDs) > 0 {
		sweeper.logger().Info("task_retention.swept", "count", len(prunedIDs))
	}
	return len(prunedIDs)
}

func (sweeper TaskRetentionSweeper) logger() *slog.Logger {
	if sweeper.Logger != nil {
		return sweeper.Logger
	}
	return slog.Default()
}
