package connectors

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/identity"
)

type PlatformInboundEvent struct {
	Platform       string
	Source         string
	EventID        string
	ConversationID string
	MessageID      string
	ReplyParentID  string
	RootMessageID  string
	SenderUserID   string
	ChannelType    string
	Text           string
	RawReceivedAt  time.Time
	IsBotMessage   bool
}

type ReplyTarget struct {
	ConversationID string
	ParentID       string
	IsDirect       bool
	DedupeKey      string
}

type HTTPParseResult struct {
	Event             PlatformInboundEvent
	HasEvent          bool
	ImmediateResponse *HTTPResponse
}

type HTTPResponse struct {
	StatusCode  int
	ContentType string
	Body        []byte
}

type ConnectorRuntimeResult struct {
	Handled         bool   `json:"handled"`
	Platform        string `json:"platform"`
	Duplicate       bool   `json:"duplicate"`
	Ignored         bool   `json:"ignored"`
	Reason          string `json:"reason,omitempty"`
	TaskRunID       string `json:"taskRunID,omitempty"`
	ReplyDispatchID string `json:"replyDispatchID,omitempty"`
}

type PlatformAdapter interface {
	Name() string
	ParseHTTPEvent(context.Context, *http.Request) (HTTPParseResult, error)
	ParseRealtimeEvent(context.Context, []byte, string) (PlatformInboundEvent, bool, error)
	ResolveIdentity(context.Context, string) (identity.PlatformAccountIdentity, error)
	ResolveBotUserID(context.Context) (string, error)
	ResolveConversationKind(context.Context, PlatformInboundEvent) (ConversationKind, error)
	PublishTyping(context.Context, string, ReplyTarget) error
	SendReply(context.Context, ReplyTarget, string) (string, error)
	NotInvitedReply() string
}

type ConversationKind struct {
	IsDirect bool
}

type ConnectorTransport interface {
	Name() string
	Platform() string
	Start(context.Context)
}

type ConnectorRuntime struct {
	identityService *identity.IdentityService
	agentKernel     *agent.AgentKernel
	logger          *slog.Logger
	typingInterval  time.Duration
	typingTimeout   time.Duration

	mutex             sync.Mutex
	adapterByPlatform map[string]PlatformAdapter
	processedResults  map[string]ConnectorRuntimeResult
	botUserByPlatform map[string]string
}

func NewConnectorRuntime(identityService *identity.IdentityService, agentKernel *agent.AgentKernel, logger *slog.Logger) *ConnectorRuntime {
	if logger == nil {
		logger = slog.Default()
	}

	return &ConnectorRuntime{
		identityService:   identityService,
		agentKernel:       agentKernel,
		logger:            logger,
		typingInterval:    4 * time.Second,
		typingTimeout:     90 * time.Second,
		adapterByPlatform: map[string]PlatformAdapter{},
		processedResults:  map[string]ConnectorRuntimeResult{},
		botUserByPlatform: map[string]string{},
	}
}

func (connectorRuntime *ConnectorRuntime) RegisterAdapter(adapter PlatformAdapter) {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()

	connectorRuntime.adapterByPlatform[adapter.Name()] = adapter
}

func (connectorRuntime *ConnectorRuntime) UseTypingTiming(interval time.Duration, timeout time.Duration) {
	connectorRuntime.typingInterval = interval
	connectorRuntime.typingTimeout = timeout
}

func (connectorRuntime *ConnectorRuntime) HandleHTTPEvent(ctx context.Context, platform string, request *http.Request) (ConnectorRuntimeResult, *HTTPResponse, error) {
	adapter, errorValue := connectorRuntime.findAdapter(platform)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, nil, errorValue
	}

	parseResult, errorValue := adapter.ParseHTTPEvent(ctx, request)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".ingress.malformed", slog.String("source", "http"), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{}, nil, errorValue
	}
	if parseResult.ImmediateResponse != nil {
		return ConnectorRuntimeResult{Handled: true, Platform: platform}, parseResult.ImmediateResponse, nil
	}
	if !parseResult.HasEvent {
		return ConnectorRuntimeResult{Handled: true, Platform: platform, Ignored: true, Reason: "no_event"}, nil, nil
	}

	parseResult.Event.Platform = platform
	parseResult.Event.Source = "http"
	result, errorValue := connectorRuntime.HandleInboundEvent(ctx, adapter, parseResult.Event)
	return result, nil, errorValue
}

