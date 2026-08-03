package intake

import (
	"context"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/model"
)

type sequenceLanguageModel struct {
	modelTier     string
	contents      []string
	textResponses []string
	requests      []model.StructuredResponseRequest
	textPrompts   []string
}

func (languageModel *sequenceLanguageModel) GenerateResponse(_ context.Context, prompt string) (string, error) {
	return languageModel.nextTextResponse(prompt), nil
}

func (languageModel *sequenceLanguageModel) GenerateRecoveryChatCompletion(_ context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	prompt := ""
	if len(request.Messages) > 0 {
		prompt = request.Messages[len(request.Messages)-1].Content
	}
	return model.ChatCompletionResponse{
		FinishReason:    "stop",
		SelectedBackend: "remote",
		Message:         model.ChatCompletionMessage{Role: "assistant", Content: languageModel.nextTextResponse(prompt)},
	}, nil
}

func (languageModel *sequenceLanguageModel) nextTextResponse(prompt string) string {
	languageModel.textPrompts = append(languageModel.textPrompts, prompt)
	index := len(languageModel.textPrompts) - 1
	if index >= len(languageModel.textResponses) {
		return ""
	}
	return languageModel.textResponses[index]
}

func (languageModel *sequenceLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	languageModel.requests = append(languageModel.requests, request)
	index := len(languageModel.requests) - 1
	if index >= len(languageModel.contents) {
		index = len(languageModel.contents) - 1
	}
	return model.StructuredResponse{ModelTier: languageModel.modelTier, Content: languageModel.contents[index]}, nil
}

func joinMessageContent(messages []model.Message) string {
	parts := []string{}
	for _, message := range messages {
		parts = append(parts, message.Content)
		for _, messagePart := range message.Parts {
			if messagePart.Type == "text" {
				parts = append(parts, messagePart.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}
