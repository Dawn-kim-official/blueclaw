package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

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
	Logger            *slog.Logger
	TypingInterval    time.Duration
	TypingTimeout     time.Duration

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
		Logger:            slog.Default(),
		TypingInterval:    4 * time.Second,
		TypingTimeout:     90 * time.Second,
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
		mattermostEventHandler.logger().Warn("mattermost.ingress.malformed", slog.String("source", "http"), slog.String("reason", "invalid_json"), slog.String("error", errorValue.Error()))
		return
	}
	if strings.TrimSpace(event.PostID) == "" {
		http.Error(responseWriter, "post_id is required", http.StatusBadRequest)
		mattermostEventHandler.logger().Warn("mattermost.ingress.malformed", slog.String("source", "http"), slog.String("reason", "missing_post_id"))
		return
	}

	result, errorValue := mattermostEventHandler.HandleMattermostEventValue(request.Context(), event, "http")
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}

	writeJSONResponse(responseWriter, http.StatusOK, result)
}

func (mattermostEventHandler *MattermostEventHandler) processMattermostEvent(ctx context.Context, event mattermost.Event) (MattermostEventResult, error) {
	return mattermostEventHandler.ProcessMattermostEvent(ctx, event, "http")
}

func (mattermostEventHandler *MattermostEventHandler) HandleMattermostEventValue(ctx context.Context, event mattermost.Event, source string) (MattermostEventResult, error) {
	if strings.TrimSpace(event.PostID) == "" {
		mattermostEventHandler.logger().Warn("mattermost.ingress.malformed", slog.String("source", source), slog.String("reason", "missing_post_id"))
		return MattermostEventResult{IsIgnored: true, Reason: "missing_post_id"}, nil
	}

	eventKey := "mattermost:" + strings.TrimSpace(event.PostID)
	if result, isFound := mattermostEventHandler.findProcessedEvent(eventKey); isFound {
		result.IsDuplicate = true
		mattermostEventHandler.logger().Info("mattermost.event.suppressed", slog.String("source", source), slog.String("reason", "duplicate"), slog.String("postID", event.PostID))
		return result, nil
	}

	result, errorValue := mattermostEventHandler.ProcessMattermostEvent(ctx, event, source)
	if errorValue != nil {
		return MattermostEventResult{}, errorValue
	}

	mattermostEventHandler.rememberProcessedEvent(eventKey, result)
	return result, nil
}

func (mattermostEventHandler *MattermostEventHandler) ProcessMattermostEvent(ctx context.Context, event mattermost.Event, source string) (MattermostEventResult, error) {
	logger := mattermostEventHandler.logger()
	logger.Info("mattermost.ingress.received",
		slog.String("source", source),
		slog.String("postID", event.PostID),
		slog.String("channelID", event.ConversationID),
		slog.String("rootID", event.RootID),
		slog.String("userID", event.UserID),
		slog.String("channelType", event.ChannelType),
	)

	botUserID, errorValue := mattermostEventHandler.resolveBotUserID()
	if errorValue == nil && botUserID != "" && event.UserID == botUserID {
		logger.Info("mattermost.event.suppressed", slog.String("reason", "self"), slog.String("postID", event.PostID))
		return MattermostEventResult{IsIgnored: true, Reason: "self"}, nil
	}
	if errorValue != nil {
		logger.Warn("mattermost.bot.lookup_failed", slog.String("error", errorValue.Error()))
	}

	authorizationResult, errorValue := mattermostEventHandler.Connector.AuthorizeEvent(event, mattermostEventHandler.IdentityService)
	if errorValue != nil {
		logger.Error("mattermost.auth.failed", slog.String("postID", event.PostID), slog.String("error", errorValue.Error()))
		return MattermostEventResult{}, errorValue
	}

	isDirectMessage := mattermostEventHandler.resolveEventIsDirectMessage(event)
	replyRootID := event.RootID
	if replyRootID == "" && !isDirectMessage {
		replyRootID = event.PostID
	}

	if !authorizationResult.IsAllowed {
		logger.Info("mattermost.auth.rejected", slog.String("postID", event.PostID), slog.String("reason", "not_invited"))
		result := MattermostEventResult{
			IsAllowed: false,
			Reply:     authorizationResult.Reply,
			Reason:    "not_invited",
		}
		replyPostID, errorValue := mattermostEventHandler.PostClient.CreatePost(event.ConversationID, replyRootID, authorizationResult.Reply)
		if errorValue == nil {
			result.ReplyPostID = replyPostID
			logger.Info("mattermost.outbound.sent", slog.String("postID", event.PostID), slog.String("replyPostID", replyPostID))
		} else {
			logger.Error("mattermost.outbound.failed", slog.String("postID", event.PostID), slog.String("error", errorValue.Error()))
		}
		return result, nil
	}
	logger.Info("mattermost.auth.allowed", slog.String("postID", event.PostID), slog.String("personID", authorizationResult.PersonID))

	taskRun, errorValue := mattermostEventHandler.AgentKernel.HandleInboundMessage(
		authorizationResult.PersonID,
		event.ConversationID,
		event.Message,
	)
	if errorValue != nil {
		logger.Error("mattermost.task.failed", slog.String("postID", event.PostID), slog.String("error", errorValue.Error()))
		return MattermostEventResult{}, errorValue
	}
	logger.Info("mattermost.task.created", slog.String("postID", event.PostID), slog.String("taskRunID", taskRun.TaskRunID))

	reply := "Working on it: " + taskRun.TaskRunID
	stopTyping := mattermostEventHandler.startTyping(ctx, botUserID, event.ConversationID, replyRootID)
	defer stopTyping()
	logger.Info("mattermost.llm.started", slog.String("postID", event.PostID), slog.String("taskRunID", taskRun.TaskRunID))
	generatedReply, errorValue := mattermostEventHandler.AgentKernel.GenerateReply(ctx, event.Message)
	if errorValue == nil {
		reply = generatedReply
		logger.Info("mattermost.llm.completed", slog.String("postID", event.PostID), slog.String("taskRunID", taskRun.TaskRunID))
	} else {
		logger.Error("mattermost.llm.failed", slog.String("postID", event.PostID), slog.String("taskRunID", taskRun.TaskRunID), slog.String("error", errorValue.Error()))
		logger.Warn("mattermost.reply.fallback_used", slog.String("postID", event.PostID), slog.String("taskRunID", taskRun.TaskRunID))
	}
	result := MattermostEventResult{
		IsAllowed: true,
		TaskRunID: taskRun.TaskRunID,
		Reply:     reply,
	}
	replyPostID, errorValue := mattermostEventHandler.PostClient.CreatePost(event.ConversationID, replyRootID, reply)
	if errorValue != nil {
		result.Reason = "reply_failed"
		logger.Error("mattermost.outbound.failed", slog.String("postID", event.PostID), slog.String("taskRunID", taskRun.TaskRunID), slog.String("error", errorValue.Error()))
		return result, nil
	}
	result.ReplyPostID = replyPostID
	logger.Info("mattermost.outbound.sent", slog.String("postID", event.PostID), slog.String("taskRunID", taskRun.TaskRunID), slog.String("replyPostID", replyPostID))

	return result, nil
}

