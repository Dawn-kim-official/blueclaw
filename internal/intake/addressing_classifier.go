package intake

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/model"
)

type addressingClassificationDocument struct {
	Target         agentcontract.AddressingTarget `json:"target"`
	ShouldRespond  bool                           `json:"shouldRespond"`
	ReactionEmoji  string                         `json:"reactionEmoji"`
	DutyMatch      bool                           `json:"dutyMatch"`
	DutyName       string                         `json:"dutyName"`
	DutyConfidence float64                        `json:"dutyConfidence"`
}

var addressingReactionEmojiOptions = append([]string{""}, agentcontract.ReactionEmojiNames...)

func addressingReactionEmojiEnumJSON() string {
	quoted := make([]string, 0, len(addressingReactionEmojiOptions))
	for _, name := range addressingReactionEmojiOptions {
		quoted = append(quoted, strconv.Quote(name))
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

func normalizeAddressingReactionEmoji(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, option := range addressingReactionEmojiOptions {
		if option != "" && option == name {
			return option
		}
	}
	return ""
}

type StandingDutySeed struct {
	Name        string
	Description string
}

var AddressingStandingDutySeeds = []StandingDutySeed{
	{Name: "calendar_upkeep", Description: "a specific meeting, deadline, or scheduled event that should be created or updated as a calendar event right now"},
	{Name: "team_flow_update", Description: "a specific work task assigned to a person that should be added, or whose status or details should be updated or completed right now"},
}

func (classifier *Classifier) ClassifyAddressing(ctx context.Context, request agentcontract.AddressingClassificationRequest) (agentcontract.AddressingDecision, error) {
	languageModel := classifier.languageModel
	if languageModel == nil {
		return agentcontract.AddressingDecision{}, errors.New("language model is not configured")
	}
	structuredResponse, errorValue := languageModel.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		Messages:               addressingClassificationMessages(request),
		StructuredOutputSchema: addressingClassificationSchema(),
	})
	if errorValue != nil {
		return agentcontract.AddressingDecision{}, errorValue
	}
	var document addressingClassificationDocument
	if errorValue := json.Unmarshal([]byte(structuredResponse.Content), &document); errorValue != nil {
		return agentcontract.AddressingDecision{}, errorValue
	}
	if !isValidAddressingTarget(document.Target) {
		return agentcontract.AddressingDecision{}, errors.New("invalid addressing target")
	}
	decision := agentcontract.AddressingDecision{
		Target:         document.Target,
		ShouldRespond:  document.ShouldRespond,
		ReactionEmoji:  normalizeAddressingReactionEmoji(document.ReactionEmoji),
		DutyMatch:      document.DutyMatch,
		DutyName:       strings.TrimSpace(document.DutyName),
		DutyConfidence: normalizedDutyConfidence(document.DutyConfidence),
	}
	if !decision.DutyMatch {
		decision.DutyName = ""
		decision.DutyConfidence = 0
	}
	if decision.Target == agentcontract.AddressingTargetHuman {
		decision.ShouldRespond = false
	}
	return decision, nil
}

func addressingClassificationMessages(request agentcontract.AddressingClassificationRequest) []model.Message {
	return []model.Message{
		{Role: "system", Content: "Classify the intended target of the latest message in a multi-person conversation. Return only the requested JSON. Do not answer the user."},
		{Role: "user", Content: addressingClassificationPrompt(request)},
	}
}

func addressingClassificationPrompt(request agentcontract.AddressingClassificationRequest) string {
	lines := []string{
		"Decide how the assistant (InternKim, Korean name 김인턴) should handle the latest message in a group conversation. Return only the requested JSON; do not answer the user.",
		"Make two independent decisions:",
		"- shouldRespond: true when the assistant should write a reply. That includes a direct request, question, or instruction to the assistant; an answer to the assistant's own question; a message that makes the assistant the intended responder; AND social or playful messages directed at the assistant where a short in-kind reply keeps the conversation going (a joke, teasing, or a compliment aimed at the assistant — reply briefly and warmly). false otherwise.",
		"- reactionEmoji: default to \"\". Choose an emoji from the allowed set only when a courteous coworker would naturally leave a reaction even though no reply is needed: a share or FYI addressed to the whole team (a link or file posted for everyone, or an explicit heads-up), news worth celebrating, or a joke posted for the room. Do not react to routine work chatter between other people, status exchanges between colleagues, personal thanks between two people, or any message that neither addresses nor includes the assistant; topic or wording alone is never a reason to react. When you do react, pick the emoji that fits: white_check_mark to acknowledge, +1 or ok_hand to approve, pray or heart for thanks aimed at the assistant, tada or clap or raised_hands or sparkles to celebrate, rocket for a launch or shipped work, fire or 100 for impressive results, muscle to cheer effort on, wave for greetings.",
		"Four outcomes: ignore (shouldRespond=false, reactionEmoji=\"\"); react only (false + emoji); respond (true + \"\"); react and respond (true + emoji). Ignore is the normal outcome for most channel traffic.",
		"Guidance:",
		"- Default to ignore for messages between other people, including their status updates, coordination, and thanks to each other.",
		"- A share or FYI posted for the whole team or for the assistant → react only.",
		"- Playful or social messages directed at the assistant (praise, teasing, or jokes aimed at the assistant) → respond briefly and in kind; this is welcome, not noise.",
		"- A closing thanks or acknowledgement to the assistant (a brief thanks, an acknowledgement, or a simple agreement) → react only or ignore, not a written reply.",
		"- When botMentioned=true with a request, question, or invitation to talk → respond. When the mention only shares, FYIs, or thanks → react only. The runtime already adds a seen-marker reaction for mentions, so add an emoji only when a specifically fitting one applies.",
		"Set dutyMatch=true only when the latest message specifies a concrete task or calendar item that should be added, updated, or completed right now, with enough detail to act such as a named assignee, a clear deliverable, or a date, even when it is not addressed to the assistant. Do not set dutyMatch for vague mentions, opinions, questions, hypotheticals, or chit-chat. Set dutyName to the exact standing duty name or empty string, and dutyConfidence 0 to 1.",
		"Standing duties:",
	}
	for _, duty := range AddressingStandingDutySeeds {
		lines = append(lines, "- "+duty.Name+": "+duty.Description)
	}
	lines = append(lines,
		"botMentioned: "+strconv.FormatBool(request.BotMentioned),
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

func recentVisibleMessages(messages []agentcontract.VisibleContextMessage, limit int) []agentcontract.VisibleContextMessage {
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

func addressingClassificationSchema() model.StructuredOutputSchema {
	reactionEmojiProperty := `"reactionEmoji":{"type":"string","enum":` + addressingReactionEmojiEnumJSON() + `}`
	document := `{"type":"object","properties":{"target":{"type":"string","enum":["bot","human","anyone","none","unclear"]},"shouldRespond":{"type":"boolean"},` + reactionEmojiProperty + `,"dutyMatch":{"type":"boolean"},"dutyName":{"type":"string"},"dutyConfidence":{"type":"number"}},"required":["target","shouldRespond","reactionEmoji","dutyMatch","dutyName","dutyConfidence"],"additionalProperties":false}`
	return model.StructuredOutputSchema{
		Name:               "blueclaw_addressing_classification",
		Document:           document,
		IsStrictlyEnforced: true,
	}
}

func isValidAddressingTarget(target agentcontract.AddressingTarget) bool {
	switch target {
	case agentcontract.AddressingTargetBot, agentcontract.AddressingTargetHuman, agentcontract.AddressingTargetAnyone, agentcontract.AddressingTargetNone, agentcontract.AddressingTargetUnclear:
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
