package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"blueclaw/internal/llm"
)

type AddressingClass string

const (
	AddressingClassAssistantRequested AddressingClass = "assistant_requested"
	AddressingClassHumanRequested     AddressingClass = "human_requested"
	AddressingClassAnyoneRequested    AddressingClass = "anyone_requested"
	AddressingClassNotARequest        AddressingClass = "not_a_request"
)

type AddressingClassificationRequest struct {
	Prompt           string
	ConversationType string
	SenderName       string
	SenderHandle     string
	VisibleContext   VisibleContext
}

type addressingClassificationDocument struct {
	AddressingClass AddressingClass `json:"addressingClass"`
}

func (agentKernel *AgentKernel) ClassifyAddressing(ctx context.Context, request AddressingClassificationRequest) (AddressingClass, error) {
	languageModel := agentKernel.addressingLanguageModel()
	if languageModel == nil {
		return "", errors.New("language model is not configured")
	}
	structuredResponse, errorValue := languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages:               addressingClassificationMessages(request),
		StructuredOutputSchema: addressingClassificationSchema(),
	})
	if errorValue != nil {
		return "", errorValue
	}
	var document addressingClassificationDocument
	if errorValue := json.Unmarshal([]byte(structuredResponse.Content), &document); errorValue != nil {
		return "", errorValue
	}
	if !isValidAddressingClass(document.AddressingClass) {
		return "", errors.New("invalid addressing class")
	}
	return document.AddressingClass, nil
}

func (agentKernel *AgentKernel) addressingLanguageModel() llm.LanguageModelProvider {
	if agentKernel.intakeLanguageModel != nil {
		return agentKernel.intakeLanguageModel
	}
	return agentKernel.languageModel
}

func addressingClassificationMessages(request AddressingClassificationRequest) []llm.Message {
	return []llm.Message{
		{Role: "system", Content: "Classify who is being asked to respond in a multi-person conversation. Return only the requested JSON enum. Do not answer the user."},
		{Role: "user", Content: addressingClassificationPrompt(request)},
	}
}

func addressingClassificationPrompt(request AddressingClassificationRequest) string {
	lines := []string{
		"conversationType: " + strings.TrimSpace(request.ConversationType),
		"senderName: " + strings.TrimSpace(request.SenderName),
		"senderHandle: " + strings.TrimSpace(request.SenderHandle),
		"message: " + strings.TrimSpace(request.Prompt),
	}
	for _, message := range recentVisibleMessages(request.VisibleContext.Messages, 6) {
		speaker := firstNonEmptyAddressingText(message.SpeakerCallingName, message.Speaker, message.SpeakerHandle, "unknown")
		lines = append(lines, "context: "+speaker+": "+strings.TrimSpace(message.Text))
	}
	return strings.Join(lines, "\n")
}

func recentVisibleMessages(messages []VisibleContextMessage, limit int) []VisibleContextMessage {
	if limit <= 0 || len(messages) <= limit {
		return messages
	}
	return messages[len(messages)-limit:]
}

func firstNonEmptyAddressingText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func addressingClassificationSchema() llm.StructuredOutputSchema {
	return llm.StructuredOutputSchema{
		Name:               "blueclaw_addressing_classification",
		Document:           `{"type":"object","properties":{"addressingClass":{"type":"string","enum":["assistant_requested","human_requested","anyone_requested","not_a_request"]}},"required":["addressingClass"],"additionalProperties":false}`,
		IsStrictlyEnforced: true,
	}
}

func isValidAddressingClass(addressingClass AddressingClass) bool {
	switch addressingClass {
	case AddressingClassAssistantRequested, AddressingClassHumanRequested, AddressingClassAnyoneRequested, AddressingClassNotARequest:
		return true
	}
	return false
}