func (mattermostEventHandler *MattermostEventHandler) startTyping(ctx context.Context, botUserID string, conversationID string, parentID string) func() {
	if strings.TrimSpace(botUserID) == "" {
		mattermostEventHandler.logger().Warn("mattermost.typing.skipped", slog.String("reason", "missing_bot_user_id"), slog.String("channelID", conversationID))
		return func() {}
	}

	typingContext, cancel := context.WithCancel(ctx)
	typingInterval := mattermostEventHandler.TypingInterval
	if typingInterval <= 0 {
		typingInterval = 4 * time.Second
	}
	typingTimeout := mattermostEventHandler.TypingTimeout
	if typingTimeout <= 0 {
		typingTimeout = 90 * time.Second
	}

	mattermostEventHandler.logger().Info("mattermost.typing.started", slog.String("channelID", conversationID), slog.String("parentID", parentID))
	mattermostEventHandler.publishTyping(botUserID, conversationID, parentID)
	go mattermostEventHandler.publishTypingUntilDone(typingContext, botUserID, conversationID, parentID, typingInterval, typingTimeout)

	return func() {
		cancel()
		mattermostEventHandler.logger().Info("mattermost.typing.stopped", slog.String("channelID", conversationID), slog.String("parentID", parentID))
	}
}

func (mattermostEventHandler *MattermostEventHandler) publishTypingUntilDone(ctx context.Context, botUserID string, conversationID string, parentID string, typingInterval time.Duration, typingTimeout time.Duration) {
	deadline := time.NewTimer(typingTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(typingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
			mattermostEventHandler.publishTyping(botUserID, conversationID, parentID)
		}
	}
}

func (mattermostEventHandler *MattermostEventHandler) publishTyping(botUserID string, conversationID string, parentID string) {
	errorValue := mattermostEventHandler.PostClient.PublishTyping(botUserID, conversationID, parentID)
	if errorValue != nil {
		mattermostEventHandler.logger().Warn("mattermost.typing.failed", slog.String("channelID", conversationID), slog.String("parentID", parentID), slog.String("error", errorValue.Error()))
	}
}

func (mattermostEventHandler *MattermostEventHandler) resolveEventIsDirectMessage(event mattermost.Event) bool {
	if event.IsDirectMessage() {
		return true
	}

	channelType, errorValue := mattermostEventHandler.PostClient.ResolveChannelType(event.ConversationID)
	return errorValue == nil && (mattermost.Event{ChannelType: channelType}).IsDirectMessage()
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

func (mattermostEventHandler *MattermostEventHandler) logger() *slog.Logger {
	if mattermostEventHandler.Logger != nil {
		return mattermostEventHandler.Logger
	}
	return slog.Default()
}

func writeJSONResponse(responseWriter http.ResponseWriter, statusCode int, value any) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(statusCode)
	_ = json.NewEncoder(responseWriter).Encode(value)
}
