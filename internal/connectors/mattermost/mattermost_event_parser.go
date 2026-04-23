package mattermost

import "encoding/json"

type Event struct {
	EventName      string `json:"event"`
	UserID         string `json:"user_id"`
	ConversationID string `json:"channel_id"`
	PostID         string `json:"post_id"`
	Message        string `json:"message"`
	RootID         string `json:"root_id"`
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
