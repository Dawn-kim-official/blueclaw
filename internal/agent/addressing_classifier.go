package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"blueclaw/internal/llm"
)

type AddressingTarget string

const (
	AddressingTargetBot     AddressingTarget = "bot"
	AddressingTargetHuman   AddressingTarget = "human"
	AddressingTargetAnyone  AddressingTarget = "anyone"
	AddressingTargetNone    AddressingTarget = "none"
	AddressingTargetUnclear AddressingTarget = "unclear"
)

type AddressingClassificationRequest struct {
	Prompt           string
	ConversationType string
	SenderName       string
	SenderHandle     string
	VisibleContext   VisibleContext
}

type AddressingDecision struct {
	Target      AddressingTarget
	ShouldReply bool
	Reason      string
}

type addressingClassificationDocument struct {
	Target      AddressingTarget `json:"target"`
	ShouldReply bool             `json:"shouldReply"`
	Reason      string           `json:"reason,omitempty"`
}

func (agentKernel *AgentKernel) ClassifyAddressing(ctx context.Context, request AddressingClassificationRequest) (AddressingDecision, error) {
	languageModel := agentKernel.addressingLanguageModel()
	if languageModel == nil {
		return AddressingDecision{}, errors.New("language model is not configured")
	}
	includeReason := agentKernel.intakeOptions.DebugAddressingReason
	structuredResponse, errorValue := languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages:               addressingClassificationMessages(request),
		StructuredOutputSchema: addressingClassificationSchema(includeReason),
	})
	if errorValue != nil {
		return AddressingDecision{}, errorValue
	}
	var document addressingClassificationDocument
	if errorValue := json.Unmarshal([]byte(structuredResponse.Content), &document); errorValue != nil {
		return AddressingDecision{}, errorValue
	}
	if !isValidAddressingTarget(document.Target) {
		return AddressingDecision{}, errors.New("invalid addressing target")
	}
	decision := AddressingDecision{
		Target:      document.Target,
		ShouldReply: document.ShouldReply,
	}
	if includeReason {
		decision.Reason = strings.TrimSpace(document.Reason)
	}
	return decision, nil
}

func (agentKernel *AgentKernel) addressingLanguageModel() llm.LanguageModelProvider {
	if agentKernel.intakeLanguageModel != nil {
		return agentKernel.intakeLanguageModel
	}
	return agentKernel.languageModel
}

func addressingClassificationMessages(request AddressingClassificationRequest) []llm.Message {
	return []llm.Message{
		{Role: "system", Content: "Classify the intended target of the latest message in a multi-person conversation. Return only the requested JSON. Do not answer the user."},
		{Role: "user", Content: addressingClassificationPrompt(request)},
	}
}

func addressingClassificationPrompt(request AddressingClassificationRequest) string {
	lines := []string{
		"Choose target:",
		"- bot: the latest message is intended for the assistant",
		"- human: the latest message is intended for another human, including short replies to a recent human-directed message",
		"- anyone: the latest message is addressed to the room or anyone present",
		"- none: the latest message has no response target",
		"- unclear: there is not enough evidence",
		"Set shouldReply=true only when the assistant should visibly handle the latest message with text, a tool action, a clarification, or an emoji reaction.",
		"Set shouldReply=false for messages intended for another human, ambient messages, or unclear target.",
		"If the latest message is a short acknowledgement such as \"네\", \"확인해볼게요\", \"좋아요\", or \"고마워\" and recent context shows it follows a human-directed message, choose target=human and shouldReply=false.",
		"Only choose target=bot when the assistant is explicitly addressed, the latest message answers the assistant's own question, or recent context clearly makes the assistant the intended responder.",
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

func addressingClassificationSchema(includeReason bool) llm.StructuredOutputSchema {
	document := `{"type":"object","properties":{"target":{"type":"string","enum":["bot","human","anyone","none","unclear"]},"shouldReply":{"type":"boolean"}},"required":["target","shouldReply"],"additionalProperties":false}`
	if includeReason {
		document = `{"type":"object","properties":{"target":{"type":"string","enum":["bot","human","anyone","none","unclear"]},"shouldReply":{"type":"boolean"},"reason":{"type":"string"}},"required":["target","shouldReply","reason"],"additionalProperties":false}`
	}
	return llm.StructuredOutputSchema{
		Name:               "blueclaw_addressing_classification",
		Document:           document,
		IsStrictlyEnforced: true,
	}
}

func isValidAddressingTarget(target AddressingTarget) bool {
	switch target {
	case AddressingTargetBot, AddressingTargetHuman, AddressingTargetAnyone, AddressingTargetNone, AddressingTargetUnclear:
		return true
	}
	return false
}
