package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

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
	MessageSentAt    time.Time
	ConversationType string
	SenderName       string
	SenderHandle     string
	VisibleContext   VisibleContext
}

type AddressingDecision struct {
	Target         AddressingTarget
	ShouldReply    bool
	Reason         string
	DutyMatch      bool
	DutyName       string
	DutyConfidence float64
}

type addressingClassificationDocument struct {
	Target         AddressingTarget `json:"target"`
	ShouldReply    bool             `json:"shouldReply"`
	DutyMatch      bool             `json:"dutyMatch"`
	DutyName       string           `json:"dutyName"`
	DutyConfidence float64          `json:"dutyConfidence"`
	Reason         string           `json:"reason,omitempty"`
}

type StandingDutySeed struct {
	Name        string
	Description string
}

var AddressingStandingDutySeeds = []StandingDutySeed{
	{Name: "calendar_upkeep", Description: "a specific meeting, deadline, or scheduled event that should be created or updated as a calendar event right now"},
	{Name: "team_flow_update", Description: "a specific work task assigned to a person that should be added, or whose status or details should be updated or completed right now"},
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
		Target:         document.Target,
		ShouldReply:    document.ShouldReply,
		DutyMatch:      document.DutyMatch,
		DutyName:       strings.TrimSpace(document.DutyName),
		DutyConfidence: normalizedDutyConfidence(document.DutyConfidence),
	}
	if !decision.DutyMatch {
		decision.DutyName = ""
		decision.DutyConfidence = 0
	}
	if decision.Target == AddressingTargetHuman {
		decision.ShouldReply = false
	}
	if includeReason {
		decision.Reason = strings.TrimSpace(document.Reason)
	}
	return decision, nil
}

func (agentKernel *AgentKernel) addressingLanguageModel() llm.LanguageModelProvider {
	return agentKernel.classificationLanguageModel()
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
		"Set shouldReply=false for messages intended for another human, non-duty ambient messages, or unclear target.",
		"Standing duties:",
	}
	for _, duty := range AddressingStandingDutySeeds {
		lines = append(lines, "- "+duty.Name+": "+duty.Description)
	}
	lines = append(lines,
		"Set dutyMatch=true only when the latest message specifies a concrete task or calendar item that should be added, updated, or completed right now, with enough detail to act such as a named assignee, a clear deliverable, or a date, even when it is not addressed to the assistant.",
		"Do not set dutyMatch for vague mentions, opinions, questions, hypotheticals, or chit-chat that does not call for adding or changing a task or schedule.",
		"Set dutyName to the exact standing duty name, or empty string when there is no match.",
		"Set dutyConfidence from 0 to 1 using the evidence in the latest message and context.",
		"If the latest message is a short acknowledgement such as \"네\", \"확인해볼게요\", \"좋아요\", or \"고마워\" and recent context shows it follows a human-directed message, choose target=human and shouldReply=false.",
		"If the latest message only mentions the assistant, such as @김인턴 or the bot handle, choose target=bot and shouldReply=true when recent context gives a topic to answer or continue.",
		"Do not treat jokes, playful remarks, or casual addressed banter as no-response noise; choose target=bot or target=anyone with shouldReply=true when the assistant is addressed or clearly invited to join.",
		"Only choose target=bot when the assistant is explicitly addressed, the latest message answers the assistant's own question, or recent context clearly makes the assistant the intended responder.",
		"conversationType: "+strings.TrimSpace(request.ConversationType),
		"senderName: "+strings.TrimSpace(request.SenderName),
		"senderHandle: "+strings.TrimSpace(request.SenderHandle),
		"message: "+strings.TrimSpace(request.Prompt),
	)
	if stamp := formatContextTimestamp(request.MessageSentAt); stamp != "" {
		lines = append(lines, "messageTime: "+stamp+" (context timestamps below share this clock; consecutive messages seconds apart from the same sender are usually one split thought)")
	}
	for _, message := range recentVisibleMessages(request.VisibleContext.Messages, 6) {
		speaker := firstNonEmptyAddressingText(message.SpeakerCallingName, message.Speaker, message.SpeakerHandle, "unknown")
		prefix := "context: "
		if stamp := formatContextTimestamp(message.SentAt); stamp != "" {
			prefix = "context: [" + stamp + "] "
		}
		lines = append(lines, prefix+speaker+": "+strings.TrimSpace(message.Text))
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
	document := `{"type":"object","properties":{"target":{"type":"string","enum":["bot","human","anyone","none","unclear"]},"shouldReply":{"type":"boolean"},"dutyMatch":{"type":"boolean"},"dutyName":{"type":"string"},"dutyConfidence":{"type":"number"}},"required":["target","shouldReply","dutyMatch","dutyName","dutyConfidence"],"additionalProperties":false}`
	if includeReason {
		document = `{"type":"object","properties":{"target":{"type":"string","enum":["bot","human","anyone","none","unclear"]},"shouldReply":{"type":"boolean"},"dutyMatch":{"type":"boolean"},"dutyName":{"type":"string"},"dutyConfidence":{"type":"number"},"reason":{"type":"string"}},"required":["target","shouldReply","dutyMatch","dutyName","dutyConfidence","reason"],"additionalProperties":false}`
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

func normalizedDutyConfidence(confidence float64) float64 {
	if confidence < 0 {
		return 0
	}
	if confidence > 1 {
		return 1
	}
	return confidence
}
