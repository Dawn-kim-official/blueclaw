package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/connectors/mattermost"
	"blueclaw/internal/identity"
	"blueclaw/internal/llm"
	"blueclaw/internal/policy"
	"blueclaw/internal/task"
)

func TestMattermostEventHandlerAllowsInvitedUserAndDeduplicates(t *testing.T) {
	handler, taskRunService, postedPosts := newTestMattermostEventHandler(t, "invited@example.com")

	result := submitMattermostEvent(t, handler, mattermost.Event{
		UserID:         "mattermost-user",
		ConversationID: "channel-1",
		PostID:         "post-1",
		Message:        "please help",
	})

	if !result.IsAllowed {
		t.Fatalf("expected invited event to be allowed: %+v", result)
	}
	if result.Reply != "Mattermost model reply" {
		t.Fatalf("expected language model reply, got %q", result.Reply)
	}
	if len(taskRunService.ListTaskRun()) != 1 {
		t.Fatalf("expected one task run, got %d", len(taskRunService.ListTaskRun()))
	}
	if len(*postedPosts) != 1 {
		t.Fatalf("expected one reply post, got %d", len(*postedPosts))
	}
	if (*postedPosts)[0].RootID != "post-1" {
		t.Fatalf("expected channel root reply to become a thread reply, got root %q", (*postedPosts)[0].RootID)
	}

	duplicateResult := submitMattermostEvent(t, handler, mattermost.Event{
		UserID:         "mattermost-user",
		ConversationID: "channel-1",
		PostID:         "post-1",
		Message:        "please help",
	})

	if !duplicateResult.IsDuplicate {
		t.Fatalf("expected duplicate event result: %+v", duplicateResult)
	}
	if len(taskRunService.ListTaskRun()) != 1 {
		t.Fatalf("expected duplicate to keep one task run, got %d", len(taskRunService.ListTaskRun()))
	}
	if len(*postedPosts) != 1 {
		t.Fatalf("expected duplicate to keep one reply post, got %d", len(*postedPosts))
	}
}

func TestMattermostEventHandlerRejectsUninvitedUser(t *testing.T) {
	handler, taskRunService, postedPosts := newTestMattermostEventHandler(t, "someone-else@example.com")

	result := submitMattermostEvent(t, handler, mattermost.Event{
		UserID:         "mattermost-user",
		ConversationID: "channel-1",
		PostID:         "post-2",
		Message:        "please help",
	})

	if result.IsAllowed {
		t.Fatalf("expected uninvited event to be rejected: %+v", result)
	}
	if result.Reason != "not_invited" {
		t.Fatalf("expected not_invited reason, got %q", result.Reason)
	}
	if result.Reply != mattermost.NotInvitedReply {
		t.Fatalf("expected not invited reply, got %q", result.Reply)
	}
	if len(taskRunService.ListTaskRun()) != 0 {
		t.Fatalf("expected no task runs, got %d", len(taskRunService.ListTaskRun()))
	}
	if len(*postedPosts) != 1 {
		t.Fatalf("expected one rejection reply post, got %d", len(*postedPosts))
	}
}

func TestMattermostEventHandlerRepliesToDirectMessageWithoutThreadRoot(t *testing.T) {
	handler, _, postedPosts := newTestMattermostEventHandler(t, "invited@example.com")

	result := submitMattermostEvent(t, handler, mattermost.Event{
		UserID:         "mattermost-user",
		ConversationID: "direct-channel-1",
		PostID:         "direct-post-1",
		Message:        "dm help",
	})

	if !result.IsAllowed {
		t.Fatalf("expected direct message to be allowed: %+v", result)
	}
	if len(*postedPosts) != 1 {
		t.Fatalf("expected one direct reply post, got %d", len(*postedPosts))
	}
	if (*postedPosts)[0].RootID != "" {
		t.Fatalf("expected direct message reply without thread root, got %q", (*postedPosts)[0].RootID)
	}
}

