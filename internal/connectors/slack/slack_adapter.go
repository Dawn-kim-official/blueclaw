package slack

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"blueclaw/internal/connectors"
	"blueclaw/internal/identity"
)

type BotIdentityClient interface {
	ResolveBotUserID() (string, error)
	ResolveUserIdentity(externalUserID string) (identity.PlatformAccountIdentity, error)
}

type ConversationClient interface {
	CreateMessage(conversationID string, parentID string, message string) (string, error)
}

type Adapter struct {
	EventParser        EventParser
	BotIdentityClient  BotIdentityClient
	ConversationClient ConversationClient
	SigningSecret      string
}

func NewAdapter(botIdentityClient BotIdentityClient, conversationClient ConversationClient, signingSecret string) Adapter {
	return Adapter{
		EventParser:        EventParser{},
		BotIdentityClient:  botIdentityClient,
		ConversationClient: conversationClient,
		SigningSecret:      strings.TrimSpace(signingSecret),
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
	if errorValue := adapter.verifySignature(request, payload); errorValue != nil {
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

func (adapter Adapter) ResolveBotUserID(context.Context) (string, error) {
	return adapter.BotIdentityClient.ResolveBotUserID()
}

func (adapter Adapter) ResolveConversationKind(_ context.Context, event connectors.PlatformInboundEvent) (connectors.ConversationKind, error) {
	channelType := strings.ToLower(strings.TrimSpace(event.ChannelType))
	return connectors.ConversationKind{
		IsDirect: channelType == "im" || strings.HasPrefix(strings.TrimSpace(event.ConversationID), "D"),
	}, nil
}

func (adapter Adapter) PublishTyping(context.Context, string, connectors.ReplyTarget) error {
	return nil
}

func (adapter Adapter) SendReply(_ context.Context, replyTarget connectors.ReplyTarget, message string) (string, error) {
	return adapter.ConversationClient.CreateMessage(replyTarget.ConversationID, replyTarget.ParentID, message)
}

func (adapter Adapter) NotInvitedReply() string {
	return NotInvitedReply
}

func (adapter Adapter) convertEvent(eventEnvelope EventEnvelope) connectors.PlatformInboundEvent {
	event := eventEnvelope.Event
	return connectors.PlatformInboundEvent{
		Platform:       adapter.Name(),
		Source:         "http",
		EventID:        eventEnvelope.EventID,
		ConversationID: event.ConversationID,
		MessageID:      event.StableMessageID(eventEnvelope.EventID),
		ReplyParentID:  event.Timestamp,
		RootMessageID:  event.RootMessageID(),
		SenderUserID:   event.UserID,
		ChannelType:    event.ChannelType,
		Text:           event.Text,
		RawReceivedAt:  time.Now(),
		IsBotMessage:   event.IsBotMessage(),
	}
}

func (adapter Adapter) verifySignature(request *http.Request, payload []byte) error {
	if strings.TrimSpace(adapter.SigningSecret) == "" {
		return nil
	}

	timestamp := request.Header.Get("X-Slack-Request-Timestamp")
	signature := request.Header.Get("X-Slack-Signature")
	if strings.TrimSpace(timestamp) == "" || strings.TrimSpace(signature) == "" {
		return errors.New("slack signature headers are required")
	}

	timestampSeconds, errorValue := strconv.ParseInt(timestamp, 10, 64)
	if errorValue != nil {
		return errors.New("slack signature timestamp is invalid")
	}
	if time.Since(time.Unix(timestampSeconds, 0)) > 5*time.Minute {
		return errors.New("slack signature timestamp is too old")
	}

	messageAuthenticationCode := hmac.New(sha256.New, []byte(adapter.SigningSecret))
	_, _ = messageAuthenticationCode.Write([]byte("v0:" + timestamp + ":"))
	_, _ = messageAuthenticationCode.Write(payload)
	expectedSignature := "v0=" + hex.EncodeToString(messageAuthenticationCode.Sum(nil))
	if !hmac.Equal([]byte(expectedSignature), []byte(signature)) {
		return errors.New("slack signature mismatch")
	}

	return nil
}

func (adapter Adapter) parser() EventParser {
	return adapter.EventParser
}
