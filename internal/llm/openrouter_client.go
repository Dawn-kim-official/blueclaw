package llm

import "context"

type OpenRouterClient struct{}

func (openRouterClient OpenRouterClient) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	_ = ctx
	return prompt, nil
}
