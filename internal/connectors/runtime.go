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

type TaskIntakeGate interface {
	IsQuiesced() bool
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
	InputParts       []agent.AgentPart      `json:"inputParts,omitempty"`
	ResponseLanguage string                 `json:"responseLanguage,omitempty"`
	Context          VisibleContext         `json:"context"`
	RawReceivedAt    time.Time              `json:"-"`
	LegacyFields     map[string]interface{} `json:"legacyFields,omitempty"`
}

type ReplyTarget struct {
	ConversationID string `json:"conversationID"`
	ReplyTargetID  string `json:"replyTargetID"`
	DedupeKey      string `json:"dedupeKey"`
}

type ReactionTarget struct {
	Platform       string `json:"platform"`
	ConversationID string `json:"conversationID"`
	MessageID      string `json:"messageID"`
	EmojiName      string `json:"emojiName"`
	Reason         string `json:"reason"`
}

type OutboundReply struct {
	Message         string                 `json:"message"`
	TaskRunID       string                 `json:"taskRunID,omitempty"`
	ReplyKind       string                 `json:"replyKind,omitempty"`
	RawEventID      string                 `json:"rawEventID,omitempty"`
	OutboxID        string                 `json:"outboxID,omitempty"`
	EphemeralUserID string                 `json:"ephemeralUserID,omitempty"`
	Attachments     []agent.FileAttachment `json:"attachments,omitempty"`
	RecoveryActions []agent.RecoveryAction `json:"recoveryActions,omitempty"`
	FailureNotice   agent.FailureNotice    `json:"failureNotice,omitempty"`
	Interaction     *AskInteraction        `json:"interaction,omitempty"`
}

type outboundReplyDocument struct {
	Message         string                    `json:"message"`
	TaskRunID       string                    `json:"taskRunID,omitempty"`
	ReplyKind       string                    `json:"replyKind,omitempty"`
	RawEventID      string                    `json:"rawEventID,omitempty"`
	OutboxID        string                    `json:"outboxID,omitempty"`
	EphemeralUserID string                    `json:"ephemeralUserID,omitempty"`
	Attachments     []outboundReplyAttachment `json:"attachments,omitempty"`
	RecoveryActions []agent.RecoveryAction    `json:"recoveryActions,omitempty"`
	FailureNotice   agent.FailureNotice       `json:"failureNotice,omitempty"`
	Interaction     *AskInteraction           `json:"interaction,omitempty"`
}

type outboundReplyAttachment struct {
	DevicePath    string `json:"devicePath"`
	Filename      string `json:"filename,omitempty"`
	ContentType   string `json:"contentType,omitempty"`
	SizeBytes     int64  `json:"sizeBytes,omitempty"`
	Title         string `json:"title,omitempty"`
	ContentBase64 string `json:"contentBase64,omitempty"`
}

type AskInteraction struct {
	InteractionID        string            `json:"interactionID"`
	TaskRunID            string            `json:"taskRunID"`
	Kind                 string            `json:"kind"`
	Message              string            `json:"message,omitempty"`
	Question             string            `json:"question,omitempty"`
	Options              []AskChoiceOption `json:"options,omitempty"`
	RecommendedOptionKey string            `json:"recommendedOptionKey,omitempty"`
	SelectionMode        string            `json:"selectionMode,omitempty"`
	ResponseLanguage     string            `json:"responseLanguage,omitempty"`
	TargetPlatformUserID string            `json:"targetPlatformUserID,omitempty"`
}

type AskChoiceOption struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	ShortLabel string `json:"shortLabel,omitempty"`
	Value      string `json:"value,omitempty"`
}

func (reply OutboundReply) MarshalJSON() ([]byte, error) {
	document := outboundReplyDocument{
		Message:         reply.Message,
		TaskRunID:       reply.TaskRunID,
		ReplyKind:       reply.ReplyKind,
		RawEventID:      reply.RawEventID,
		OutboxID:        reply.OutboxID,
		EphemeralUserID: reply.EphemeralUserID,
		Attachments:     outboundReplyAttachments(reply.Attachments),
		RecoveryActions: reply.RecoveryActions,
		FailureNotice:   reply.FailureNotice,
		Interaction:     reply.Interaction,
	}
	return json.Marshal(document)
}

func (reply *OutboundReply) UnmarshalJSON(documentBytes []byte) error {
	var document outboundReplyDocument
	if errorValue := json.Unmarshal(documentBytes, &document); errorValue != nil {
		return errorValue
	}
	reply.Message = document.Message
	reply.TaskRunID = document.TaskRunID
	reply.ReplyKind = document.ReplyKind
	reply.RawEventID = document.RawEventID
	reply.OutboxID = document.OutboxID
	reply.EphemeralUserID = document.EphemeralUserID
	reply.Attachments = fileAttachmentsFromOutboundReplyAttachments(document.Attachments)
	reply.RecoveryActions = append([]agent.RecoveryAction{}, document.RecoveryActions...)
	reply.FailureNotice = document.FailureNotice
	reply.Interaction = document.Interaction
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
	Addressing       AddressingMetadata      `json:"addressing,omitempty"`
	InputAttachments []InputAttachment       `json:"inputAttachments,omitempty"`
	Materials        []InputAttachment       `json:"materials,omitempty"`
}

