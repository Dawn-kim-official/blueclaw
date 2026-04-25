package mattermost

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"blueclaw/internal/identity"
)

const NotInvitedReply = "This Intern Kim has not invited your account yet. Ask the administrator for access."

type Connector struct {
	eventParser          EventParser
	userIdentityResolver UserIdentityResolver
}

type AuthorizationResult struct {
	IsAllowed bool
	PersonID  string
	Reply     string
}

type UserIdentityResolver interface {
	ResolveUserIdentity(externalUserID string) (identity.PlatformAccountIdentity, error)
}

type UserProfileClient struct {
	BaseURL    string
	BotToken   string
	HTTPClient *http.Client
}

type PostClient struct {
	BaseURL    string
	BotToken   string
	HTTPClient *http.Client
}

func NewConnector() *Connector {
	return &Connector{eventParser: EventParser{}}
}

func NewConnectorWithIdentityResolver(userIdentityResolver UserIdentityResolver) *Connector {
	return &Connector{
		eventParser:          EventParser{},
		userIdentityResolver: userIdentityResolver,
	}
}

func (connector *Connector) AuthorizeEvent(event Event, identityService *identity.IdentityService) (AuthorizationResult, error) {
	personID, isFound := identityService.ResolvePersonIDByPlatformAccount("mattermost", event.UserID)
	if isFound {
		return AuthorizationResult{IsAllowed: true, PersonID: personID}, nil
	}
	if connector.userIdentityResolver == nil {
		return AuthorizationResult{}, errors.New("mattermost identity resolver missing")
	}

	platformAccountIdentity, errorValue := connector.userIdentityResolver.ResolveUserIdentity(event.UserID)
	if errorValue != nil {
		return AuthorizationResult{}, errorValue
	}
	platformAccountIdentity.Platform = "mattermost"
	platformAccountIdentity.ExternalUserID = event.UserID
	identityService.RememberPlatformAccount(platformAccountIdentity)

	personID, isFound = identityService.ResolvePersonIDByPlatformAccount("mattermost", event.UserID)
	if !isFound {
		return AuthorizationResult{IsAllowed: false, Reply: NotInvitedReply}, nil
	}

	return AuthorizationResult{IsAllowed: true, PersonID: personID}, nil
}

func (postClient PostClient) CreatePost(conversationID string, rootID string, message string) (string, error) {
	httpClient := postClient.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	body, errorValue := json.Marshal(map[string]string{
		"channel_id": conversationID,
		"root_id":    rootID,
		"message":    message,
	})
	if errorValue != nil {
		return "", errorValue
	}

	requestURL := strings.TrimRight(postClient.BaseURL, "/") + "/api/v4/posts"
	request, errorValue := http.NewRequest(http.MethodPost, requestURL, bytes.NewReader(body))
	if errorValue != nil {
		return "", errorValue
	}
	request.Header.Set("Authorization", "Bearer "+postClient.BotToken)
	request.Header.Set("Content-Type", "application/json")

	response, errorValue := httpClient.Do(request)
	if errorValue != nil {
		return "", errorValue
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		responseDocument, _ := io.ReadAll(response.Body)
		return "", errors.New("mattermost post creation failed: " + string(responseDocument))
	}

	var postResponse struct {
		ID string `json:"id"`
	}
	errorValue = json.NewDecoder(response.Body).Decode(&postResponse)
	if errorValue != nil {
		return "", errorValue
	}
	return postResponse.ID, nil
}

func (postClient PostClient) PublishTyping(userID string, conversationID string, parentID string) error {
	httpClient := postClient.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	body := map[string]string{
		"channel_id": conversationID,
	}
	if strings.TrimSpace(parentID) != "" {
		body["parent_id"] = parentID
	}

	document, errorValue := json.Marshal(body)
	if errorValue != nil {
		return errorValue
	}

	requestURL := strings.TrimRight(postClient.BaseURL, "/") + "/api/v4/users/" + userID + "/typing"
	request, errorValue := http.NewRequest(http.MethodPost, requestURL, bytes.NewReader(document))
	if errorValue != nil {
		return errorValue
	}
	request.Header.Set("Authorization", "Bearer "+postClient.BotToken)
	request.Header.Set("Content-Type", "application/json")

	response, errorValue := httpClient.Do(request)
	if errorValue != nil {
		return errorValue
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		responseDocument, _ := io.ReadAll(response.Body)
		return errors.New("mattermost typing publish failed: " + string(responseDocument))
	}

	return nil
}

