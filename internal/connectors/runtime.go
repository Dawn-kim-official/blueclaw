package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/agentruntime"
	"blueclaw/internal/capability"
	"blueclaw/internal/identity"
	"blueclaw/internal/mcp"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
	"blueclaw/internal/security"
	"blueclaw/internal/task"
)

type IngressGate interface {
	IsPaused() bool
}

type ConnectorEventRepository interface {
	TryInsertConnectorEvent(PlatformInboundEvent) (bool, ConnectorRuntimeResult, error)
	SaveConnectorResult(PlatformInboundEvent, ConnectorRuntimeResult) error
}

type ConnectorQueueRepository interface {
	TryEnqueueConnectorEvent(PlatformInboundEvent) (bool, ConnectorRuntimeResult, error)
	ClaimPendingConnectorEvents(int, time.Duration) ([]QueuedConnectorEvent, error)
	MarkConnectorEventSucceeded(PlatformInboundEvent, ConnectorRuntimeResult) error
	MarkConnectorEventFailed(QueuedConnectorEvent, error, time.Time) error
}

type ConnectorOutboxRepository interface {
	EnqueueConnectorReply(PlatformInboundEvent, ReplyTarget, OutboundReply) (string, error)
	ClaimPendingConnectorReplies(int, time.Duration) ([]QueuedConnectorReply, error)
	MarkConnectorReplySent(QueuedConnectorReply, string) error
	MarkConnectorReplyFailed(QueuedConnectorReply, error, time.Time) error
}

type PlatformInboundEvent struct {
	Platform         string                 `json:"-"`
	Source           string                 `json:"-"`
	ConversationID   string                 `json:"conversationID"`
	MessageID        string                 `json:"messageID"`
	SenderID         string                 `json:"senderID"`
	ReplyTargetID    string                 `json:"replyTargetID"`
	Prompt           string                 `json:"prompt"`
	ResponseLanguage string                 `json:"responseLanguage,omitempty"`
	Context          VisibleContext         `json:"context"`
	RawReceivedAt    time.Time              `json:"-"`
	LegacyFields     map[string]interface{} `json:"-"`
}

type ReplyTarget struct {
	ConversationID string `json:"conversationID"`
	ReplyTargetID  string `json:"replyTargetID"`
	DedupeKey      string `json:"dedupeKey"`
}

type OutboundReply struct {
	Message         string                 `json:"message"`
	RawEventID      string                 `json:"rawEventID,omitempty"`
	OutboxID        string                 `json:"outboxID,omitempty"`
	Attachments     []agent.FileAttachment `json:"attachments,omitempty"`
	RecoveryActions []agent.RecoveryAction `json:"recoveryActions,omitempty"`
}

type outboundReplyDocument struct {
	Message         string                    `json:"message"`
	RawEventID      string                    `json:"rawEventID,omitempty"`
	OutboxID        string                    `json:"outboxID,omitempty"`
	Attachments     []outboundReplyAttachment `json:"attachments,omitempty"`
	RecoveryActions []agent.RecoveryAction    `json:"recoveryActions,omitempty"`
}

type outboundReplyAttachment struct {
	DevicePath    string `json:"devicePath"`
	Filename      string `json:"filename,omitempty"`
	ContentType   string `json:"contentType,omitempty"`
	SizeBytes     int64  `json:"sizeBytes,omitempty"`
	Title         string `json:"title,omitempty"`
	ContentBase64 string `json:"contentBase64,omitempty"`
}

func (reply OutboundReply) MarshalJSON() ([]byte, error) {
	document := outboundReplyDocument{
		Message:         reply.Message,
		RawEventID:      reply.RawEventID,
		OutboxID:        reply.OutboxID,
		Attachments:     outboundReplyAttachments(reply.Attachments),
		RecoveryActions: reply.RecoveryActions,
	}
	return json.Marshal(document)
}

func (reply *OutboundReply) UnmarshalJSON(documentBytes []byte) error {
	var document outboundReplyDocument
	if errorValue := json.Unmarshal(documentBytes, &document); errorValue != nil {
		return errorValue
	}
	reply.Message = document.Message
	reply.RawEventID = document.RawEventID
	reply.OutboxID = document.OutboxID
	reply.Attachments = fileAttachmentsFromOutboundReplyAttachments(document.Attachments)
	reply.RecoveryActions = append([]agent.RecoveryAction{}, document.RecoveryActions...)
	return nil
}

func outboundReplyAttachments(attachments []agent.FileAttachment) []outboundReplyAttachment {
	replyAttachments := []outboundReplyAttachment{}
	for _, attachment := range attachments {
		replyAttachments = append(replyAttachments, outboundReplyAttachment{
			DevicePath:    attachment.DevicePath,
			Filename:      attachment.Filename,
			ContentType:   attachment.ContentType,
			SizeBytes:     attachment.SizeBytes,
			Title:         attachment.Title,
			ContentBase64: attachment.ContentBase64,
		})
	}
	return replyAttachments
}

func fileAttachmentsFromOutboundReplyAttachments(attachments []outboundReplyAttachment) []agent.FileAttachment {
	fileAttachments := []agent.FileAttachment{}
	for _, attachment := range attachments {
		fileAttachments = append(fileAttachments, agent.FileAttachment{
			DevicePath:    attachment.DevicePath,
			Filename:      attachment.Filename,
			ContentType:   attachment.ContentType,
			SizeBytes:     attachment.SizeBytes,
			Title:         attachment.Title,
			ContentBase64: attachment.ContentBase64,
		})
	}
	return fileAttachments
}

type QueuedConnectorEvent struct {
	Event        PlatformInboundEvent
	AttemptCount int
}

type QueuedConnectorReply struct {
	OutboxID     string
	RawEventID   string
	Platform     string
	ReplyTarget  ReplyTarget
	Reply        OutboundReply
	AttemptCount int
}

type VisibleContext struct {
	Messages         []VisibleContextMessage `json:"messages"`
	HasMoreBefore    bool                    `json:"hasMoreBefore"`
	HistoryCursor    string                  `json:"historyCursor"`
	ResponseLanguage string                  `json:"responseLanguage,omitempty"`
	Sender           VisibleContextSender    `json:"sender,omitempty"`
	ConversationType string                  `json:"conversationType,omitempty"`
	ChannelID        string                  `json:"channelID,omitempty"`
	ChannelName      string                  `json:"channelName,omitempty"`
}

type VisibleContextSender struct {
	Platform    string `json:"platform,omitempty"`
	SenderID    string `json:"senderID,omitempty"`
	Handle      string `json:"handle,omitempty"`
	Email       string `json:"email,omitempty"`
	Name        string `json:"name,omitempty"`
	CallingName string `json:"callingName,omitempty"`
}

type VisibleContextMessage struct {
	Speaker            string `json:"speaker"`
	SpeakerCallingName string `json:"speakerCallingName,omitempty"`
	SpeakerHandle      string `json:"speakerHandle,omitempty"`
	Text               string `json:"text"`
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
	StartProgress(context.Context, ReplyTarget) error
	StopProgress(context.Context, ReplyTarget) error
	SendReply(context.Context, ReplyTarget, OutboundReply) (string, error)
	FetchHistory(context.Context, string, int) (VisibleContext, error)
	NotInvitedReply() string
}

type ConnectorTransport interface {
	Name() string
	Platform() string
	Start(context.Context)
}

