package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/capability"
	"blueclaw/internal/identity"
	"blueclaw/internal/mcp"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
)

type IngressGate interface {
	IsPaused() bool
}

type ConnectorEventRepository interface {
	TryInsertConnectorEvent(PlatformInboundEvent) (bool, ConnectorRuntimeResult, error)
	SaveConnectorResult(PlatformInboundEvent, ConnectorRuntimeResult) error
}

type PlatformInboundEvent struct {
	Platform       string                 `json:"-"`
	Source         string                 `json:"-"`
	ConversationID string                 `json:"conversationID"`
	MessageID      string                 `json:"messageID"`
	SenderID       string                 `json:"senderID"`
	ReplyTargetID  string                 `json:"replyTargetID"`
	Prompt         string                 `json:"prompt"`
	Context        VisibleContext         `json:"context"`
	RawReceivedAt  time.Time              `json:"-"`
	LegacyFields   map[string]interface{} `json:"-"`
}

type ReplyTarget struct {
	ConversationID string `json:"conversationID"`
	ReplyTargetID  string `json:"replyTargetID"`
	DedupeKey      string `json:"dedupeKey"`
}

type VisibleContext struct {
	Messages      []VisibleContextMessage `json:"messages"`
	HasMoreBefore bool                    `json:"hasMoreBefore"`
	HistoryCursor string                  `json:"historyCursor"`
}

type VisibleContextMessage struct {
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
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
	SendReply(context.Context, ReplyTarget, string) (string, error)
	FetchHistory(context.Context, string, int) (VisibleContext, error)
	NotInvitedReply() string
}

type ConnectorTransport interface {
	Name() string
	Platform() string
	Start(context.Context)
}

type ConnectorRuntime struct {
	identityService     *identity.IdentityService
	agentKernel         *agent.AgentKernel
	memoryService       *memory.MemoryService
	memoryRouter        *memory.MemoryScopeRouter
	workspaceID         string
	logger              *slog.Logger
	mcpRegistry         *mcp.McpRegistry
	capabilityClient    capability.Client
	capabilityToolNames []string
	allowedToolNames    []string

	mutex             sync.Mutex
	adapterByPlatform map[string]PlatformAdapter
	processedResults  map[string]ConnectorRuntimeResult
	eventRepository   ConnectorEventRepository
	ingressGate       IngressGate
}

func NewConnectorRuntime(identityService *identity.IdentityService, agentKernel *agent.AgentKernel, logger *slog.Logger) *ConnectorRuntime {
	if logger == nil {
		logger = slog.Default()
	}

	return &ConnectorRuntime{
		identityService:   identityService,
		agentKernel:       agentKernel,
		logger:            logger,
		adapterByPlatform: map[string]PlatformAdapter{},
		processedResults:  map[string]ConnectorRuntimeResult{},
		allowedToolNames:  []string{"conversation.history", "memory.search"},
	}
}

func (connectorRuntime *ConnectorRuntime) RegisterAdapter(adapter PlatformAdapter) {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()

	connectorRuntime.adapterByPlatform[adapter.Name()] = adapter
}

func (connectorRuntime *ConnectorRuntime) UseMemoryService(memoryService *memory.MemoryService) {
	connectorRuntime.memoryService = memoryService
}

func (connectorRuntime *ConnectorRuntime) UseMemoryScopeRouter(memoryRouter *memory.MemoryScopeRouter) {
	connectorRuntime.memoryRouter = memoryRouter
}

func (connectorRuntime *ConnectorRuntime) UseWorkspaceID(workspaceID string) {
	connectorRuntime.workspaceID = strings.TrimSpace(workspaceID)
}

func (connectorRuntime *ConnectorRuntime) UseEventRepository(eventRepository ConnectorEventRepository) {
	connectorRuntime.eventRepository = eventRepository
}

func (connectorRuntime *ConnectorRuntime) UseIngressGate(ingressGate IngressGate) {
	connectorRuntime.ingressGate = ingressGate
}

func (connectorRuntime *ConnectorRuntime) UseMCPRegistry(mcpRegistry *mcp.McpRegistry) {
	connectorRuntime.mcpRegistry = mcpRegistry
}

