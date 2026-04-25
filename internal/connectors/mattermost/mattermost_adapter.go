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
	ResolveBotUserID() (string, error)
	ResolveUserIdentity(externalUserID string) (identity.PlatformAccountIdentity, error)
}

type ConversationClient interface {
	CreatePost(conversationID string, rootID string, message string) (string, error)
	PublishTyping(userID string, conversationID string, parentID string) error
	ResolveChannelType(conversationID string) (string, error)
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

func (adapter Adapter) ResolveBotUserID(context.Context) (string, error) {
	return adapter.BotIdentityClient.ResolveBotUserID()
}

func (adapter Adapter) ResolveConversationKind(_ context.Context, event connectors.PlatformInboundEvent) (connectors.ConversationKind, error) {
	channelType := strings.TrimSpace(event.ChannelType)
	if channelType == "" {
		resolvedChannelType, errorValue := adapter.ConversationClient.ResolveChannelType(event.ConversationID)
		if errorValue != nil {
			return connectors.ConversationKind{}, errorValue
		}
		channelType = resolvedChannelType
	}

	return connectors.ConversationKind{
		IsDirect: strings.EqualFold(channelType, "D"),
	}, nil
}

func (adapter Adapter) PublishTyping(_ context.Context, botUserID string, replyTarget connectors.ReplyTarget) error {
	return adapter.ConversationClient.PublishTyping(botUserID, replyTarget.ConversationID, replyTarget.ParentID)
}

func (adapter Adapter) SendReply(_ context.Context, replyTarget connectors.ReplyTarget, message string) (string, error) {
	return adapter.ConversationClient.CreatePost(replyTarget.ConversationID, replyTarget.ParentID, message)
}

func (adapter Adapter) NotInvitedReply() string {
	return NotInvitedReply
}

func (adapter Adapter) convertEvent(event Event, source string) connectors.PlatformInboundEvent {
	return connectors.PlatformInboundEvent{
		Platform:       adapter.Name(),
		Source:         source,
		EventID:        event.EventName,
		ConversationID: event.ConversationID,
		MessageID:      event.PostID,
		ReplyParentID:  event.PostID,
		RootMessageID:  event.RootID,
		SenderUserID:   event.UserID,
		ChannelType:    event.ChannelType,
		Text:           event.Message,
		RawReceivedAt:  time.Now(),
		IsBotMessage:   strings.TrimSpace(event.Type) != "",
	}
}

func (adapter Adapter) parser() EventParser {
	if adapter.EventParser == (EventParser{}) {
		return EventParser{}
	}
	return adapter.EventParser
}