const connectorInboxWorkerCount = 4
const connectorOutboxWorkerCount = 2
const connectorWorkerIdleDelay = time.Second
const connectorClaimLeaseDuration = 15 * time.Minute
const connectorProgressHeartbeatInterval = time.Minute
const connectorMaximumAttemptCount = 5

type ConnectorRuntime struct {
	identityService    *identity.IdentityService
	agentKernel        *agent.AgentKernel
	taskLauncher       *agentruntime.TaskLauncher
	toolCatalogBuilder *agentruntime.ToolCatalogBuilder
	memoryService      *memory.MemoryService
	memoryRouter       *memory.GraphitiIngestionRouter
	workspaceID        string
	logger             *slog.Logger

	mutex             sync.Mutex
	adapterByPlatform map[string]PlatformAdapter
	processedResults  map[string]ConnectorRuntimeResult
	eventRepository   ConnectorEventRepository
	ingressGate       IngressGate
	conversationLocks map[string]*sync.Mutex
	started           bool
	inboxHeartbeats   []time.Time
	outboxHeartbeats  []time.Time
}

type ConnectorRuntimeHealth struct {
	Started                     bool      `json:"started"`
	HasEventRepository          bool      `json:"hasEventRepository"`
	HasQueueRepository          bool      `json:"hasQueueRepository"`
	HasOutboxRepository         bool      `json:"hasOutboxRepository"`
	RegisteredPlatforms         []string  `json:"registeredPlatforms"`
	MattermostAdapterRegistered bool      `json:"mattermostAdapterRegistered"`
	InboxWorkerCount            int       `json:"inboxWorkerCount"`
	OutboxWorkerCount           int       `json:"outboxWorkerCount"`
	InboxWorkersAlive           bool      `json:"inboxWorkersAlive"`
	OutboxWorkersAlive          bool      `json:"outboxWorkersAlive"`
	LastInboxHeartbeatAt        time.Time `json:"lastInboxHeartbeatAt,omitempty"`
	LastOutboxHeartbeatAt       time.Time `json:"lastOutboxHeartbeatAt,omitempty"`
	Passed                      bool      `json:"passed"`
}

type pendingApproval struct {
	TaskRun          task.TaskRun
	IntentPrompt     string
	ApprovalQuestion string
	ResponseLanguage string
}

func NewConnectorRuntime(identityService *identity.IdentityService, agentKernel *agent.AgentKernel, logger *slog.Logger) *ConnectorRuntime {
	if logger == nil {
		logger = slog.Default()
	}
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"conversation.history", "memory.search", "math.calculate", "terminal.run", "terminal.session", "browser_handoff.openURL", "approval.request", "file.write", "file.attach", "schedule.create", "schedule.cancel"})

	return &ConnectorRuntime{
		identityService:    identityService,
		agentKernel:        agentKernel,
		toolCatalogBuilder: toolCatalogBuilder,
		logger:             logger,
		adapterByPlatform:  map[string]PlatformAdapter{},
		processedResults:   map[string]ConnectorRuntimeResult{},
		conversationLocks:  map[string]*sync.Mutex{},
	}
}

func (connectorRuntime *ConnectorRuntime) RegisterAdapter(adapter PlatformAdapter) {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()

	connectorRuntime.adapterByPlatform[adapter.Name()] = adapter
}

func (connectorRuntime *ConnectorRuntime) UseMemoryService(memoryService *memory.MemoryService) {
	connectorRuntime.memoryService = memoryService
	connectorRuntime.toolCatalogBuilder.UseMemoryService(memoryService)
}

func (connectorRuntime *ConnectorRuntime) UseGraphitiIngestionRouter(memoryRouter *memory.GraphitiIngestionRouter) {
	connectorRuntime.memoryRouter = memoryRouter
}

func (connectorRuntime *ConnectorRuntime) UseWorkspaceID(workspaceID string) {
	connectorRuntime.workspaceID = strings.TrimSpace(workspaceID)
}

func (connectorRuntime *ConnectorRuntime) UseWorkspaceRootPath(workspaceRootPath string) {
	connectorRuntime.toolCatalogBuilder.UseWorkspaceRootPath(workspaceRootPath)
}

func (connectorRuntime *ConnectorRuntime) UseTerminalService(terminalService *security.TerminalSessionService) {
	connectorRuntime.toolCatalogBuilder.UseTerminalService(terminalService)
}

func (connectorRuntime *ConnectorRuntime) UseTaskScheduleRepository(taskScheduleRepository task.TaskScheduleRepository) {
	connectorRuntime.toolCatalogBuilder.UseTaskScheduleRepository(taskScheduleRepository)
}

func (connectorRuntime *ConnectorRuntime) UseTaskWaitTokenRepository(taskWaitTokenRepository task.TaskWaitTokenRepository) {
	connectorRuntime.toolCatalogBuilder.UseTaskWaitTokenRepository(taskWaitTokenRepository)
}

func (connectorRuntime *ConnectorRuntime) UseTaskRunService(taskRunService *task.TaskRunService) {
	connectorRuntime.toolCatalogBuilder.UseTaskRunService(taskRunService)
}

func (connectorRuntime *ConnectorRuntime) UseEventRepository(eventRepository ConnectorEventRepository) {
	connectorRuntime.eventRepository = eventRepository
}

func (connectorRuntime *ConnectorRuntime) UseIngressGate(ingressGate IngressGate) {
	connectorRuntime.ingressGate = ingressGate
}

func (connectorRuntime *ConnectorRuntime) UseMCPRegistry(mcpRegistry *mcp.McpRegistry) {
	connectorRuntime.toolCatalogBuilder.UseMCPRegistry(mcpRegistry)
}

func (connectorRuntime *ConnectorRuntime) UseCapabilityTools(capabilityClient capability.Client, toolNames []string) {
	connectorRuntime.toolCatalogBuilder.UseCapabilityTools(capabilityClient, toolNames)
}

func (connectorRuntime *ConnectorRuntime) UseAllowedToolNames(allowedToolNames []string) {
	trimmedToolNames := trimNonEmptyStrings(allowedToolNames)
	if len(trimmedToolNames) == 0 {
		trimmedToolNames = []string{"conversation.history", "memory.search", "math.calculate", "terminal.run", "terminal.session", "browser_handoff.openURL", "approval.request", "file.write", "file.attach", "schedule.create", "schedule.cancel"}
	}
	connectorRuntime.toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, trimmedToolNames)
}

func (connectorRuntime *ConnectorRuntime) UseAllowedToolNamesByProfile(allowedToolNamesByProfile map[string][]string, fallbackAllowedToolNames []string) {
	connectorRuntime.toolCatalogBuilder.UseAllowedToolNamesByProfile(allowedToolNamesByProfile, fallbackAllowedToolNames)
}

func (connectorRuntime *ConnectorRuntime) UseTaskLauncher(taskLauncher *agentruntime.TaskLauncher) {
	connectorRuntime.taskLauncher = taskLauncher
}

