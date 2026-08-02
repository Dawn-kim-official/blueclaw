package bluecollar

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/internal/model"
)

type activeTaskFollowUpClassificationDocument struct {
	RelatesToActiveTask bool `json:"relatesToActiveTask"`
}

// ClassifyActiveTaskFollowUp answers a single narrow question with a cheap model tier:
// does the latest message continue, correct, cancel, or ask about the task already in
// progress, or is it a self-contained new and unrelated request. Callers use this to decide
// whether a message deserves an immediate shot at steering/cancelling that task instead of
// waiting behind it in the per-conversation queue.
func (agentKernel *AgentKernel) ClassifyActiveTaskFollowUp(ctx context.Context, request ActiveTaskFollowUpClassificationRequest) (bool, error) {
	languageModel := agentKernel.classificationLanguageModel()
	if languageModel == nil {
		return false, errors.New("language model is not configured")
	}
	structuredResponse, errorValue := languageModel.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		Messages:               activeTaskFollowUpClassificationMessages(request),
		StructuredOutputSchema: activeTaskFollowUpClassificationSchema(),
	})
	if errorValue != nil {
		return false, errorValue
	}
	var document activeTaskFollowUpClassificationDocument
	if errorValue := json.Unmarshal([]byte(structuredResponse.Content), &document); errorValue != nil {
		return false, errorValue
	}
	return document.RelatesToActiveTask, nil
}

func activeTaskFollowUpClassificationMessages(request ActiveTaskFollowUpClassificationRequest) []model.Message {
	return []model.Message{
		{
			Role:    "system",
			Content: "Decide whether the latest user message continues, corrects, cancels, or asks about the task already in progress, versus a self-contained new and unrelated request. Return only the requested JSON.",
		},
		{
			Role: "user",
			Content: strings.Join([]string{
				"Task in progress: " + strings.TrimSpace(request.ActiveTaskPrompt),
				"Task status: " + strings.TrimSpace(request.ActiveTaskStatus),
				"Latest user message: " + strings.TrimSpace(request.LatestMessage),
			}, "\n"),
		},
	}
}

func activeTaskFollowUpClassificationSchema() model.StructuredOutputSchema {
	return model.StructuredOutputSchema{
		Name:               "blueclaw_active_task_followup",
		Document:           `{"type":"object","properties":{"relatesToActiveTask":{"type":"boolean"}},"required":["relatesToActiveTask"],"additionalProperties":false}`,
		IsStrictlyEnforced: true,
	}
}
