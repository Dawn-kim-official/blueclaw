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

type memorySearchStatus string

const (
	memorySearchComplete memorySearchStatus = "complete"
	memorySearchDegraded memorySearchStatus = "degraded"
)

type memorySearchSource string

const (
	memorySearchGraphSource  memorySearchSource = "graph_memory"
	memorySearchPinnedSource memorySearchSource = "pinned_markdown"
	memorySearchRecentSource memorySearchSource = "recent_memory"
)

type memorySearchFact struct {
	FactID     string    `json:"factID"`
	ScopeType  string    `json:"scopeType"`
	Content    string    `json:"content"`
	SourceKind string    `json:"sourceKind"`
	ValidAt    time.Time `json:"validAt"`
	Score      *float64  `json:"score,omitempty"`
}

type memorySearchToolOutput struct {
	Facts        []memorySearchFact   `json:"facts"`
	SearchStatus memorySearchStatus   `json:"searchStatus"`
	Sources      []memorySearchSource `json:"sources"`
}

var (
	memorySearchInputSchema    = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","minLength":1,"pattern":"\\S"}},"required":["query"],"additionalProperties":false}`)
	memorySearchOutputSchema   = json.RawMessage(`{"type":"object","properties":{"facts":{"type":"array","items":{"type":"object","properties":{"factID":{"type":"string"},"scopeType":{"type":"string"},"content":{"type":"string"},"sourceKind":{"type":"string"},"validAt":{"type":"string","format":"date-time"},"score":{"type":"number"}},"required":["factID","scopeType","content","sourceKind","validAt"],"additionalProperties":false}},"searchStatus":{"type":"string","enum":["complete","degraded"]},"sources":{"type":"array","items":{"type":"string","enum":["graph_memory","pinned_markdown","recent_memory"]},"uniqueItems":true}},"required":["facts","searchStatus","sources"],"additionalProperties":false,"allOf":[{"if":{"properties":{"searchStatus":{"const":"complete"}},"required":["searchStatus"]},"then":{"properties":{"sources":{"type":"array","items":{"const":"graph_memory"},"minItems":1,"maxItems":1}}}},{"if":{"properties":{"searchStatus":{"const":"degraded"}},"required":["searchStatus"]},"then":{"properties":{"sources":{"type":"array","items":{"enum":["pinned_markdown","recent_memory"]},"minItems":1,"maxItems":2}}}}]}`)
	memoryRememberInputSchema  = json.RawMessage(`{"type":"object","properties":{"content":{"type":"string","minLength":1,"maxLength":600,"pattern":"\\S"}},"required":["content"],"additionalProperties":false}`)
	memoryRememberOutputSchema = json.RawMessage(`{"type":"object","properties":{"accepted":{"type":"boolean"},"jobID":{"type":"string","pattern":"\\S"},"status":{"type":"string","enum":["persisted","queued_volatile","failed"]},"durability":{"type":"string","enum":["durable","volatile","none"]},"graphitiStatus":{"type":"string"},"markdownUpdated":{"type":"boolean"},"failureCode":{"type":"string"},"failureComponent":{"type":"string"}},"required":["accepted","jobID","status","durability"],"additionalProperties":false}`)
)