func (connectorRuntime *ConnectorRuntime) HandleRealtimeEvent(ctx context.Context, platform string, payload []byte, source string) (ConnectorRuntimeResult, error) {
	adapter, errorValue := connectorRuntime.findAdapter(platform)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}

	event, hasEvent, errorValue := adapter.ParseRealtimeEvent(ctx, payload, source)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".realtime.malformed", slog.String("source", source), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{}, errorValue
	}
	if !hasEvent {
		return ConnectorRuntimeResult{Handled: true, Platform: platform, Ignored: true, Reason: "no_event"}, nil
	}

	event.Platform = platform
	event.Source = source
	return connectorRuntime.HandleInboundEvent(ctx, adapter, event)
}

func (connectorRuntime *ConnectorRuntime) HandleInboundEvent(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (ConnectorRuntimeResult, error) {
	if strings.TrimSpace(event.MessageID) == "" {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".ingress.malformed", slog.String("source", event.Source), slog.String("reason", "missing_message_id"))
		return ConnectorRuntimeResult{Handled: true, Platform: adapter.Name(), Ignored: true, Reason: "missing_message_id"}, nil
	}

	eventKey := event.DedupeKey()
	if result, isFound := connectorRuntime.findProcessedResult(eventKey); isFound {
		result.Duplicate = true
		connectorRuntime.logger.Info("connector."+adapter.Name()+".event.suppressed", slog.String("source", event.Source), slog.String("reason", "duplicate"), slog.String("messageID", event.MessageID))
		return result, nil
	}

	result, errorValue := connectorRuntime.processInboundEvent(ctx, adapter, event)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}

	connectorRuntime.rememberProcessedResult(eventKey, result)
	return result, nil
}

