package slack

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"blueclaw/internal/identity"
)

func TestAdapterRespondsToURLVerificationChallenge(t *testing.T) {
	adapter := NewAdapter(fakeSlackIdentityClient{}, fakeSlackConversationClient{})
	request := newSlackRequest([]byte(`{"type":"url_verification","challenge":"challenge-value"}`))

	parseResult, errorValue := adapter.ParseHTTPEvent(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected challenge parse: %v", errorValue)
	}

	if parseResult.ImmediateResponse == nil {
		t.Fatal("expected immediate challenge response")
	}
	if string(parseResult.ImmediateResponse.Body) != "challenge-value" {
		t.Fatalf("expected challenge body, got %q", string(parseResult.ImmediateResponse.Body))
	}
}

func TestAdapterIgnoresSlackBotMessages(t *testing.T) {
	for _, payload := range []string{
		`{"type":"event_callback","event":{"type":"message","subtype":"bot_message","user":"bot-1","channel":"D123","ts":"111.222","text":"hello"}}`,
		`{"type":"event_callback","event":{"type":"message","bot_id":"bot-1","channel":"D123","ts":"111.222","text":"hello"}}`,
	} {
		adapter := NewAdapter(fakeSlackIdentityClient{}, fakeSlackConversationClient{})
		request := newSlackRequest([]byte(payload))

		parseResult, errorValue := adapter.ParseHTTPEvent(context.Background(), request)
		if errorValue != nil {
			t.Fatalf("expected bot event parse: %v", errorValue)
		}
		if parseResult.HasEvent {
			t.Fatalf("expected bot event to be ignored, got %+v", parseResult.Event)
		}
	}
}

func TestAdapterNormalizesSlackEventCallback(t *testing.T) {
	adapter := NewAdapter(fakeSlackIdentityClient{}, fakeSlackConversationClient{})
	request := newSlackRequest([]byte(`{
		"type":"event_callback",
		"event_id":"event-1",
		"team_id":"team-1",
		"event":{
			"type":"message",
			"user":"user-1",
			"channel":"D123",
			"channel_type":"im",
			"client_msg_id":"client-message-1",
			"event_ts":"111.222",
			"ts":"111.222",
			"text":"hello"
		}
	}`))

	parseResult, errorValue := adapter.ParseHTTPEvent(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected event parse: %v", errorValue)
	}

	if !parseResult.HasEvent {
		t.Fatal("expected event")
	}
	if parseResult.Event.MessageID != "client-message-1" {
		t.Fatalf("expected client message id, got %q", parseResult.Event.MessageID)
	}
	if parseResult.Event.ReplyTargetID != "111.222" {
		t.Fatalf("expected slack timestamp reply target id, got %q", parseResult.Event.ReplyTargetID)
	}
	if parseResult.Event.Prompt != "hello" {
		t.Fatalf("expected prompt, got %q", parseResult.Event.Prompt)
	}
}

func newSlackRequest(payload []byte) *http.Request {
	request, _ := http.NewRequest(http.MethodPost, "/connectors/slack/events", bytes.NewReader(payload))
	return request
}

type fakeSlackIdentityClient struct{}

func (client fakeSlackIdentityClient) ResolveUserIdentity(externalUserID string) (identity.PlatformAccountIdentity, error) {
	return identity.PlatformAccountIdentity{
		Platform:       "slack",
		ExternalUserID: externalUserID,
		Email:          "user@example.com",
		DisplayName:    "User",
	}, nil
}

type fakeSlackConversationClient struct{}

func (client fakeSlackConversationClient) CreateMessage(conversationID string, parentID string, message string) (string, error) {
	return "reply-ts", nil
}
