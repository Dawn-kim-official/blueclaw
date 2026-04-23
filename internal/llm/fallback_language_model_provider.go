package llm

import (
	"context"
	"errors"
)

type FallbackLanguageModelProvider struct {
	PrimaryProvider  LanguageModelProvider
	FallbackProvider LanguageModelProvider
}

func (fallbackLanguageModelProvider FallbackLanguageModelProvider) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	if fallbackLanguageModelProvider.PrimaryProvider == nil {
		return "", errors.New("primary provider is not configured")
	}

	response, errorValue := fallbackLanguageModelProvider.PrimaryProvider.GenerateResponse(responseContext, prompt)
	if errorValue == nil || fallbackLanguageModelProvider.FallbackProvider == nil {
		return response, errorValue
	}

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

	structuredResponse, errorValue = fallbackLanguageModelProvider.FallbackProvider.GenerateStructuredResponse(responseContext, structuredResponseRequest)
	if errorValue != nil {
		return StructuredResponse{}, errorValue
	}

	structuredResponse.UsedFallback = true
	return structuredResponse, nil
}
