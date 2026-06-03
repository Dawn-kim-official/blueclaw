package mattermost

import "testing"

func TestEventParserParsesWebSocketPostedEvent(t *testing.T) {
	payload := []byte(`{
		"event":"posted",
		"data":{
			"channel_type":"D",
			"post":"{\"id\":\"post-1\",\"user_id\":\"user-1\",\"channel_id\":\"direct-1\",\"message\":\"hello\",\"root_id\":\"\",\"file_ids\":[\"file-1\"]}"
		}
	}`)

	event, isPostedEvent, errorValue := EventParser{}.ParseWebSocketMessage(payload)
	if errorValue != nil {
		t.Fatalf("expected websocket event parse: %v", errorValue)
	}
	if !isPostedEvent {
		t.Fatal("expected posted event")
	}
	if event.PostID != "post-1" {
		t.Fatalf("expected post id, got %q", event.PostID)
	}
	if len(event.FileIDs) != 1 || event.FileIDs[0] != "file-1" {
		t.Fatalf("expected file ids, got %+v", event.FileIDs)
	}
	if !event.IsDirectMessage() {
		t.Fatal("expected direct message channel type")
	}
}

func TestEventParserIgnoresNonPostedWebSocketEvent(t *testing.T) {
	_, isPostedEvent, errorValue := EventParser{}.ParseWebSocketMessage([]byte(`{"event":"hello","data":{}}`))
	if errorValue != nil {
		t.Fatalf("expected websocket event parse: %v", errorValue)
	}
	if isPostedEvent {
		t.Fatal("expected non-posted event to be ignored")
	}
}
