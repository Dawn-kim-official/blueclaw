package httpserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"blueclaw/internal/agent"
	"blueclaw/internal/connectors/mattermost"
	"blueclaw/internal/identity"
)

type MattermostEventHandler struct {
	Connector         *mattermost.Connector
	IdentityService   *identity.IdentityService
	AgentKernel       *agent.AgentKernel
	UserProfileClient mattermost.UserProfileClient
	PostClient        mattermost.PostClient

	mutex           sync.Mutex
	processedEvents map[string]MattermostEventResult
	botUserID       string
}

type MattermostEventResult struct {
	IsAllowed   bool   `json:"isAllowed"`
	IsIgnored   bool   `json:"isIgnored"`
	IsDuplicate bool   `json:"isDuplicate"`
	TaskRunID   string `json:"taskRunID,omitempty"`
	Reply       string `json:"reply,omitempty"`
	ReplyPostID string `json:"replyPostID,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

func NewMattermostEventHandler(
	connector *mattermost.Connector,
	identityService *identity.IdentityService,
	agentKernel *agent.AgentKernel,
	userProfileClient mattermost.UserProfileClient,
	postClient mattermost.PostClient,
) *MattermostEventHandler {
	return &MattermostEventHandler{
		Connector:         connector,
		IdentityService:   identityService,
		AgentKernel:       agentKernel,
		UserProfileClient: userProfileClient,
		PostClient:        postClient,
		processedEvents:   map[string]MattermostEventResult{},
	}
}

func (mattermostEventHandler *MattermostEventHandler) HandleMattermostEvent(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(responseWriter, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var event mattermost.Event
	errorValue := json.NewDecoder(request.Body).Decode(&event)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(event.PostID) == "" {
		http.Error(responseWriter, "post_id is required", http.StatusBadRequest)
		return
	}

	eventKey := "mattermost:" + strings.TrimSpace(event.PostID)
	if result, isFound := mattermostEventHandler.findProcessedEvent(eventKey); isFound {
		result.IsDuplicate = true
		writeJSONResponse(responseWriter, http.StatusOK, result)
		return
	}

	result, errorValue := mattermostEventHandler.processMattermostEvent(event)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}

	mattermostEventHandler.rememberProcessedEvent(eventKey, result)
	writeJSONResponse(responseWriter, http.StatusOK, result)
}

func (mattermostEventHandler *MattermostEventHandler) processMattermostEvent(event mattermost.Event) (MattermostEventResult, error) {
	botUserID, errorValue := mattermostEventHandler.resolveBotUserID()
	if errorValue == nil && botUserID != "" && event.UserID == botUserID {
		return MattermostEventResult{IsIgnored: true, Reason: "self"}, nil
	}

	authorizationResult, errorValue := mattermostEventHandler.Connector.AuthorizeEvent(event, mattermostEventHandler.IdentityService)
	if errorValue != nil {
		return MattermostEventResult{}, errorValue
	}

	replyRootID := event.RootID
	if replyRootID == "" {
		replyRootID = event.PostID
	}

	if !authorizationResult.IsAllowed {
		result := MattermostEventResult{
			IsAllowed: false,
			Reply:     authorizationResult.Reply,
			Reason:    "not_invited",
		}
		replyPostID, errorValue := mattermostEventHandler.PostClient.CreatePost(event.ConversationID, replyRootID, authorizationResult.Reply)
		if errorValue == nil {
			result.ReplyPostID = replyPostID
		}
		return result, nil
	}

	taskRun, errorValue := mattermostEventHandler.AgentKernel.HandleInboundMessage(
		authorizationResult.PersonID,
		event.ConversationID,
		event.Message,
	)
	if errorValue != nil {
		return MattermostEventResult{}, errorValue
	}

	reply := "Working on it: " + taskRun.TaskRunID
	result := MattermostEventResult{
		IsAllowed: true,
		TaskRunID: taskRun.TaskRunID,
		Reply:     reply,
	}
	replyPostID, errorValue := mattermostEventHandler.PostClient.CreatePost(event.ConversationID, replyRootID, reply)
	if errorValue != nil {
		result.Reason = "reply_failed"
		return result, nil
	}
	result.ReplyPostID = replyPostID

	return result, nil
}

func (mattermostEventHandler *MattermostEventHandler) resolveBotUserID() (string, error) {
	mattermostEventHandler.mutex.Lock()
	defer mattermostEventHandler.mutex.Unlock()

	if mattermostEventHandler.botUserID != "" {
		return mattermostEventHandler.botUserID, nil
	}

	botUserID, errorValue := mattermostEventHandler.UserProfileClient.ResolveBotUserID()
	if errorValue != nil {
		return "", errorValue
	}
	mattermostEventHandler.botUserID = botUserID
	return botUserID, nil
}

func (mattermostEventHandler *MattermostEventHandler) findProcessedEvent(eventKey string) (MattermostEventResult, bool) {
	mattermostEventHandler.mutex.Lock()
	defer mattermostEventHandler.mutex.Unlock()

	result, isFound := mattermostEventHandler.processedEvents[eventKey]
	return result, isFound
}

func (mattermostEventHandler *MattermostEventHandler) rememberProcessedEvent(eventKey string, result MattermostEventResult) {
	mattermostEventHandler.mutex.Lock()
	defer mattermostEventHandler.mutex.Unlock()

	mattermostEventHandler.processedEvents[eventKey] = result
}

func writeJSONResponse(responseWriter http.ResponseWriter, statusCode int, value any) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(statusCode)
	_ = json.NewEncoder(responseWriter).Encode(value)
}