func (connectorRuntime *ConnectorRuntime) Start(ctx context.Context) {
	if connectorRuntime.queueRepository() != nil {
		connectorRuntime.prepareConnectorWorkers("inbox", connectorInboxWorkerCount)
		for index := 0; index < connectorInboxWorkerCount; index++ {
			go connectorRuntime.runConnectorInboxWorker(ctx, index)
		}
	}
	if connectorRuntime.outboxRepository() != nil {
		connectorRuntime.prepareConnectorWorkers("outbox", connectorOutboxWorkerCount)
		for index := 0; index < connectorOutboxWorkerCount; index++ {
			go connectorRuntime.runConnectorOutboxWorker(ctx, index)
		}
	}
	connectorRuntime.mutex.Lock()
	connectorRuntime.started = true
	connectorRuntime.mutex.Unlock()
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
	result, errorValue := connectorRuntime.HandleInboundEvent(detachedConnectorContext(ctx), adapter, parseResult.Event)
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
	event.Platform = adapter.Name()
	if connectorRuntime.ingressGate != nil && connectorRuntime.ingressGate.IsPaused() {
		return ConnectorRuntimeResult{Handled: true, Platform: adapter.Name(), Ignored: true, Reason: "backup_prepare_active"}, nil
	}
	if strings.TrimSpace(event.MessageID) == "" {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".ingress.malformed", slog.String("source", event.Source), slog.String("reason", "missing_message_id"))
		return ConnectorRuntimeResult{Handled: true, Platform: adapter.Name(), Ignored: true, Reason: "missing_message_id"}, nil
	}
	if strings.TrimSpace(event.ConversationID) == "" {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".ingress.malformed", slog.String("source", event.Source), slog.String("reason", "missing_conversation_id"))
		return ConnectorRuntimeResult{Handled: true, Platform: adapter.Name(), Ignored: true, Reason: "missing_conversation_id"}, nil
	}
	if strings.TrimSpace(event.SenderID) == "" {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".ingress.malformed", slog.String("source", event.Source), slog.String("reason", "missing_sender_id"))
		return ConnectorRuntimeResult{Handled: true, Platform: adapter.Name(), Ignored: true, Reason: "missing_sender_id"}, nil
	}
	if strings.TrimSpace(event.ReplyTargetID) == "" {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".ingress.malformed", slog.String("source", event.Source), slog.String("reason", "missing_reply_target_id"))
		return ConnectorRuntimeResult{Handled: true, Platform: adapter.Name(), Ignored: true, Reason: "missing_reply_target_id"}, nil
	}
	if strings.TrimSpace(event.Prompt) == "" {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".ingress.malformed", slog.String("source", event.Source), slog.String("reason", "missing_prompt"))
		return ConnectorRuntimeResult{Handled: true, Platform: adapter.Name(), Ignored: true, Reason: "missing_prompt"}, nil
	}
	if event.Context.HasMoreBefore && strings.TrimSpace(event.Context.HistoryCursor) == "" {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".ingress.malformed", slog.String("source", event.Source), slog.String("reason", "missing_history_cursor"))
		return ConnectorRuntimeResult{Handled: true, Platform: adapter.Name(), Ignored: true, Reason: "missing_history_cursor"}, nil
	}

	if queueRepository := connectorRuntime.queueRepository(); queueRepository != nil {
		return connectorRuntime.enqueueInboundEvent(event, queueRepository)
	}
	if connectorRuntime.eventRepository != nil {
		return ConnectorRuntimeResult{}, errors.New("connector queue repository is required when connector event repository is configured")
	}

	return connectorRuntime.handleInboundEventImmediately(ctx, adapter, event)
}

func (connectorRuntime *ConnectorRuntime) handleInboundEventImmediately(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (ConnectorRuntimeResult, error) {
	eventKey := event.DedupeKey()
	if connectorRuntime.eventRepository != nil {
		isDuplicate, result, errorValue := connectorRuntime.eventRepository.TryInsertConnectorEvent(event)
		if errorValue != nil {
			return ConnectorRuntimeResult{}, errorValue
		}
		if isDuplicate {
			result.Handled = true
			result.Platform = adapter.Name()
			result.Duplicate = true
			connectorRuntime.logger.Info("connector."+adapter.Name()+".event.suppressed", slog.String("source", event.Source), slog.String("reason", "duplicate"), slog.String("messageID", event.MessageID))
			return result, nil
		}
		result, errorValue = connectorRuntime.processInboundEvent(ctx, adapter, event)
		if errorValue != nil {
			return ConnectorRuntimeResult{}, errorValue
		}
		_ = connectorRuntime.eventRepository.SaveConnectorResult(event, result)
		return result, nil
	}
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

func (connectorRuntime *ConnectorRuntime) enqueueInboundEvent(event PlatformInboundEvent, queueRepository ConnectorQueueRepository) (ConnectorRuntimeResult, error) {
	isDuplicate, result, errorValue := queueRepository.TryEnqueueConnectorEvent(event)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}
	if isDuplicate {
		result.Handled = true
		result.Platform = event.Platform
		result.Duplicate = true
		connectorRuntime.logger.Info("connector."+event.Platform+".event.suppressed", slog.String("source", event.Source), slog.String("reason", "duplicate"), slog.String("messageID", event.MessageID))
		return result, nil
	}
	return ConnectorRuntimeResult{Handled: true, Platform: event.Platform, Reason: "queued"}, nil
}

func (connectorRuntime *ConnectorRuntime) runConnectorInboxWorker(ctx context.Context, workerIndex int) {
	workerContext, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go connectorRuntime.recordConnectorWorkerHeartbeatUntilStopped(workerContext, "inbox", workerIndex)
	for ctx.Err() == nil {
		connectorRuntime.recordConnectorWorkerHeartbeat("inbox", workerIndex)
		if connectorRuntime.processNextQueuedConnectorEvent(ctx) {
			continue
		}
		sleepConnectorWorker(ctx)
	}
}

func (connectorRuntime *ConnectorRuntime) processNextQueuedConnectorEvent(ctx context.Context) bool {
	queueRepository := connectorRuntime.queueRepository()
	if queueRepository == nil {
		return false
	}
	queuedEvents, errorValue := queueRepository.ClaimPendingConnectorEvents(1, connectorClaimLeaseDuration)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector.inbox.claim_failed", slog.String("error", errorValue.Error()))
		return false
	}
	if len(queuedEvents) == 0 {
		return false
	}
	connectorRuntime.processQueuedConnectorEvent(ctx, queuedEvents[0])
	return true
}

func (connectorRuntime *ConnectorRuntime) processQueuedConnectorEvent(ctx context.Context, queuedEvent QueuedConnectorEvent) {
	event := queuedEvent.Event
	adapter, errorValue := connectorRuntime.findAdapter(event.Platform)
	if errorValue != nil {
		connectorRuntime.markQueuedConnectorEventFailed(queuedEvent, errorValue)
		return
	}
	lock := connectorRuntime.conversationLock(event.Platform + ":" + event.ConversationID)
	lock.Lock()
	defer lock.Unlock()
	if ctx.Err() != nil {
		return
	}
	result, errorValue := connectorRuntime.processInboundEventWithReplySender(ctx, adapter, event, connectorRuntime.enqueueConnectorReply)
	if ctx.Err() != nil {
		return
	}
	if errorValue != nil {
		connectorRuntime.markQueuedConnectorEventFailed(queuedEvent, errorValue)
		return
	}
	if errorValue := connectorRuntime.queueRepository().MarkConnectorEventSucceeded(event, result); errorValue != nil {
		connectorRuntime.logger.Warn("connector."+event.Platform+".inbox.mark_succeeded_failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
	}
}

func (connectorRuntime *ConnectorRuntime) enqueueConnectorReply(ctx context.Context, replyTarget ReplyTarget, reply OutboundReply) (string, error) {
	event, isFound := connectorEventFromContext(ctx)
	if !isFound {
		return "", errors.New("connector event context is missing")
	}
	outboxRepository := connectorRuntime.outboxRepository()
	if outboxRepository == nil {
		if connectorRuntime.eventRepository != nil {
			return "", errors.New("connector outbox repository is required when connector event repository is configured")
		}
		adapter, errorValue := connectorRuntime.findAdapter(event.Platform)
		if errorValue != nil {
			return "", errorValue
		}
		return adapter.SendReply(ctx, replyTarget, reply)
	}
	return outboxRepository.EnqueueConnectorReply(event, replyTarget, reply)
}

func (connectorRuntime *ConnectorRuntime) runConnectorOutboxWorker(ctx context.Context, workerIndex int) {
	workerContext, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go connectorRuntime.recordConnectorWorkerHeartbeatUntilStopped(workerContext, "outbox", workerIndex)
	for ctx.Err() == nil {
		connectorRuntime.recordConnectorWorkerHeartbeat("outbox", workerIndex)
		if connectorRuntime.processNextQueuedConnectorReply(ctx) {
			continue
		}
		sleepConnectorWorker(ctx)
	}
}

func (connectorRuntime *ConnectorRuntime) processNextQueuedConnectorReply(ctx context.Context) bool {
	outboxRepository := connectorRuntime.outboxRepository()
	if outboxRepository == nil {
		return false
	}
	queuedReplies, errorValue := outboxRepository.ClaimPendingConnectorReplies(1, connectorClaimLeaseDuration)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector.outbox.claim_failed", slog.String("error", errorValue.Error()))
		return false
	}
	if len(queuedReplies) == 0 {
		return false
	}
	connectorRuntime.processQueuedConnectorReply(ctx, queuedReplies[0])
	return true
}

