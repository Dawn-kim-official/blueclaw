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
	ChannelType string `json:"channelType"`
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
	errorValue := client.Client.Call(context.Background(), "mattermost.botUser", map[string]string{}, &response)
	return response.UserID, errorValue
}

func (client MattermostClient) ResolveUserIdentity(externalUserID string) (identity.PlatformAccountIdentity, error) {
	var response userResponse
	errorValue := client.Client.Call(context.Background(), "mattermost.lookupUser", userRequest{ExternalUserID: externalUserID}, &response)
	if errorValue != nil {
		return identity.PlatformAccountIdentity{}, errorValue
	}
	return identity.PlatformAccountIdentity{
		Platform:       "mattermost",
		ExternalUserID: response.ExternalUserID,
		Email:          response.Email,
		DisplayName:    response.DisplayName,
	}, nil
}

func (client MattermostClient) CreatePost(conversationID string, rootID string, message string) (string, error) {
	var response replyResponse
	errorValue := client.Client.Call(context.Background(), "mattermost.reply", replyRequest{
		ConversationID: conversationID,
		ParentID:       rootID,
		Message:        message,
	}, &response)
	return response.DispatchID, errorValue
}

func (client MattermostClient) PublishTyping(botUserID string, conversationID string, parentID string) error {
	return client.Client.Call(context.Background(), "mattermost.typing", typingRequest{
		BotUserID:      botUserID,
		ConversationID: conversationID,
		ParentID:       parentID,
	}, nil)
}

func (client MattermostClient) ResolveChannelType(conversationID string) (string, error) {
	var response channelResponse
	errorValue := client.Client.Call(context.Background(), "mattermost.channelType", channelRequest{ConversationID: conversationID}, &response)
	return response.ChannelType, errorValue
}

func (client SlackClient) ResolveBotUserID() (string, error) {
	var response botResponse
	errorValue := client.Client.Call(context.Background(), "slack.botUser", map[string]string{}, &response)
	return response.UserID, errorValue
}

func (client SlackClient) ResolveUserIdentity(externalUserID string) (identity.PlatformAccountIdentity, error) {
	var response userResponse
	errorValue := client.Client.Call(context.Background(), "slack.lookupUser", userRequest{ExternalUserID: externalUserID}, &response)
	if errorValue != nil {
		return identity.PlatformAccountIdentity{}, errorValue
	}
	return identity.PlatformAccountIdentity{
		Platform:       "slack",
		ExternalUserID: response.ExternalUserID,
		Email:          response.Email,
		DisplayName:    response.DisplayName,
	}, nil
}

func (client SlackClient) CreateMessage(conversationID string, parentID string, message string) (string, error) {
	var response replyResponse
	errorValue := client.Client.Call(context.Background(), "slack.reply", replyRequest{
		ConversationID: conversationID,
		ParentID:       parentID,
		Message:        message,
	}, &response)
	return response.DispatchID, errorValue
}