func newTestMattermostEventHandler(t *testing.T, invitedEmail string) (*MattermostEventHandler, *task.TaskRunService, *[]testMattermostPost) {
	t.Helper()

	postedPosts := []testMattermostPost{}
	httpClient := &http.Client{Transport: testMattermostRoundTripper{postedPosts: &postedPosts}}

	policyDocument := policy.PolicyDocument{
		People: []policy.PersonPolicy{
			{
				PersonID:    "person-1",
				DisplayName: "Invited",
				Emails:      []string{invitedEmail},
			},
		},
	}
	policyProjection := policy.PolicyProjectionService{}.ReplacePolicyProjectionTransactionally(policyDocument)
	identityService := identity.NewIdentityService(policyProjection)
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskStepService := task.NewTaskStepService()
	agentKernel := agent.NewAgentKernel(taskRunService, taskStepService)
	agentKernel.UseLanguageModelProvider(staticReplyLanguageModelProvider{
		content: `{"reply":"Mattermost model reply"}`,
	})
	userProfileClient := mattermost.UserProfileClient{
		BaseURL:    "http://mattermost.test",
		BotToken:   "bot-token",
		HTTPClient: httpClient,
	}
	postClient := mattermost.PostClient{
		BaseURL:    "http://mattermost.test",
		BotToken:   "bot-token",
		HTTPClient: httpClient,
	}

	return NewMattermostEventHandler(
		mattermost.NewConnectorWithIdentityResolver(userProfileClient),
		identityService,
		agentKernel,
		userProfileClient,
		postClient,
	), taskRunService, &postedPosts
}

func submitMattermostEvent(t *testing.T, handler *MattermostEventHandler, event mattermost.Event) MattermostEventResult {
	t.Helper()

	payload, errorValue := json.Marshal(event)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	request := httptest.NewRequest(http.MethodPost, "/connectors/mattermost/events", bytes.NewReader(payload))
	responseRecorder := httptest.NewRecorder()

	handler.HandleMattermostEvent(responseRecorder, request)
	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected status ok, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}

	var result MattermostEventResult
	if errorValue := json.NewDecoder(responseRecorder.Body).Decode(&result); errorValue != nil {
		t.Fatal(errorValue)
	}
	return result
}

type testMattermostRoundTripper struct {
	postedPosts *[]testMattermostPost
}

type testMattermostPost struct {
	ChannelID string
	RootID    string
	Message   string
}

func (roundTripper testMattermostRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	statusCode := http.StatusOK
	responseBody := map[string]string{}

	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/api/v4/users/me":
		responseBody = map[string]string{"id": "bot-user"}
	case request.Method == http.MethodGet && request.URL.Path == "/api/v4/users/mattermost-user":
		responseBody = map[string]string{
			"email":    "invited@example.com",
			"username": "invited",
		}
	case request.Method == http.MethodGet && request.URL.Path == "/api/v4/channels/direct-channel-1":
		responseBody = map[string]string{"type": "D"}
	case request.Method == http.MethodPost && request.URL.Path == "/api/v4/posts":
		statusCode = http.StatusCreated
		roundTripper.recordPostMessage(request)
		responseBody = map[string]string{"id": "reply-post"}
	default:
		statusCode = http.StatusNotFound
		responseBody = map[string]string{"error": "not found"}
	}

	body, errorValue := json.Marshal(responseBody)
	if errorValue != nil {
		return nil, errorValue
	}

	return &http.Response{
		StatusCode: statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    request,
	}, nil
}

func (roundTripper testMattermostRoundTripper) recordPostMessage(request *http.Request) {
	var postRequest struct {
		ChannelID string `json:"channel_id"`
		RootID    string `json:"root_id"`
		Message   string `json:"message"`
	}
	if errorValue := json.NewDecoder(request.Body).Decode(&postRequest); errorValue != nil {
		return
	}
	*roundTripper.postedPosts = append(*roundTripper.postedPosts, testMattermostPost{
		ChannelID: postRequest.ChannelID,
		RootID:    postRequest.RootID,
		Message:   postRequest.Message,
	})
}

type staticReplyLanguageModelProvider struct {
	content string
}

func (languageModelProvider staticReplyLanguageModelProvider) GenerateResponse(responseContext context.Context, prompt string) (string, error) {
	_ = responseContext
	_ = prompt
	return languageModelProvider.content, nil
}

func (languageModelProvider staticReplyLanguageModelProvider) GenerateStructuredResponse(responseContext context.Context, structuredResponseRequest llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	_ = responseContext
	_ = structuredResponseRequest
	return llm.StructuredResponse{Content: languageModelProvider.content}, nil
}
