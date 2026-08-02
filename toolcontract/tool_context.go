package toolcontract

import (
	"context"
	"strings"
)

type toolContextKey string

const taskRunIDContextKey toolContextKey = "taskRunID"
const observationIDContextKey toolContextKey = "observationID"
const responseLanguageContextKey toolContextKey = "responseLanguage"
const userFacingMessageContextKey toolContextKey = "userFacingMessage"
const toolConflictResolutionContextKey toolContextKey = "toolConflictResolution"

type ToolConflictResolution string

const ToolConflictResolutionAllowDuplicate ToolConflictResolution = "allow_duplicate"

func WithUserFacingMessage(ctx context.Context, userFacingMessage string) context.Context {
	if strings.TrimSpace(userFacingMessage) == "" {
		return ctx
	}
	return context.WithValue(ctx, userFacingMessageContextKey, userFacingMessage)
}

func UserFacingMessageFromContext(ctx context.Context) string {
	userFacingMessage, _ := ctx.Value(userFacingMessageContextKey).(string)
	return userFacingMessage
}

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

func WithObservationID(ctx context.Context, observationID string) context.Context {
	if observationID == "" {
		return ctx
	}
	return context.WithValue(ctx, observationIDContextKey, observationID)
}

func ObservationIDFromContext(ctx context.Context) string {
	observationID, _ := ctx.Value(observationIDContextKey).(string)
	return observationID
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

func WithToolConflictResolution(ctx context.Context, resolution ToolConflictResolution) context.Context {
	if resolution == "" {
		return ctx
	}
	return context.WithValue(ctx, toolConflictResolutionContextKey, resolution)
}

func ToolConflictResolutionFromContext(ctx context.Context) ToolConflictResolution {
	resolution, _ := ctx.Value(toolConflictResolutionContextKey).(ToolConflictResolution)
	return resolution
}
