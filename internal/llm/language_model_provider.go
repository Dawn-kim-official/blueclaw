package llm

import "context"

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type StructuredOutputSchema struct {
	Name               string
	Document           string
	IsStrictlyEnforced bool
}

type StructuredResponseRequest struct {
	Messages               []Message
	StructuredOutputSchema StructuredOutputSchema
}

type StructuredResponse struct {
	ProviderName string
	ModelName    string
	Content      string
	UsedFallback bool
}

type LanguageModelProvider interface {
	GenerateResponse(context.Context, string) (string, error)
	GenerateStructuredResponse(context.Context, StructuredResponseRequest) (StructuredResponse, error)
}
