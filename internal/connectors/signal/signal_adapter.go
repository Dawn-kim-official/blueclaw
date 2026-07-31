package signal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/connectors"
	"github.com/Dawn-kim-official/blueclaw/internal/identity"
)

type Event struct {
	EventID        string `json:"event_id"`
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
	SenderUserID   string `json:"sender_user_id"`
	Text           string `json:"text"`
	IsGroup        bool   `json:"is_group"`
}

type Adapter struct{}

func (adapter Adapter) Name() string {
	return "signal"
}

func (adapter Adapter) ParseHTTPEvent(_ context.Context, request *http.Request) (connectors.HTTPParseResult, error) {
	payload, errorValue := io.ReadAll(request.Body)
	if errorValue != nil {
		return connectors.HTTPParseResult{}, errorValue
	}

	var event Event
	errorValue = json.Unmarshal(payload, &event)
	if errorValue != nil {
		return connectors.HTTPParseResult{}, errorValue
	}

	return connectors.HTTPParseResult{
		Event:    adapter.convertEvent(event, "http"),
		HasEvent: strings.TrimSpace(event.MessageID) != "",
	}, nil
}

func (adapter Adapter) ParseRealtimeEvent(_ context.Context, payload []byte, source string) (connectors.PlatformInboundEvent, bool, error) {
	var event Event
	errorValue := json.Unmarshal(payload, &event)
	if errorValue != nil {
		return connectors.PlatformInboundEvent{}, false, errorValue
	}
	return adapter.convertEvent(event, source), strings.TrimSpace(event.MessageID) != "", nil
}

func (adapter Adapter) ResolveIdentity(context.Context, string) (identity.PlatformAccountIdentity, error) {
	return identity.PlatformAccountIdentity{}, errors.New("signal connector is experimental-disabled in v1")
}

func (adapter Adapter) StartProgress(context.Context, connectors.ReplyTarget) error {
	return nil
}

func (adapter Adapter) StopProgress(context.Context, connectors.ReplyTarget) error {
	return nil
}

func (adapter Adapter) SendReply(context.Context, connectors.ReplyTarget, connectors.OutboundReply) (string, error) {
	return "", errors.New("signal connector is experimental-disabled in v1")
}

func (adapter Adapter) FetchHistory(context.Context, string, int) (connectors.VisibleContext, error) {
	return connectors.VisibleContext{}, errors.New("signal connector is experimental-disabled in v1")
}

func (adapter Adapter) convertEvent(event Event, source string) connectors.PlatformInboundEvent {
	return connectors.PlatformInboundEvent{
		Platform:       adapter.Name(),
		Source:         source,
		ConversationID: event.ConversationID,
		MessageID:      event.MessageID,
		SenderID:       event.SenderUserID,
		ReplyTargetID:  event.MessageID,
		Prompt:         event.Text,
		RawReceivedAt:  time.Now(),
	}
}
