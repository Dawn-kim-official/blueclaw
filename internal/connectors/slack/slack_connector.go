package slack

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
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

func (userProfileClient UserProfileClient) ResolveUserIdentity(externalUserID string) (identity.PlatformAccountIdentity, error) {
	httpClient := userProfileClient.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	requestURL := slackAPIURL(userProfileClient.BaseURL, "/users.info?user="+url.QueryEscape(externalUserID))
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

func (userProfileClient UserProfileClient) ResolveBotUserID() (string, error) {
	httpClient := userProfileClient.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	requestURL := slackAPIURL(userProfileClient.BaseURL, "/auth.test")
	request, errorValue := http.NewRequest(http.MethodPost, requestURL, nil)
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
		responseDocument, _ := io.ReadAll(response.Body)
		return "", errors.New("slack auth test failed: " + string(responseDocument))
	}

	var authResponse struct {
		IsOK   bool   `json:"ok"`
		UserID string `json:"user_id"`
		Error  string `json:"error"`
	}
	errorValue = json.NewDecoder(response.Body).Decode(&authResponse)
	if errorValue != nil {
		return "", errorValue
	}
	if !authResponse.IsOK {
		return "", errors.New("slack auth test failed: " + authResponse.Error)
	}
	return strings.TrimSpace(authResponse.UserID), nil
}

func (postClient PostClient) CreateMessage(conversationID string, parentID string, message string) (string, error) {
	httpClient := postClient.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	body := map[string]string{
		"channel": conversationID,
		"text":    message,
	}
	if strings.TrimSpace(parentID) != "" {
		body["thread_ts"] = parentID
	}
	document, errorValue := json.Marshal(body)
	if errorValue != nil {
		return "", errorValue
	}

	request, errorValue := http.NewRequest(http.MethodPost, slackAPIURL(postClient.BaseURL, "/chat.postMessage"), bytes.NewReader(document))
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
	responseDocument, _ := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", errors.New("slack message post failed: " + string(responseDocument))
	}

	var postResponse struct {
		IsOK  bool   `json:"ok"`
		TS    string `json:"ts"`
		Error string `json:"error"`
	}
	errorValue = json.Unmarshal(responseDocument, &postResponse)
	if errorValue != nil {
		return "", errorValue
	}
	if !postResponse.IsOK {
		return "", errors.New("slack message post failed: " + postResponse.Error)
	}
	return postResponse.TS, nil
}

func slackDisplayName(realName string, name string) string {
	if strings.TrimSpace(realName) != "" {
		return strings.TrimSpace(realName)
	}
	return strings.TrimSpace(name)
}

func slackAPIURL(baseURL string, path string) string {
	trimmedBaseURL := strings.TrimRight(baseURL, "/")
	if trimmedBaseURL == "" {
		trimmedBaseURL = "https://slack.com/api"
	}
	return trimmedBaseURL + path
}