func (connectorRuntime *ConnectorRuntime) processQueuedConnectorReply(ctx context.Context, queuedReply QueuedConnectorReply) {
	adapter, errorValue := connectorRuntime.findAdapter(queuedReply.Platform)
	if errorValue != nil {
		connectorRuntime.markQueuedConnectorReplyFailed(queuedReply, errorValue)
		return
	}
	queuedReply.Reply.RawEventID = firstNonEmptyString(queuedReply.Reply.RawEventID, queuedReply.RawEventID)
	queuedReply.Reply.OutboxID = firstNonEmptyString(queuedReply.Reply.OutboxID, queuedReply.OutboxID)
	dispatchID, errorValue := adapter.SendReply(ctx, queuedReply.ReplyTarget, queuedReply.Reply)
	if ctx.Err() != nil {
		return
	}
	if errorValue != nil {
		connectorRuntime.markQueuedConnectorReplyFailed(queuedReply, errorValue)
		return
	}
	if errorValue := connectorRuntime.outboxRepository().MarkConnectorReplySent(queuedReply, dispatchID); errorValue != nil {
		connectorRuntime.logger.Warn("connector."+queuedReply.Platform+".outbox.mark_sent_failed", slog.String("outboxID", queuedReply.OutboxID), slog.String("error", errorValue.Error()))
	}
}

func (connectorRuntime *ConnectorRuntime) processInboundEvent(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (ConnectorRuntimeResult, error) {
	return connectorRuntime.processInboundEventWithReplySender(ctx, adapter, event, adapter.SendReply)
}

func (connectorRuntime *ConnectorRuntime) processInboundEventWithReplySender(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) (ConnectorRuntimeResult, error) {
	ctx = withConnectorEvent(ctx, event)
	platform := adapter.Name()
	connectorRuntime.logger.Info(
		"connector."+platform+".ingress.received",
		slog.String("source", event.Source),
		slog.String("messageID", event.MessageID),
		slog.String("conversationID", event.ConversationID),
		slog.String("senderID", event.SenderID),
		slog.String("replyTargetID", event.ReplyTargetID),
		slog.Bool("hasMoreBefore", event.Context.HasMoreBefore),
	)

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
		dispatchID, sendError := sendReply(ctx, replyTarget, OutboundReply{Message: adapter.NotInvitedReply()})
		if sendError != nil {
			connectorRuntime.logger.Error("connector."+platform+".outbound.failed", slog.String("messageID", event.MessageID), slog.String("error", sendError.Error()))
			return ConnectorRuntimeResult{Handled: true, Platform: platform, Reason: "not_invited"}, nil
		}
		connectorRuntime.logger.Info("connector."+platform+".outbound.sent", slog.String("messageID", event.MessageID), slog.String("replyDispatchID", dispatchID))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, Reason: "not_invited", ReplyDispatchID: dispatchID}, nil
	}

	connectorRuntime.logger.Info("connector."+platform+".auth.allowed", slog.String("messageID", event.MessageID), slog.String("personID", personID))
	personAccess := connectorRuntime.identityService.ResolvePersonAccess(personID)
	pendingApproval, isApprovalContinuation := connectorRuntime.resolveApprovalContinuation(ctx, platform, personID, event)
	if isApprovalContinuation {
		event = approvedContinuationEvent(event, pendingApproval)
	}
	stopProgress := connectorRuntime.startProgressHeartbeat(ctx, adapter, replyTarget)
	defer stopProgress()

	connectorRuntime.logger.Info("connector."+platform+".agent.started", slog.String("messageID", event.MessageID))
	launchResult, errorValue := connectorRuntime.currentTaskLauncher().Launch(ctx, agentruntime.TaskLaunchRequest{
		Source:                    agentruntime.TaskLaunchSourceConnector,
		SourceReference:           event.DedupeKey(),
		RequesterPersonID:         personID,
		RequesterName:             event.Context.Sender.Name,
		RequesterCallingName:      event.Context.Sender.CallingName,
		RequesterHandle:           event.Context.Sender.Handle,
		RequesterEmail:            connectorRuntime.identityService.ResolvePersonPrimaryEmail(personID),
		RequesterPlatformUserID:   event.SenderID,
		IsApprovalContinuation:    isApprovalContinuation,
		ProfileName:               "default",
		Platform:                  platform,
		ConversationID:            event.ConversationID,
		ConversationType:          event.Context.ConversationType,
		ConversationChannelID:     event.Context.ChannelID,
		ConversationChannelName:   event.Context.ChannelName,
		ReplyTargetID:             event.ReplyTargetID,
		Prompt:                    event.Prompt,
		ResponseLanguage:          responseLanguageForEvent(event),
		VisibleContext:            event.Context.ToAgentVisibleContext(),
		HistoryProvider:           connectorHistoryProvider{adapter: adapter},
		PersonAccess:              personAccess,
		MemoryNamespaces:          connectorRuntime.accessibleNamespaces(personID, personAccess, event),
		AccessibleConversationIDs: []string{event.ConversationID},
	})
	if errorValue != nil {
		connectorRuntime.logger.Error("connector."+platform+".agent.failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{}, errorValue
	}
	turnResult := launchResult.TurnResult
	taskRunID := turnResult.TaskRun.TaskRunID
	if isApprovalContinuation {
		connectorRuntime.completeApprovedPendingTask(pendingApproval.TaskRun, taskRunID, turnResult.FinalReply)
	}
	connectorRuntime.logger.Info("connector."+platform+".agent.completed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID))
	connectorRuntime.ingestMemory(ctx, platform, personID, personAccess, event, taskRunID)
	if turnResult.TaskRun.Status != task.TaskStatusCompleted {
		dispatchID, isSent := connectorRuntime.sendIncompleteTaskReply(ctx, platform, event, taskRunID, replyTarget, turnResult, sendReply)
		if isSent {
			return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, Reason: "task_not_completed", ReplyDispatchID: dispatchID}, nil
		}
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, Reason: "task_not_completed"}, nil
	}
	if agent.FinalReplyContainsNonDeliverableArtifactLocator(turnResult.FinalReply) {
		connectorRuntime.agentKernel.AppendTaskEvent(taskRunID, "connector.outbox.blocked", "reply exposes non-deliverable artifact locator")
		connectorRuntime.logger.Warn("connector."+platform+".outbound.blocked", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("reason", "non_deliverable_artifact_locator"))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, Reason: "non_deliverable_artifact_locator"}, nil
	}
	if agent.FinalReplyClaimsAttachmentDelivery(turnResult.FinalReply) && len(turnResult.Attachments) == 0 {
		connectorRuntime.agentKernel.AppendTaskEvent(taskRunID, "connector.outbox.blocked", "reply claims attachments without evidence")
		connectorRuntime.logger.Warn("connector."+platform+".outbound.blocked", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("reason", "missing_attachment_evidence"))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, Reason: "missing_attachment_evidence"}, nil
	}

	dispatchID, errorValue := sendReply(ctx, replyTarget, OutboundReply{
		Message:         turnResult.FinalReply,
		Attachments:     turnResult.Attachments,
		RecoveryActions: recoveryActionsForEvent(turnResult.RecoveryActions, event),
	})
	if errorValue != nil {
		connectorRuntime.logger.Error("connector."+platform+".outbound.failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, Reason: "reply_failed"}, nil
	}

	connectorRuntime.logger.Info("connector."+platform+".outbound.sent", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("replyDispatchID", dispatchID))
	return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, ReplyDispatchID: dispatchID}, nil
}

