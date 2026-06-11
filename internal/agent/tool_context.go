package agent

import "context"

type toolContextKey string

const taskRunIDContextKey toolContextKey = "taskRunID"
const responseLanguageContextKey toolContextKey = "responseLanguage"
const workKindsContextKey toolContextKey = "workKinds"

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

func WithWorkKinds(ctx context.Context, workKinds []string) context.Context {
	if len(workKinds) == 0 {
		return ctx
	}
	return context.WithValue(ctx, workKindsContextKey, append([]string{}, workKinds...))
}

func WorkKindsFromContext(ctx context.Context) []string {
	workKinds, _ := ctx.Value(workKindsContextKey).([]string)
	return workKinds
}