func (connectorRuntime *ConnectorRuntime) processInboundEvent(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (ConnectorRuntimeResult, error) {
	platform := adapter.Name()
	connectorRuntime.logger.Info(
		"connector."+platform+".ingress.received",
		slog.String("source", event.Source),
		slog.String("eventID", event.EventID),
		slog.String("messageID", event.MessageID),
		slog.String("channelID", event.ConversationID),
		slog.String("rootID", event.RootMessageID),
		slog.String("userID", event.SenderUserID),
		slog.String("channelType", event.ChannelType),
	)

	botUserID, errorValue := connectorRuntime.resolveBotUserID(ctx, adapter)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".bot.lookup_failed", slog.String("error", errorValue.Error()))
	}
	if event.IsBotMessage || botUserID != "" && event.SenderUserID == botUserID {
		connectorRuntime.logger.Info("connector."+platform+".event.suppressed", slog.String("reason", "self"), slog.String("messageID", event.MessageID))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, Ignored: true, Reason: "self"}, nil
	}

	replyTarget, errorValue := connectorRuntime.buildReplyTarget(ctx, adapter, event)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}

	personID, isAllowed, errorValue := connectorRuntime.authorizeSender(ctx, adapter, event)
	if errorValue != nil {
		connectorRuntime.logger.Error("connector."+platform+".auth.failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{}, errorValue
	}
	if !isAllowed {
		connectorRuntime.logger.Info("connector."+platform+".auth.rejected", slog.String("messageID", event.MessageID), slog.String("reason", "not_invited"))
		dispatchID, sendError := adapter.SendReply(ctx, replyTarget, adapter.NotInvitedReply())
		if sendError != nil {
			connectorRuntime.logger.Error("connector."+platform+".outbound.failed", slog.String("messageID", event.MessageID), slog.String("error", sendError.Error()))
			return ConnectorRuntimeResult{Handled: true, Platform: platform, Reason: "not_invited"}, nil
		}
		connectorRuntime.logger.Info("connector."+platform+".outbound.sent", slog.String("messageID", event.MessageID), slog.String("replyDispatchID", dispatchID))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, Reason: "not_invited", ReplyDispatchID: dispatchID}, nil
	}

	connectorRuntime.logger.Info("connector."+platform+".auth.allowed", slog.String("messageID", event.MessageID), slog.String("personID", personID))
	taskRun, errorValue := connectorRuntime.agentKernel.HandleInboundMessage(personID, event.ConversationID, event.Text)
	if errorValue != nil {
		connectorRuntime.logger.Error("connector."+platform+".task.failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{}, errorValue
	}

	connectorRuntime.logger.Info("connector."+platform+".task.created", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRun.TaskRunID))
	reply := "I am having trouble reaching the language model right now. I logged the failure so the model configuration can be fixed."
	stopTyping := connectorRuntime.startTyping(ctx, adapter, botUserID, replyTarget)
	defer stopTyping()

	connectorRuntime.logger.Info("connector."+platform+".llm.started", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRun.TaskRunID))
	generatedReply, errorValue := connectorRuntime.agentKernel.GenerateReply(ctx, event.Text)
	if errorValue == nil {
		reply = generatedReply
		connectorRuntime.logger.Info("connector."+platform+".llm.completed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRun.TaskRunID))
	} else {
		connectorRuntime.logger.Error("connector."+platform+".llm.failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRun.TaskRunID), slog.String("error", errorValue.Error()))
		connectorRuntime.logger.Warn("connector."+platform+".reply.fallback_used", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRun.TaskRunID))
	}

	dispatchID, errorValue := adapter.SendReply(ctx, replyTarget, reply)
	if errorValue != nil {
		connectorRuntime.logger.Error("connector."+platform+".outbound.failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRun.TaskRunID), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRun.TaskRunID, Reason: "reply_failed"}, nil
	}

	connectorRuntime.logger.Info("connector."+platform+".outbound.sent", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRun.TaskRunID), slog.String("replyDispatchID", dispatchID))
	return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRun.TaskRunID, ReplyDispatchID: dispatchID}, nil
}

