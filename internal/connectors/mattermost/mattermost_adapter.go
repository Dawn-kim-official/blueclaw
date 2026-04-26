package mattermost

import (
	"context"
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
	CreatePost(conversationID string, rootID string, message string) (string, error)
	PublishTyping(userID string, conversationID string, parentID string) error
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
	return "mattermost"
}

func (adapter Adapter) ParseHTTPEvent(_ context.Context, request *http.Request) (connectors.HTTPParseResult, error) {
	payload, errorValue := io.ReadAll(request.Body)
	if errorValue != nil {
		return connectors.HTTPParseResult{}, errorValue
	}

	event, errorValue := adapter.parser().ParseEvent(payload)
	if errorValue == nil {
		return connectors.HTTPParseResult{
			Event:    adapter.convertEvent(event, "http"),
			HasEvent: strings.TrimSpace(event.PostID) != "",
		}, nil
	}

	event, hasEvent, realtimeError := adapter.parser().ParseWebSocketMessage(payload)
	if realtimeError != nil || !hasEvent {
		return connectors.HTTPParseResult{}, errorValue
	}
	return connectors.HTTPParseResult{
		Event:    adapter.convertEvent(event, "http"),
		HasEvent: strings.TrimSpace(event.PostID) != "",
	}, nil
}

func (adapter Adapter) ParseRealtimeEvent(_ context.Context, payload []byte, source string) (connectors.PlatformInboundEvent, bool, error) {
	event, hasEvent, errorValue := adapter.parser().ParseWebSocketMessage(payload)
	if errorValue != nil || !hasEvent {
		return connectors.PlatformInboundEvent{}, hasEvent, errorValue
	}

	return adapter.convertEvent(event, source), true, nil
}

func (adapter Adapter) ResolveIdentity(_ context.Context, senderUserID string) (identity.PlatformAccountIdentity, error) {
	return adapter.BotIdentityClient.ResolveUserIdentity(senderUserID)
}

func (adapter Adapter) StartProgress(_ context.Context, replyTarget connectors.ReplyTarget) error {
	return adapter.ConversationClient.PublishTyping("", replyTarget.ConversationID, replyTarget.ReplyTargetID)
}

func (adapter Adapter) StopProgress(context.Context, connectors.ReplyTarget) error {
	return nil
}

func (adapter Adapter) SendReply(_ context.Context, replyTarget connectors.ReplyTarget, message string) (string, error) {
	return adapter.ConversationClient.CreatePost(replyTarget.ConversationID, replyTarget.ReplyTargetID, message)
}

func (adapter Adapter) FetchHistory(context.Context, string, int) (connectors.VisibleContext, error) {
	return connectors.VisibleContext{}, nil
}

func (adapter Adapter) NotInvitedReply() string {
	return NotInvitedReply
}

func (adapter Adapter) convertEvent(event Event, source string) connectors.PlatformInboundEvent {
	return connectors.PlatformInboundEvent{
		Platform:       adapter.Name(),
		Source:         source,
		ConversationID: event.ConversationID,
		MessageID:      event.PostID,
		SenderID:       event.UserID,
		ReplyTargetID:  firstNonEmpty(event.RootID, event.PostID),
		Prompt:         event.Message,
		RawReceivedAt:  time.Now(),
	}
}

func (adapter Adapter) parser() EventParser {
	if adapter.EventParser == (EventParser{}) {
		return EventParser{}
	}
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
