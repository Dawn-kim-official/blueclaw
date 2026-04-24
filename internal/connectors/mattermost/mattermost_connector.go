package mattermost

import (
	"encoding/json"
	"errors"
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

func NewConnector() *Connector {
	return &Connector{eventParser: EventParser{}}
}

func NewConnectorWithIdentityResolver(userIdentityResolver UserIdentityResolver) *Connector {
	return &Connector{
		eventParser:          EventParser{},
		userIdentityResolver: userIdentityResolver,
	}
}

func (connector *Connector) StartListening() error {
	return nil
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

func (connector *Connector) SendDirectReply(conversationID string, message string) string {
	return conversationID + ":" + message
}

func (connector *Connector) SendChannelReply(conversationID string, message string) string {
	return conversationID + ":" + message
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
