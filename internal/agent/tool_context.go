package agent

import "context"

type toolContextKey string

const taskRunIDContextKey toolContextKey = "taskRunID"

func WithTaskRunID(ctx context.Context, taskRunID string) context.Context {
	if taskRunID == "" {
		return ctx
	}
	return context.WithValue(ctx, taskRunIDContextKey, taskRunID)
}

func TaskRunIDFromContext(ctx context.Context) string {
	taskRunID, _ := ctx.Value(taskRunIDContextKey).(string)
	return taskRunID
}
