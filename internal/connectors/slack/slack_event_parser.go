package slack

import "encoding/json"

type Event struct {
	Type            string `json:"type"`
	UserID          string `json:"user"`
	ConversationID  string `json:"channel"`
	Text            string `json:"text"`
	ThreadTimestamp string `json:"thread_ts"`
	ParentUserID    string `json:"parent_user_id"`
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