func (postClient PostClient) ResolveChannelType(conversationID string) (string, error) {
	httpClient := postClient.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	requestURL := strings.TrimRight(postClient.BaseURL, "/") + "/api/v4/channels/" + conversationID
	request, errorValue := http.NewRequest(http.MethodGet, requestURL, nil)
	if errorValue != nil {
		return "", errorValue
	}
	request.Header.Set("Authorization", "Bearer "+postClient.BotToken)

	response, errorValue := httpClient.Do(request)
	if errorValue != nil {
		return "", errorValue
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", errors.New("mattermost channel lookup failed")
	}

	var channelResponse struct {
		Type string `json:"type"`
	}
	errorValue = json.NewDecoder(response.Body).Decode(&channelResponse)
	if errorValue != nil {
		return "", errorValue
	}
	return channelResponse.Type, nil
}

func (userProfileClient UserProfileClient) ResolveBotUserID() (string, error) {
	httpClient := userProfileClient.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	requestURL := strings.TrimRight(userProfileClient.BaseURL, "/") + "/api/v4/users/me"
	request, errorValue := http.NewRequest(http.MethodGet, requestURL, nil)
	if errorValue != nil {
		return "", errorValue
	}
	request.Header.Set("Authorization", "Bearer "+userProfileClient.BotToken)

	response, errorValue := httpClient.Do(request)
	if errorValue != nil {
		return "", errorValue
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", errors.New("mattermost bot lookup failed")
	}

	var userProfile struct {
		ID string `json:"id"`
	}
	errorValue = json.NewDecoder(response.Body).Decode(&userProfile)
	if errorValue != nil {
		return "", errorValue
	}
	return userProfile.ID, nil
}

func (userProfileClient UserProfileClient) ResolveUserIdentity(externalUserID string) (identity.PlatformAccountIdentity, error) {
	httpClient := userProfileClient.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	requestURL := strings.TrimRight(userProfileClient.BaseURL, "/") + "/api/v4/users/" + externalUserID
	request, errorValue := http.NewRequest(http.MethodGet, requestURL, nil)
	if errorValue != nil {
		return identity.PlatformAccountIdentity{}, errorValue
	}
	request.Header.Set("Authorization", "Bearer "+userProfileClient.BotToken)

	response, errorValue := httpClient.Do(request)
	if errorValue != nil {
		return identity.PlatformAccountIdentity{}, errorValue
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return identity.PlatformAccountIdentity{}, errors.New("mattermost user lookup failed")
	}

	var userProfile struct {
		Email     string `json:"email"`
		Username  string `json:"username"`
		Nickname  string `json:"nickname"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	}
	errorValue = json.NewDecoder(response.Body).Decode(&userProfile)
	if errorValue != nil {
		return identity.PlatformAccountIdentity{}, errorValue
	}

	return identity.PlatformAccountIdentity{
		Platform:       "mattermost",
		ExternalUserID: externalUserID,
		Email:          userProfile.Email,
		DisplayName:    mattermostDisplayName(userProfile.FirstName, userProfile.LastName, userProfile.Nickname, userProfile.Username),
	}, nil
}

func mattermostDisplayName(firstName string, lastName string, nickname string, username string) string {
	fullName := strings.TrimSpace(strings.TrimSpace(firstName) + " " + strings.TrimSpace(lastName))
	if fullName != "" {
		return fullName
	}
	if strings.TrimSpace(nickname) != "" {
		return strings.TrimSpace(nickname)
	}
	return strings.TrimSpace(username)
}
