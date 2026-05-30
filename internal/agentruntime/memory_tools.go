package agentruntime

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
)

type memorySearchToolInput struct {
	Query string `json:"query"`
}

type memoryRememberToolInput struct {
	Content string `json:"content"`
}

func registerMemoryTools(toolCatalogBuilder *ToolCatalogBuilder, toolRegistry *agent.ToolSet, request ToolCatalogRequest) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[memorySearchToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "memory.search",
			Description: "Search Blueclaw graph memory allowed for this requester and conversation.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`),
		},
		Handler: func(toolContext context.Context, input memorySearchToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.searchMemoryTool(toolContext, input, request)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[memoryRememberToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "memory.remember",
			Description: "Queue a durable memory update for the current person or active circle.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"content":{"type":"string"}},"required":["content"]}`),
		},
		Handler: func(toolContext context.Context, input memoryRememberToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.rememberMemoryTool(toolContext, input, request)
		},
		Result: agent.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) searchMemoryTool(toolContext context.Context, input memorySearchToolInput, request ToolCatalogRequest) (agent.ToolResult, error) {
	if request.ActiveCircleConflict {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.Conflict, "memory_search", "memory.search has multiple active circle candidates"), nil
	}
	memoryFacts, errorValue := toolCatalogBuilder.SearchMemory(toolContext, TaskMemoryRequest{
		Query:                     firstNonEmptyString(input.Query, request.Prompt),
		RequesterPersonID:         request.RequesterPersonID,
		ConversationID:            request.ConversationID,
		PersonAccess:              request.PersonAccess,
		MemoryNamespaces:          searchMemoryNamespaces(request),
		AccessibleConversationIDs: request.AccessibleConversationIDs,
	})
	if errorValue != nil {
		return memorySearchUnavailableResult(), nil
	}
	return agent.ToolSuccess(marshalToolResult(memoryFacts)), nil
}

func memorySearchUnavailableResult() agent.ToolResult {
	message := "Persistent memory search is unavailable."
	return agent.ToolResult{
		Output: agent.ToolOutput{Content: message},
		Failure: &agent.ToolFailure{
			Kind:            agent.FailureDependencyUnavailable,
			Code:            agent.FailureCodes.Unavailable.String(),
			Stage:           "graphiti_search",
			UserSafeSummary: message,
			Retryable:       true,
			SafeRetry:       false,
		},
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) SearchMemory(ctx context.Context, request TaskMemoryRequest) ([]memory.MemoryFact, error) {
	if toolCatalogBuilder.memoryService == nil {
		return nil, nil
	}
	return toolCatalogBuilder.memoryService.SearchMemory(ctx, memory.MemorySearchRequest{
		Query:                     request.Query,
		ReaderPersonID:            request.RequesterPersonID,
		ReaderCircles:             request.PersonAccess.Circles,
		ResourceAccessRules:       request.PersonAccess.ResourceAccessRules,
		ReaderSecurityLevelRank:   request.PersonAccess.SecurityLevelRank,
		ReaderGrantedClasses:      request.PersonAccess.GrantedClasses,
		ConversationID:            request.ConversationID,
		AccessibleConversationIDs: request.AccessibleConversationIDs,
		Namespaces:                request.MemoryNamespaces,
		ExplicitNamespacesOnly:    true,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) LoadPinnedMemory(ctx context.Context, request TaskPinnedMemoryRequest) ([]memory.MemoryFact, error) {
	if toolCatalogBuilder.pinnedMemoryStore == nil {
		return nil, nil
	}
	return toolCatalogBuilder.pinnedMemoryStore.LoadPinnedMemory(ctx, request.RequesterPersonID)
}

func (toolCatalogBuilder *ToolCatalogBuilder) rememberMemoryTool(toolContext context.Context, input memoryRememberToolInput, request ToolCatalogRequest) (agent.ToolResult, error) {
	content := strings.TrimSpace(input.Content)
	if content == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "memory_remember", "memory.remember requires content"), nil
	}
	if request.ActiveCircleConflict {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.Conflict, "memory_remember", "memory.remember has multiple active circle candidates"), nil
	}
	namespace, errorMessage := resolveRememberMemoryNamespace(request)
	if errorMessage != "" {
		return agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "memory_remember", errorMessage), nil
	}
	if toolCatalogBuilder.memoryUpdateQueue == nil {
		return agent.ToolFailureResult(agent.FailureDependencyUnavailable, agent.FailureCodes.Unavailable, "memory_remember", "memory update queue is unavailable"), nil
	}
	accepted, errorValue := toolCatalogBuilder.memoryUpdateQueue.Enqueue(memory.MemoryUpdateJob{
		Namespace:       namespace,
		Content:         content,
		Platform:        request.Platform,
		ConversationID:  request.ConversationID,
		SenderPersonID:  request.RequesterPersonID,
		SourceReference: firstNonEmptyString(request.ReplyTargetID, request.ConversationID),
		OccurredAt:      time.Now().UTC(),
	})
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "memory_remember", errorValue.Error()), nil
	}
	return agent.ToolSuccess(marshalToolResult(accepted)), nil
}

func resolveRememberMemoryNamespace(request ToolCatalogRequest) (memory.MemoryNamespace, string) {
	if strings.TrimSpace(request.ActiveCircleID) == "" {
		return resolvePersonMemoryNamespace(request)
	}
	return resolveCircleMemoryNamespace(request.ActiveCircleID, request)
}

func resolvePersonMemoryNamespace(request ToolCatalogRequest) (memory.MemoryNamespace, string) {
	if strings.TrimSpace(request.RequesterPersonID) == "" {
		return memory.MemoryNamespace{}, "memory.remember person scope requires requester person ID"
	}
	for _, namespace := range request.MemoryNamespaces {
		if namespace.ScopeType == memory.ScopeTypeUser && namespace.ScopePersonID == request.RequesterPersonID {
			return namespace, ""
		}
	}
	return memory.UserNamespace(request.RequesterPersonID), ""
}

func resolveCircleMemoryNamespace(circleID string, request ToolCatalogRequest) (memory.MemoryNamespace, string) {
	normalizedCircleID := strings.ToLower(strings.TrimSpace(circleID))
	if normalizedCircleID == "" {
		return memory.MemoryNamespace{}, "memory.remember circle memory requires active circle context"
	}
	if !personAccessIncludesCircle(request.PersonAccess, normalizedCircleID) {
		return memory.MemoryNamespace{}, "memory.remember circle memory is not accessible"
	}
	for _, namespace := range request.MemoryNamespaces {
		if namespace.ScopeType == memory.ScopeTypeCircle && namespace.ScopeCircleID == normalizedCircleID {
			return namespace, ""
		}
	}
	return memory.CircleNamespace(memory.DefaultWorkspaceID, normalizedCircleID), ""
}

func personAccessIncludesCircle(personAccess policy.PersonAccess, circleID string) bool {
	for _, accessibleCircleID := range personAccess.Circles {
		if strings.ToLower(strings.TrimSpace(accessibleCircleID)) == circleID {
			return true
		}
	}
	return false
}
