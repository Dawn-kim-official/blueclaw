package slack

import (
	"encoding/json"
	"strings"
)

type Event struct {
	Type            string `json:"type"`
	UserID          string `json:"user"`
	ConversationID  string `json:"channel"`
	Text            string `json:"text"`
	ThreadTimestamp string `json:"thread_ts"`
	Timestamp       string `json:"ts"`
	EventTimestamp  string `json:"event_ts"`
	ClientMessageID string `json:"client_msg_id"`
	ChannelType     string `json:"channel_type"`
	ParentUserID    string `json:"parent_user_id"`
	BotID           string `json:"bot_id"`
	Subtype         string `json:"subtype"`
}

type EventEnvelope struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
	EventID   string `json:"event_id"`
	TeamID    string `json:"team_id"`
	Event     Event  `json:"event"`
}

type EventParser struct{}

func (eventParser EventParser) ParseEvent(payload []byte) (Event, error) {
	var event Event
	errorValue := json.Unmarshal(payload, &event)
	if errorValue != nil {
		return Event{}, errorValue
	}

	return event, nil
}

func (eventParser EventParser) ParseEnvelope(payload []byte) (EventEnvelope, error) {
	var eventEnvelope EventEnvelope
	errorValue := json.Unmarshal(payload, &eventEnvelope)
	if errorValue != nil {
		return EventEnvelope{}, errorValue
	}

	return eventEnvelope, nil
}

func (event Event) StableMessageID(fallbackEventID string) string {
	for _, value := range []string{event.ClientMessageID, event.EventTimestamp, event.Timestamp, fallbackEventID} {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}

func (event Event) RootMessageID() string {
	threadTimestamp := strings.TrimSpace(event.ThreadTimestamp)
	if threadTimestamp == "" || threadTimestamp == strings.TrimSpace(event.Timestamp) {
		return ""
	}
	return threadTimestamp
}

func (event Event) IsBotMessage() bool {
	return strings.TrimSpace(event.BotID) != "" || strings.TrimSpace(event.Subtype) == "bot_message"
}
