package mattermost

import (
	"encoding/json"
	"strings"
)

type Event struct {
	EventName      string `json:"event"`
	UserID         string `json:"user_id"`
	ConversationID string `json:"channel_id"`
	ChannelType    string `json:"channel_type"`
	PostID         string `json:"post_id"`
	Message        string `json:"message"`
	RootID         string `json:"root_id"`
	Type           string `json:"type"`
}

type EventParser struct{}

type WebSocketMessage struct {
	Event string                 `json:"event"`
	Data  map[string]interface{} `json:"data"`
}

func (eventParser EventParser) ParseEvent(payload []byte) (Event, error) {
	var event Event
	errorValue := json.Unmarshal(payload, &event)
	if errorValue != nil {
		return Event{}, errorValue
	}

	return event, nil
}

func (event Event) IsDirectMessage() bool {
	return strings.EqualFold(strings.TrimSpace(event.ChannelType), "D")
}

func (eventParser EventParser) ParseWebSocketMessage(payload []byte) (Event, bool, error) {
	var message WebSocketMessage
	errorValue := json.Unmarshal(payload, &message)
	if errorValue != nil {
		return Event{}, false, errorValue
	}
	if message.Event != "posted" {
		return Event{}, false, nil
	}

	postDocument, isFound := message.Data["post"].(string)
	if !isFound || strings.TrimSpace(postDocument) == "" {
		return Event{}, false, nil
	}

	var post struct {
		ID        string `json:"id"`
		UserID    string `json:"user_id"`
		ChannelID string `json:"channel_id"`
		Message   string `json:"message"`
		RootID    string `json:"root_id"`
		Type      string `json:"type"`
	}
	errorValue = json.Unmarshal([]byte(postDocument), &post)
	if errorValue != nil {
		return Event{}, false, errorValue
	}

	channelType, _ := message.Data["channel_type"].(string)
	return Event{
		EventName:      message.Event,
		UserID:         post.UserID,
		ConversationID: post.ChannelID,
		ChannelType:    channelType,
		PostID:         post.ID,
		Message:        post.Message,
		RootID:         post.RootID,
		Type:           post.Type,
	}, true, nil
}
