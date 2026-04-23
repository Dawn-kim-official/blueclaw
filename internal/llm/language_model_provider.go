package llm

import "context"

type LanguageModelProvider interface {
	GenerateResponse(ctx context.Context, prompt string) (string, error)
}
