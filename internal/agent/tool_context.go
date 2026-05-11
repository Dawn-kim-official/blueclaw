package agent

import "context"

type toolContextKey string

const taskRunIDContextKey toolContextKey = "taskRunID"
const responseLanguageContextKey toolContextKey = "responseLanguage"

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

func WithResponseLanguage(ctx context.Context, responseLanguage string) context.Context {
	normalizedLanguage := ResolveResponseLanguage(responseLanguage)
	if normalizedLanguage == "" {
		return ctx
	}
	return context.WithValue(ctx, responseLanguageContextKey, normalizedLanguage)
}

func ResponseLanguageFromContext(ctx context.Context) string {
	responseLanguage, _ := ctx.Value(responseLanguageContextKey).(string)
	return ResolveResponseLanguage(responseLanguage)
}