func (connectorRuntime *ConnectorRuntime) UseCapabilityTools(capabilityClient capability.Client, toolNames []string) {
	connectorRuntime.capabilityClient = capabilityClient
	connectorRuntime.capabilityToolNames = trimNonEmptyStrings(toolNames)
}

func (connectorRuntime *ConnectorRuntime) UseAllowedToolNames(allowedToolNames []string) {
	trimmedToolNames := trimNonEmptyStrings(allowedToolNames)
	if len(trimmedToolNames) == 0 {
		trimmedToolNames = []string{"conversation.history", "memory.search"}
	}
	connectorRuntime.allowedToolNames = trimmedToolNames
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

func (connectorRuntime *ConnectorRuntime) processInboundEvent(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (ConnectorRuntimeResult, error) {
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
		dispatchID, sendError := adapter.SendReply(ctx, replyTarget, adapter.NotInvitedReply())
		if sendError != nil {
			connectorRuntime.logger.Error("connector."+platform+".outbound.failed", slog.String("messageID", event.MessageID), slog.String("error", sendError.Error()))
			return ConnectorRuntimeResult{Handled: true, Platform: platform, Reason: "not_invited"}, nil
		}
		connectorRuntime.logger.Info("connector."+platform+".outbound.sent", slog.String("messageID", event.MessageID), slog.String("replyDispatchID", dispatchID))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, Reason: "not_invited", ReplyDispatchID: dispatchID}, nil
	}

	connectorRuntime.logger.Info("connector."+platform+".auth.allowed", slog.String("messageID", event.MessageID), slog.String("personID", personID))
	personAccess := connectorRuntime.identityService.ResolvePersonAccess(personID)
	toolRegistry := connectorRuntime.buildTurnToolRegistry(adapter, event, personID, personAccess)
	stopProgress := connectorRuntime.startProgress(ctx, adapter, replyTarget)
	defer stopProgress()

	memoryFacts, memoryError := connectorRuntime.searchAccessibleMemory(ctx, personID, personAccess, event)
	if memoryError != nil {
		connectorRuntime.logger.Warn("connector."+platform+".memory.search_failed", slog.String("messageID", event.MessageID), slog.String("error", memoryError.Error()))
	}

	connectorRuntime.logger.Info("connector."+platform+".agent.started", slog.String("messageID", event.MessageID))
	turnResult, errorValue := connectorRuntime.agentKernel.RunTurn(ctx, agent.AgentTurnRequest{
		RequesterPersonID: personID,
		ConversationID:    event.ConversationID,
		Prompt:            event.Prompt,
		VisibleContext:    event.Context.ToAgentVisibleContext(),
		MemoryFacts:       memoryFacts,
		ToolRegistry:      toolRegistry,
	})
	if errorValue != nil {
		connectorRuntime.logger.Error("connector."+platform+".agent.failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{}, errorValue
	}
	taskRunID := turnResult.TaskRun.TaskRunID
	connectorRuntime.logger.Info("connector."+platform+".agent.completed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID))

	dispatchID, errorValue := adapter.SendReply(ctx, replyTarget, turnResult.FinalReply)
	if errorValue != nil {
		connectorRuntime.logger.Error("connector."+platform+".outbound.failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, Reason: "reply_failed"}, nil
	}

	connectorRuntime.logger.Info("connector."+platform+".outbound.sent", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("replyDispatchID", dispatchID))
	connectorRuntime.ingestMemory(ctx, platform, personID, personAccess, event, taskRunID)
	return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, ReplyDispatchID: dispatchID}, nil
}

func (connectorRuntime *ConnectorRuntime) buildTurnToolRegistry(adapter PlatformAdapter, event PlatformInboundEvent, personID string, personAccess policy.PersonAccess) *agent.ToolRegistry {
	toolRegistry := agent.NewToolRegistry(connectorRuntime.allowedToolNames)
	toolRegistry.RegisterTool(agent.ToolDefinition{
		Name:        "conversation.history",
		Description: "Fetch earlier visible messages for this conversation using the opaque history cursor.",
	}, func(toolContext context.Context, toolInvocation agent.ToolInvocation) (agent.ToolResult, error) {
		var input struct {
			HistoryCursor string `json:"historyCursor"`
			Limit         int    `json:"limit"`
			Direction     string `json:"direction"`
		}
		if errorValue := agent.UnmarshalToolInput(toolInvocation.Input, &input); errorValue != nil {
			return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
		}
		historyCursor := strings.TrimSpace(input.HistoryCursor)
		if historyCursor == "" {
			historyCursor = event.Context.HistoryCursor
		}
		if historyCursor == "" {
			return agent.ToolResult{Content: "history cursor is unavailable", IsError: true}, nil
		}
		limit := input.Limit
		if limit <= 0 || limit > 50 {
			limit = 20
		}
		visibleContext, errorValue := adapter.FetchHistory(toolContext, historyCursor, limit)
		if errorValue != nil {
			return agent.ToolResult{}, errorValue
		}
		return agent.ToolResult{Content: marshalConnectorToolResult(visibleContext)}, nil
	})
	toolRegistry.RegisterTool(agent.ToolDefinition{
		Name:        "memory.search",
		Description: "Search Blueclaw graph memory allowed for this requester and conversation.",
	}, func(toolContext context.Context, toolInvocation agent.ToolInvocation) (agent.ToolResult, error) {
		var input struct {
			Query string `json:"query"`
		}
		if errorValue := agent.UnmarshalToolInput(toolInvocation.Input, &input); errorValue != nil {
			return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
		}
		query := strings.TrimSpace(input.Query)
		if query == "" {
			query = event.Prompt
		}
		searchEvent := event
		searchEvent.Prompt = query
		memoryFacts, errorValue := connectorRuntime.searchAccessibleMemory(toolContext, personID, personAccess, searchEvent)
		if errorValue != nil {
			return agent.ToolResult{}, errorValue
		}
		return agent.ToolResult{Content: marshalConnectorToolResult(memoryFacts)}, nil
	})
	if connectorRuntime.mcpRegistry != nil {
		for _, toolDefinition := range connectorRuntime.mcpRegistry.ListTool() {
			mcpToolDefinition := toolDefinition
			toolRegistry.RegisterTool(agent.ToolDefinition{
				Name:        mcpToolDefinition.Name,
				Description: "MCP tool from " + mcpToolDefinition.ServerName,
			}, func(toolContext context.Context, toolInvocation agent.ToolInvocation) (agent.ToolResult, error) {
				output, errorValue := connectorRuntime.mcpRegistry.InvokeTool(toolContext, mcp.Invocation{
					ServerName: mcpToolDefinition.ServerName,
					ToolName:   mcpToolDefinition.Name,
					Input:      string(toolInvocation.Input),
				})
				if errorValue != nil {
					return agent.ToolResult{}, errorValue
				}
				return agent.ToolResult{Content: output}, nil
			})
		}
	}
	for _, capabilityToolName := range connectorRuntime.capabilityToolNames {
		toolName := capabilityToolName
		toolRegistry.RegisterTool(agent.ToolDefinition{
			Name:        toolName,
			Description: "InternKim capability tool",
		}, func(toolContext context.Context, toolInvocation agent.ToolInvocation) (agent.ToolResult, error) {
			var response struct {
				Content string `json:"content"`
				IsError bool   `json:"isError"`
			}
			request := map[string]any{"input": json.RawMessage(toolInvocation.Input)}
			errorValue := connectorRuntime.capabilityClient.PostJSON(toolContext, "/v1/tools/"+url.PathEscape(toolName)+"/invoke", request, &response)
			if errorValue != nil {
				return agent.ToolResult{}, errorValue
			}
			return agent.ToolResult{Content: response.Content, IsError: response.IsError}, nil
		})
	}
	return toolRegistry
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

func (connectorRuntime *ConnectorRuntime) searchAccessibleMemory(ctx context.Context, personID string, personAccess policy.PersonAccess, event PlatformInboundEvent) ([]memory.MemoryFact, error) {
	if connectorRuntime.memoryService == nil {
		return nil, nil
	}
	namespaces := connectorRuntime.accessibleNamespaces(personID, personAccess, event)
	return connectorRuntime.memoryService.SearchMemory(ctx, memory.MemorySearchRequest{
		Query:                     event.Prompt,
		ReaderPersonID:            personID,
		ReaderSecurityLevelRank:   personAccess.SecurityLevelRank,
		ReaderGrantedClasses:      personAccess.GrantedClasses,
		ConversationID:            event.ConversationID,
		AccessibleConversationIDs: []string{event.ConversationID},
		Namespaces:                namespaces,
	})
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
	route, errorValue := connectorRuntime.memoryRouter.Route(ctx, memory.ScopeRouteInput{
		PersonID:                 personID,
		Prompt:                   event.Prompt,
		ConversationID:           event.ConversationID,
		WorkspaceID:              connectorRuntime.workspaceID,
		DefaultSecurityLevelRank: defaultSecurityLevelRank,
		DefaultRequiredClasses:   defaultRequiredClasses,
	})
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".memory.scope_route_failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
		connectorRuntime.agentKernel.AppendTaskEvent(taskRunID, "memory.scope_route_failed", errorValue.Error())
		route = memory.ScopeRoute{Namespaces: connectorRuntime.accessibleNamespaces(personID, personAccess, event)}
	}

	episode := memory.MemoryEpisode{
		EpisodeID:       event.DedupeKey(),
		Platform:        platform,
		MessageID:       event.MessageID,
		ConversationID:  event.ConversationID,
		SenderPersonID:  personID,
		Prompt:          event.Prompt,
		OccurredAt:      event.RawReceivedAt,
		Namespaces:      route.Namespaces,
		Source:          "message",
		SourceReference: event.ReplyTargetID,
	}
	if episode.OccurredAt.IsZero() {
		episode.OccurredAt = time.Now().UTC()
	}
	errorValue = connectorRuntime.memoryService.AddEpisode(ctx, episode)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".memory.ingestion_failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
		connectorRuntime.agentKernel.AppendTaskEvent(taskRunID, "memory.ingestion_failed", errorValue.Error())
		return
	}
	connectorRuntime.logger.Info("connector."+platform+".memory.episode_ingested", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.Int("namespaceCount", len(route.Namespaces)))
	connectorRuntime.agentKernel.AppendTaskEvent(taskRunID, "memory.episode_ingested", strconv.Itoa(len(route.Namespaces)))
}

func (connectorRuntime *ConnectorRuntime) accessibleNamespaces(personID string, personAccess policy.PersonAccess, event PlatformInboundEvent) []memory.MemoryNamespace {
	conversationSecurityLevelRank := personAccess.SecurityLevelRank
	conversationRequiredClasses := append([]string{}, personAccess.GrantedClasses...)
	if channelPolicy, isFound := connectorRuntime.identityService.ResolveConversationPolicy(event.Platform, event.ConversationID); isFound {
		conversationSecurityLevelRank = channelPolicy.DefaultSecurityLevelRank
		conversationRequiredClasses = append([]string{}, channelPolicy.DefaultRequiredClasses...)
	}
	return []memory.MemoryNamespace{
		memory.UserNamespace(personID),
		memory.WorkspaceNamespace(connectorRuntime.workspaceID, personAccess.SecurityLevelRank, personAccess.GrantedClasses),
		memory.ConversationNamespace(event.ConversationID, conversationSecurityLevelRank, conversationRequiredClasses),
	}
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
	platform := adapter.Name()
	connectorRuntime.logger.Info("connector."+platform+".progress.started", slog.String("conversationID", replyTarget.ConversationID), slog.String("replyTargetID", replyTarget.ReplyTargetID))
	if errorValue := adapter.StartProgress(ctx, replyTarget); errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".progress.start_failed", slog.String("conversationID", replyTarget.ConversationID), slog.String("replyTargetID", replyTarget.ReplyTargetID), slog.String("error", errorValue.Error()))
	}

	return func() {
		if errorValue := adapter.StopProgress(ctx, replyTarget); errorValue != nil {
			connectorRuntime.logger.Warn("connector."+platform+".progress.stop_failed", slog.String("conversationID", replyTarget.ConversationID), slog.String("replyTargetID", replyTarget.ReplyTargetID), slog.String("error", errorValue.Error()))
		}
		connectorRuntime.logger.Info("connector."+platform+".progress.stopped", slog.String("conversationID", replyTarget.ConversationID), slog.String("replyTargetID", replyTarget.ReplyTargetID))
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
			Speaker: message.Speaker,
			Text:    message.Text,
		})
	}

	return agent.VisibleContext{
		Messages:      messages,
		HasMoreBefore: visibleContext.HasMoreBefore,
		HistoryCursor: visibleContext.HistoryCursor,
	}
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