func registerMemoryTools(toolCatalogBuilder *ToolCatalogBuilder, toolRegistry *agent.ToolSet, request ToolCatalogRequest) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[memorySearchToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "memory.search",
			Description: "Search Blueclaw graph memory allowed for this requester and conversation. Returns durable facts, preferences, and relationships by meaning, not exact rows; for exact queries, counts, or aggregates over records you stored in a table, use db.sql instead.",
			InputSchema: memorySearchInputSchema,
		},
		Handler: func(toolContext context.Context, input memorySearchToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.searchMemoryTool(toolContext, input, request)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[memoryRememberToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "memory.remember",
			Description: "Store one durable fact, preference, or relationship for the current person or active circle; nothing is remembered unless this tool is called. Keep content a single compact standalone fact. Do not store secrets, one-off requests, transient task details, or small talk; for structured records you query, count, or aggregate over many rows, store them with db.sql instead.",
			InputSchema: memoryRememberInputSchema,
		},
		Handler: func(toolContext context.Context, input memoryRememberToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.rememberMemoryTool(toolContext, input, request)
		},
		Result: agent.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) searchMemoryTool(toolContext context.Context, input memorySearchToolInput, request ToolCatalogRequest) (agent.ToolResult, error) {
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "memory_search", "memory.search query is required"), nil
	}
	if request.ActiveCircleConflict {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.Conflict, "memory_search", "memory.search has multiple active circle candidates"), nil
	}
	memoryRequest := TaskMemoryRequest{
		Query:                     query,
		RequesterPersonID:         request.RequesterPersonID,
		ConversationID:            request.ConversationID,
		PersonAccess:              request.PersonAccess,
		MemoryNamespaces:          searchMemoryNamespaces(request),
		AccessibleConversationIDs: request.AccessibleConversationIDs,
	}
	if !toolCatalogBuilder.canSearchGraphMemory() {
		return toolCatalogBuilder.searchFallbackMemoryTool(toolContext, memoryRequest)
	}
	memoryFacts, errorValue := toolCatalogBuilder.SearchMemory(toolContext, memoryRequest)
	if errorValue != nil {
		return toolCatalogBuilder.searchFallbackMemoryTool(toolContext, memoryRequest)
	}
	return memorySearchSuccess(memoryFacts, memorySearchComplete, []memorySearchSource{memorySearchGraphSource}), nil
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

func (toolCatalogBuilder *ToolCatalogBuilder) searchFallbackMemoryTool(ctx context.Context, request TaskMemoryRequest) (agent.ToolResult, error) {
	memoryFacts, sources := toolCatalogBuilder.searchFallbackMemory(ctx, request)
	if len(memoryFacts) == 0 {
		return memorySearchUnavailableResult(), nil
	}
	return memorySearchSuccess(memoryFacts, memorySearchDegraded, sources), nil
}

func memorySearchSuccess(memoryFacts []memory.MemoryFact, searchStatus memorySearchStatus, sources []memorySearchSource) agent.ToolResult {
	output := memorySearchToolOutput{
		Facts:        projectMemorySearchFacts(memoryFacts),
		SearchStatus: searchStatus,
		Sources:      append([]memorySearchSource{}, sources...),
	}
	document := json.RawMessage(marshalToolResult(output))
	return agent.ToolSuccessData(string(document), document)
}

func projectMemorySearchFacts(memoryFacts []memory.MemoryFact) []memorySearchFact {
	projectedFacts := make([]memorySearchFact, 0, len(memoryFacts))
	for _, memoryFact := range memoryFacts {
		projectedFact := memorySearchFact{
			FactID:     memoryFact.FactID,
			ScopeType:  memoryFact.ScopeType,
			Content:    memoryFact.Content,
			SourceKind: memoryFact.SourceKind,
			ValidAt:    memoryFact.ValidAt,
		}
		if memoryFact.Score != 0 {
			projectedFact.Score = &memoryFact.Score
		}
		projectedFacts = append(projectedFacts, projectedFact)
	}
	return projectedFacts
}

func (toolCatalogBuilder *ToolCatalogBuilder) searchFallbackMemory(ctx context.Context, request TaskMemoryRequest) ([]memory.MemoryFact, []memorySearchSource) {
	memoryFacts := []memory.MemoryFact{}
	sources := []memorySearchSource{}
	pinnedMemoryFacts, pinnedError := toolCatalogBuilder.loadPinnedFallbackMemory(ctx, request)
	if pinnedError == nil && len(pinnedMemoryFacts) > 0 {
		memoryFacts = append(memoryFacts, pinnedMemoryFacts...)
		sources = append(sources, memorySearchPinnedSource)
	}
	localMemoryFacts, localError := toolCatalogBuilder.SearchLocalMemory(ctx, request)
	if localError == nil && len(localMemoryFacts) > 0 {
		memoryFacts = appendMemoryFacts(memoryFacts, localMemoryFacts)
		sources = append(sources, memorySearchRecentSource)
	}
	return memoryFacts, sources
}