func (connectorRuntime *ConnectorRuntime) resolveApprovalContinuation(ctx context.Context, platform string, personID string, event PlatformInboundEvent) (pendingApproval, bool) {
	approval, isFound := connectorRuntime.findPendingApproval(personID, event.ConversationID)
	if !isFound {
		return pendingApproval{}, false
	}
	decision, errorValue := connectorRuntime.agentKernel.ClassifyApprovalReply(ctx, approval.IntentPrompt, approval.ApprovalQuestion, event.Prompt)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".approval.classify_failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", approval.TaskRun.TaskRunID), slog.String("error", errorValue.Error()))
		return pendingApproval{}, false
	}
	connectorRuntime.agentKernel.AppendTaskEvent(approval.TaskRun.TaskRunID, "approval.reply_classified", marshalConnectorEventBody(map[string]any{
		"messageID":   event.MessageID,
		"isApproval":  decision.IsApproval,
		"reason":      decision.Reason,
		"replyPrompt": strings.TrimSpace(event.Prompt),
	}))
	if !decision.IsApproval {
		return pendingApproval{}, false
	}
	connectorRuntime.logger.Info("connector."+platform+".approval.accepted", slog.String("messageID", event.MessageID), slog.String("taskRunID", approval.TaskRun.TaskRunID))
	return approval, true
}

func (connectorRuntime *ConnectorRuntime) findPendingApproval(personID string, conversationID string) (pendingApproval, bool) {
	taskRuns := connectorRuntime.agentKernel.ListTaskRunByPersonID(personID)
	var selectedTaskRun task.TaskRun
	isSelected := false
	for _, taskRun := range taskRuns {
		if taskRun.Status != task.TaskStatusWaitingApproval {
			continue
		}
		if taskRun.OriginConversationID != conversationID {
			continue
		}
		if time.Since(taskRun.UpdatedAt) > 24*time.Hour {
			continue
		}
		if isSelected && !taskRun.UpdatedAt.After(selectedTaskRun.UpdatedAt) {
			continue
		}
		selectedTaskRun = taskRun
		isSelected = true
	}
	if !isSelected {
		return pendingApproval{}, false
	}
	taskEvents := connectorRuntime.agentKernel.ListTaskEvent(selectedTaskRun.TaskRunID)
	approvalQuestion := latestApprovalQuestion(taskEvents)
	responseLanguage := latestApprovalResponseLanguage(taskEvents)
	return pendingApproval{
		TaskRun:          selectedTaskRun,
		IntentPrompt:     pendingApprovalIntentPrompt(selectedTaskRun.Prompt, approvalQuestion),
		ApprovalQuestion: approvalQuestion,
		ResponseLanguage: responseLanguage,
	}, true
}

func approvedContinuationEvent(event PlatformInboundEvent, approval pendingApproval) PlatformInboundEvent {
	event.Prompt = approvedContinuationPrompt(approval.IntentPrompt, event.Prompt)
	event.ResponseLanguage = agent.ResolveResponseLanguage(event.ResponseLanguage, approval.ResponseLanguage)
	return event
}

func approvedContinuationPrompt(intentPrompt string, approvalReply string) string {
	lines := []string{
		strings.TrimSpace(intentPrompt),
		"",
		"The user has approved this pending action. Proceed now without calling approval.request again.",
		"Approval reply: " + strings.TrimSpace(approvalReply),
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func pendingApprovalIntentPrompt(taskPrompt string, approvalQuestion string) string {
	taskPrompt = strings.TrimSpace(taskPrompt)
	if shouldUseApprovalQuestionAsIntent(taskPrompt, approvalQuestion) {
		return approvalQuestion
	}
	return firstNonEmptyString(taskPrompt, approvalQuestion)
}

func shouldUseApprovalQuestionAsIntent(taskPrompt string, approvalQuestion string) bool {
	if strings.TrimSpace(approvalQuestion) == "" {
		return false
	}
	normalizedPrompt := strings.TrimSpace(strings.ToLower(taskPrompt))
	if normalizedPrompt == "" {
		return true
	}
	approvalReplies := map[string]bool{
		"ㅇ":        true,
		"응":        true,
		"네":        true,
		"예":        true,
		"그래":       true,
		"좋아":       true,
		"진행해":      true,
		"진행해줘":     true,
		"해":        true,
		"해줘":       true,
		"yes":      true,
		"y":        true,
		"ok":       true,
		"okay":     true,
		"go ahead": true,
	}
	return approvalReplies[normalizedPrompt]
}

func latestApprovalQuestion(taskEvents []task.TaskEvent) string {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name != "approval.requested" {
			continue
		}
		var approvalRequest struct {
			Message string `json:"message"`
			Reason  string `json:"reason"`
		}
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &approvalRequest); errorValue != nil {
			continue
		}
		question := firstNonEmptyString(approvalRequest.Message, approvalRequest.Reason)
		if strings.TrimSpace(question) != "" {
			return strings.TrimSpace(question)
		}
	}
	return ""
}

func latestApprovalResponseLanguage(taskEvents []task.TaskEvent) string {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name != "approval.requested" {
			continue
		}
		var approvalRequest struct {
			ResponseLanguage string `json:"responseLanguage"`
		}
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &approvalRequest); errorValue != nil {
			continue
		}
		if responseLanguage := agent.NormalizeResponseLanguage(approvalRequest.ResponseLanguage); responseLanguage != "" {
			return responseLanguage
		}
	}
	return ""
}

func (connectorRuntime *ConnectorRuntime) completeApprovedPendingTask(pendingTaskRun task.TaskRun, continuationTaskRunID string, finalReply string) {
	result := strings.TrimSpace(finalReply)
	if result == "" {
		result = "Approved and continued in task " + continuationTaskRunID + "."
	}
	connectorRuntime.agentKernel.AppendTaskEvent(pendingTaskRun.TaskRunID, "approval.continued", marshalConnectorEventBody(map[string]string{
		"continuationTaskRunID": continuationTaskRunID,
		"result":                result,
	}))
	_, _ = connectorRuntime.agentKernel.CompleteTask(pendingTaskRun.TaskRunID, result)
}

