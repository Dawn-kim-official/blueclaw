package capability

import (
	"context"

	capabilityapi "blueclaw/internal/capability"
	"blueclaw/internal/identity"
)

type MattermostClient struct {
	Client capabilityapi.Client
}

type SlackClient struct {
	Client capabilityapi.Client
}

type userRequest struct {
	ExternalUserID string `json:"externalUserID"`
}

type userResponse struct {
	Platform       string `json:"platform"`
	ExternalUserID string `json:"externalUserID"`
	UserID         string `json:"userID"`
	Email          string `json:"email"`
	DisplayName    string `json:"displayName"`
}

type botResponse struct {
	UserID string `json:"userID"`
}

type channelRequest struct {
	ConversationID string `json:"conversationID"`
}

type channelResponse struct {
	ChannelType      string `json:"channelType"`
	ConversationKind string `json:"conversationKind"`
}

type replyRequest struct {
	ConversationID string `json:"conversationID"`
	ParentID       string `json:"parentID"`
	Message        string `json:"message"`
}

type replyResponse struct {
	DispatchID string `json:"dispatchID"`
}

type typingRequest struct {
	BotUserID      string `json:"botUserID"`
	ConversationID string `json:"conversationID"`
	ParentID       string `json:"parentID"`
}

func (client MattermostClient) ResolveBotUserID() (string, error) {
	var response botResponse
	errorValue := client.Client.Post(context.Background(), "/v1/platform/mattermost/bot.resolve", map[string]string{}, &response)
	return response.UserID, errorValue
}

func (client MattermostClient) ResolveUserIdentity(externalUserID string) (identity.PlatformAccountIdentity, error) {
	var response userResponse
	errorValue := client.Client.Post(context.Background(), "/v1/platform/mattermost/identity.resolve", userRequest{ExternalUserID: externalUserID}, &response)
	if errorValue != nil {
		return identity.PlatformAccountIdentity{}, errorValue
	}
	return identity.PlatformAccountIdentity{
		Platform:       "mattermost",
		ExternalUserID: firstCapabilityValue(response.ExternalUserID, response.UserID),
		Email:          response.Email,
		DisplayName:    response.DisplayName,
	}, nil
}

func (client MattermostClient) CreatePost(conversationID string, rootID string, message string) (string, error) {
	var response replyResponse
	errorValue := client.Client.Post(context.Background(), "/v1/platform/mattermost/reply.send", replyRequest{
		ConversationID: conversationID,
		ParentID:       rootID,
		Message:        message,
	}, &response)
	return response.DispatchID, errorValue
}

func (client MattermostClient) PublishTyping(botUserID string, conversationID string, parentID string) error {
	return client.Client.Post(context.Background(), "/v1/platform/mattermost/typing.publish", typingRequest{
		BotUserID:      botUserID,
		ConversationID: conversationID,
		ParentID:       parentID,
	}, nil)
}

func (client MattermostClient) ResolveChannelType(conversationID string) (string, error) {
	var response channelResponse
	errorValue := client.Client.Post(context.Background(), "/v1/platform/mattermost/conversation.kind", channelRequest{ConversationID: conversationID}, &response)
	return firstCapabilityValue(response.ChannelType, response.ConversationKind), errorValue
}

func (client SlackClient) ResolveBotUserID() (string, error) {
	var response botResponse
	errorValue := client.Client.Post(context.Background(), "/v1/platform/slack/bot.resolve", map[string]string{}, &response)
	return response.UserID, errorValue
}

func (client SlackClient) ResolveUserIdentity(externalUserID string) (identity.PlatformAccountIdentity, error) {
	var response userResponse
	errorValue := client.Client.Post(context.Background(), "/v1/platform/slack/identity.resolve", userRequest{ExternalUserID: externalUserID}, &response)
	if errorValue != nil {
		return identity.PlatformAccountIdentity{}, errorValue
	}
	return identity.PlatformAccountIdentity{
		Platform:       "slack",
		ExternalUserID: firstCapabilityValue(response.ExternalUserID, response.UserID),
		Email:          response.Email,
		DisplayName:    response.DisplayName,
	}, nil
}

func (client SlackClient) CreateMessage(conversationID string, parentID string, message string) (string, error) {
	var response replyResponse
	errorValue := client.Client.Post(context.Background(), "/v1/platform/slack/reply.send", replyRequest{
		ConversationID: conversationID,
		ParentID:       parentID,
		Message:        message,
	}, &response)
	return response.DispatchID, errorValue
}

func firstCapabilityValue(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