func appendMemoryFacts(memoryFacts []memory.MemoryFact, additionalMemoryFacts []memory.MemoryFact) []memory.MemoryFact {
	seenMemoryFacts := map[string]bool{}
	for _, memoryFact := range memoryFacts {
		seenMemoryFacts[memorySearchFactKey(memoryFact)] = true
	}
	for _, memoryFact := range additionalMemoryFacts {
		key := memorySearchFactKey(memoryFact)
		if key == "" || seenMemoryFacts[key] {
			continue
		}
		seenMemoryFacts[key] = true
		memoryFacts = append(memoryFacts, memoryFact)
	}
	return memoryFacts
}

func memorySearchFactKey(memoryFact memory.MemoryFact) string {
	content := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(memoryFact.Content))), " ")
	if content == "" {
		return ""
	}
	return memoryFact.NamespaceID + ":" + content
}

func (toolCatalogBuilder *ToolCatalogBuilder) canSearchGraphMemory() bool {
	return toolCatalogBuilder.memoryService != nil && toolCatalogBuilder.memoryService.HasGraphStore()
}

func (toolCatalogBuilder *ToolCatalogBuilder) SearchMemory(ctx context.Context, request TaskMemoryRequest) ([]memory.MemoryFact, error) {
	if toolCatalogBuilder.memoryService == nil {
		return nil, nil
	}
	return toolCatalogBuilder.memoryService.SearchMemory(ctx, memorySearchRequest(request))
}

func (toolCatalogBuilder *ToolCatalogBuilder) SearchLocalMemory(ctx context.Context, request TaskMemoryRequest) ([]memory.MemoryFact, error) {
	if toolCatalogBuilder.memoryService == nil {
		return nil, nil
	}
	return toolCatalogBuilder.memoryService.SearchLocalMemory(ctx, memorySearchRequest(request))
}

func memorySearchRequest(request TaskMemoryRequest) memory.MemorySearchRequest {
	return memory.MemorySearchRequest{
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
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) LoadPinnedMemory(ctx context.Context, request TaskPinnedMemoryRequest) ([]memory.MemoryFact, error) {
	if toolCatalogBuilder.pinnedMemoryStore == nil {
		return nil, nil
	}
	return toolCatalogBuilder.pinnedMemoryStore.LoadPinnedMemory(ctx, request.RequesterPersonID)
}

func (toolCatalogBuilder *ToolCatalogBuilder) loadPinnedFallbackMemory(ctx context.Context, request TaskMemoryRequest) ([]memory.MemoryFact, error) {
	if toolCatalogBuilder.pinnedMemoryStore == nil {
		return nil, nil
	}
	return toolCatalogBuilder.pinnedMemoryStore.LoadPinnedMemoryForNamespaces(ctx, request.MemoryNamespaces)
}

func (toolCatalogBuilder *ToolCatalogBuilder) rememberMemoryTool(toolContext context.Context, input memoryRememberToolInput, request ToolCatalogRequest) (agent.ToolResult, error) {
	content := strings.TrimSpace(input.Content)
	if gateMessage := memory.RememberContentGateMessage(content); gateMessage != "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "memory_remember", gateMessage), nil
	}
	if request.ActiveCircleConflict {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.Conflict, "memory_remember", "memory.remember has multiple active circle candidates"), nil
	}
	namespace, errorMessage := resolveRememberMemoryNamespace(request)
	if errorMessage != "" {
		return agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "memory_remember", errorMessage), nil
	}
	job := memory.PrepareMemoryUpdateJob(memory.MemoryUpdateJob{
		Namespace:       namespace,
		Content:         content,
		Platform:        request.Platform,
		ConversationID:  request.ConversationID,
		SenderPersonID:  request.RequesterPersonID,
		SourceReference: firstNonEmptyString(request.ReplyTargetID, request.ConversationID),
		OccurredAt:      time.Now().UTC(),
	})
	if toolCatalogBuilder.canPersistMemoryUpdate(job) {
		return toolCatalogBuilder.persistMemoryUpdateTool(toolContext, job), nil
	}
	return toolCatalogBuilder.enqueueVolatileMemoryUpdateTool(job), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) canPersistMemoryUpdate(job memory.MemoryUpdateJob) bool {
	return toolCatalogBuilder.pinnedMemoryStore != nil &&
		job.Namespace.ScopeType == memory.ScopeTypeUser &&
		strings.TrimSpace(job.Namespace.ScopePersonID) != ""
}