func marshalConnectorEventBody(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return fmt.Sprint(value)
	}
	return string(document)
}

func (connectorRuntime *ConnectorRuntime) sendIncompleteTaskReply(ctx context.Context, platform string, event PlatformInboundEvent, taskRunID string, replyTarget ReplyTarget, turnResult agent.AgentTurnResult, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) (string, bool) {
	reply := strings.TrimSpace(turnResult.FinalReply)
	if reply == "" {
		connectorRuntime.agentKernel.AppendTaskEvent(taskRunID, "connector.outbox.skipped_no_llm_reply", "task run is not completed and no LLM-generated reply is available")
		connectorRuntime.logger.Info("connector."+platform+".outbound.skipped", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("reason", "no_llm_reply"))
		return "", false
	}
	if agent.FinalReplyClaimsAttachmentDelivery(reply) || agent.ValidateFinalReplyDelivery(reply, nil, true) != nil {
		connectorRuntime.agentKernel.AppendTaskEvent(taskRunID, "connector.outbox.skipped_no_llm_reply", "LLM-generated incomplete task reply claimed unavailable artifact delivery")
		connectorRuntime.logger.Info("connector."+platform+".outbound.skipped", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("reason", "invalid_llm_reply"))
		return "", false
	}
	dispatchID, errorValue := sendReply(ctx, replyTarget, OutboundReply{Message: reply, RecoveryActions: recoveryActionsForEvent(turnResult.RecoveryActions, event)})
	if errorValue != nil {
		connectorRuntime.logger.Error("connector."+platform+".outbound.failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
		return "", false
	}
	connectorRuntime.agentKernel.AppendTaskEvent(taskRunID, "connector.outbox.enqueued_blocked", "incomplete task reply enqueued")
	connectorRuntime.logger.Info("connector."+platform+".outbound.sent", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("replyDispatchID", dispatchID), slog.String("reason", "task_not_completed"))
	return dispatchID, true
}

func recoveryActionsForEvent(recoveryActions []agent.RecoveryAction, event PlatformInboundEvent) []agent.RecoveryAction {
	enrichedRecoveryActions := []agent.RecoveryAction{}
	for _, recoveryAction := range recoveryActions {
		if strings.TrimSpace(recoveryAction.Kind) == "" {
			continue
		}
		if strings.TrimSpace(recoveryAction.PlatformUserID) == "" {
			recoveryAction.PlatformUserID = strings.TrimSpace(event.SenderID)
		}
		enrichedRecoveryActions = append(enrichedRecoveryActions, recoveryAction)
	}
	return enrichedRecoveryActions
}

func (connectorRuntime *ConnectorRuntime) Health() ConnectorRuntimeHealth {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()

	platforms := []string{}
	for platform := range connectorRuntime.adapterByPlatform {
		platforms = append(platforms, platform)
	}
	health := ConnectorRuntimeHealth{
		Started:                     connectorRuntime.started,
		HasEventRepository:          connectorRuntime.eventRepository != nil,
		HasQueueRepository:          connectorRuntime.queueRepository() != nil,
		HasOutboxRepository:         connectorRuntime.outboxRepository() != nil,
		RegisteredPlatforms:         platforms,
		MattermostAdapterRegistered: connectorRuntime.adapterByPlatform["mattermost"] != nil,
		InboxWorkerCount:            len(connectorRuntime.inboxHeartbeats),
		OutboxWorkerCount:           len(connectorRuntime.outboxHeartbeats),
		LastInboxHeartbeatAt:        latestTime(connectorRuntime.inboxHeartbeats),
		LastOutboxHeartbeatAt:       latestTime(connectorRuntime.outboxHeartbeats),
	}
	health.InboxWorkersAlive = connectorWorkersAlive(connectorRuntime.inboxHeartbeats, 2*connectorWorkerIdleDelay)
	health.OutboxWorkersAlive = connectorWorkersAlive(connectorRuntime.outboxHeartbeats, 2*connectorWorkerIdleDelay)
	health.Passed = health.Started &&
		health.HasEventRepository &&
		health.HasQueueRepository &&
		health.HasOutboxRepository &&
		health.MattermostAdapterRegistered &&
		health.InboxWorkersAlive &&
		health.OutboxWorkersAlive
	return health
}

func (connectorRuntime *ConnectorRuntime) prepareConnectorWorkers(kind string, count int) {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()

	heartbeats := make([]time.Time, count)
	now := time.Now()
	for index := range heartbeats {
		heartbeats[index] = now
	}
	if kind == "inbox" {
		connectorRuntime.inboxHeartbeats = heartbeats
		return
	}
	connectorRuntime.outboxHeartbeats = heartbeats
}

func (connectorRuntime *ConnectorRuntime) recordConnectorWorkerHeartbeat(kind string, workerIndex int) {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()

	now := time.Now()
	if kind == "inbox" && workerIndex >= 0 && workerIndex < len(connectorRuntime.inboxHeartbeats) {
		connectorRuntime.inboxHeartbeats[workerIndex] = now
		return
	}
	if kind == "outbox" && workerIndex >= 0 && workerIndex < len(connectorRuntime.outboxHeartbeats) {
		connectorRuntime.outboxHeartbeats[workerIndex] = now
	}
}

func (connectorRuntime *ConnectorRuntime) recordConnectorWorkerHeartbeatUntilStopped(ctx context.Context, kind string, workerIndex int) {
	ticker := time.NewTicker(connectorWorkerIdleDelay)
	defer ticker.Stop()
	for {
		connectorRuntime.recordConnectorWorkerHeartbeat(kind, workerIndex)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func connectorWorkersAlive(heartbeats []time.Time, maximumAge time.Duration) bool {
	if len(heartbeats) == 0 {
		return false
	}
	oldestAllowed := time.Now().Add(-maximumAge)
	for _, heartbeat := range heartbeats {
		if heartbeat.IsZero() || heartbeat.Before(oldestAllowed) {
			return false
		}
	}
	return true
}

func latestTime(values []time.Time) time.Time {
	var latest time.Time
	for _, value := range values {
		if value.After(latest) {
			latest = value
		}
	}
	return latest
}

func (connectorRuntime *ConnectorRuntime) buildTurnToolSet(adapter PlatformAdapter, event PlatformInboundEvent, personID string, personAccess policy.PersonAccess) *agent.ToolSet {
	return connectorRuntime.toolCatalogBuilder.BuildToolSet(agentruntime.ToolCatalogRequest{
		ProfileName:               "default",
		Prompt:                    event.Prompt,
		RequesterPersonID:         personID,
		RequesterName:             event.Context.Sender.Name,
		RequesterEmail:            connectorRuntime.identityService.ResolvePersonPrimaryEmail(personID),
		RequesterPlatformUserID:   event.SenderID,
		ConversationID:            event.ConversationID,
		ConversationType:          event.Context.ConversationType,
		ConversationChannelID:     event.Context.ChannelID,
		ConversationChannelName:   event.Context.ChannelName,
		ReplyTargetID:             event.ReplyTargetID,
		Platform:                  adapter.Name(),
		HistoryCursor:             event.Context.HistoryCursor,
		HistoryProvider:           connectorHistoryProvider{adapter: adapter},
		PersonAccess:              personAccess,
		MemoryNamespaces:          connectorRuntime.accessibleNamespaces(personID, personAccess, event),
		AccessibleConversationIDs: []string{event.ConversationID},
	})
}

func (connectorRuntime *ConnectorRuntime) currentTaskLauncher() *agentruntime.TaskLauncher {
	if connectorRuntime.taskLauncher != nil {
		return connectorRuntime.taskLauncher
	}
	return agentruntime.NewTaskLauncher(connectorRuntime.agentKernel, connectorRuntime.toolCatalogBuilder)
}

type connectorHistoryProvider struct {
	adapter PlatformAdapter
}

func (historyProvider connectorHistoryProvider) FetchHistory(ctx context.Context, historyCursor string, limit int) (agent.VisibleContext, error) {
	visibleContext, errorValue := historyProvider.adapter.FetchHistory(ctx, historyCursor, limit)
	if errorValue != nil {
		return agent.VisibleContext{}, errorValue
	}
	return visibleContext.ToAgentVisibleContext(), nil
}

func marshalConnectorToolResult(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return fmt.Sprint(value)
	}
	return string(document)
}

func trimNonEmptyStrings(values []string) []string {
	trimmedValues := []string{}
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			trimmedValues = append(trimmedValues, trimmedValue)
		}
	}
	return trimmedValues
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}

func detachedConnectorContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

func (connectorRuntime *ConnectorRuntime) ingestMemory(ctx context.Context, platform string, personID string, personAccess policy.PersonAccess, event PlatformInboundEvent, taskRunID string) {
	if connectorRuntime.memoryService == nil || connectorRuntime.memoryRouter == nil {
		return
	}
	defaultSecurityLevelRank := personAccess.SecurityLevelRank
	defaultRequiredClasses := append([]string{}, personAccess.GrantedClasses...)
	if channelPolicy, isFound := connectorRuntime.identityService.ResolveConversationPolicy(platform, event.ConversationID); isFound {
		defaultSecurityLevelRank = channelPolicy.DefaultSecurityLevelRank
		defaultRequiredClasses = append([]string{}, channelPolicy.DefaultRequiredClasses...)
	}
	route, errorValue := connectorRuntime.memoryRouter.Route(ctx, memory.GraphitiIngestionInput{
		PersonID:                 personID,
		Prompt:                   event.Prompt,
		ConversationID:           event.ConversationID,
		WorkspaceID:              connectorRuntime.workspaceID,
		DefaultSecurityLevelRank: defaultSecurityLevelRank,
		DefaultRequiredClasses:   defaultRequiredClasses,
		IsPrivateConversation:    isPrivateConversationID(event.ConversationID),
	})
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".memory.ingestion_route_failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
		connectorRuntime.agentKernel.AppendTaskEvent(taskRunID, "memory.ingestion_route_failed", errorValue.Error())
		route = memory.GraphitiIngestionRoute{ShouldStore: true, Namespaces: connectorRuntime.accessibleNamespaces(personID, personAccess, event), Reason: "route_failed_fallback", Confidence: 0.25}
	}
	if !route.ShouldStore {
		connectorRuntime.logger.Info("connector."+platform+".memory.ingestion_skipped", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("reason", route.Reason))
		connectorRuntime.agentKernel.AppendTaskEvent(taskRunID, "memory.ingestion_skipped", marshalConnectorToolResult(route))
		return
	}

	connectorRuntime.addMemoryEpisode(ctx, platform, personID, event, taskRunID, route.Namespaces)
}

func (connectorRuntime *ConnectorRuntime) addMemoryEpisode(ctx context.Context, platform string, personID string, event PlatformInboundEvent, taskRunID string, namespaces []memory.MemoryNamespace) {
	episode := memory.MemoryEpisode{
		EpisodeID:       event.DedupeKey(),
		Platform:        platform,
		MessageID:       event.MessageID,
		ConversationID:  event.ConversationID,
		SenderPersonID:  personID,
		Prompt:          event.Prompt,
		OccurredAt:      event.RawReceivedAt,
		Namespaces:      namespaces,
		Source:          "message",
		SourceReference: event.ReplyTargetID,
	}
	if episode.OccurredAt.IsZero() {
		episode.OccurredAt = time.Now().UTC()
	}
	result, errorValue := connectorRuntime.memoryService.AddEpisode(ctx, episode)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".memory.ingestion_failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
		connectorRuntime.agentKernel.AppendTaskEvent(taskRunID, "memory.ingestion_failed", errorValue.Error())
		return
	}
	connectorRuntime.logger.Info("connector."+platform+".memory.ingestion_succeeded", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.Int("namespaceCount", result.NamespaceCount))
	connectorRuntime.agentKernel.AppendTaskEvent(taskRunID, "memory.ingestion_succeeded", marshalConnectorToolResult(result))
}

func (connectorRuntime *ConnectorRuntime) accessibleNamespaces(personID string, personAccess policy.PersonAccess, event PlatformInboundEvent) []memory.MemoryNamespace {
	conversationSecurityLevelRank := personAccess.SecurityLevelRank
	conversationRequiredClasses := append([]string{}, personAccess.GrantedClasses...)
	if channelPolicy, isFound := connectorRuntime.identityService.ResolveConversationPolicy(event.Platform, event.ConversationID); isFound {
		conversationSecurityLevelRank = channelPolicy.DefaultSecurityLevelRank
		conversationRequiredClasses = append([]string{}, channelPolicy.DefaultRequiredClasses...)
	}
	namespaces := []memory.MemoryNamespace{
		memory.UserNamespace(personID),
		memory.PrivatePersonNamespace(personID),
		memory.WorkspaceNamespace(connectorRuntime.workspaceID, personAccess.SecurityLevelRank, personAccess.GrantedClasses),
	}
	if !isPrivateConversationID(event.ConversationID) {
		namespaces = append(namespaces, memory.ConversationNamespace(event.ConversationID, conversationSecurityLevelRank, conversationRequiredClasses))
	}
	for _, circleID := range personAccess.Circles {
		namespaces = append(namespaces, memory.CircleNamespace(connectorRuntime.workspaceID, circleID))
	}
	return namespaces
}

func isPrivateConversationID(conversationID string) bool {
	return strings.HasPrefix(strings.TrimSpace(conversationID), "dm:")
}

