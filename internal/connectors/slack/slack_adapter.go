package slack

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"blueclaw/internal/connectors"
	"blueclaw/internal/identity"
)

const NotInvitedReply = "This Intern Kim has not invited your account yet. Ask the administrator for access."

type BotIdentityClient interface {
	ResolveUserIdentity(externalUserID string) (identity.PlatformAccountIdentity, error)
}

type ConversationClient interface {
	CreateMessage(conversationID string, parentID string, message string) (string, error)
}

type Adapter struct {
	EventParser        EventParser
	BotIdentityClient  BotIdentityClient
	ConversationClient ConversationClient
}

func NewAdapter(botIdentityClient BotIdentityClient, conversationClient ConversationClient) Adapter {
	return Adapter{
		EventParser:        EventParser{},
		BotIdentityClient:  botIdentityClient,
		ConversationClient: conversationClient,
	}
}

func (adapter Adapter) Name() string {
	return "slack"
}

func (adapter Adapter) ParseHTTPEvent(_ context.Context, request *http.Request) (connectors.HTTPParseResult, error) {
	payload, errorValue := io.ReadAll(request.Body)
	if errorValue != nil {
		return connectors.HTTPParseResult{}, errorValue
	}

	eventEnvelope, errorValue := adapter.parser().ParseEnvelope(payload)
	if errorValue != nil {
		return connectors.HTTPParseResult{}, errorValue
	}
	if eventEnvelope.Type == "url_verification" {
		return connectors.HTTPParseResult{
			ImmediateResponse: &connectors.HTTPResponse{
				StatusCode:  http.StatusOK,
				ContentType: "text/plain; charset=utf-8",
				Body:        []byte(eventEnvelope.Challenge),
			},
		}, nil
	}
	if eventEnvelope.Type != "event_callback" {
		return connectors.HTTPParseResult{}, nil
	}
	if eventEnvelope.Event.Type != "message" && eventEnvelope.Event.Type != "app_mention" {
		return connectors.HTTPParseResult{}, nil
	}

	return connectors.HTTPParseResult{
		Event:    adapter.convertEvent(eventEnvelope),
		HasEvent: true,
	}, nil
}

func (adapter Adapter) ParseRealtimeEvent(context.Context, []byte, string) (connectors.PlatformInboundEvent, bool, error) {
	return connectors.PlatformInboundEvent{}, false, errors.New("slack realtime transport is not enabled in v1")
}

func (adapter Adapter) ResolveIdentity(_ context.Context, senderUserID string) (identity.PlatformAccountIdentity, error) {
	return adapter.BotIdentityClient.ResolveUserIdentity(senderUserID)
}

func (adapter Adapter) StartProgress(context.Context, connectors.ReplyTarget) error {
	return nil
}

func (adapter Adapter) StopProgress(context.Context, connectors.ReplyTarget) error {
	return nil
}

func (adapter Adapter) SendReply(_ context.Context, replyTarget connectors.ReplyTarget, reply connectors.OutboundReply) (string, error) {
	return adapter.ConversationClient.CreateMessage(replyTarget.ConversationID, replyTarget.ReplyTargetID, reply.Message)
}

func (adapter Adapter) FetchHistory(context.Context, string, int) (connectors.VisibleContext, error) {
	return connectors.VisibleContext{}, nil
}

func (adapter Adapter) NotInvitedReply() string {
	return NotInvitedReply
}

func (adapter Adapter) convertEvent(eventEnvelope EventEnvelope) connectors.PlatformInboundEvent {
	event := eventEnvelope.Event
	return connectors.PlatformInboundEvent{
		Platform:       adapter.Name(),
		Source:         "http",
		ConversationID: event.ConversationID,
		MessageID:      event.StableMessageID(eventEnvelope.EventID),
		SenderID:       event.UserID,
		ReplyTargetID:  firstNonEmpty(event.RootMessageID(), event.Timestamp),
		Prompt:         event.Text,
		RawReceivedAt:  time.Now(),
	}
}

func (adapter Adapter) parser() EventParser {
	return adapter.EventParser
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}