func (toolCatalogBuilder *ToolCatalogBuilder) persistMemoryUpdateTool(ctx context.Context, job memory.MemoryUpdateJob) agent.ToolResult {
	isUpdated, errorValue := toolCatalogBuilder.pinnedMemoryStore.MergePersonMemory(ctx, job.Namespace.ScopePersonID, job.Content)
	if errorValue != nil {
		return memoryRememberResult(failedMemoryUpdate(job.JobID, "markdown_write_failed", "markdown"))
	}
	job.SkipMarkdown = true
	accepted := memory.MemoryUpdateAccepted{
		Accepted:        true,
		JobID:           job.JobID,
		Status:          "persisted",
		Durability:      "durable",
		MarkdownUpdated: isUpdated,
	}
	if toolCatalogBuilder.memoryUpdateQueue == nil {
		accepted.GraphitiStatus = "queue_unavailable"
		return memoryRememberResult(accepted)
	}
	graphitiAccepted, graphitiError := toolCatalogBuilder.memoryUpdateQueue.Enqueue(job)
	accepted.JobID = firstNonEmptyString(graphitiAccepted.JobID, accepted.JobID)
	accepted.GraphitiStatus = graphitiUpdateStatus(graphitiAccepted, graphitiError)
	return memoryRememberResult(accepted)
}

func (toolCatalogBuilder *ToolCatalogBuilder) enqueueVolatileMemoryUpdateTool(job memory.MemoryUpdateJob) agent.ToolResult {
	if toolCatalogBuilder.memoryUpdateQueue == nil {
		return memoryRememberResult(failedMemoryUpdate(job.JobID, "queue_unavailable", "queue"))
	}
	accepted, errorValue := toolCatalogBuilder.memoryUpdateQueue.Enqueue(job)
	if errorValue != nil {
		return memoryRememberResult(failedMemoryUpdate(job.JobID, memoryUpdateFailureCode(errorValue), "queue"))
	}
	return memoryRememberResult(queuedVolatileMemoryUpdate(accepted))
}

func memoryRememberResult(accepted memory.MemoryUpdateAccepted) agent.ToolResult {
	document := json.RawMessage(marshalToolResult(accepted))
	if !accepted.Accepted {
		return agent.ToolFailureData(
			agent.FailureExternalService,
			agent.FailureCodes.OperationFailed,
			firstNonEmptyString(accepted.FailureComponent, "memory_remember"),
			"memory update was not accepted",
			document,
		)
	}
	return agent.ToolSuccessData(string(document), document)
}

func queuedVolatileMemoryUpdate(accepted memory.MemoryUpdateAccepted) memory.MemoryUpdateAccepted {
	accepted.Accepted = true
	if strings.TrimSpace(accepted.Status) == "" {
		accepted.Status = "queued_volatile"
	}
	if strings.TrimSpace(accepted.Durability) == "" {
		accepted.Durability = "volatile"
	}
	if strings.TrimSpace(accepted.GraphitiStatus) == "" {
		accepted.GraphitiStatus = "queued"
	}
	return accepted
}

func failedMemoryUpdate(jobID string, failureCode string, failureComponent string) memory.MemoryUpdateAccepted {
	return memory.MemoryUpdateAccepted{
		Accepted:         false,
		JobID:            jobID,
		Status:           "failed",
		Durability:       "none",
		GraphitiStatus:   "not_queued",
		FailureCode:      failureCode,
		FailureComponent: failureComponent,
	}
}

func graphitiUpdateStatus(accepted memory.MemoryUpdateAccepted, errorValue error) string {
	if errorValue != nil {
		return memoryUpdateFailureCode(errorValue)
	}
	if strings.TrimSpace(accepted.GraphitiStatus) != "" {
		return accepted.GraphitiStatus
	}
	return "queued"
}

func memoryUpdateFailureCode(errorValue error) string {
	errorMessage := strings.ToLower(strings.TrimSpace(errorValue.Error()))
	if strings.Contains(errorMessage, "full") {
		return "queue_full"
	}
	if strings.Contains(errorMessage, "unavailable") {
		return "queue_unavailable"
	}
	return "operation_failed"
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