func (connectorRuntime *ConnectorRuntime) authorizeSender(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (string, bool, error) {
	personID, isFound := connectorRuntime.identityService.ResolvePersonIDByPlatformAccount(adapter.Name(), event.SenderID)
	if isFound {
		return personID, true, nil
	}

	platformAccountIdentity, errorValue := adapter.ResolveIdentity(ctx, event.SenderID)
	if errorValue != nil {
		return "", false, errorValue
	}
	platformAccountIdentity.Platform = adapter.Name()
	platformAccountIdentity.ExternalUserID = event.SenderID
	connectorRuntime.identityService.RememberPlatformAccount(platformAccountIdentity)

	personID, isFound = connectorRuntime.identityService.ResolvePersonIDByPlatformAccount(adapter.Name(), event.SenderID)
	return personID, isFound, nil
}

func (connectorRuntime *ConnectorRuntime) buildReplyTarget(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (ReplyTarget, error) {
	_ = ctx
	_ = adapter

	return ReplyTarget{
		ConversationID: event.ConversationID,
		ReplyTargetID:  event.ReplyTargetID,
		DedupeKey:      event.DedupeKey(),
	}, nil
}

func (connectorRuntime *ConnectorRuntime) startProgress(ctx context.Context, adapter PlatformAdapter, replyTarget ReplyTarget) func() {
	return connectorRuntime.startProgressHeartbeat(ctx, adapter, replyTarget)
}

func (connectorRuntime *ConnectorRuntime) startProgressHeartbeat(ctx context.Context, adapter PlatformAdapter, replyTarget ReplyTarget) func() {
	platform := adapter.Name()
	connectorRuntime.logger.Info("connector."+platform+".progress.started", slog.String("conversationID", replyTarget.ConversationID), slog.String("replyTargetID", replyTarget.ReplyTargetID))
	if errorValue := adapter.StartProgress(ctx, replyTarget); errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".progress.start_failed", slog.String("conversationID", replyTarget.ConversationID), slog.String("replyTargetID", replyTarget.ReplyTargetID), slog.String("error", errorValue.Error()))
	}

	progressContext, stopHeartbeat := context.WithCancel(ctx)
	go connectorRuntime.refreshProgressUntilStopped(progressContext, adapter, replyTarget)

	return func() {
		stopHeartbeat()
		stopContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if errorValue := adapter.StopProgress(stopContext, replyTarget); errorValue != nil {
			connectorRuntime.logger.Warn("connector."+platform+".progress.stop_failed", slog.String("conversationID", replyTarget.ConversationID), slog.String("replyTargetID", replyTarget.ReplyTargetID), slog.String("error", errorValue.Error()))
		}
		connectorRuntime.logger.Info("connector."+platform+".progress.stopped", slog.String("conversationID", replyTarget.ConversationID), slog.String("replyTargetID", replyTarget.ReplyTargetID))
	}
}

func (connectorRuntime *ConnectorRuntime) refreshProgressUntilStopped(ctx context.Context, adapter PlatformAdapter, replyTarget ReplyTarget) {
	ticker := time.NewTicker(connectorProgressHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if errorValue := adapter.StartProgress(ctx, replyTarget); errorValue != nil {
				connectorRuntime.logger.Warn("connector."+adapter.Name()+".progress.refresh_failed", slog.String("conversationID", replyTarget.ConversationID), slog.String("replyTargetID", replyTarget.ReplyTargetID), slog.String("error", errorValue.Error()))
			}
		}
	}
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

func (connectorRuntime *ConnectorRuntime) queueRepository() ConnectorQueueRepository {
	queueRepository, isFound := connectorRuntime.eventRepository.(ConnectorQueueRepository)
	if !isFound {
		return nil
	}
	return queueRepository
}

func (connectorRuntime *ConnectorRuntime) outboxRepository() ConnectorOutboxRepository {
	outboxRepository, isFound := connectorRuntime.eventRepository.(ConnectorOutboxRepository)
	if !isFound {
		return nil
	}
	return outboxRepository
}

func (connectorRuntime *ConnectorRuntime) conversationLock(name string) *sync.Mutex {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()
	lock, isFound := connectorRuntime.conversationLocks[name]
	if isFound {
		return lock
	}
	lock = &sync.Mutex{}
	connectorRuntime.conversationLocks[name] = lock
	return lock
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

func (connectorRuntime *ConnectorRuntime) markQueuedConnectorEventFailed(queuedEvent QueuedConnectorEvent, errorValue error) {
	nextAttemptAt := nextConnectorAttemptAt(queuedEvent.AttemptCount)
	if markError := connectorRuntime.queueRepository().MarkConnectorEventFailed(queuedEvent, errorValue, nextAttemptAt); markError != nil {
		connectorRuntime.logger.Warn("connector."+queuedEvent.Event.Platform+".inbox.mark_failed_failed", slog.String("messageID", queuedEvent.Event.MessageID), slog.String("error", markError.Error()))
	}
}

func (connectorRuntime *ConnectorRuntime) markQueuedConnectorReplyFailed(queuedReply QueuedConnectorReply, errorValue error) {
	nextAttemptAt := nextConnectorAttemptAt(queuedReply.AttemptCount)
	if markError := connectorRuntime.outboxRepository().MarkConnectorReplyFailed(queuedReply, errorValue, nextAttemptAt); markError != nil {
		connectorRuntime.logger.Warn("connector."+queuedReply.Platform+".outbox.mark_failed_failed", slog.String("outboxID", queuedReply.OutboxID), slog.String("error", markError.Error()))
	}
}

func nextConnectorAttemptAt(attemptCount int) time.Time {
	if attemptCount >= connectorMaximumAttemptCount {
		return time.Time{}
	}
	delaySeconds := 1 << max(0, min(attemptCount, 6))
	return time.Now().UTC().Add(time.Duration(delaySeconds) * time.Second)
}

func sleepConnectorWorker(ctx context.Context) {
	timer := time.NewTimer(connectorWorkerIdleDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

type connectorEventContextKey struct{}

func withConnectorEvent(ctx context.Context, event PlatformInboundEvent) context.Context {
	return context.WithValue(ctx, connectorEventContextKey{}, event)
}

func connectorEventFromContext(ctx context.Context) (PlatformInboundEvent, bool) {
	event, isFound := ctx.Value(connectorEventContextKey{}).(PlatformInboundEvent)
	return event, isFound
}

func (event PlatformInboundEvent) DedupeKey() string {
	messageID := strings.TrimSpace(event.MessageID)
	conversationID := strings.TrimSpace(event.ConversationID)
	return event.Platform + ":" + conversationID + ":" + messageID
}

func (event *PlatformInboundEvent) UnmarshalJSON(document []byte) error {
	type platformInboundEvent PlatformInboundEvent
	var parsedEvent platformInboundEvent
	if errorValue := json.Unmarshal(document, &parsedEvent); errorValue != nil {
		return errorValue
	}

	var rawFields map[string]interface{}
	if errorValue := json.Unmarshal(document, &rawFields); errorValue == nil {
		parsedEvent.LegacyFields = rawFields
	}

	if strings.TrimSpace(parsedEvent.Prompt) == "" {
		parsedEvent.Prompt = stringField(rawFields, "text")
	}
	if strings.TrimSpace(parsedEvent.SenderID) == "" {
		parsedEvent.SenderID = stringField(rawFields, "senderUserID")
	}

	*event = PlatformInboundEvent(parsedEvent)
	return nil
}

func (visibleContext VisibleContext) ToAgentVisibleContext() agent.VisibleContext {
	messages := make([]agent.VisibleContextMessage, 0, len(visibleContext.Messages))
	for _, message := range visibleContext.Messages {
		messages = append(messages, agent.VisibleContextMessage{
			Speaker:            message.Speaker,
			SpeakerCallingName: message.SpeakerCallingName,
			SpeakerHandle:      message.SpeakerHandle,
			Text:               message.Text,
		})
	}

	return agent.VisibleContext{
		Messages:         messages,
		HasMoreBefore:    visibleContext.HasMoreBefore,
		HistoryCursor:    visibleContext.HistoryCursor,
		ResponseLanguage: visibleContext.ResponseLanguage,
	}
}

func responseLanguageForEvent(event PlatformInboundEvent) string {
	return agent.ResolveResponseLanguage(event.ResponseLanguage, event.Context.ResponseLanguage)
}

func stringField(fields map[string]interface{}, name string) string {
	if fields == nil {
		return ""
	}
	value, isFound := fields[name]
	if !isFound {
		return ""
	}
	stringValue, isString := value.(string)
	if !isString {
		return ""
	}
	return strings.TrimSpace(stringValue)
}
