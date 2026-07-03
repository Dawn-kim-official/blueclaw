package llm

import (
	"context"
	"errors"
	"log/slog"
)

type FallbackLanguageModelProvider struct {
	PrimaryProvider  LanguageModelProvider
	FallbackProvider LanguageModelProvider
	PrimaryLabel     string
	FallbackLabel    string
	Logger           *slog.Logger
}

func (fallbackLanguageModelProvider FallbackLanguageModelProvider) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	if fallbackLanguageModelProvider.PrimaryProvider == nil {
		return "", errors.New("primary provider is not configured")
	}

	response, errorValue := fallbackLanguageModelProvider.PrimaryProvider.GenerateResponse(responseContext, prompt)
	if errorValue == nil || fallbackLanguageModelProvider.FallbackProvider == nil {
		return response, errorValue
	}

	fallbackLanguageModelProvider.logFallback("text", errorValue)
	return fallbackLanguageModelProvider.FallbackProvider.GenerateResponse(responseContext, prompt)
}

func (fallbackLanguageModelProvider FallbackLanguageModelProvider) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest StructuredResponseRequest) (StructuredResponse, error) {
	if fallbackLanguageModelProvider.PrimaryProvider == nil {
		return StructuredResponse{}, errors.New("primary provider is not configured")
	}

	structuredResponse, errorValue := fallbackLanguageModelProvider.PrimaryProvider.GenerateStructuredResponse(responseContext, structuredResponseRequest)
	if errorValue == nil || fallbackLanguageModelProvider.FallbackProvider == nil {
		return structuredResponse, errorValue
	}

	fallbackLanguageModelProvider.logFallback("structured", errorValue)
	structuredResponse, errorValue = fallbackLanguageModelProvider.FallbackProvider.GenerateStructuredResponse(responseContext, structuredResponseRequest)
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}

	structuredResponse.UsedFallback = true
	return structuredResponse, nil
}

func (fallbackLanguageModelProvider FallbackLanguageModelProvider) logFallback(callKind string, primaryError error) {
	if fallbackLanguageModelProvider.Logger == nil {
		return
	}
	fallbackLanguageModelProvider.Logger.Warn(
		"language model call failed; falling back to alternate tier",
		"callKind", callKind,
		"failedTier", fallbackLanguageModelProvider.PrimaryLabel,
		"fallbackTier", fallbackLanguageModelProvider.FallbackLabel,
		"error", primaryError.Error(),
	)
}
