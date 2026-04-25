package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"blueclaw/internal/connectors"
)

func TestAdapterRespondsToURLVerificationChallenge(t *testing.T) {
	adapter := NewAdapter(UserProfileClient{}, PostClient{}, "")
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

func TestAdapterNormalizesSlackEventCallback(t *testing.T) {
	adapter := NewAdapter(UserProfileClient{}, PostClient{}, "")
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
	if parseResult.Event.ReplyParentID != "111.222" {
		t.Fatalf("expected slack timestamp reply parent id, got %q", parseResult.Event.ReplyParentID)
	}
	if parseResult.Event.ChannelType != "im" {
		t.Fatalf("expected channel type, got %q", parseResult.Event.ChannelType)
	}
}

func TestPostClientSendsThreadReplyAndReportsSlackErrors(t *testing.T) {
	var requestDocument map[string]string
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/chat.postMessage" {
			return testHTTPResponse(http.StatusNotFound, `not found`), nil
		}
		_ = json.NewDecoder(request.Body).Decode(&requestDocument)
		return testHTTPResponse(http.StatusOK, `{"ok":true,"ts":"reply-ts"}`), nil
	})}

	postClient := PostClient{BaseURL: "http://slack.test", BotToken: "token", HTTPClient: httpClient}
	dispatchID, errorValue := postClient.CreateMessage("channel-1", "root-ts", "hello")
	if errorValue != nil {
		t.Fatalf("expected slack post: %v", errorValue)
	}

	if dispatchID != "reply-ts" {
		t.Fatalf("expected slack timestamp dispatch id, got %q", dispatchID)
	}
	if requestDocument["thread_ts"] != "root-ts" {
		t.Fatalf("expected thread timestamp, got %q", requestDocument["thread_ts"])
	}

	errorHTTPClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return testHTTPResponse(http.StatusOK, `{"ok":false,"error":"channel_not_found"}`), nil
	})}
	errorPostClient := PostClient{BaseURL: "http://slack.test", BotToken: "token", HTTPClient: errorHTTPClient}
	_, errorValue = errorPostClient.CreateMessage("missing", "", "hello")
	if errorValue == nil {
		t.Fatal("expected slack error")
	}
}

func TestAdapterResolvesSlackConversationKind(t *testing.T) {
	adapter := NewAdapter(UserProfileClient{}, PostClient{}, "")

	conversationKind, errorValue := adapter.ResolveConversationKind(context.Background(), connectors.PlatformInboundEvent{
		ConversationID: "D123",
		ChannelType:    "im",
	})
	if errorValue != nil {
		t.Fatalf("expected conversation kind: %v", errorValue)
	}
	if !conversationKind.IsDirect {
		t.Fatal("expected direct conversation")
	}
}

func newSlackRequest(payload []byte) *http.Request {
	request, _ := http.NewRequest(http.MethodPost, "/connectors/slack/events", bytes.NewReader(payload))
	return request
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func testHTTPResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
		Header:     make(http.Header),
	}
}
