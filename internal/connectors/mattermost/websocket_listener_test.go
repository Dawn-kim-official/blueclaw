package mattermost

import (
	"bufio"
	"bytes"
	"testing"
)

func TestWebSocketTextFrameRoundTrip(t *testing.T) {
	transport := &bytes.Buffer{}
	errorValue := writeWebSocketTextFrame(transport, []byte(`{"event":"posted"}`))
	if errorValue != nil {
		t.Fatalf("expected frame write: %v", errorValue)
	}

	payload, errorValue := readWebSocketFrame(transport, bufio.NewReader(transport))
	if errorValue != nil {
		t.Fatalf("expected frame read: %v", errorValue)
	}
	if string(payload) != `{"event":"posted"}` {
		t.Fatalf("expected payload to round trip, got %q", string(payload))
	}
}

func TestDeriveWebSocketURL(t *testing.T) {
	webSocketURL := DeriveWebSocketURL("https://mattermost.example")
	if webSocketURL != "wss://mattermost.example/api/v4/websocket" {
		t.Fatalf("expected websocket url, got %q", webSocketURL)
	}
}
