package mattermost

import (
	"context"
	"io"
	"net/http"
	"sort"
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
	FetchPosts(conversationID string, beforePostID string, limit int) ([]ConversationPost, error)
	AddReaction(postID string, emojiName string) error
}

type ConversationPost struct {
	ID       string
	UserID   string
	Message  string
	FileIDs  []string
	CreateAt int64
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

func (adapter Adapter) SendReply(_ context.Context, replyTarget connectors.ReplyTarget, reply connectors.OutboundReply) (string, error) {
	return adapter.ConversationClient.CreatePost(replyTarget.ConversationID, replyTarget.ReplyTargetID, reply.Message)
}

func (adapter Adapter) AddReaction(_ context.Context, target connectors.ReactionTarget) error {
	return adapter.ConversationClient.AddReaction(target.MessageID, target.EmojiName)
}

func (adapter Adapter) FetchHistory(_ context.Context, historyCursor string, limit int) (connectors.VisibleContext, error) {
	conversationID, beforePostID := parseHistoryCursor(historyCursor)
	if conversationID == "" {
		return connectors.VisibleContext{}, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	posts, errorValue := adapter.ConversationClient.FetchPosts(conversationID, beforePostID, limit)
	if errorValue != nil {
		return connectors.VisibleContext{}, errorValue
	}
	sort.SliceStable(posts, func(leftIndex int, rightIndex int) bool {
		return posts[leftIndex].CreateAt < posts[rightIndex].CreateAt
	})
	messages := make([]connectors.VisibleContextMessage, 0, len(posts))
	for _, post := range posts {
		message := strings.TrimSpace(post.Message)
		inputAttachments := mattermostInputAttachments(post.ID, post.FileIDs)
		if strings.TrimSpace(post.ID) == beforePostID || (message == "" && len(inputAttachments) == 0) {
			continue
		}
		messages = append(messages, connectors.VisibleContextMessage{
			Speaker:          firstNonEmpty(strings.TrimSpace(post.UserID), "mattermost"),
			SpeakerHandle:    strings.TrimSpace(post.UserID),
			Text:             message,
			InputAttachments: inputAttachments,
		})
	}
	return connectors.VisibleContext{
		Messages:      messages,
		HasMoreBefore: len(posts) >= limit,
		HistoryCursor: conversationID,
	}, nil
}

func (adapter Adapter) NotInvitedReply() string {
	return NotInvitedReply
}

func (adapter Adapter) convertEvent(event Event, source string) connectors.PlatformInboundEvent {
	inputAttachments := mattermostInputAttachments(event.PostID, event.FileIDs)
	return connectors.PlatformInboundEvent{
		Platform:       adapter.Name(),
		Source:         source,
		ConversationID: event.ConversationID,
		MessageID:      event.PostID,
		SenderID:       event.UserID,
		ReplyTargetID:  firstNonEmpty(event.RootID, event.PostID),
		Prompt:         event.Message,
		RawReceivedAt:  time.Now(),
		Context: connectors.VisibleContext{
			HasMoreBefore:    true,
			HistoryCursor:    historyCursor(event.ConversationID, event.PostID),
			ConversationType: event.ChannelType,
			ChannelID:        event.ConversationID,
			ChannelName:      event.ChannelName,
			InputAttachments: inputAttachments,
			Materials:        inputAttachments,
		},
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

func historyCursor(conversationID string, beforePostID string) string {
	conversationID = strings.TrimSpace(conversationID)
	beforePostID = strings.TrimSpace(beforePostID)
	if conversationID == "" || beforePostID == "" {
		return conversationID
	}
	return conversationID + ":" + beforePostID
}

func mattermostInputAttachments(messageID string, fileIDs []string) []connectors.InputAttachment {
	attachments := []connectors.InputAttachment{}
	for _, fileID := range fileIDs {
		fileID = strings.TrimSpace(fileID)
		if fileID == "" {
			continue
		}
		attachments = append(attachments, connectors.InputAttachment{
			Platform:  "mattermost",
			FileID:    fileID,
			MessageID: messageID,
		})
	}
	return attachments
}

func parseHistoryCursor(historyCursor string) (string, string) {
	trimmedCursor := strings.TrimSpace(historyCursor)
	if trimmedCursor == "" {
		return "", ""
	}
	conversationID, beforePostID, hasBeforePostID := strings.Cut(trimmedCursor, ":")
	if !hasBeforePostID {
		return trimmedCursor, ""
	}
	return strings.TrimSpace(conversationID), strings.TrimSpace(beforePostID)
}
