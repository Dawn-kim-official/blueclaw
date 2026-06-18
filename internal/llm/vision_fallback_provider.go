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
		for _, part := range message.Parts {
			if part.Type == "image" {
				return true
			}
		}
	}
	return false
}