type InputAttachment struct {
	Platform    string `json:"platform,omitempty"`
	FileID      string `json:"fileID,omitempty"`
	MessageID   string `json:"messageID,omitempty"`
	Filename    string `json:"filename,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	SizeBytes   int64  `json:"sizeBytes,omitempty"`
	Path        string `json:"path,omitempty"`
	IsAvailable bool   `json:"isAvailable,omitempty"`
	ErrorCode   string `json:"errorCode,omitempty"`
	Message     string `json:"message,omitempty"`
}

type AddressingMetadata struct {
	BotMentioned         bool `json:"botMentioned,omitempty"`
	OtherPersonMentioned bool `json:"otherPersonMentioned,omitempty"`
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
	Speaker            string            `json:"speaker"`
	SpeakerCallingName string            `json:"speakerCallingName,omitempty"`
	SpeakerHandle      string            `json:"speakerHandle,omitempty"`
	Text               string            `json:"text"`
	InputAttachments   []InputAttachment `json:"inputAttachments,omitempty"`
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

const NotInvitedReply = "This Intern Kim has not invited your account yet. Ask the administrator for access."

type PlatformAdapter interface {
	Name() string
	ParseHTTPEvent(context.Context, *http.Request) (HTTPParseResult, error)
	ParseRealtimeEvent(context.Context, []byte, string) (PlatformInboundEvent, bool, error)
	ResolveIdentity(context.Context, string) (identity.PlatformAccountIdentity, error)
	StartProgress(context.Context, ReplyTarget) error
	StopProgress(context.Context, ReplyTarget) error
	SendReply(context.Context, ReplyTarget, OutboundReply) (string, error)
	FetchHistory(context.Context, string, int) (VisibleContext, error)
}

type InteractionResolvingAdapter interface {
	ResolveInteraction(context.Context, InteractionResolution) error
}

type InputAttachmentImportingAdapter interface {
	ImportInputAttachments(context.Context, InputAttachmentImportRequest) (InputAttachmentImportResult, error)
}

type InputAttachmentImportRequest struct {
	MessageID           string            `json:"messageID"`
	TargetDirectoryPath string            `json:"targetDirectoryPath"`
	InputAttachments    []InputAttachment `json:"inputAttachments"`
}

type InputAttachmentImportResult struct {
	InputParts       []agent.AgentPart `json:"inputParts,omitempty"`
	InputAttachments []InputAttachment `json:"inputAttachments,omitempty"`
}

type MessageReactionAdapter interface {
	AddReaction(context.Context, ReactionTarget) error
}

type InteractionResolution struct {
	Platform       string `json:"platform"`
	ConversationID string `json:"conversationID"`
	ReplyTargetID  string `json:"replyTargetID"`
	DispatchID     string `json:"dispatchID"`
	TaskRunID      string `json:"taskRunID"`
	InteractionID  string `json:"interactionID"`
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
const connectorProgressHeartbeatInterval = 5 * time.Second
const connectorMaximumAttemptCount = 5
const connectorReplyKindSuccess = "success"
const connectorReplyKindCheckpoint = "checkpoint"
const connectorReplyKindUserNotice = "user_notice"
const connectorReplyKindPermissionNotice = "permission_notice"

type ConnectorRuntime struct {
	identityService      *identity.IdentityService
	agentKernel          *agent.AgentKernel
	taskLauncher         *agentruntime.TaskLauncher
	toolCatalogBuilder   *agentruntime.ToolCatalogBuilder
	memoryService        *memory.MemoryService
	memoryRouter         *memory.GraphitiIngestionRouter
	workspaceID          string
	adminTaskLinkBaseURL string
	logger               *slog.Logger

	mutex                   sync.Mutex
	adapterByPlatform       map[string]PlatformAdapter
	processedResults        map[string]ConnectorRuntimeResult
	eventRepository         ConnectorEventRepository
	ingressGate             IngressGate
	taskIntakeGate          TaskIntakeGate
	taskWaitTokenRepository task.TaskWaitTokenRepository
	conversationLocks       map[string]*sync.Mutex
	started                 bool
	inboxHeartbeats         []time.Time
	outboxHeartbeats        []time.Time
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
	TaskRun                 task.TaskRun
	IntentPrompt            string
	ApprovalQuestion        string
	ResponseLanguage        string
	ContinuationInstruction string
	ActiveGoal              agent.ActiveGoal
}

type inboundTaskWaitResolution struct {
	TaskWaitToken      task.TaskWaitToken
	HasTaskWaitToken   bool
	IsAmbiguous        bool
	AmbiguousTaskWaits []task.TaskWaitToken
	Reason             string
}

func NewConnectorRuntime(identityService *identity.IdentityService, agentKernel *agent.AgentKernel, logger *slog.Logger) *ConnectorRuntime {
	if logger == nil {
		logger = slog.Default()
	}
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, connectorRuntimeDefaultAllowedToolNames())

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

func (connectorRuntime *ConnectorRuntime) UseAdminTaskLinkBaseURL(adminTaskLinkBaseURL string) {
	connectorRuntime.adminTaskLinkBaseURL = strings.TrimRight(strings.TrimSpace(adminTaskLinkBaseURL), "/")
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

func (connectorRuntime *ConnectorRuntime) UseWorkspaceActorFactory(workspaceActorFactory security.WorkspaceActorFactory) {
	connectorRuntime.toolCatalogBuilder.UseWorkspaceActorFactory(workspaceActorFactory)
}

func (connectorRuntime *ConnectorRuntime) UseTaskScheduleRepository(taskScheduleRepository task.TaskScheduleRepository) {
	connectorRuntime.toolCatalogBuilder.UseTaskScheduleRepository(taskScheduleRepository)
}

func (connectorRuntime *ConnectorRuntime) UseTaskWaitTokenRepository(taskWaitTokenRepository task.TaskWaitTokenRepository) {
	connectorRuntime.taskWaitTokenRepository = taskWaitTokenRepository
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

func (connectorRuntime *ConnectorRuntime) UseTaskIntakeGate(taskIntakeGate TaskIntakeGate) {
	connectorRuntime.taskIntakeGate = taskIntakeGate
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
		trimmedToolNames = connectorRuntimeDefaultAllowedToolNames()
	}
	connectorRuntime.toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, trimmedToolNames)
}

func connectorRuntimeDefaultAllowedToolNames() []string {
	return append([]string{"conversation.history"}, agentruntime.DefaultAllowedToolNames()...)
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
	connectorRuntime.logConnectorQueueWait(event)
	lock := connectorRuntime.conversationLock(event.Platform + ":" + event.ConversationID)
	if shouldProcessBeforeConversationLock(event) {
		connectorRuntime.processQueuedConnectorEventWithAdapter(ctx, adapter, queuedEvent)
		return
	}
	lockStartedAt := time.Now()
	lock.Lock()
	connectorRuntime.logConnectorLockWait(event, time.Since(lockStartedAt))
	defer lock.Unlock()
	connectorRuntime.processQueuedConnectorEventWithAdapter(ctx, adapter, queuedEvent)
}

func (connectorRuntime *ConnectorRuntime) logConnectorQueueWait(event PlatformInboundEvent) {
	if event.RawReceivedAt.IsZero() {
		return
	}
	waitDuration := time.Since(event.RawReceivedAt)
	if waitDuration < 0 {
		return
	}
	connectorRuntime.logger.Info(
		"blueclaw.connector.queue_wait",
		slog.String("platform", event.Platform),
		slog.String("messageID", event.MessageID),
		slog.String("conversationID", event.ConversationID),
		slog.Int64("duration_ms", waitDuration.Milliseconds()),
	)
}

func (connectorRuntime *ConnectorRuntime) logConnectorLockWait(event PlatformInboundEvent, waitDuration time.Duration) {
	connectorRuntime.logger.Info(
		"blueclaw.connector.lock_wait",
		slog.String("platform", event.Platform),
		slog.String("messageID", event.MessageID),
		slog.String("conversationID", event.ConversationID),
		slog.Int64("duration_ms", waitDuration.Milliseconds()),
	)
}

func (connectorRuntime *ConnectorRuntime) processQueuedConnectorEventWithAdapter(ctx context.Context, adapter PlatformAdapter, queuedEvent QueuedConnectorEvent) {
	event := queuedEvent.Event
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
	if shouldDeferQueuedConnectorEvent(result) {
		connectorRuntime.logger.Info("connector."+event.Platform+".inbox.deferred", slog.String("messageID", event.MessageID), slog.String("reason", result.Reason))
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
	outboxID, errorValue := outboxRepository.EnqueueConnectorReply(event, replyTarget, reply)
	if errorValue == nil {
		connectorRuntime.appendConnectorReplyEvent(reply.TaskRunID, "connector.reply.enqueued", connectorReplyEventBody(event, reply, outboxID, "", ""))
	}
	return outboxID, errorValue
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
	connectorRuntime.appendConnectorReplyEvent(queuedReply.Reply.TaskRunID, "connector.reply.sent", connectorReplyEventBody(PlatformInboundEvent{MessageID: queuedReply.RawEventID}, queuedReply.Reply, queuedReply.OutboxID, dispatchID, ""))
	connectorRuntime.recordTaskWaitTokenForReply(
		queuedReply.Platform,
		PlatformInboundEvent{Platform: queuedReply.Platform, ConversationID: queuedReply.ReplyTarget.ConversationID, MessageID: queuedReply.RawEventID},
		queuedReply.ReplyTarget,
		queuedReply.Reply,
		dispatchID,
	)
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
		if shouldIgnoreUninvitedAddressing(event) {
			connectorRuntime.logger.Info("connector."+platform+".ingress.ignored", slog.String("messageID", event.MessageID), slog.String("reason", "not_addressed_to_bot"))
			return ConnectorRuntimeResult{Handled: true, Platform: platform, Ignored: true, Reason: "not_addressed_to_bot"}, nil
		}
		connectorRuntime.logger.Info("connector."+platform+".auth.rejected", slog.String("messageID", event.MessageID), slog.String("reason", "not_invited"))
		dispatchID, sendError := sendReply(ctx, replyTarget, OutboundReply{Message: NotInvitedReply, ReplyKind: connectorReplyKindPermissionNotice})
		if sendError != nil {
			connectorRuntime.logger.Error("connector."+platform+".outbound.failed", slog.String("messageID", event.MessageID), slog.String("error", sendError.Error()))
			return ConnectorRuntimeResult{Handled: true, Platform: platform, Reason: "not_invited"}, nil
		}
		connectorRuntime.logger.Info("connector."+platform+".outbound.sent", slog.String("messageID", event.MessageID), slog.String("replyDispatchID", dispatchID))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, Reason: "not_invited", ReplyDispatchID: dispatchID}, nil
	}

	connectorRuntime.logger.Info("connector."+platform+".auth.allowed", slog.String("messageID", event.MessageID), slog.String("personID", personID))
	if result, isHandled := connectorRuntime.suppressDuplicateSourceTaskIfNeeded(platform, event, personID); isHandled {
		return result, nil
	}
	if result, isHandled := connectorRuntime.handleTaskControlIfRequested(ctx, platform, adapter, event, replyTarget, personID, sendReply); isHandled {
		return result, nil
	}
	if result, isHandled := connectorRuntime.handleDebugControlIfRequested(ctx, platform, event, replyTarget, personID, sendReply); isHandled {
		return result, nil
	}
	stopProgress := func() {}
	isProgressStarted := false
	defer func() {
		if isProgressStarted {
			stopProgress()
		}
	}()
	if shouldStartProgressBeforeAddressing(event) {
		stopProgress = connectorRuntime.startProgressHeartbeat(ctx, adapter, replyTarget)
		isProgressStarted = true
	}
	personAccess := connectorRuntime.identityService.ResolvePersonAccess(personID)
	requesterEmail := connectorRuntime.requesterEmailForEvent(personID, event)
	taskWaitResolution := connectorRuntime.resolveInboundTaskWait(personID, platform, event)
	if taskWaitResolution.IsAmbiguous {
		return connectorRuntime.handleAmbiguousTaskWait(ctx, platform, adapter, event, replyTarget, personID, requesterEmail, personAccess, taskWaitResolution, sendReply)
	}
	pendingApproval, turnDecision, hasPendingConfirmation := connectorRuntime.resolveConfirmationReply(ctx, platform, personID, event, taskWaitResolution)
	isApprovalContinuation := hasPendingConfirmation && turnDecision.Approval != nil && *turnDecision.Approval == agent.ApprovalSignalApprove
	if hasPendingConfirmation {
		connectorRuntime.resolveAskInteractionMessage(ctx, adapter, event, pendingApproval.TaskRun.TaskRunID, AskInteraction{InteractionID: latestAskInteractionID(connectorRuntime.agentKernel.ListTaskEvent(pendingApproval.TaskRun.TaskRunID))})
		connectorRuntime.resolveTaskWaitToken(taskWaitResolution)
	}
	if hasPendingConfirmation && !isApprovalContinuation && shouldStopAfterPendingConfirmation(turnDecision) {
		return connectorRuntime.handleRejectedConfirmation(ctx, platform, adapter, event, replyTarget, pendingApproval, agent.ConfirmationReplyDecision{Decision: string(agent.ApprovalSignalReject), Reason: turnDecision.Reason}, sendReply)
	}
	if hasPendingConfirmation && !isApprovalContinuation && turnDecision.Route == agent.TurnRouteAnswerQuestion {
		return connectorRuntime.handlePendingConfirmationQuestion(ctx, platform, adapter, event, replyTarget, pendingApproval, turnDecision, sendReply)
	}
	if hasPendingConfirmation && !isApprovalContinuation {
		connectorRuntime.cancelPendingConfirmation(event, pendingApproval, turnDecision)
	}
	if connectorRuntime.shouldIgnoreOrphanAskAction(personID, platform, event, hasPendingConfirmation, taskWaitResolution) {
		connectorRuntime.logger.Info("connector."+platform+".ingress.ignored", slog.String("messageID", event.MessageID), slog.String("reason", "ask_no_pending_interaction"))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, Ignored: true, Reason: "ask_no_pending_interaction"}, nil
	}
	pendingAskInteraction, hasPendingAskInteraction := connectorRuntime.findPendingAskInteraction(personID, platform, event, taskWaitResolution)
	previousPrompt := event.Prompt
	event, askTurnDecision, hasAskTurnDecision := connectorRuntime.resolveAskReply(ctx, platform, personID, event, taskWaitResolution)
	if hasPendingAskInteraction && askReplyConsumesInteraction(pendingAskInteraction, previousPrompt, event, askTurnDecision, hasAskTurnDecision) {
		connectorRuntime.appendAskResolvedEvent(pendingAskInteraction, event, askTurnDecision)
		connectorRuntime.resolveAskInteractionMessage(ctx, adapter, event, pendingAskInteraction.TaskRunID, pendingAskInteraction)
		connectorRuntime.resolveTaskWaitToken(taskWaitResolution)
	}
	activeGoal, hasActiveGoal := connectorRuntime.findActiveGoal(personID, platform, event, taskWaitResolution)
	if !isApprovalContinuation && hasActiveGoal && turnDecision.Route == agent.TurnRouteStartTask {
		activeGoal = agent.ActiveGoal{}
		hasActiveGoal = false
	}
	if isApprovalContinuation {
		event = approvedContinuationEvent(event, pendingApproval)
		activeGoal = pendingApprovalActiveGoal(pendingApproval, event.Prompt)
		hasActiveGoal = true
	}
	event = connectorRuntime.withInitialVisibleContext(ctx, adapter, event)
	addressingLaunch := connectorRuntime.resolveInboundEngagement(ctx, platform, event)
	if !addressingLaunch.ShouldLaunch {
		connectorRuntime.logger.Info("connector."+platform+".ingress.ignored", slog.String("messageID", event.MessageID), slog.String("reason", addressingLaunch.IgnoreReason))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, Ignored: true, Reason: addressingLaunch.IgnoreReason}, nil
	}
	if connectorRuntime.shouldDeferNewTaskLaunch(isApprovalContinuation, hasPendingAskInteraction, hasActiveGoal) {
		connectorRuntime.logger.Info("connector."+platform+".ingress.deferred", slog.String("messageID", event.MessageID), slog.String("reason", "task_intake_quiesced"))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, Ignored: true, Reason: "task_intake_quiesced"}, nil
	}
	if !isApprovalContinuation && !hasPendingAskInteraction {
		busyResult, errorValue := connectorRuntime.handleBusyMessageIfNeeded(ctx, platform, event, replyTarget, personID, sendReply)
		if errorValue != nil {
			return ConnectorRuntimeResult{}, errorValue
		}
		if busyResult.isHandled {
			return busyResult.connectorResult, nil
		}
		if busyResult.clearActiveGoal {
			activeGoal = agent.ActiveGoal{}
			hasActiveGoal = false
		}
	}
	if !isProgressStarted {
		stopProgress = connectorRuntime.startProgressHeartbeat(ctx, adapter, replyTarget)
		isProgressStarted = true
	}
	event = connectorRuntime.withAttachmentMaterials(ctx, adapter, event, personID)

	connectorRuntime.logger.Info("connector."+platform+".agent.started", slog.String("messageID", event.MessageID))
	precomputedTurnDecision := precomputedTurnDecisionForLaunch(turnDecision, hasPendingConfirmation, askTurnDecision, hasAskTurnDecision)
	if precomputedTurnDecision == nil && addressingLaunch.AmbientDuty.IsMatch && !isApprovalContinuation && !hasActiveGoal {
		precomputedTurnDecision = ambientCaptureTurnDecision(addressingLaunch.AmbientDuty.Name, responseLanguageForEvent(event))
	}
	taskStartedAt := time.Now()
	conversationTurn := ConversationTurn{
		Platform:                  platform,
		Adapter:                   adapter,
		Event:                     event,
		ReplyTarget:               replyTarget,
		RequesterPersonID:         personID,
		RequesterEmail:            requesterEmail,
		PersonAccess:              personAccess,
		IsApprovalContinuation:    isApprovalContinuation,
		ActiveGoal:                activeGoal,
		HasActiveGoal:             hasActiveGoal,
		PrecomputedTurnDecision:   precomputedTurnDecision,
		AmbientDuty:               addressingLaunch.AmbientDuty,
		CheckpointSender:          connectorRuntime.checkpointSenderForTurn(platform, event, replyTarget, sendReply),
		AccessibleConversationIDs: []string{event.ConversationID},
		IsBlockedContinuation:     activeGoal.Status == agent.ActiveGoalStatusBlocked && hasActiveGoal,
	}
	launchResult, errorValue := connectorRuntime.currentTaskLauncher().Launch(ctx, connectorRuntime.buildTaskLaunchRequest(conversationTurn))
	if errorValue != nil {
		connectorRuntime.logger.Error("connector."+platform+".agent.failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{}, errorValue
	}
	turnResult := launchResult.TurnResult
	taskRunID := turnResult.TaskRun.TaskRunID
	taskDuration := time.Since(taskStartedAt)
	connectorRuntime.logger.Info("connector."+platform+".agent.completed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.Int64("duration_ms", taskDuration.Milliseconds()))
	connectorRuntime.appendTaskExecutionDuration(taskRunID, taskDuration)
	return connectorRuntime.dispatchTaskReply(ctx, platform, adapter, event, replyTarget, turnResult, addressingLaunch.AmbientDuty.IsMatch, sendReply)
}

func (connectorRuntime *ConnectorRuntime) shouldDeferNewTaskLaunch(isApprovalContinuation bool, hasPendingAskInteraction bool, hasActiveGoal bool) bool {
	if connectorRuntime.taskIntakeGate == nil || !connectorRuntime.taskIntakeGate.IsQuiesced() {
		return false
	}
	return !isApprovalContinuation && !hasPendingAskInteraction && !hasActiveGoal
}

func shouldDeferQueuedConnectorEvent(result ConnectorRuntimeResult) bool {
	return result.Ignored && result.Reason == "task_intake_quiesced"
}

func (connectorRuntime *ConnectorRuntime) addConsumeReaction(ctx context.Context, platform string, adapter PlatformAdapter, event PlatformInboundEvent, taskRunID string, reactionEmojiName string) string {
	reactionAdapter, isSupported := adapter.(MessageReactionAdapter)
	if !isSupported {
		connectorRuntime.agentKernel.AppendTaskEvent(taskRunID, "connector.reaction.skipped", marshalConnectorEventBody(map[string]string{
			"messageID": event.MessageID,
			"reason":    "reaction_adapter_unavailable",
		}))
		return "consume_no_reaction_adapter"
	}
	target := ReactionTarget{
		Platform:       platform,
		ConversationID: event.ConversationID,
		MessageID:      event.MessageID,
		EmojiName:      consumeReactionEmojiName(reactionEmojiName),
		Reason:         "consume",
	}
	if errorValue := reactionAdapter.AddReaction(ctx, target); errorValue != nil {
		connectorRuntime.agentKernel.AppendTaskEvent(taskRunID, "connector.reaction.failed", marshalConnectorEventBody(map[string]string{
			"messageID": event.MessageID,
			"emojiName": target.EmojiName,
			"error":     errorValue.Error(),
		}))
		connectorRuntime.logger.Warn("connector."+platform+".reaction.failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
		return "consume_reaction_failed"
	}
	connectorRuntime.agentKernel.AppendTaskEvent(taskRunID, "connector.reaction.sent", marshalConnectorEventBody(map[string]string{
		"messageID": event.MessageID,
		"emojiName": target.EmojiName,
		"reason":    target.Reason,
	}))
	return "consume_reacted"
}

func consumeReactionEmojiName(reactionEmojiName string) string {
	reactionEmojiName = strings.TrimSpace(reactionEmojiName)
	if reactionEmojiName == "" {
		return agent.DefaultReactionEmojiName
	}
	return reactionEmojiName
}

func (connectorRuntime *ConnectorRuntime) appendTaskExecutionDuration(taskRunID string, duration time.Duration) {
	if strings.TrimSpace(taskRunID) == "" {
		return
	}
	connectorRuntime.agentKernel.AppendTaskEvent(taskRunID, "blueclaw.task.execution_duration", marshalConnectorEventBody(map[string]any{
		"durationMs": duration.Milliseconds(),
	}))
}

func (connectorRuntime *ConnectorRuntime) shouldIgnoreOrphanAskAction(personID string, platform string, event PlatformInboundEvent, hasPendingConfirmation bool, taskWaitResolution inboundTaskWaitResolution) bool {
	action, isFound := event.LegacyFields["askAction"].(string)
	if !isFound {
		return false
	}
	switch strings.TrimSpace(action) {
	case "confirm", "cancel":
		return !hasPendingConfirmation
	case "choice":
		_, hasPendingAsk := connectorRuntime.findPendingAskInteraction(personID, platform, event, taskWaitResolution)
		return !hasPendingAsk
	default:
		return true
	}
}

func (connectorRuntime *ConnectorRuntime) resolveConfirmationReply(ctx context.Context, platform string, personID string, event PlatformInboundEvent, taskWaitResolution inboundTaskWaitResolution) (pendingApproval, agent.TurnDecision, bool) {
	approval, isFound := connectorRuntime.findPendingApproval(personID, platform, event, taskWaitResolution)
	if !isFound {
		return pendingApproval{}, agent.TurnDecision{}, false
	}
	if action, isFound := event.LegacyFields["askAction"].(string); isFound {
		switch strings.TrimSpace(action) {
		case "confirm":
			approvalSignal := agent.ApprovalSignalApprove
			return approval, agent.TurnDecision{Route: agent.TurnRouteContinueTask, Approval: &approvalSignal, Classification: agent.IntakeClassificationBoundedTask, TaskShape: agent.TaskShapeMaintenanceTask, EffortLevel: agent.EffortLevelStandard, ResponseLanguage: responseLanguageForEvent(event), Reason: "interactive_confirm"}, true
		case "cancel":
			approvalSignal := agent.ApprovalSignalReject
			return approval, agent.TurnDecision{Route: agent.TurnRouteConsume, Approval: &approvalSignal, Classification: agent.IntakeClassificationQuickReply, TaskShape: agent.TaskShapeImmediateReply, EffortLevel: agent.EffortLevelQuick, ResponseLanguage: responseLanguageForEvent(event), Reason: "interactive_cancel"}, true
		}
	}
	if decision, isFound := deterministicConfirmationReplyDecision(event); isFound {
		connectorRuntime.agentKernel.AppendTaskEvent(approval.TaskRun.TaskRunID, "confirmation.reply_classified", marshalConnectorEventBody(map[string]any{
			"messageID":   event.MessageID,
			"route":       decision.Route,
			"approval":    decision.Approval,
			"reason":      decision.Reason,
			"replyPrompt": strings.TrimSpace(event.Prompt),
		}))
		return approval, decision, true
	}
	decision := connectorRuntime.agentKernel.RouteTurn(ctx, agent.AgentRequest{
		RequesterPersonID: personID,
		ConversationID:    event.ConversationID,
		Prompt:            event.Prompt,
		ResponseLanguage:  responseLanguageForEvent(event),
		VisibleContext:    event.Context.ToAgentVisibleContext(),
		PendingConfirmation: agent.PendingConfirmationContext{
			TaskRunID: approval.TaskRun.TaskRunID,
			Prompt:    approval.IntentPrompt,
			Question:  approval.ApprovalQuestion,
		},
		TurnStartedAt: time.Now(),
	})
	connectorRuntime.agentKernel.AppendTaskEvent(approval.TaskRun.TaskRunID, "confirmation.reply_classified", marshalConnectorEventBody(map[string]any{
		"messageID":   event.MessageID,
		"route":       decision.Route,
		"approval":    decision.Approval,
		"reason":      decision.Reason,
		"replyPrompt": strings.TrimSpace(event.Prompt),
	}))
	if decision.Approval != nil && *decision.Approval == agent.ApprovalSignalApprove {
		connectorRuntime.logger.Info("connector."+platform+".confirmation.accepted", slog.String("messageID", event.MessageID), slog.String("taskRunID", approval.TaskRun.TaskRunID))
	}
	return approval, decision, true
}

func deterministicConfirmationReplyDecision(event PlatformInboundEvent) (agent.TurnDecision, bool) {
	switch deterministicConfirmationReplyKind(event.Prompt) {
	case "confirm":
		approvalSignal := agent.ApprovalSignalApprove
		return agent.TurnDecision{Route: agent.TurnRouteContinueTask, Approval: &approvalSignal, Classification: agent.IntakeClassificationBoundedTask, TaskShape: agent.TaskShapeMaintenanceTask, EffortLevel: agent.EffortLevelStandard, ResponseLanguage: responseLanguageForEvent(event), Reason: "deterministic_confirm"}, true
	case "cancel":
		approvalSignal := agent.ApprovalSignalReject
		return agent.TurnDecision{Route: agent.TurnRouteConsume, Approval: &approvalSignal, Classification: agent.IntakeClassificationQuickReply, TaskShape: agent.TaskShapeImmediateReply, EffortLevel: agent.EffortLevelQuick, ResponseLanguage: responseLanguageForEvent(event), Reason: "deterministic_cancel"}, true
	default:
		return agent.TurnDecision{}, false
	}
}

func deterministicConfirmationReplyKind(reply string) string {
	normalizedReply := strings.TrimSpace(strings.ToLower(reply))
	confirmReplies := map[string]bool{
		"ㅇ": true, "응": true, "네": true, "예": true, "확인": true, "승인": true,
		"좋아": true, "그래": true, "진행": true, "진행해": true, "진행해줘": true, "해": true, "해줘": true,
		"approved": true, "approve": true, "confirm": true, "yes": true, "y": true, "ok": true, "okay": true, "go ahead": true,
	}
	cancelReplies := map[string]bool{
		"ㄴ": true, "아니": true, "아니오": true, "취소": true, "취소해": true, "취소해줘": true, "거절": true,
		"하지마": true, "하지 마": true, "안돼": true, "안 돼": true, "멈춰": true,
		"rejected": true, "reject": true, "cancel": true, "no": true, "n": true, "stop": true,
	}
	if confirmReplies[normalizedReply] {
		return "confirm"
	}
	if cancelReplies[normalizedReply] {
		return "cancel"
	}
	return ""
}

func (connectorRuntime *ConnectorRuntime) resolveAskReply(ctx context.Context, platform string, personID string, event PlatformInboundEvent, taskWaitResolution inboundTaskWaitResolution) (PlatformInboundEvent, agent.TurnDecision, bool) {
	pendingInteraction, isFound := connectorRuntime.findPendingAskInteraction(personID, platform, event, taskWaitResolution)
	if !isFound {
		return event, agent.TurnDecision{}, false
	}
	if action, isFound := event.LegacyFields["askAction"].(string); isFound {
		return resolveAskInteractiveReply(event, pendingInteraction, action), askInteractiveTurnDecision(event, pendingInteraction, action), true
	}
	if pendingInteraction.Kind == "ask_input" {
		decision := connectorRuntime.agentKernel.RouteTurn(ctx, agent.AgentRequest{
			RequesterPersonID: personID,
			ConversationID:    event.ConversationID,
			Prompt:            event.Prompt,
			ResponseLanguage:  responseLanguageForEvent(event),
			VisibleContext:    event.Context.ToAgentVisibleContext(),
			PendingInput: agent.PendingInputContext{
				TaskRunID: pendingInteraction.TaskRunID,
				Question:  pendingInteraction.Question,
			},
			TurnStartedAt: time.Now(),
		})
		return event, decision, true
	}
	if pendingInteraction.Kind != "ask_choice_single" && pendingInteraction.Kind != "ask_choice_multiple" {
		return event, agent.TurnDecision{}, false
	}
	if choices, isFound := deterministicChoiceSelections(event.Prompt, pendingInteraction); isFound {
		decision := agent.TurnDecision{
			Route:            agent.TurnRouteContinueTask,
			Classification:   agent.IntakeClassificationBoundedTask,
			TaskShape:        agent.TaskShapeMaintenanceTask,
			EffortLevel:      agent.EffortLevelStandard,
			ResponseLanguage: responseLanguageForEvent(event),
			Reason:           "deterministic_choice_selection",
			Choices:          choices,
		}
		connectorRuntime.agentKernel.AppendTaskEvent(pendingInteraction.TaskRunID, "ask.reply_classified", marshalConnectorEventBody(map[string]any{
			"messageID": event.MessageID,
			"choices":   decision.Choices,
			"route":     decision.Route,
			"reason":    decision.Reason,
		}))
		event.Prompt = resolvedChoicePrompt(pendingInteraction, decision.Choices)
		return event, decision, true
	}
	decision := connectorRuntime.agentKernel.RouteTurn(ctx, agent.AgentRequest{
		RequesterPersonID: personID,
		ConversationID:    event.ConversationID,
		Prompt:            event.Prompt,
		ResponseLanguage:  responseLanguageForEvent(event),
		VisibleContext:    event.Context.ToAgentVisibleContext(),
		PendingChoice: agent.PendingChoiceContext{
			TaskRunID:     pendingInteraction.TaskRunID,
			Question:      pendingInteraction.Question,
			SelectionMode: pendingInteraction.SelectionMode,
			Options:       choiceReplyOptions(pendingInteraction.Options),
		},
		TurnStartedAt: time.Now(),
	})
	connectorRuntime.agentKernel.AppendTaskEvent(pendingInteraction.TaskRunID, "ask.reply_classified", marshalConnectorEventBody(map[string]any{
		"messageID": event.MessageID,
		"choices":   decision.Choices,
		"route":     decision.Route,
		"reason":    decision.Reason,
	}))
	if len(decision.Choices) == 0 {
		return event, decision, true
	}
	event.Prompt = resolvedChoicePrompt(pendingInteraction, decision.Choices)
	return event, decision, true
}

func deterministicChoiceSelections(reply string, interaction AskInteraction) ([]string, bool) {
	choices := []string{}
	seenChoices := map[string]bool{}
	for _, token := range choiceSelectionTokens(reply) {
		choiceKey := choiceKeyForSelectionToken(token, interaction.Options)
		if choiceKey == "" || seenChoices[choiceKey] {
			continue
		}
		seenChoices[choiceKey] = true
		choices = append(choices, choiceKey)
	}
	if len(choices) == 0 {
		return nil, false
	}
	if strings.TrimSpace(interaction.SelectionMode) != "multiple" && len(choices) > 1 {
		return nil, false
	}
	return choices, true
}

func choiceSelectionTokens(reply string) []string {
	tokens := []string{}
	for _, field := range strings.FieldsFunc(strings.TrimSpace(reply), choiceSelectionSeparator) {
		token := leadingChoiceDigits(field)
		if token != "" {
			tokens = append(tokens, token)
		}
	}
	return tokens
}

func choiceSelectionSeparator(character rune) bool {
	switch character {
	case ',', '/', '&', '+', ' ', '\t', '\n', '\r':
		return true
	default:
		return false
	}
}

func leadingChoiceDigits(value string) string {
	builder := strings.Builder{}
	for _, character := range strings.TrimSpace(value) {
		if character < '0' || character > '9' {
			break
		}
		builder.WriteRune(character)
	}
	return builder.String()
}

func choiceKeyForSelectionToken(token string, options []AskChoiceOption) string {
	for index, option := range options {
		if token == strings.TrimSpace(option.Key) || token == fmt.Sprintf("%d", index+1) {
			return strings.TrimSpace(option.Key)
		}
	}
	return ""
}

func askInteractiveTurnDecision(event PlatformInboundEvent, interaction AskInteraction, action string) agent.TurnDecision {
	switch strings.TrimSpace(action) {
	case "choice":
		return agent.TurnDecision{Route: agent.TurnRouteContinueTask, Classification: agent.IntakeClassificationBoundedTask, TaskShape: agent.TaskShapeMaintenanceTask, EffortLevel: agent.EffortLevelStandard, ResponseLanguage: responseLanguageForEvent(event), Reason: "interactive_choice", Choices: []string{strings.TrimSpace(legacyString(event.LegacyFields, "choiceKey"))}}
	case "confirm":
		approvalSignal := agent.ApprovalSignalApprove
		return agent.TurnDecision{Route: agent.TurnRouteContinueTask, Approval: &approvalSignal, Classification: agent.IntakeClassificationBoundedTask, TaskShape: agent.TaskShapeMaintenanceTask, EffortLevel: agent.EffortLevelStandard, ResponseLanguage: responseLanguageForEvent(event), Reason: "interactive_confirm"}
	case "cancel":
		approvalSignal := agent.ApprovalSignalReject
		return agent.TurnDecision{Route: agent.TurnRouteConsume, Approval: &approvalSignal, Classification: agent.IntakeClassificationQuickReply, TaskShape: agent.TaskShapeImmediateReply, EffortLevel: agent.EffortLevelQuick, ResponseLanguage: responseLanguageForEvent(event), Reason: "interactive_cancel"}
	default:
		return agent.TurnDecision{}
	}
}

func resolveAskInteractiveReply(event PlatformInboundEvent, interaction AskInteraction, action string) PlatformInboundEvent {
	switch strings.TrimSpace(action) {
	case "choice":
		choiceKey, _ := event.LegacyFields["choiceKey"].(string)
		event.Prompt = resolvedChoiceKeyPrompt(interaction, choiceKey)
	case "confirm":
		event.Prompt = "approved"
	case "cancel":
		event.Prompt = "rejected"
	}
	return event
}

func choiceReplyOptions(options []AskChoiceOption) []agent.ChoiceReplyOption {
	replyOptions := []agent.ChoiceReplyOption{}
	for _, option := range options {
		replyOptions = append(replyOptions, agent.ChoiceReplyOption{
			Key:        strings.TrimSpace(option.Key),
			Label:      strings.TrimSpace(option.Label),
			ShortLabel: strings.TrimSpace(option.ShortLabel),
			Value:      strings.TrimSpace(option.Value),
		})
	}
	return replyOptions
}

func resolvedChoicePrompt(interaction AskInteraction, keys []string) string {
	values := []string{}
	for _, key := range keys {
		values = append(values, resolvedChoiceKeyText(interaction, key))
	}
	return "User selected: " + strings.Join(trimNonEmptyConnectorStrings(values), ", ")
}

func resolvedChoiceKeyPrompt(interaction AskInteraction, choiceKey string) string {
	return "User selected: " + resolvedChoiceKeyText(interaction, choiceKey)
}

func shouldStopAfterPendingConfirmation(decision agent.TurnDecision) bool {
	if decision.Approval != nil && *decision.Approval == agent.ApprovalSignalApprove {
		return false
	}
	switch decision.Route {
	case agent.TurnRouteAnswerQuestion, agent.TurnRouteStartTask, agent.TurnRouteReviseTask:
		return false
	default:
		return true
	}
}

func precomputedTurnDecisionForLaunch(confirmationDecision agent.TurnDecision, hasConfirmationDecision bool, askDecision agent.TurnDecision, hasAskDecision bool) *agent.TurnDecision {
	if hasConfirmationDecision {
		return &confirmationDecision
	}
	if hasAskDecision {
		return &askDecision
	}
	return nil
}

func (connectorRuntime *ConnectorRuntime) cancelPendingConfirmation(event PlatformInboundEvent, approval pendingApproval, decision agent.TurnDecision) {
	_, _ = connectorRuntime.agentKernel.CancelTask(approval.TaskRun.TaskRunID, approval.TaskRun.RequesterPersonID, "confirmation.replaced")
	connectorRuntime.agentKernel.AppendTaskEvent(approval.TaskRun.TaskRunID, "confirmation.replaced", marshalConnectorEventBody(map[string]string{
		"messageID": event.MessageID,
		"route":     string(decision.Route),
		"reason":    strings.TrimSpace(decision.Reason),
	}))
}

func resolvedChoiceKeyText(interaction AskInteraction, choiceKey string) string {
	normalizedChoiceKey := strings.TrimSpace(choiceKey)
	for _, option := range interaction.Options {
		if strings.TrimSpace(option.Key) != normalizedChoiceKey {
			continue
		}
		value := strings.TrimSpace(option.Value)
		if value == "" {
			value = strings.TrimSpace(option.Label)
		}
		if value == "" {
			value = normalizedChoiceKey
		}
		return normalizedChoiceKey + " / " + value
	}
	return normalizedChoiceKey
}

func askReplyConsumesInteraction(interaction AskInteraction, previousPrompt string, event PlatformInboundEvent, decision agent.TurnDecision, hasDecision bool) bool {
	if !hasDecision {
		return false
	}
	if _, isFound := event.LegacyFields["askAction"].(string); isFound {
		return true
	}
	switch strings.TrimSpace(interaction.Kind) {
	case "ask_choice_single", "ask_choice_multiple":
		if len(decision.Choices) > 0 && strings.TrimSpace(event.Prompt) != strings.TrimSpace(previousPrompt) {
			return true
		}
		return len(decision.Choices) == 0 && (decision.Route == agent.TurnRouteStartTask || decision.Route == agent.TurnRouteReviseTask)
	case "ask_input":
		return decision.Route == agent.TurnRouteContinueTask || decision.Route == agent.TurnRouteReviseTask || decision.Route == agent.TurnRouteStartTask
	default:
		return strings.TrimSpace(event.Prompt) != strings.TrimSpace(previousPrompt)
	}
}

func (connectorRuntime *ConnectorRuntime) appendAskResolvedEvent(interaction AskInteraction, event PlatformInboundEvent, decision agent.TurnDecision) {
	connectorRuntime.agentKernel.AppendTaskEvent(interaction.TaskRunID, "ask.resolved", marshalConnectorEventBody(map[string]any{
		"interactionID": strings.TrimSpace(interaction.InteractionID),
		"kind":          strings.TrimSpace(interaction.Kind),
		"messageID":     strings.TrimSpace(event.MessageID),
		"choices":       append([]string{}, decision.Choices...),
		"route":         strings.TrimSpace(string(decision.Route)),
		"reason":        strings.TrimSpace(decision.Reason),
	}))
}

func trimNonEmptyConnectorStrings(values []string) []string {
	trimmedValues := []string{}
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			trimmedValues = append(trimmedValues, trimmedValue)
		}
	}
	return trimmedValues
}

func (connectorRuntime *ConnectorRuntime) resolveAskInteractionMessage(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent, taskRunID string, interaction AskInteraction) {
	if strings.TrimSpace(interaction.TargetPlatformUserID) != "" {
		return
	}
	if legacyBool(event.LegacyFields, "ephemeralAsk") {
		return
	}
	resolver, isSupported := adapter.(InteractionResolvingAdapter)
	if !isSupported {
		return
	}
	dispatchID := firstNonEmptyString(legacyString(event.LegacyFields, "postID"), latestAskPromptDispatchID(connectorRuntime.agentKernel.ListTaskEvent(taskRunID)))
	if dispatchID == "" {
		return
	}
	resolution := InteractionResolution{
		Platform:       event.Platform,
		ConversationID: event.ConversationID,
		ReplyTargetID:  event.ReplyTargetID,
		DispatchID:     dispatchID,
		TaskRunID:      taskRunID,
		InteractionID:  interaction.InteractionID,
	}
	if errorValue := resolver.ResolveInteraction(ctx, resolution); errorValue != nil {
		connectorRuntime.agentKernel.AppendTaskEvent(taskRunID, "ask.interaction_resolve_failed", marshalConnectorEventBody(map[string]string{
			"dispatchID": dispatchID,
			"error":      errorValue.Error(),
		}))
		return
	}
	connectorRuntime.agentKernel.AppendTaskEvent(taskRunID, "ask.interaction_resolved", marshalConnectorEventBody(map[string]string{
		"dispatchID": dispatchID,
	}))
}

func (connectorRuntime *ConnectorRuntime) resolveInboundTaskWait(personID string, platform string, event PlatformInboundEvent) inboundTaskWaitResolution {
	if connectorRuntime.taskWaitTokenRepository == nil {
		return inboundTaskWaitResolution{}
	}
	if resolution := connectorRuntime.findInboundTaskWaitByPayload(personID, event); resolution.HasTaskWaitToken {
		return resolution
	}
	if resolution := connectorRuntime.findInboundTaskWaitByReplyTarget(personID, platform, event); resolution.HasTaskWaitToken {
		return resolution
	}
	if resolution := connectorRuntime.findInboundTaskWaitByThreadRoot(personID, platform, event); resolution.HasTaskWaitToken {
		return resolution
	}
	if resolution := connectorRuntime.findInboundTaskWaitByDispatchID(personID, platform, event); resolution.HasTaskWaitToken {
		return resolution
	}
	return connectorRuntime.findSingleInboundTaskWait(personID, platform, event)
}

func (connectorRuntime *ConnectorRuntime) findInboundTaskWaitByPayload(personID string, event PlatformInboundEvent) inboundTaskWaitResolution {
	if waitID := firstNonEmptyString(legacyString(event.LegacyFields, "waitID"), legacyString(event.LegacyFields, "wait_id")); waitID != "" {
		return connectorRuntime.findOpenTaskWait(func() (task.TaskWaitToken, bool, error) {
			return connectorRuntime.taskWaitTokenRepository.FindOpenByWaitID(waitID)
		}, personID, "payload_wait_id")
	}
	taskRunID := legacyString(event.LegacyFields, "taskRunID")
	interactionID := legacyString(event.LegacyFields, "interactionID")
	if taskRunID == "" || interactionID == "" {
		return inboundTaskWaitResolution{}
	}
	return connectorRuntime.findOpenTaskWait(func() (task.TaskWaitToken, bool, error) {
		return connectorRuntime.taskWaitTokenRepository.FindOpenByPersonTaskRunAndInteraction(personID, taskRunID, interactionID)
	}, personID, "payload_task_interaction")
}

func (connectorRuntime *ConnectorRuntime) findInboundTaskWaitByReplyTarget(personID string, platform string, event PlatformInboundEvent) inboundTaskWaitResolution {
	replyTargetID := strings.TrimSpace(event.ReplyTargetID)
	if replyTargetID == "" {
		return inboundTaskWaitResolution{}
	}
	return connectorRuntime.findOpenTaskWait(func() (task.TaskWaitToken, bool, error) {
		return connectorRuntime.taskWaitTokenRepository.FindOpenByPersonConversationAndReplyTarget(personID, platform, event.ConversationID, replyTargetID)
	}, personID, "reply_target_id")
}

func (connectorRuntime *ConnectorRuntime) findInboundTaskWaitByThreadRoot(personID string, platform string, event PlatformInboundEvent) inboundTaskWaitResolution {
	threadRootID := eventThreadRootID(event)
	if threadRootID == "" {
		return inboundTaskWaitResolution{}
	}
	return connectorRuntime.findOpenTaskWait(func() (task.TaskWaitToken, bool, error) {
		return connectorRuntime.taskWaitTokenRepository.FindOpenByPersonConversationAndThreadRoot(personID, platform, event.ConversationID, threadRootID)
	}, personID, "thread_root_id")
}

func (connectorRuntime *ConnectorRuntime) findInboundTaskWaitByDispatchID(personID string, platform string, event PlatformInboundEvent) inboundTaskWaitResolution {
	dispatchID := firstNonEmptyString(legacyString(event.LegacyFields, "dispatchID"), legacyString(event.LegacyFields, "postID"))
	if dispatchID == "" {
		return inboundTaskWaitResolution{}
	}
	return connectorRuntime.findOpenTaskWait(func() (task.TaskWaitToken, bool, error) {
		return connectorRuntime.taskWaitTokenRepository.FindOpenByPersonConversationAndDispatchID(personID, platform, event.ConversationID, dispatchID)
	}, personID, "dispatch_id")
}

func (connectorRuntime *ConnectorRuntime) findSingleInboundTaskWait(personID string, platform string, event PlatformInboundEvent) inboundTaskWaitResolution {
	taskWaitTokens, errorValue := connectorRuntime.taskWaitTokenRepository.FindOpenByPersonAndConversation(personID, platform, event.ConversationID)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".wait.lookup_failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		return inboundTaskWaitResolution{}
	}
	switch len(taskWaitTokens) {
	case 0:
		return inboundTaskWaitResolution{}
	case 1:
		return inboundTaskWaitResolution{TaskWaitToken: taskWaitTokens[0], HasTaskWaitToken: true, Reason: "single_open_wait"}
	default:
		return inboundTaskWaitResolution{IsAmbiguous: true, AmbiguousTaskWaits: taskWaitTokens, Reason: "multiple_open_waits"}
	}
}

func (connectorRuntime *ConnectorRuntime) findOpenTaskWait(find func() (task.TaskWaitToken, bool, error), personID string, reason string) inboundTaskWaitResolution {
	taskWaitToken, isFound, errorValue := find()
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector.wait.lookup_failed", slog.String("reason", reason), slog.String("error", errorValue.Error()))
		return inboundTaskWaitResolution{}
	}
	if !isFound || taskWaitToken.PersonID != strings.TrimSpace(personID) {
		return inboundTaskWaitResolution{}
	}
	return inboundTaskWaitResolution{TaskWaitToken: taskWaitToken, HasTaskWaitToken: true, Reason: reason}
}

func eventThreadRootID(event PlatformInboundEvent) string {
	threadRootID := firstNonEmptyString(
		legacyString(event.LegacyFields, "threadRootID"),
		legacyString(event.LegacyFields, "thread_root_id"),
		legacyString(event.LegacyFields, "rootMessageID"),
		legacyString(event.LegacyFields, "rootMessageId"),
		legacyString(event.LegacyFields, "rootID"),
		legacyString(event.LegacyFields, "root_id"),
	)
	if threadRootID != "" {
		return threadRootID
	}
	if eventIsThreadReply(event) {
		return strings.TrimSpace(event.ReplyTargetID)
	}
	return ""
}

func (connectorRuntime *ConnectorRuntime) resolveTaskWaitToken(taskWaitResolution inboundTaskWaitResolution) {
	if connectorRuntime.taskWaitTokenRepository == nil || !taskWaitResolution.HasTaskWaitToken {
		return
	}
	if errorValue := connectorRuntime.taskWaitTokenRepository.ResolveTaskWait(taskWaitResolution.TaskWaitToken.WaitID, time.Now().UTC()); errorValue != nil {
		connectorRuntime.logger.Warn("connector.wait.resolve_failed", slog.String("waitID", taskWaitResolution.TaskWaitToken.WaitID), slog.String("error", errorValue.Error()))
	}
}

func (connectorRuntime *ConnectorRuntime) handleAmbiguousTaskWait(
	ctx context.Context,
	platform string,
	adapter PlatformAdapter,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	personID string,
	requesterEmail string,
	personAccess policy.PersonAccess,
	taskWaitResolution inboundTaskWaitResolution,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (ConnectorRuntimeResult, error) {
	turnDecision := ambiguousTaskWaitTurnDecision(taskWaitResolution.AmbiguousTaskWaits, responseLanguageForEvent(event))
	conversationTurn := ConversationTurn{
		Platform:                  platform,
		Adapter:                   adapter,
		Event:                     event,
		ReplyTarget:               replyTarget,
		RequesterPersonID:         personID,
		RequesterEmail:            requesterEmail,
		PersonAccess:              personAccess,
		PrecomputedTurnDecision:   &turnDecision,
		CheckpointSender:          connectorRuntime.checkpointSenderForTurn(platform, event, replyTarget, sendReply),
		AccessibleConversationIDs: []string{event.ConversationID},
	}
	launchResult, errorValue := connectorRuntime.currentTaskLauncher().Launch(ctx, connectorRuntime.buildTaskLaunchRequest(conversationTurn))
	if errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}
	return connectorRuntime.dispatchTaskReply(ctx, platform, adapter, event, replyTarget, launchResult.TurnResult, false, sendReply)
}

func ambiguousTaskWaitTurnDecision(taskWaitTokens []task.TaskWaitToken, responseLanguage string) agent.TurnDecision {
	return agent.TurnDecision{
		Route:                  agent.TurnRouteClarify,
		Classification:         agent.IntakeClassificationNeedsConfirmation,
		TaskShape:              agent.TaskShapeApprovalGatedTask,
		EffortLevel:            agent.EffortLevelStandard,
		ResponseLanguage:       responseLanguage,
		Reason:                 "ambiguous_wait_resolution",
		ClarificationOptions:   taskWaitClarificationOptions(taskWaitTokens),
		ExpectedResults:        []agent.ExpectedResult{{ID: "wait-disambiguation", Type: "message", Description: "ask.choice", Required: true, AcceptanceHints: []string{"ask.choice"}}},
		RequestedOutputFormats: nil,
	}
}

func taskWaitClarificationOptions(taskWaitTokens []task.TaskWaitToken) []agent.ClarificationOption {
	options := []agent.ClarificationOption{}
	for index, taskWaitToken := range taskWaitTokens {
		taskRunLabel := strings.TrimSpace(taskWaitToken.TaskRunID)
		if len(taskRunLabel) > 8 {
			taskRunLabel = taskRunLabel[:8]
		}
		options = append(options, agent.ClarificationOption{
			Key:   string(rune('A' + index)),
			Label: taskRunLabel,
			Value: taskWaitToken.WaitID,
		})
	}
	return options
}

func (connectorRuntime *ConnectorRuntime) findPendingAskInteraction(personID string, _ string, event PlatformInboundEvent, taskWaitResolution inboundTaskWaitResolution) (AskInteraction, bool) {
	if taskWaitResolution.HasTaskWaitToken {
		return connectorRuntime.findPendingAskInteractionByTaskRunID(taskWaitResolution.TaskWaitToken.TaskRunID)
	}
	taskRuns := connectorRuntime.agentKernel.ListTaskRunByPersonID(personID)
	var selectedInteraction AskInteraction
	var selectedTaskRun task.TaskRun
	isSelected := false
	for _, taskRun := range taskRuns {
		if taskRun.Status != task.TaskStatusWaitingUserInput {
			continue
		}
		if taskRun.OriginConversationID != event.ConversationID {
			continue
		}
		interaction, isFound := latestAskInteraction(taskRun.TaskRunID, connectorRuntime.agentKernel.ListTaskEvent(taskRun.TaskRunID))
		if !isFound {
			continue
		}
		if isSelected && !taskRun.UpdatedAt.After(selectedTaskRun.UpdatedAt) {
			continue
		}
		selectedTaskRun = taskRun
		selectedInteraction = interaction
		isSelected = true
	}
	return selectedInteraction, isSelected
}

func (connectorRuntime *ConnectorRuntime) findPendingAskInteractionByTaskRunID(taskRunID string) (AskInteraction, bool) {
	taskRun, isFound := connectorRuntime.agentKernel.FindTaskRun(taskRunID)
	if !isFound || taskRun.Status != task.TaskStatusWaitingUserInput {
		return AskInteraction{}, false
	}
	return latestAskInteraction(taskRun.TaskRunID, connectorRuntime.agentKernel.ListTaskEvent(taskRun.TaskRunID))
}

func (connectorRuntime *ConnectorRuntime) findPendingApproval(personID string, _ string, event PlatformInboundEvent, taskWaitResolution inboundTaskWaitResolution) (pendingApproval, bool) {
	if taskWaitResolution.HasTaskWaitToken {
		return connectorRuntime.findPendingApprovalByTaskRunID(taskWaitResolution.TaskWaitToken.TaskRunID)
	}
	taskRuns := connectorRuntime.agentKernel.ListTaskRunByPersonID(personID)
	var selectedTaskRun task.TaskRun
	isSelected := false
	for _, taskRun := range taskRuns {
		if taskRun.Status != task.TaskStatusWaitingApproval {
			continue
		}
		if taskRun.OriginConversationID != event.ConversationID {
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
	return connectorRuntime.pendingApprovalForTaskRun(selectedTaskRun), true
}

func (connectorRuntime *ConnectorRuntime) findPendingApprovalByTaskRunID(taskRunID string) (pendingApproval, bool) {
	taskRun, isFound := connectorRuntime.agentKernel.FindTaskRun(taskRunID)
	if !isFound || taskRun.Status != task.TaskStatusWaitingApproval {
		return pendingApproval{}, false
	}
	return connectorRuntime.pendingApprovalForTaskRun(taskRun), true
}

func (connectorRuntime *ConnectorRuntime) pendingApprovalForTaskRun(selectedTaskRun task.TaskRun) pendingApproval {
	taskEvents := connectorRuntime.agentKernel.ListTaskEvent(selectedTaskRun.TaskRunID)
	approvalQuestion := latestApprovalQuestion(taskEvents)
	responseLanguage := latestApprovalResponseLanguage(taskEvents)
	continuationInstruction := latestConfirmationContinuationInstruction(taskEvents)
	activeGoal := latestActiveGoal(taskEvents)
	return pendingApproval{
		TaskRun:                 selectedTaskRun,
		IntentPrompt:            pendingApprovalIntentPrompt(selectedTaskRun.Prompt, approvalQuestion),
		ApprovalQuestion:        approvalQuestion,
		ResponseLanguage:        responseLanguage,
		ContinuationInstruction: continuationInstruction,
		ActiveGoal:              activeGoal,
	}
}

func (connectorRuntime *ConnectorRuntime) findActiveGoal(personID string, _ string, event PlatformInboundEvent, taskWaitResolution inboundTaskWaitResolution) (agent.ActiveGoal, bool) {
	if taskWaitResolution.HasTaskWaitToken {
		return connectorRuntime.findActiveGoalByTaskRunID(taskWaitResolution.TaskWaitToken.TaskRunID)
	}
	taskRuns := connectorRuntime.agentKernel.ListTaskRunByPersonID(personID)
	var selectedTaskRun task.TaskRun
	isSelected := false
	for _, taskRun := range taskRuns {
		taskEvents := connectorRuntime.agentKernel.ListTaskEvent(taskRun.TaskRunID)
		if !taskRunCanContinueGoal(taskRun, taskEvents) {
			continue
		}
		if taskRun.OriginConversationID != event.ConversationID {
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
		return agent.ActiveGoal{}, false
	}
	return connectorRuntime.activeGoalForTaskRun(selectedTaskRun), true
}

func (connectorRuntime *ConnectorRuntime) findActiveGoalByTaskRunID(taskRunID string) (agent.ActiveGoal, bool) {
	taskRun, isFound := connectorRuntime.agentKernel.FindTaskRun(taskRunID)
	if !isFound {
		return agent.ActiveGoal{}, false
	}
	taskEvents := connectorRuntime.agentKernel.ListTaskEvent(taskRun.TaskRunID)
	if !taskRunCanContinueGoal(taskRun, taskEvents) {
		return agent.ActiveGoal{}, false
	}
	return connectorRuntime.activeGoalForTaskRun(taskRun), true
}

func (connectorRuntime *ConnectorRuntime) activeGoalForTaskRun(selectedTaskRun task.TaskRun) agent.ActiveGoal {
	taskEvents := connectorRuntime.agentKernel.ListTaskEvent(selectedTaskRun.TaskRunID)
	activeGoal := latestActiveGoal(taskEvents)
	if strings.TrimSpace(activeGoal.TaskRunID) == "" {
		activeGoal.TaskRunID = selectedTaskRun.TaskRunID
	}
	if strings.TrimSpace(activeGoal.GoalID) == "" {
		activeGoal.GoalID = selectedTaskRun.TaskRunID
	}
	if strings.TrimSpace(activeGoal.OriginalInstruction) == "" {
		activeGoal.OriginalInstruction = selectedTaskRun.Prompt
	}
	if activeGoal.Status == "" {
		activeGoal.Status = activeGoalStatusForTaskRun(selectedTaskRun)
	}
	return activeGoal
}

func taskRunCanContinueGoal(taskRun task.TaskRun, taskEvents []task.TaskEvent) bool {
	switch taskRun.Status {
	case task.TaskStatusWaitingUserInput, task.TaskStatusWaitingApproval:
		return true
	case task.TaskStatusBlocked:
		return taskRunHasLimitStop(taskEvents)
	default:
		return false
	}
}

func taskRunHasLimitStop(taskEvents []task.TaskEvent) bool {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name == "agent.limit_stop" {
			return true
		}
	}
	return false
}

func latestActiveGoal(taskEvents []task.TaskEvent) agent.ActiveGoal {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if !strings.HasPrefix(taskEvent.Name, "agent.goal.") {
			continue
		}
		var activeGoal agent.ActiveGoal
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &activeGoal); errorValue != nil {
			continue
		}
		return activeGoal
	}
	return activeGoalFromConfirmationPlan(taskEvents)
}

func activeGoalFromConfirmationPlan(taskEvents []task.TaskEvent) agent.ActiveGoal {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name != "confirmation.plan_created" {
			continue
		}
		var executionPlan agent.ExecutionPlan
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &executionPlan); errorValue != nil {
			continue
		}
		return agent.ActiveGoal{
			OriginalInstruction: strings.TrimSpace(executionPlan.OriginalInstruction),
			CurrentObjective:    strings.TrimSpace(executionPlan.Summary),
			MissingInformation:  append([]string{}, executionPlan.MissingInformation...),
			Status:              agent.ActiveGoalStatusWaitingUserInput,
		}
	}
	return agent.ActiveGoal{}
}

func activeGoalStatusForTaskRun(taskRun task.TaskRun) agent.ActiveGoalStatus {
	switch taskRun.Status {
	case task.TaskStatusWaitingApproval:
		return agent.ActiveGoalStatusWaitingApproval
	case task.TaskStatusWaitingUserInput:
		return agent.ActiveGoalStatusWaitingUserInput
	case task.TaskStatusBlocked:
		return agent.ActiveGoalStatusBlocked
	default:
		return agent.ActiveGoalStatusActive
	}
}

func approvedContinuationEvent(event PlatformInboundEvent, approval pendingApproval) PlatformInboundEvent {
	event.ResponseLanguage = agent.ResolveResponseLanguage(event.ResponseLanguage, approval.ResponseLanguage)
	return event
}

func pendingApprovalActiveGoal(approval pendingApproval, approvalReply string) agent.ActiveGoal {
	activeGoal := approval.ActiveGoal
	activeGoal.GoalID = firstNonEmptyString(activeGoal.GoalID, approval.TaskRun.TaskRunID)
	activeGoal.TaskRunID = firstNonEmptyString(activeGoal.TaskRunID, approval.TaskRun.TaskRunID)
	activeGoal.OriginalInstruction = firstNonEmptyString(activeGoal.OriginalInstruction, approval.IntentPrompt)
	approvedAction := firstNonEmptyString(activeGoal.CurrentObjective, approval.ContinuationInstruction, approval.IntentPrompt)
	executionDirective := "The user already approved this action; perform it now and do not call ask.confirm again."
	activeGoal.CurrentObjective = strings.TrimSpace(approvedAction + " " + executionDirective)
	activeGoal.KnownContext = append(activeGoal.KnownContext, "The user approved the pending action in the latest message: "+strings.TrimSpace(approvalReply))
	activeGoal.Status = agent.ActiveGoalStatusActive
	return activeGoal
}

func activeGoalForLaunch(activeGoal agent.ActiveGoal, hasActiveGoal bool) agent.ActiveGoal {
	if !hasActiveGoal {
		return agent.ActiveGoal{}
	}
	return activeGoal
}

func pendingConfirmationTaskRunID(approval pendingApproval, isApprovalContinuation bool) string {
	if !isApprovalContinuation {
		return ""
	}
	return strings.TrimSpace(approval.TaskRun.TaskRunID)
}

func existingGoalTaskRunID(approval pendingApproval, isApprovalContinuation bool, activeGoal agent.ActiveGoal, hasActiveGoal bool) string {
	if isApprovalContinuation {
		return pendingConfirmationTaskRunID(approval, true)
	}
	if !hasActiveGoal {
		return ""
	}
	return strings.TrimSpace(activeGoal.TaskRunID)
}

func (connectorRuntime *ConnectorRuntime) handleRejectedConfirmation(ctx context.Context, platform string, adapter PlatformAdapter, event PlatformInboundEvent, replyTarget ReplyTarget, approval pendingApproval, decision agent.ConfirmationReplyDecision, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) (ConnectorRuntimeResult, error) {
	_, _ = connectorRuntime.agentKernel.CancelTask(approval.TaskRun.TaskRunID, approval.TaskRun.RequesterPersonID, "confirmation.rejected")
	connectorRuntime.agentKernel.AppendTaskEvent(approval.TaskRun.TaskRunID, "confirmation.rejected", marshalConnectorEventBody(map[string]string{
		"messageID": event.MessageID,
		"reason":    decision.Reason,
	}))
	reply, errorValue := connectorRuntime.agentKernel.GenerateReply(ctx, rejectedConfirmationReplyPrompt(event.Prompt, approval.ResponseLanguage))
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".confirmation.reject_reply_failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", approval.TaskRun.TaskRunID), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: approval.TaskRun.TaskRunID, Reason: "confirmation_rejected"}, nil
	}
	dispatchID, errorValue := sendReply(ctx, replyTarget, OutboundReply{Message: reply})
	if errorValue != nil {
		connectorRuntime.logger.Error("connector."+platform+".outbound.failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", approval.TaskRun.TaskRunID), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: approval.TaskRun.TaskRunID, Reason: "reply_failed"}, nil
	}
	connectorRuntime.logger.Info("connector."+adapter.Name()+".confirmation.rejected", slog.String("messageID", event.MessageID), slog.String("taskRunID", approval.TaskRun.TaskRunID), slog.String("replyDispatchID", dispatchID))
	return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: approval.TaskRun.TaskRunID, Reason: "confirmation_rejected", ReplyDispatchID: dispatchID}, nil
}

func (connectorRuntime *ConnectorRuntime) handlePendingConfirmationQuestion(ctx context.Context, platform string, adapter PlatformAdapter, event PlatformInboundEvent, replyTarget ReplyTarget, approval pendingApproval, decision agent.TurnDecision, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) (ConnectorRuntimeResult, error) {
	connectorRuntime.cancelPendingConfirmation(event, approval, decision)
	reply, errorValue := connectorRuntime.agentKernel.GenerateReplyWithContext(ctx, event.Prompt, event.Context.ToAgentVisibleContext(), nil)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".confirmation.question_reply_failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", approval.TaskRun.TaskRunID), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: approval.TaskRun.TaskRunID, Reason: "confirmation_question"}, nil
	}
	dispatchID, errorValue := sendReply(ctx, replyTarget, OutboundReply{Message: reply})
	if errorValue != nil {
		connectorRuntime.logger.Error("connector."+platform+".outbound.failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", approval.TaskRun.TaskRunID), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: approval.TaskRun.TaskRunID, Reason: "reply_failed"}, nil
	}
	connectorRuntime.logger.Info("connector."+adapter.Name()+".confirmation.question_answered", slog.String("messageID", event.MessageID), slog.String("taskRunID", approval.TaskRun.TaskRunID), slog.String("replyDispatchID", dispatchID))
	return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: approval.TaskRun.TaskRunID, Reason: "confirmation_question", ReplyDispatchID: dispatchID}, nil
}

func rejectedConfirmationReplyPrompt(reply string, responseLanguage string) string {
	return strings.Join([]string{
		connectorResponseLanguageInstruction(responseLanguage),
		"The user rejected a pending confirmation. Write one brief user-facing reply saying the pending action has been cancelled.",
		"Latest user reply: " + strings.TrimSpace(reply),
	}, "\n")
}

func connectorResponseLanguageInstruction(responseLanguage string) string {
	if agent.ResolveResponseLanguage(responseLanguage) == agent.ResponseLanguageEnglish {
		return "Write in English."
	}
	return "Write in Korean."
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
		if taskEvent.Name != "confirmation.requested" {
			continue
		}
		var approvalRequest struct {
			UserFacingMessage string `json:"userFacingMessage"`
			Message           string `json:"message"`
		}
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &approvalRequest); errorValue != nil {
			continue
		}
		question := firstNonEmptyString(approvalRequest.UserFacingMessage, approvalRequest.Message)
		if strings.TrimSpace(question) != "" {
			return strings.TrimSpace(question)
		}
	}
	return ""
}

func latestApprovalResponseLanguage(taskEvents []task.TaskEvent) string {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name != "confirmation.requested" {
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

func latestAskInteraction(taskRunID string, taskEvents []task.TaskEvent) (AskInteraction, bool) {
	resolvedInteractionIDs := map[string]bool{}
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name == "ask.resolved" {
			interactionID := askResolvedInteractionID(taskEvent)
			if interactionID != "" {
				resolvedInteractionIDs[interactionID] = true
			}
			continue
		}
		if taskEvent.Name != "ask.requested" {
			continue
		}
		var interaction AskInteraction
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &interaction); errorValue != nil {
			continue
		}
		interaction.TaskRunID = firstNonEmptyString(interaction.TaskRunID, taskRunID)
		interaction.InteractionID = firstNonEmptyString(interaction.InteractionID, taskEvent.TaskEventID)
		if resolvedInteractionIDs[strings.TrimSpace(interaction.InteractionID)] {
			continue
		}
		interaction.Kind = normalizedAskInteractionKind(interaction.Kind)
		if strings.TrimSpace(interaction.Question) == "" {
			interaction.Question = strings.TrimSpace(interaction.Message)
		}
		if strings.TrimSpace(interaction.Message) == "" {
			interaction.Message = strings.TrimSpace(interaction.Question)
		}
		if strings.TrimSpace(interaction.Kind) != "" {
			return interaction, true
		}
	}
	return AskInteraction{}, false
}

func askResolvedInteractionID(taskEvent task.TaskEvent) string {
	var resolution struct {
		InteractionID string `json:"interactionID"`
	}
	if errorValue := json.Unmarshal([]byte(taskEvent.Body), &resolution); errorValue != nil {
		return ""
	}
	return strings.TrimSpace(resolution.InteractionID)
}

func latestAskInteractionID(taskEvents []task.TaskEvent) string {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name == "ask.requested" {
			return strings.TrimSpace(taskEvent.TaskEventID)
		}
	}
	return ""
}

func latestAskPromptDispatchID(taskEvents []task.TaskEvent) string {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name != "connector.reply.sent" {
			continue
		}
		var replyEvent struct {
			ReplyKind  string `json:"replyKind"`
			DispatchID string `json:"dispatchID"`
		}
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &replyEvent); errorValue != nil {
			continue
		}
		if strings.TrimSpace(replyEvent.ReplyKind) == connectorReplyKindUserNotice && strings.TrimSpace(replyEvent.DispatchID) != "" {
			return strings.TrimSpace(replyEvent.DispatchID)
		}
	}
	return ""
}

func legacyString(fields map[string]interface{}, key string) string {
	value, _ := fields[key].(string)
	return strings.TrimSpace(value)
}

func legacyBool(fields map[string]interface{}, key string) bool {
	value, _ := fields[key].(bool)
	return value
}

func normalizedAskInteractionKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case "confirm":
		return "ask_confirm"
	case "choice_single":
		return "ask_choice_single"
	case "choice_multiple":
		return "ask_choice_multiple"
	case "input":
		return "ask_input"
	default:
		return strings.TrimSpace(kind)
	}
}

func latestConfirmationContinuationInstruction(taskEvents []task.TaskEvent) string {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name != "confirmation.requested" {
			continue
		}
		var request struct {
			ContinuationInstruction string `json:"continuationInstruction"`
		}
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &request); errorValue != nil {
			continue
		}
		if instruction := strings.TrimSpace(request.ContinuationInstruction); instruction != "" {
			return instruction
		}
	}
	return ""
}

func (connectorRuntime *ConnectorRuntime) completeApprovedPendingTask(pendingTaskRun task.TaskRun, continuationTaskRunID string, finishMessage string) {
	result := strings.TrimSpace(finishMessage)
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

func connectorReplyEventBody(event PlatformInboundEvent, reply OutboundReply, outboxID string, dispatchID string, reason string) map[string]string {
	return map[string]string{
		"taskRunID":  strings.TrimSpace(reply.TaskRunID),
		"replyKind":  strings.TrimSpace(reply.ReplyKind),
		"outboxID":   strings.TrimSpace(outboxID),
		"dispatchID": strings.TrimSpace(dispatchID),
		"messageID":  strings.TrimSpace(event.MessageID),
		"reason":     strings.TrimSpace(reason),
	}
}

func (connectorRuntime *ConnectorRuntime) recordTaskWaitTokenForReply(platform string, event PlatformInboundEvent, replyTarget ReplyTarget, reply OutboundReply, dispatchID string) {
	if connectorRuntime.taskWaitTokenRepository == nil {
		return
	}
	taskRunID := strings.TrimSpace(reply.TaskRunID)
	if taskRunID == "" || reply.ReplyKind != connectorReplyKindUserNotice {
		return
	}
	taskRun, isFound := connectorRuntime.agentKernel.FindTaskRun(taskRunID)
	if !isFound || !taskRunCanContinueGoal(taskRun, connectorRuntime.agentKernel.ListTaskEvent(taskRunID)) {
		return
	}
	taskWaitToken := connectorRuntime.taskWaitTokenForReply(platform, event, replyTarget, reply, dispatchID, taskRun)
	if taskWaitToken.Kind == "" {
		return
	}
	if errorValue := connectorRuntime.taskWaitTokenRepository.InsertTaskWaitToken(taskWaitToken); errorValue != nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, "task.wait.persist_failed", connectorReplyEventBody(event, reply, "", dispatchID, errorValue.Error()))
		connectorRuntime.logger.Warn("connector."+platform+".wait.persist_failed", slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
	}
}

func (connectorRuntime *ConnectorRuntime) taskWaitTokenForReply(platform string, event PlatformInboundEvent, replyTarget ReplyTarget, reply OutboundReply, dispatchID string, taskRun task.TaskRun) task.TaskWaitToken {
	now := time.Now().UTC()
	interactionID := replyInteractionID(reply, connectorRuntime.agentKernel.ListTaskEvent(taskRun.TaskRunID))
	return task.TaskWaitToken{
		WaitID:         taskWaitID(taskRun.TaskRunID, interactionID, dispatchID),
		TaskRunID:      taskRun.TaskRunID,
		PersonID:       taskRun.RequesterPersonID,
		Platform:       strings.TrimSpace(platform),
		ConversationID: firstNonEmptyString(event.ConversationID, replyTarget.ConversationID, taskRun.OriginConversationID),
		ReplyTargetID:  firstNonEmptyString(dispatchID, replyTarget.ReplyTargetID, taskRun.OriginReplyTargetID),
		ThreadRootID:   firstNonEmptyString(eventThreadRootID(event), replyTarget.ReplyTargetID, taskRun.OriginReplyTargetID),
		DispatchID:     strings.TrimSpace(dispatchID),
		InteractionID:  interactionID,
		Kind:           taskWaitKind(reply, taskRun),
		State:          "open",
		ExpiresAt:      now.Add(24 * time.Hour),
		CreatedAt:      now,
	}
}

func taskWaitID(taskRunID string, interactionID string, dispatchID string) string {
	return strings.Join([]string{
		"wait",
		strings.TrimSpace(taskRunID),
		firstNonEmptyString(interactionID, "interaction"),
		firstNonEmptyString(dispatchID, "dispatch"),
	}, ":")
}

func replyInteractionID(reply OutboundReply, taskEvents []task.TaskEvent) string {
	if reply.Interaction != nil {
		return strings.TrimSpace(reply.Interaction.InteractionID)
	}
	return latestAskInteractionID(taskEvents)
}

func taskWaitKind(reply OutboundReply, taskRun task.TaskRun) string {
	if taskRun.Status == task.TaskStatusWaitingApproval {
		return "approval"
	}
	if reply.Interaction == nil {
		if taskRun.Status == task.TaskStatusWaitingUserInput {
			return "input"
		}
		return ""
	}
	switch normalizedAskInteractionKind(reply.Interaction.Kind) {
	case "ask_confirm":
		return "approval"
	case "ask_choice_single", "ask_choice_multiple":
		return "choice"
	case "ask_input":
		return "input"
	default:
		return ""
	}
}

func (connectorRuntime *ConnectorRuntime) appendConnectorReplyEvent(taskRunID string, name string, body map[string]string) {
	if strings.TrimSpace(taskRunID) == "" {
		return
	}
	connectorRuntime.agentKernel.AppendTaskEvent(taskRunID, name, marshalConnectorEventBody(body))
}

func (connectorRuntime *ConnectorRuntime) sendCheckpointReply(ctx context.Context, platform string, event PlatformInboundEvent, replyTarget ReplyTarget, checkpoint agent.AgentCheckpoint, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) error {
	message := strings.TrimSpace(checkpoint.Message)
	taskRunID := strings.TrimSpace(checkpoint.TaskRunID)
	reply := OutboundReply{
		Message:         message,
		TaskRunID:       taskRunID,
		ReplyKind:       connectorReplyKindCheckpoint,
		EphemeralUserID: strings.TrimSpace(event.SenderID),
	}
	if message == "" {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, "connector.reply.suppressed", connectorReplyEventBody(event, reply, "", "", "missing_checkpoint_message"))
		return errors.New("missing checkpoint message")
	}
	if errorValue := agent.ValidateUserNoticeDelivery(message); errorValue != nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, "connector.reply.suppressed", connectorReplyEventBody(event, reply, "", "", "invalid_checkpoint_message"))
		return errorValue
	}
	dispatchID, errorValue := sendReply(ctx, replyTarget, reply)
	if errorValue != nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, "connector.reply.failed", connectorReplyEventBody(event, reply, "", "", errorValue.Error()))
		connectorRuntime.logger.Error("connector."+platform+".checkpoint.failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
		return errorValue
	}
	if connectorRuntime.outboxRepository() == nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, "connector.reply.sent", connectorReplyEventBody(event, reply, "", dispatchID, ""))
	}
	connectorRuntime.logger.Info("connector."+platform+".checkpoint.sent", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("replyDispatchID", dispatchID))
	return nil
}

func (connectorRuntime *ConnectorRuntime) sendUserNoticeReply(ctx context.Context, platform string, event PlatformInboundEvent, taskRunID string, replyTarget ReplyTarget, turnResult agent.AgentTurnResult, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) (string, bool) {
	notice, failureNotice, missingReason := userNoticeReplyMessage(turnResult)
	if missingReason != "" {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, "connector.reply.suppressed", connectorReplyEventBody(event, OutboundReply{TaskRunID: taskRunID, ReplyKind: connectorReplyKindUserNotice}, "", "", missingReason))
		connectorRuntime.logger.Info("connector."+platform+".outbound.skipped", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("reason", missingReason))
		return "", false
	}
	if errorValue := agent.ValidateUserNoticeDelivery(notice); errorValue != nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, "connector.reply.suppressed", connectorReplyEventBody(event, OutboundReply{TaskRunID: taskRunID, ReplyKind: connectorReplyKindUserNotice}, "", "", "invalid_user_notice"))
		connectorRuntime.logger.Info("connector."+platform+".outbound.skipped", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("reason", "invalid_user_notice"), slog.String("error", errorValue.Error()))
		return "", false
	}
	if taskStatusRequiresFailureNotice(turnResult.TaskRun.Status) {
		notice += failureRunFooter(taskRunID, connectorRuntime.adminTaskLinkBaseURL)
	}
	reply := OutboundReply{
		Message:         notice,
		TaskRunID:       taskRunID,
		ReplyKind:       connectorReplyKindUserNotice,
		RecoveryActions: recoveryActionsForEvent(turnResult.RecoveryActions, event),
		FailureNotice:   failureNotice,
	}
	interaction, _ := latestAskInteraction(taskRunID, connectorRuntime.agentKernel.ListTaskEvent(taskRunID))
	reply.Interaction = optionalAskInteraction(interaction, event.SenderID)
	if reply.Interaction != nil {
		reply.EphemeralUserID = strings.TrimSpace(event.SenderID)
	}
	dispatchID, errorValue := sendReply(ctx, replyTarget, reply)
	if errorValue != nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, "connector.reply.failed", connectorReplyEventBody(event, reply, "", "", errorValue.Error()))
		connectorRuntime.logger.Error("connector."+platform+".outbound.failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
		return "", false
	}
	if connectorRuntime.outboxRepository() == nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, "connector.reply.sent", connectorReplyEventBody(event, reply, "", dispatchID, ""))
	}
	connectorRuntime.recordTaskWaitTokenForReply(platform, event, replyTarget, reply, dispatchID)
	connectorRuntime.logger.Info("connector."+platform+".outbound.sent", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("replyDispatchID", dispatchID), slog.String("reason", "task_not_completed"))
	return dispatchID, true
}

func failureRunFooter(taskRunID string, adminTaskLinkBaseURL string) string {
	trimmedTaskRunID := strings.TrimSpace(taskRunID)
	if trimmedTaskRunID == "" {
		return ""
	}
	shortTaskRunID := trimmedTaskRunID
	if len(shortTaskRunID) > 6 {
		shortTaskRunID = shortTaskRunID[:6]
	}
	footer := "\n\n`" + shortTaskRunID + "`"
	if adminTaskLinkBaseURL != "" {
		footer += " " + adminTaskLinkBaseURL + "/tasks/" + trimmedTaskRunID
	}
	return footer
}

func userNoticeReplyMessage(turnResult agent.AgentTurnResult) (string, agent.FailureNotice, string) {
	if taskStatusRequiresFailureNotice(turnResult.TaskRun.Status) {
		message := turnResult.FailureNotice.SendableMessage()
		if message != "" {
			return message, turnResult.FailureNotice, ""
		}
		if fallbackMessage := safeFailureUserNotice(turnResult.UserNotice); fallbackMessage != "" {
			return fallbackMessage, turnResult.FailureNotice, ""
		}
		return "", turnResult.FailureNotice, "missing_failure_notice"
	}
	message := strings.TrimSpace(turnResult.UserNotice)
	if message == "" {
		return "", agent.FailureNotice{}, "missing_user_notice"
	}
	return message, agent.FailureNotice{}, ""
}

func safeFailureUserNotice(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	if agent.ValidateUserNoticeDelivery(message) != nil {
		return ""
	}
	return message
}

func taskStatusRequiresFailureNotice(status task.TaskStatus) bool {
	return status == task.TaskStatusFailed || status == task.TaskStatusBlocked
}

func optionalAskInteraction(interaction AskInteraction, targetPlatformUserID string) *AskInteraction {
	if strings.TrimSpace(interaction.Kind) == "" {
		return nil
	}
	interaction.TargetPlatformUserID = strings.TrimSpace(targetPlatformUserID)
	return &interaction
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

func (connectorRuntime *ConnectorRuntime) withInitialVisibleContext(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) PlatformInboundEvent {
	if len(event.Context.Messages) > 0 {
		return event
	}
	if !event.Context.HasMoreBefore && strings.TrimSpace(event.Context.HistoryCursor) == "" {
		return event
	}
	historyCursor := firstNonEmptyString(event.Context.HistoryCursor, event.ConversationID)
	if historyCursor == "" {
		return event
	}
	visibleContext, errorValue := adapter.FetchHistory(ctx, historyCursor, 20)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".history.fetch_failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		return event
	}
	if strings.TrimSpace(event.Context.HistoryCursor) == "" {
		event.Context.HistoryCursor = historyCursor
	}
	if len(visibleContext.Messages) == 0 && len(visibleContext.Materials) == 0 {
		return event
	}
	event.Context.Messages = visibleContext.Messages
	event.Context.Materials = append(event.Context.Materials, visibleContext.Materials...)
	event.Context.HasMoreBefore = visibleContext.HasMoreBefore
	event.Context.HistoryCursor = firstNonEmptyString(visibleContext.HistoryCursor, event.Context.HistoryCursor)
	event.Context.ResponseLanguage = firstNonEmptyString(event.Context.ResponseLanguage, visibleContext.ResponseLanguage)
	return event
}

func (connectorRuntime *ConnectorRuntime) withAttachmentMaterials(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent, personID string) PlatformInboundEvent {
	attachments := connectorVisibleInputAttachments(event.Context)
	if len(attachments) == 0 {
		return event
	}
	importingAdapter, isSupported := adapter.(InputAttachmentImportingAdapter)
	if !isSupported {
		event.Context.Materials = attachments
		return event
	}
	scope := connectorInputAttachmentScope(personID, event)
	targetDirectoryPath := connectorInputAttachmentDirectory(scope, event)
	result, errorValue := importingAdapter.ImportInputAttachments(ctx, InputAttachmentImportRequest{
		MessageID:           event.MessageID,
		TargetDirectoryPath: targetDirectoryPath,
		InputAttachments:    attachments,
	})
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".attachments.import_failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		event.Context.Materials = attachments
		return event
	}
	if len(result.InputAttachments) > 0 {
		importedAttachments := connectorReadableInputAttachments(result.InputAttachments, personID, scope)
		event.Context.InputAttachments = connectorReplaceImportedInputAttachments(event.Context.InputAttachments, importedAttachments)
		event.Context.Materials = connectorReplaceImportedInputAttachments(event.Context.Materials, importedAttachments)
		event.Context.Messages = connectorReplaceImportedMessageAttachments(event.Context.Messages, importedAttachments)
	}
	if len(result.InputParts) > 0 {
		readableInputParts := connectorReadableAgentParts(result.InputParts, personID, scope)
		event.InputParts = append(event.InputParts, connectorCurrentInputParts(readableInputParts, event)...)
	}
	return event
}

func connectorVisibleInputAttachments(visibleContext VisibleContext) []InputAttachment {
	attachments := []InputAttachment{}
	attachments = append(attachments, visibleContext.Materials...)
	attachments = append(attachments, visibleContext.InputAttachments...)
	for _, message := range visibleContext.Messages {
		attachments = append(attachments, message.InputAttachments...)
	}
	return connectorUniqueInputAttachments(attachments)
}

func connectorReplaceImportedMessageAttachments(messages []VisibleContextMessage, importedAttachments []InputAttachment) []VisibleContextMessage {
	result := make([]VisibleContextMessage, 0, len(messages))
	for _, message := range messages {
		message.InputAttachments = connectorReplaceImportedInputAttachments(message.InputAttachments, importedAttachments)
		result = append(result, message)
	}
	return result
}

func connectorReplaceImportedInputAttachments(attachments []InputAttachment, importedAttachments []InputAttachment) []InputAttachment {
	importedByKey := connectorImportedAttachmentByKey(importedAttachments)
	replacedAttachments := make([]InputAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		key := connectorInputAttachmentKey(attachment)
		if importedAttachment, isFound := importedByKey[key]; isFound {
			replacedAttachments = append(replacedAttachments, importedAttachment)
			continue
		}
		replacedAttachments = append(replacedAttachments, attachment)
	}
	return connectorUniqueInputAttachments(replacedAttachments)
}

func connectorImportedAttachmentByKey(importedAttachments []InputAttachment) map[string]InputAttachment {
	importedByKey := map[string]InputAttachment{}
	for _, attachment := range importedAttachments {
		key := connectorInputAttachmentKey(attachment)
		if key != "" {
			importedByKey[key] = attachment
		}
	}
	return importedByKey
}

func connectorCurrentInputParts(parts []agent.AgentPart, event PlatformInboundEvent) []agent.AgentPart {
	currentFileIDs := map[string]bool{}
	for _, attachment := range event.Context.InputAttachments {
		fileID := strings.TrimSpace(attachment.FileID)
		if fileID != "" {
			currentFileIDs[fileID] = true
		}
	}
	currentParts := []agent.AgentPart{}
	for _, part := range parts {
		if connectorIsCurrentInputPart(part, event, currentFileIDs) {
			currentParts = append(currentParts, part)
		}
	}
	return currentParts
}

func connectorIsCurrentInputPart(part agent.AgentPart, event PlatformInboundEvent, currentFileIDs map[string]bool) bool {
	if strings.TrimSpace(part.Source.MessageID) != "" && strings.TrimSpace(part.Source.MessageID) == strings.TrimSpace(event.MessageID) {
		return true
	}
	if strings.TrimSpace(part.Source.FileID) != "" && currentFileIDs[strings.TrimSpace(part.Source.FileID)] {
		return true
	}
	return false
}

func connectorInputAttachmentScope(personID string, event PlatformInboundEvent) agentruntime.ConversationResourceScope {
	return agentruntime.ConversationScopeForRequest("/workspace", agentruntime.ToolCatalogRequest{
		RequesterPersonID:       personID,
		ConversationID:          event.ConversationID,
		ConversationType:        event.Context.ConversationType,
		ConversationChannelID:   event.Context.ChannelID,
		ConversationChannelName: event.Context.ChannelName,
	})
}

func connectorInputAttachmentDirectory(scope agentruntime.ConversationResourceScope, event PlatformInboundEvent) string {
	platform := connectorSafePathSegment(firstNonEmptyString(event.Platform, "platform"))
	conversationLabel := connectorAttachmentConversationLabel(event)
	return strings.TrimRight(scope.DefaultDirectoryPath, "/") + "/inbox/" + platform + "/" + conversationLabel
}

func connectorAttachmentConversationLabel(event PlatformInboundEvent) string {
	if channelName := strings.TrimSpace(event.Context.ChannelName); channelName != "" {
		return connectorSafePathSegment(channelName)
	}
	conversationID := strings.TrimSpace(event.ConversationID)
	if strings.HasPrefix(strings.ToLower(conversationID), "dm:") {
		return "dm"
	}
	return connectorSafePathSegment(firstNonEmptyString(conversationID, "conversation"))
}

func connectorReadableInputAttachments(attachments []InputAttachment, personID string, scope agentruntime.ConversationResourceScope) []InputAttachment {
	result := make([]InputAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		attachment.Path = connectorReadableAttachmentPath(attachment.Path, personID, scope)
		result = append(result, attachment)
	}
	return result
}

func connectorReadableAgentParts(parts []agent.AgentPart, personID string, scope agentruntime.ConversationResourceScope) []agent.AgentPart {
	result := make([]agent.AgentPart, 0, len(parts))
	for _, part := range parts {
		if part.File != nil {
			file := *part.File
			file.Path = connectorReadableAttachmentPath(file.Path, personID, scope)
			part.File = &file
		}
		if part.Image != nil {
			image := *part.Image
			image.Path = connectorReadableAttachmentPath(image.Path, personID, scope)
			part.Image = &image
		}
		result = append(result, part)
	}
	return result
}

func connectorReadableAttachmentPath(path string, personID string, scope agentruntime.ConversationResourceScope) string {
	if scope.Kind != "private" || strings.TrimSpace(personID) == "" {
		return path
	}
	prefix := "/workspace/private/people/" + connectorSafePathSegment(personID)
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == prefix {
		return "home"
	}
	if strings.HasPrefix(trimmedPath, prefix+"/") {
		return "home/" + strings.TrimPrefix(trimmedPath, prefix+"/")
	}
	return path
}

func connectorUniqueInputAttachments(attachments []InputAttachment) []InputAttachment {
	seen := map[string]bool{}
	result := make([]InputAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		key := connectorInputAttachmentKey(attachment)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, attachment)
	}
	return result
}

func connectorInputAttachmentKey(attachment InputAttachment) string {
	if strings.TrimSpace(attachment.FileID) != "" {
		return strings.TrimSpace(attachment.Platform) + ":" + strings.TrimSpace(attachment.FileID)
	}
	if strings.TrimSpace(attachment.Path) != "" {
		return strings.TrimSpace(attachment.Platform) + ":" + strings.TrimSpace(attachment.Path)
	}
	return ""
}

type connectorAttachmentMaterialResolver struct {
	adapter  PlatformAdapter
	personID string
	event    PlatformInboundEvent
}

func (resolver connectorAttachmentMaterialResolver) ResolveAttachmentMaterial(ctx context.Context, materialID string) (agent.VisibleContextMaterial, error) {
	attachment, isFound, errorValue := resolver.findAttachmentMaterial(ctx, materialID)
	if errorValue != nil {
		return agent.VisibleContextMaterial{}, errorValue
	}
	if !isFound {
		return agent.VisibleContextMaterial{}, errors.New("attachment material is not visible in this conversation")
	}
	return resolver.importAttachmentMaterial(ctx, attachment)
}

func (resolver connectorAttachmentMaterialResolver) findAttachmentMaterial(ctx context.Context, materialID string) (InputAttachment, bool, error) {
	if attachment, isFound := findAttachmentMaterialInContext(resolver.event.Context, materialID); isFound {
		return attachment, true, nil
	}
	if strings.TrimSpace(resolver.event.Context.HistoryCursor) == "" {
		return InputAttachment{}, false, nil
	}
	visibleContext, errorValue := resolver.adapter.FetchHistory(ctx, resolver.event.Context.HistoryCursor, 50)
	if errorValue != nil {
		return InputAttachment{}, false, errors.New("attachment history lookup failed: " + errorValue.Error())
	}
	attachment, isFound := findAttachmentMaterialInContext(visibleContext, materialID)
	return attachment, isFound, nil
}

func findAttachmentMaterialInContext(visibleContext VisibleContext, materialID string) (InputAttachment, bool) {
	trimmedMaterialID := strings.TrimSpace(materialID)
	for _, attachment := range visibleContextAttachmentMaterials(visibleContext) {
		if attachmentMaterialID(attachment) == trimmedMaterialID {
			return attachment, true
		}
	}
	return InputAttachment{}, false
}

func visibleContextAttachmentMaterials(visibleContext VisibleContext) []InputAttachment {
	attachments := []InputAttachment{}
	attachments = append(attachments, visibleContext.Materials...)
	attachments = append(attachments, visibleContext.InputAttachments...)
	for _, message := range visibleContext.Messages {
		attachments = append(attachments, message.InputAttachments...)
	}
	return connectorUniqueInputAttachments(attachments)
}

func (resolver connectorAttachmentMaterialResolver) importAttachmentMaterial(ctx context.Context, attachment InputAttachment) (agent.VisibleContextMaterial, error) {
	importingAdapter, isSupported := resolver.adapter.(InputAttachmentImportingAdapter)
	if isSupported && strings.TrimSpace(attachment.FileID) != "" {
		material, errorValue := resolver.importAttachmentWithAdapter(ctx, importingAdapter, attachment)
		if errorValue == nil {
			return material, nil
		}
		if strings.TrimSpace(attachment.Path) == "" || strings.TrimSpace(attachment.ErrorCode) != "" {
			return agent.VisibleContextMaterial{}, errorValue
		}
	}
	if strings.TrimSpace(attachment.Path) != "" && strings.TrimSpace(attachment.ErrorCode) == "" {
		return connectorAttachmentToAgentMaterial(resolver.personID, resolver.event, attachment), nil
	}
	if !isSupported {
		return agent.VisibleContextMaterial{}, errors.New("attachment import is unavailable for this platform")
	}
	return resolver.importAttachmentWithAdapter(ctx, importingAdapter, attachment)
}

func (resolver connectorAttachmentMaterialResolver) importAttachmentWithAdapter(ctx context.Context, importingAdapter InputAttachmentImportingAdapter, attachment InputAttachment) (agent.VisibleContextMaterial, error) {
	scope := connectorInputAttachmentScope(resolver.personID, resolver.event)
	messageID := firstNonEmptyString(attachment.MessageID, resolver.event.MessageID)
	result, errorValue := importingAdapter.ImportInputAttachments(ctx, InputAttachmentImportRequest{
		MessageID:           messageID,
		TargetDirectoryPath: connectorInputAttachmentDirectory(scope, resolver.event),
		InputAttachments:    []InputAttachment{attachment},
	})
	if errorValue != nil {
		return agent.VisibleContextMaterial{}, errorValue
	}
	if len(result.InputAttachments) == 0 {
		return agent.VisibleContextMaterial{}, errors.New("attachment import returned no material")
	}
	importedAttachment := connectorReadableInputAttachments(result.InputAttachments, resolver.personID, scope)[0]
	if strings.TrimSpace(importedAttachment.Path) == "" {
		return agent.VisibleContextMaterial{}, errors.New("attachment import returned no readable path")
	}
	material := agentVisibleContextMaterials([]InputAttachment{importedAttachment})[0]
	return connectorMaterialWithPreview(material, result.InputParts), nil
}

func connectorMaterialWithPreview(material agent.VisibleContextMaterial, parts []agent.AgentPart) agent.VisibleContextMaterial {
	materialID := strings.TrimSpace(material.MaterialID)
	path := strings.TrimSpace(material.Path)
	for _, part := range parts {
		if part.File == nil {
			continue
		}
		if materialID != "" && connectorAgentPartMaterialID(part) != materialID {
			continue
		}
		if materialID == "" && path != "" && strings.TrimSpace(part.File.Path) != path {
			continue
		}
		material.MarkdownPreview = strings.TrimSpace(part.File.MarkdownPreview)
		material.ConversionStatus = strings.TrimSpace(part.File.ConversionStatus)
		material.ConversionMessage = strings.TrimSpace(part.File.ConversionMessage)
		return material
	}
	return material
}

func connectorAgentPartMaterialID(part agent.AgentPart) string {
	fileID := strings.TrimSpace(part.Source.FileID)
	platform := firstNonEmptyString(strings.TrimSpace(part.Source.Platform), "attachment")
	if fileID != "" {
		return platform + ":" + fileID
	}
	if part.File != nil && strings.TrimSpace(part.File.Path) != "" {
		return platform + ":" + connectorSafePathSegment(part.File.Path)
	}
	return ""
}

func connectorAttachmentToAgentMaterial(personID string, event PlatformInboundEvent, attachment InputAttachment) agent.VisibleContextMaterial {
	scope := connectorInputAttachmentScope(personID, event)
	readableAttachments := connectorReadableInputAttachments([]InputAttachment{attachment}, personID, scope)
	materials := agentVisibleContextMaterials(readableAttachments)
	if len(materials) == 0 {
		return agent.VisibleContextMaterial{}
	}
	return materials[0]
}

func connectorSafePathSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	result := strings.Builder{}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
			result.WriteRune(character)
			continue
		}
		result.WriteRune('-')
	}
	segment := strings.Trim(result.String(), "-_")
	if segment == "" {
		return "unknown"
	}
	return segment
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
	requesterEmail := connectorRuntime.requesterEmailForEvent(personID, event)
	return connectorRuntime.toolCatalogBuilder.BuildToolSet(agentruntime.ToolCatalogRequest{
		ProfileName:                "default",
		Prompt:                     event.Prompt,
		RequesterPersonID:          personID,
		RequesterName:              connectorRuntime.requesterNameForEvent(personID, event),
		RequesterEmail:             requesterEmail,
		RequesterPlatformUserID:    event.SenderID,
		ConversationID:             event.ConversationID,
		ConversationType:           event.Context.ConversationType,
		ConversationChannelID:      event.Context.ChannelID,
		ConversationChannelName:    event.Context.ChannelName,
		ReplyTargetID:              event.ReplyTargetID,
		Platform:                   adapter.Name(),
		HistoryCursor:              event.Context.HistoryCursor,
		HistoryProvider:            connectorHistoryProvider{adapter: adapter},
		AttachmentMaterialResolver: connectorAttachmentMaterialResolver{adapter: adapter, personID: personID, event: event},
		PersonAccess:               personAccess,
		MemoryNamespaces:           connectorRuntime.accessibleNamespaces(personID, personAccess, event),
		AccessibleConversationIDs:  []string{event.ConversationID},
		InputParts:                 append([]agent.AgentPart{}, event.InputParts...),
	})
}

func (connectorRuntime *ConnectorRuntime) requesterEmailForEvent(personID string, event PlatformInboundEvent) string {
	email := strings.ToLower(strings.TrimSpace(connectorRuntime.identityService.ResolvePersonPrimaryEmail(personID)))
	if email != "" {
		return email
	}
	return strings.ToLower(strings.TrimSpace(event.Context.Sender.Email))
}

func (connectorRuntime *ConnectorRuntime) requesterNameForEvent(personID string, event PlatformInboundEvent) string {
	if name := strings.TrimSpace(event.Context.Sender.Name); name != "" {
		return name
	}
	return strings.TrimSpace(connectorRuntime.identityService.ResolvePersonDisplayName(personID))
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

func shouldStartProgressBeforeAddressing(event PlatformInboundEvent) bool {
	return !isMultiPersonConversation(event)
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
	connectorRuntime.appendConnectorReplyEvent(queuedReply.Reply.TaskRunID, "connector.reply.failed", connectorReplyEventBody(PlatformInboundEvent{MessageID: queuedReply.RawEventID}, queuedReply.Reply, queuedReply.OutboxID, "", errorValue.Error()))
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

func (connectorRuntime *ConnectorRuntime) suppressDuplicateSourceTaskIfNeeded(platform string, event PlatformInboundEvent, personID string) (ConnectorRuntimeResult, bool) {
	sourceReference := event.DedupeKey()
	taskRun, isFound := connectorRuntime.findTaskRunBySourceReference(personID, sourceReference)
	if !isFound {
		return ConnectorRuntimeResult{}, false
	}
	connectorRuntime.agentKernel.AppendTaskEvent(taskRun.TaskRunID, "connector.duplicate_source_suppressed", marshalConnectorEventBody(map[string]string{
		"messageID":       event.MessageID,
		"sourceReference": sourceReference,
	}))
	connectorRuntime.logger.Info("connector."+platform+".event.suppressed", slog.String("source", event.Source), slog.String("reason", "duplicate_source_reference"), slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRun.TaskRunID))
	return ConnectorRuntimeResult{Handled: true, Platform: platform, Duplicate: true, Reason: "duplicate_source_reference", TaskRunID: taskRun.TaskRunID}, true
}

func (connectorRuntime *ConnectorRuntime) findTaskRunBySourceReference(personID string, sourceReference string) (task.TaskRun, bool) {
	trimmedSourceReference := strings.TrimSpace(sourceReference)
	if trimmedSourceReference == "" {
		return task.TaskRun{}, false
	}
	var selectedTaskRun task.TaskRun
	isFound := false
	for _, taskRun := range connectorRuntime.agentKernel.ListTaskRunByPersonID(personID) {
		if !connectorRuntime.taskRunHasSourceReference(taskRun.TaskRunID, trimmedSourceReference) {
			continue
		}
		if isFound && !taskRun.UpdatedAt.After(selectedTaskRun.UpdatedAt) {
			continue
		}
		selectedTaskRun = taskRun
		isFound = true
	}
	return selectedTaskRun, isFound
}

func (connectorRuntime *ConnectorRuntime) taskRunHasSourceReference(taskRunID string, sourceReference string) bool {
	for _, taskEvent := range connectorRuntime.agentKernel.ListTaskEvent(taskRunID) {
		if taskEvent.Name != "agent.task_source" && taskEvent.Name != "agent.task_launched" {
			continue
		}
		if taskEventSourceReference(taskEvent) == sourceReference {
			return true
		}
	}
	return false
}

func taskEventSourceReference(taskEvent task.TaskEvent) string {
	var document struct {
		SourceReference string `json:"sourceReference"`
	}
	if json.Unmarshal([]byte(taskEvent.Body), &document) != nil {
		return ""
	}
	return strings.TrimSpace(document.SourceReference)
}

func (event *PlatformInboundEvent) UnmarshalJSON(document []byte) error {
	type platformInboundEvent PlatformInboundEvent
	var parsedEvent platformInboundEvent
	if errorValue := json.Unmarshal(document, &parsedEvent); errorValue != nil {
		return errorValue
	}

	var rawFields map[string]interface{}
	if errorValue := json.Unmarshal(document, &rawFields); errorValue == nil {
		if len(parsedEvent.LegacyFields) == 0 {
			parsedEvent.LegacyFields = rawFields
		}
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
			Materials:          agentVisibleContextMaterials(message.InputAttachments),
		})
	}

	currentMaterials := agentVisibleContextMaterials(visibleContext.InputAttachments)
	return agent.VisibleContext{
		Messages:         messages,
		CurrentMaterials: currentMaterials,
		Materials:        agentPreviousVisibleContextMaterials(visibleContext.Materials, currentMaterials),
		HasMoreBefore:    visibleContext.HasMoreBefore,
		HistoryCursor:    visibleContext.HistoryCursor,
		ResponseLanguage: visibleContext.ResponseLanguage,
	}
}

func agentPreviousVisibleContextMaterials(attachments []InputAttachment, currentMaterials []agent.VisibleContextMaterial) []agent.VisibleContextMaterial {
	currentMaterialIDs := map[string]bool{}
	for _, material := range currentMaterials {
		currentMaterialID := strings.TrimSpace(material.MaterialID)
		if currentMaterialID != "" {
			currentMaterialIDs[currentMaterialID] = true
		}
	}
	materials := []agent.VisibleContextMaterial{}
	for _, material := range agentVisibleContextMaterials(attachments) {
		if currentMaterialIDs[strings.TrimSpace(material.MaterialID)] {
			continue
		}
		materials = append(materials, material)
	}
	return materials
}

func agentVisibleContextMaterials(attachments []InputAttachment) []agent.VisibleContextMaterial {
	materials := make([]agent.VisibleContextMaterial, 0, len(attachments))
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.FileID) == "" && strings.TrimSpace(attachment.Path) == "" {
			continue
		}
		materials = append(materials, agent.VisibleContextMaterial{
			MaterialID:  attachmentMaterialID(attachment),
			Platform:    attachment.Platform,
			MessageID:   attachment.MessageID,
			Filename:    attachment.Filename,
			ContentType: attachment.ContentType,
			SizeBytes:   attachment.SizeBytes,
			Path:        attachment.Path,
			IsAvailable: attachment.IsAvailable,
			ErrorCode:   attachment.ErrorCode,
			Message:     attachment.Message,
		})
	}
	return materials
}

func attachmentMaterialID(attachment InputAttachment) string {
	fileID := strings.TrimSpace(attachment.FileID)
	if fileID != "" {
		return firstNonEmptyString(strings.TrimSpace(attachment.Platform), "attachment") + ":" + fileID
	}
	return firstNonEmptyString(strings.TrimSpace(attachment.Platform), "attachment") + ":" + connectorSafePathSegment(attachment.Path)
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
