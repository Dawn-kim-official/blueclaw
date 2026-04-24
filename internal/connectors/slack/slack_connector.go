package slack

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
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
	personID, isFound := identityService.ResolvePersonIDByPlatformAccount("slack", event.UserID)
	if isFound {
		return AuthorizationResult{IsAllowed: true, PersonID: personID}, nil
	}
	if connector.userIdentityResolver == nil {
		return AuthorizationResult{}, errors.New("slack identity resolver missing")
	}

	platformAccountIdentity, errorValue := connector.userIdentityResolver.ResolveUserIdentity(event.UserID)
	if errorValue != nil {
		return AuthorizationResult{}, errorValue
	}
	platformAccountIdentity.Platform = "slack"
	platformAccountIdentity.ExternalUserID = event.UserID
	identityService.RememberPlatformAccount(platformAccountIdentity)

	personID, isFound = identityService.ResolvePersonIDByPlatformAccount("slack", event.UserID)
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

	baseURL := strings.TrimRight(userProfileClient.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://slack.com/api"
	}
	requestURL := baseURL + "/users.info?user=" + url.QueryEscape(externalUserID)
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
		return identity.PlatformAccountIdentity{}, errors.New("slack user lookup failed")
	}

	var userProfileResponse struct {
		IsOK bool `json:"ok"`
		User struct {
			Name     string `json:"name"`
			RealName string `json:"real_name"`
			Profile  struct {
				Email string `json:"email"`
			} `json:"profile"`
		} `json:"user"`
		Error string `json:"error"`
	}
	errorValue = json.NewDecoder(response.Body).Decode(&userProfileResponse)
	if errorValue != nil {
		return identity.PlatformAccountIdentity{}, errorValue
	}
	if !userProfileResponse.IsOK {
		return identity.PlatformAccountIdentity{}, errors.New("slack user lookup failed: " + userProfileResponse.Error)
	}

	return identity.PlatformAccountIdentity{
		Platform:       "slack",
		ExternalUserID: externalUserID,
		Email:          userProfileResponse.User.Profile.Email,
		DisplayName:    slackDisplayName(userProfileResponse.User.RealName, userProfileResponse.User.Name),
	}, nil
}

func slackDisplayName(realName string, name string) string {
	if strings.TrimSpace(realName) != "" {
		return strings.TrimSpace(realName)
	}
	return strings.TrimSpace(name)
}