func (connectorRuntime *ConnectorRuntime) authorizeSender(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (string, bool, error) {
	personID, isFound := connectorRuntime.identityService.ResolvePersonIDByPlatformAccount(adapter.Name(), event.SenderUserID)
	if isFound {
		return personID, true, nil
	}

	platformAccountIdentity, errorValue := adapter.ResolveIdentity(ctx, event.SenderUserID)
	if errorValue != nil {
		return "", false, errorValue
	}
	platformAccountIdentity.Platform = adapter.Name()
	platformAccountIdentity.ExternalUserID = event.SenderUserID
	connectorRuntime.identityService.RememberPlatformAccount(platformAccountIdentity)

	personID, isFound = connectorRuntime.identityService.ResolvePersonIDByPlatformAccount(adapter.Name(), event.SenderUserID)
	return personID, isFound, nil
}

func (connectorRuntime *ConnectorRuntime) buildReplyTarget(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (ReplyTarget, error) {
	conversationKind, errorValue := adapter.ResolveConversationKind(ctx, event)
	if errorValue != nil {
		return ReplyTarget{}, errorValue
	}

	parentID := event.RootMessageID
	if parentID == "" && !conversationKind.IsDirect {
		parentID = firstNonEmpty(event.ReplyParentID, event.MessageID)
	}

	return ReplyTarget{
		ConversationID: event.ConversationID,
		ParentID:       parentID,
		IsDirect:       conversationKind.IsDirect,
		DedupeKey:      event.DedupeKey(),
	}, nil
}

func (connectorRuntime *ConnectorRuntime) startTyping(ctx context.Context, adapter PlatformAdapter, botUserID string, replyTarget ReplyTarget) func() {
	platform := adapter.Name()
	if botUserID == "" {
		connectorRuntime.logger.Warn("connector."+platform+".typing.skipped", slog.String("reason", "missing_bot_user_id"), slog.String("conversationID", replyTarget.ConversationID))
		return func() {}
	}

	typingContext, cancel := context.WithCancel(ctx)
	connectorRuntime.logger.Info("connector."+platform+".typing.started", slog.String("conversationID", replyTarget.ConversationID), slog.String("parentID", replyTarget.ParentID))
	connectorRuntime.publishTyping(typingContext, adapter, botUserID, replyTarget)
	go connectorRuntime.publishTypingUntilDone(typingContext, adapter, botUserID, replyTarget)

	return func() {
		cancel()
		connectorRuntime.logger.Info("connector."+platform+".typing.stopped", slog.String("conversationID", replyTarget.ConversationID), slog.String("parentID", replyTarget.ParentID))
	}
}

func (connectorRuntime *ConnectorRuntime) publishTypingUntilDone(ctx context.Context, adapter PlatformAdapter, botUserID string, replyTarget ReplyTarget) {
	typingInterval := connectorRuntime.typingInterval
	if typingInterval <= 0 {
		typingInterval = 4 * time.Second
	}
	typingTimeout := connectorRuntime.typingTimeout
	if typingTimeout <= 0 {
		typingTimeout = 90 * time.Second
	}

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
			connectorRuntime.publishTyping(ctx, adapter, botUserID, replyTarget)
		}
	}
}

func (connectorRuntime *ConnectorRuntime) publishTyping(ctx context.Context, adapter PlatformAdapter, botUserID string, replyTarget ReplyTarget) {
	errorValue := adapter.PublishTyping(ctx, botUserID, replyTarget)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".typing.failed", slog.String("conversationID", replyTarget.ConversationID), slog.String("parentID", replyTarget.ParentID), slog.String("error", errorValue.Error()))
	}
}

func (connectorRuntime *ConnectorRuntime) resolveBotUserID(ctx context.Context, adapter PlatformAdapter) (string, error) {
	connectorRuntime.mutex.Lock()
	botUserID := connectorRuntime.botUserByPlatform[adapter.Name()]
	connectorRuntime.mutex.Unlock()
	if botUserID != "" {
		return botUserID, nil
	}

	resolvedBotUserID, errorValue := adapter.ResolveBotUserID(ctx)
	if errorValue != nil {
		return "", errorValue
	}

	connectorRuntime.mutex.Lock()
	connectorRuntime.botUserByPlatform[adapter.Name()] = resolvedBotUserID
	connectorRuntime.mutex.Unlock()
	return resolvedBotUserID, nil
}

func (connectorRuntime *ConnectorRuntime) findAdapter(platform string) (PlatformAdapter, error) {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()

	adapter, isFound := connectorRuntime.adapterByPlatform[platform]
	if !isFound {
		return nil, errors.New("connector adapter not registered: " + platform)
	}
	return adapter, nil
}

func (connectorRuntime *ConnectorRuntime) findProcessedResult(eventKey string) (ConnectorRuntimeResult, bool) {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()

	result, isFound := connectorRuntime.processedResults[eventKey]
	return result, isFound
}

func (connectorRuntime *ConnectorRuntime) rememberProcessedResult(eventKey string, result ConnectorRuntimeResult) {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()

	connectorRuntime.processedResults[eventKey] = result
}

func (event PlatformInboundEvent) DedupeKey() string {
	messageID := strings.TrimSpace(event.MessageID)
	if messageID == "" {
		messageID = strings.TrimSpace(event.EventID)
	}
	conversationID := strings.TrimSpace(event.ConversationID)
	if conversationID == "" {
		return event.Platform + ":" + messageID
	}
	return event.Platform + ":" + conversationID + ":" + messageID
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
