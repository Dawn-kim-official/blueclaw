package llm

import "context"

type VisionFallbackProvider struct {
	TextOnlyModel LanguageModelProvider
	VisionModel   LanguageModelProvider
}

func (provider VisionFallbackProvider) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	return provider.TextOnlyModel.GenerateResponse(responseContext, prompt)
}

func (provider VisionFallbackProvider) GenerateStructuredResponse(responseContext context.Context, request StructuredResponseRequest) (StructuredResponse, error) {
	if requestContainsImage(request) {
		return provider.VisionModel.GenerateStructuredResponse(responseContext, request)
	}
	return provider.TextOnlyModel.GenerateStructuredResponse(responseContext, request)
}

func requestContainsImage(request StructuredResponseRequest) bool {
	for _, message := range request.Messages {
		if messagePartsContainImage(message.Parts) {
			return true
		}
	}
	return false
}

func chatRequestContainsImage(request ChatCompletionRequest) bool {
	for _, message := range request.Messages {
		if messagePartsContainImage(message.Parts) {
			return true
		}
	}
	return false
}

func messagePartsContainImage(parts []MessagePart) bool {
	for _, part := range parts {
		if part.Type == "image" {
			return true
		}
	}
	return false
}
