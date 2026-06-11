package llm

import "context"

type Message struct {
	Role    string        `json:"role"`
	Content string        `json:"content,omitempty"`
	Parts   []MessagePart `json:"parts,omitempty"`
}

type MessagePart struct {
	Type       string `json:"type"`
	Text       string `json:"text,omitempty"`
	MimeType   string `json:"mimeType,omitempty"`
	DataBase64 string `json:"dataBase64,omitempty"`
}

type StructuredOutputSchema struct {
	Name               string
	Document           string
	IsStrictlyEnforced bool
}

type GenerationOptions struct {
	Seed        *int64   `json:"seed,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
}

type StructuredResponseRequest struct {
	Messages               []Message
	StructuredOutputSchema StructuredOutputSchema
	GenerationOptions      GenerationOptions
}

type RequestContext struct {
	RequesterPersonID       string `json:"requesterPersonID,omitempty"`
	RequesterEmail          string `json:"requesterEmail,omitempty"`
	RequesterName           string `json:"requesterName,omitempty"`
	RequesterPlatformUserID string `json:"requesterPlatformUserID,omitempty"`
	ConversationID          string `json:"conversationID,omitempty"`
	Platform                string `json:"platform,omitempty"`
}

type requestContextKey struct{}

func ContextWithRequestContext(ctx context.Context, requestContext RequestContext) context.Context {
	return context.WithValue(ctx, requestContextKey{}, requestContext)
}

func RequestContextFromContext(ctx context.Context) RequestContext {
	requestContext, _ := ctx.Value(requestContextKey{}).(RequestContext)
	return requestContext
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

type RecoveryResponder interface {
	GenerateRecoveryResponse(context.Context, string) (string, error)
}

type LocalRecoveryResponder interface {
	GenerateLocalRecoveryResponse(context.Context, string) (string, error)
}
