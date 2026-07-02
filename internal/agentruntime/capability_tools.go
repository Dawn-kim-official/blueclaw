package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"sort"
	"strings"

	"blueclaw/internal/access"
	"blueclaw/internal/agent"
	"blueclaw/internal/mcp"
)

var idempotentCapabilityToolNames = map[string]bool{
	"message.send":       true,
	"mail.message.send":  true,
	"google.gmail.send":  true,
	"slack.message.send": true,
}

func capabilityToolIdempotencyKey(toolContext context.Context, toolName string) string {
	if !idempotentCapabilityToolNames[strings.TrimSpace(toolName)] {
		return ""
	}
	taskRunID := strings.TrimSpace(agent.TaskRunIDFromContext(toolContext))
	observationID := strings.TrimSpace(agent.ObservationIDFromContext(toolContext))
	if taskRunID == "" || observationID == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(taskRunID + ":" + observationID + ":" + strings.TrimSpace(toolName)))
	return hex.EncodeToString(digest[:])
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerMCPTools(toolRegistry *agent.ToolSet) {
	if toolCatalogBuilder.mcpRegistry == nil {
		return
	}
	for _, toolDefinition := range toolCatalogBuilder.mcpRegistry.ListTool() {
		mcpToolDefinition := toolDefinition
		toolRegistry.RegisterTool(agent.ToolDefinition{
			Name:        mcpToolDefinition.Name,
			Description: mcpToolDescription(mcpToolDefinition),
			InputSchema: mcpToolDefinition.InputSchema,
		}, func(toolContext context.Context, toolInvocation agent.ToolInvocation) (agent.ToolResult, error) {
			output, errorValue := toolCatalogBuilder.mcpRegistry.InvokeTool(toolContext, mcp.Invocation{
				ServerName: mcpToolDefinition.ServerName,
				ToolName:   mcpToolDefinition.Name,
				Input:      string(toolInvocation.Input),
			})
			if errorValue != nil {
				return agent.ToolResult{}, errorValue
			}
			return agent.ToolSuccess(output), nil
		})
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerCapabilityTools(toolRegistry *agent.ToolSet, request ToolCatalogRequest) {
	for _, capabilityToolDescriptor := range toolCatalogBuilder.capabilityToolDefinitions() {
		toolDescriptor := capabilityToolDescriptor
		toolName := toolDescriptor.Name
		if toolName == "file.read" {
			continue
		}
		if toolName == "ask.confirm" {
			continue
		}
		if request.IsScheduledRun && isInteractiveCapabilityTool(toolName) {
			continue
		}
		toolDescription := firstNonEmptyString(toolDescriptor.Description, defaultCapabilityToolDescription(toolName))
		toolRegistry.RegisterBoundTool(agent.BoundTool{
			Definition: agent.ToolDefinition{
				Name:             toolName,
				Description:      toolDescription,
				InputSchema:      toolDescriptor.InputSchema,
				OutputSchema:     toolDescriptor.OutputSchema,
				PolicyResource:   toolDescriptor.PolicyResource,
				SideEffectClass:  toolDescriptor.SideEffectClass,
				RequiresApproval: toolDescriptor.RequiresApproval,
			},
			Availability: capabilityToolAvailability(toolDescriptor, request),
			Handler: func(toolContext context.Context, toolInvocation agent.ToolInvocation) (agent.ToolResult, error) {
				return toolCatalogBuilder.invokeCapabilityOperation(toolContext, toolName, toolDescriptor, request, json.RawMessage(toolInvocation.Input))
			},
		})
	}
	toolCatalogBuilder.registerGenericCapabilityTool(toolRegistry, request)
}

func (toolCatalogBuilder *ToolCatalogBuilder) invokeCapabilityOperation(toolContext context.Context, operation string, toolDescriptor CapabilityToolDescriptor, request ToolCatalogRequest, rawInput json.RawMessage) (agent.ToolResult, error) {
	var response struct {
		Content      string          `json:"content"`
		IsError      bool            `json:"isError"`
		Status       string          `json:"status"`
		Message      string          `json:"message"`
		ErrorCode    string          `json:"errorCode"`
		FailureStage string          `json:"failureStage"`
		Retryable    bool            `json:"retryable"`
		SafeRetry    bool            `json:"safeRetry"`
		Result       json.RawMessage `json:"result"`
	}
	policyResource := firstNonEmptyString(toolDescriptor.PolicyResource, "tool:"+operation)
	if !access.CanAccess(access.Request{PersonAccess: request.PersonAccess, Action: access.ActionExecute, Resource: policyResource}) {
		return agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "capability_access", "current account cannot execute this tool"), nil
	}
	toolInput, toolFailure, errorValue := toolCatalogBuilder.prepareCapabilityToolInput(toolContext, operation, request, rawInput)
	if toolFailure != nil {
		return *toolFailure, nil
	}
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "capability_input", errorValue.Error()), nil
	}
	if missing := missingRequiredCapabilityInputFields(toolDescriptor.InputSchema, toolInput); len(missing) > 0 {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "capability_input", operation+" needs these input fields: "+strings.Join(missing, ", ")+". Call "+operation+" again with input set to an object that contains them."), nil
	}
	if errorValue := toolCatalogBuilder.validateCapabilityToolInputAccess(operation, request, toolInput); errorValue != nil {
		return agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "file_read_access", errorValue.Error()), nil
	}
	errorValue = toolCatalogBuilder.capabilityClient.PostJSON(toolContext, "/v1/tools/"+url.PathEscape(operation)+"/invoke", capabilityToolRequest(toolContext, operation, request, toolInput), &response)
	if errorValue != nil {
		return agent.ToolResult{}, errorValue
	}
	if !response.IsError && response.Status != "error" && response.Status != "denied" {
		toolFailure, errorValue := toolCatalogBuilder.handleCapabilityToolSuccess(toolContext, operation, request, &response.Result)
		if toolFailure != nil {
			return *toolFailure, nil
		}
		if errorValue != nil {
			return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "capability_result", errorValue.Error()), nil
		}
	}
	content := strings.TrimSpace(response.Content)
	if content == "" && len(response.Result) > 0 {
		content = string(response.Result)
	}
	isError := response.IsError || response.Status == "error" || response.Status == "denied"
	return capabilityToolResult(content, response.Result, isError, response.Message, response.ErrorCode, response.FailureStage, response.Retryable, response.SafeRetry), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerGenericCapabilityTool(toolRegistry *agent.ToolSet, request ToolCatalogRequest) {
	if toolCatalogBuilder.capabilityClient.HTTPClient == nil && strings.TrimSpace(toolCatalogBuilder.capabilityClient.Endpoint) == "" {
		return
	}
	toolRegistry.RegisterBoundTool(agent.BoundTool{
		Definition: agent.ToolDefinition{
			Name:        agent.CapabilityInvokeToolName,
			Description: toolCatalogBuilder.genericCapabilityToolDescription(),
			InputSchema: genericCapabilityInvokeInputSchema(),
		},
		Availability: agent.ToolAvailability{Status: agent.ToolAvailabilityAvailable},
		Handler: func(toolContext context.Context, toolInvocation agent.ToolInvocation) (agent.ToolResult, error) {
			var call struct {
				Operation string          `json:"operation"`
				Input     json.RawMessage `json:"input"`
			}
			if errorValue := json.Unmarshal(toolInvocation.Input, &call); errorValue != nil {
				return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "capability_input", "capability.invoke requires an operation name and an input object"), nil
			}
			operation := strings.TrimSpace(call.Operation)
			if operation == "" {
				return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "capability_input", "capability.invoke requires an operation name"), nil
			}
			if !toolRegistry.IsRegistered(operation) {
				return toolCatalogBuilder.unknownCapabilityOperationResult(operation), nil
			}
			return toolRegistry.InvokeRegistered(toolContext, agent.ToolInvocation{ToolName: operation, Input: call.Input})
		},
	})
}

var genericCapabilityCatalogExcluded = map[string]bool{
	"llm.text":         true,
	"llm.structured":   true,
	"embedding.create": true,
	"platform.reply":   true,
	"file.read":        true,
	"image.read":       true,
	"document.read":    true,
	"ask.confirm":      true,
	"ask.input":        true,
}

func (toolCatalogBuilder *ToolCatalogBuilder) genericCapabilityToolDescription() string {
	lines := []string{
		"Invoke a workspace capability operation by name. Set operation to one of the operations below and set input to an object holding that operation's fields. Each operation lists its input fields as { name type (required), ... } — you MUST include every (required) field with a real value; an empty or partial input is rejected, so never call with input:{}. Identity, approval, and delivery are handled by the runtime — never pass requester identity in input.",
		"",
		"Available operations:",
	}
	for _, entry := range toolCatalogBuilder.capabilityCatalogEntries() {
		lines = append(lines, "- "+entry)
	}
	return strings.Join(lines, "\n")
}

func (toolCatalogBuilder *ToolCatalogBuilder) capabilityCatalogEntries() []string {
	entries := []string{}
	seenName := map[string]bool{}
	for _, toolDescriptor := range toolCatalogBuilder.capabilityToolDefinitions() {
		name := strings.TrimSpace(toolDescriptor.Name)
		if name == "" || genericCapabilityCatalogExcluded[name] || seenName[name] {
			continue
		}
		seenName[name] = true
		entry := name + ": " + capabilityCatalogSummary(toolDescriptor.Description)
		if parameters := capabilityCatalogParameters(toolDescriptor.InputSchema); parameters != "" {
			entry += " — input: " + parameters
		}
		if toolDescriptor.RequiresApproval {
			entry += " — needs the user's approval before it runs; put a one-line confirmation of exactly what you will do in continue.message so the user sees what they are approving."
		}
		entries = append(entries, entry)
	}
	sort.Strings(entries)
	return entries
}

func capabilityCatalogParameters(inputSchema json.RawMessage) string {
	if len(inputSchema) == 0 {
		return ""
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if json.Unmarshal(inputSchema, &schema) != nil || len(schema.Properties) == 0 {
		return ""
	}
	requiredParameter := map[string]bool{}
	for _, name := range schema.Required {
		requiredParameter[name] = true
	}
	requiredNames := []string{}
	optionalNames := []string{}
	for name := range schema.Properties {
		if requiredParameter[name] {
			requiredNames = append(requiredNames, name)
		} else {
			optionalNames = append(optionalNames, name)
		}
	}
	sort.Strings(requiredNames)
	sort.Strings(optionalNames)
	fields := []string{}
	for _, name := range requiredNames {
		fields = append(fields, name+" "+capabilityCatalogFieldType(schema.Properties[name])+" (required)")
	}
	for _, name := range optionalNames {
		fields = append(fields, name+" "+capabilityCatalogFieldType(schema.Properties[name]))
	}
	return "{ " + strings.Join(fields, ", ") + " }"
}

func capabilityCatalogFieldType(property json.RawMessage) string {
	var document struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(property, &document) == nil && strings.TrimSpace(document.Type) != "" {
		return document.Type
	}
	return "value"
}

func missingRequiredCapabilityInputFields(inputSchema json.RawMessage, toolInput json.RawMessage) []string {
	if len(inputSchema) == 0 {
		return nil
	}
	var schema struct {
		Required []string `json:"required"`
	}
	if json.Unmarshal(inputSchema, &schema) != nil || len(schema.Required) == 0 {
		return nil
	}
	input := map[string]json.RawMessage{}
	if len(toolInput) > 0 {
		json.Unmarshal(toolInput, &input)
	}
	missing := []string{}
	for _, field := range schema.Required {
		if value, exists := input[field]; !exists || isEmptyCapabilityInputValue(value) {
			missing = append(missing, field)
		}
	}
	return missing
}

func isEmptyCapabilityInputValue(value json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(value))
	return trimmed == "" || trimmed == "null" || trimmed == `""` || trimmed == "{}" || trimmed == "[]"
}

func capabilityCatalogSummary(description string) string {
	trimmed := strings.TrimSpace(description)
	if trimmed == "" {
		return "Workspace capability operation."
	}
	if index := strings.IndexByte(trimmed, '.'); index > 0 {
		return strings.TrimSpace(trimmed[:index+1])
	}
	if len(trimmed) > 140 {
		return strings.TrimSpace(trimmed[:140])
	}
	return trimmed
}

func genericCapabilityInvokeInputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"operation":{"type":"string","description":"The capability operation name from the available operations list in this tool's description."},"input":{"type":"object","description":"The parameters object for the chosen operation."}},"required":["operation"]}`)
}

func (toolCatalogBuilder *ToolCatalogBuilder) unknownCapabilityOperationResult(operation string) agent.ToolResult {
	message := "operation \"" + operation + "\" is not a valid capability operation."
	if suggestions := toolCatalogBuilder.suggestCapabilityOperations(operation); len(suggestions) > 0 {
		message += " Did you mean: " + strings.Join(suggestions, ", ") + "?"
	}
	message += " Use an exact operation name from this tool's available operations list."
	return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "capability.invoke", message)
}

func (toolCatalogBuilder *ToolCatalogBuilder) suggestCapabilityOperations(operation string) []string {
	domainSegments, finalSegment := capabilityOperationSegments(operation)
	type scoredOperation struct {
		name        string
		score       int
		matchDomain bool
	}
	scoredOperations := []scoredOperation{}
	for _, entry := range toolCatalogBuilder.capabilityCatalogEntries() {
		name := strings.TrimSpace(strings.SplitN(entry, ":", 2)[0])
		candidateDomainSegments, candidateFinalSegment := capabilityOperationSegments(name)
		score := 0
		matchDomain := false
		for segment := range candidateDomainSegments {
			if domainSegments[segment] {
				score += 2
				matchDomain = true
			}
		}
		if finalSegment != "" && candidateFinalSegment == finalSegment {
			score++
		}
		if score > 0 {
			scoredOperations = append(scoredOperations, scoredOperation{name: name, score: score, matchDomain: matchDomain})
		}
	}
	hasDomainMatch := false
	for _, scoredOperation := range scoredOperations {
		if scoredOperation.matchDomain {
			hasDomainMatch = true
			break
		}
	}
	sort.SliceStable(scoredOperations, func(leftIndex int, rightIndex int) bool {
		return scoredOperations[leftIndex].score > scoredOperations[rightIndex].score
	})
	suggestions := []string{}
	for _, scoredOperation := range scoredOperations {
		if hasDomainMatch && !scoredOperation.matchDomain {
			continue
		}
		suggestions = append(suggestions, scoredOperation.name)
		if len(suggestions) >= 6 {
			break
		}
	}
	return suggestions
}

func capabilityOperationSegments(operation string) (map[string]bool, string) {
	segments := strings.Split(operation, ".")
	domainSegments := map[string]bool{}
	finalSegment := ""
	for index, segment := range segments {
		trimmedSegment := strings.TrimSpace(segment)
		if trimmedSegment == "" {
			continue
		}
		if index == len(segments)-1 {
			finalSegment = trimmedSegment
			continue
		}
		domainSegments[trimmedSegment] = true
	}
	return domainSegments, finalSegment
}

func isInteractiveCapabilityTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "user.confirm", "user.input", "ask.confirm", "ask.input":
		return true
	default:
		return false
	}
}

func defaultCapabilityToolDescription(toolName string) string {
	switch strings.TrimSpace(toolName) {
	case "document.read":
		return "Read a workspace document path as Markdown using the shared document conversion pipeline. Prefer file.preview for paths listed in the conversation attachment catalog."
	case "image.read":
		return "Load an image path from the conversation attachment catalog or workspace into the model as an image input. Use only when visual inspection is needed; do not call for PDFs or text documents."
	case "message.context":
		return "Read the current platform conversation, thread, channel, requester, and bot message context."
	case "message.search":
		return "Search platform messages by scope, author, and queries. queries is an OR list; use one item for a single keyword. Returns compact messageIDs before previews. Use before deleting or editing messages described in natural language."
	case "message.delete":
		return "Delete assistant bot messages by exact messageIDs returned from message.search. Deletes one selected page at a time and never searches internally."
	case "message.send":
		return "Send a platform message to a direct message, current thread, current channel, or named channel. Recipient resolution and ambiguity are handled by this tool."
	case "message.update":
		return "Update an assistant bot message text or pin state. Use only for platform messages that should be edited or pinned."
	default:
		return "Workspace capability tool"
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) capabilityToolDefinitions() []CapabilityToolDescriptor {
	toolDescriptors := copyCapabilityToolDescriptors(toolCatalogBuilder.capabilityToolDescriptors)
	toolNameByName := map[string]bool{}
	for _, toolDescriptor := range toolDescriptors {
		toolNameByName[strings.TrimSpace(toolDescriptor.Name)] = true
	}
	for _, toolName := range toolCatalogBuilder.capabilityToolNames {
		if !toolNameByName[toolName] {
			toolDescriptors = append(toolDescriptors, CapabilityToolDescriptor{Name: toolName})
		}
	}
	toolCatalogBuilder.overlayLiveCapabilityInputSchemas(toolDescriptors)
	return toolDescriptors
}

func capabilityToolAvailability(toolDescriptor CapabilityToolDescriptor, request ToolCatalogRequest) agent.ToolAvailability {
	policyResource := firstNonEmptyString(toolDescriptor.PolicyResource, "tool:"+toolDescriptor.Name)
	if !access.CanAccess(access.Request{PersonAccess: request.PersonAccess, Action: access.ActionExecute, Resource: policyResource}) {
		return agent.ToolAvailability{Status: agent.ToolAvailabilityDenied, Reason: "access denied"}
	}
	if toolDescriptor.RequiresApproval {
		if isApprovalExemptCapabilityTool(toolDescriptor.Name, request) {
			return agent.ToolAvailability{Status: agent.ToolAvailabilityAvailable}
		}
		return agent.ToolAvailability{Status: agent.ToolAvailabilityAvailable}
	}
	return agent.ToolAvailability{Status: agent.ToolAvailabilityAvailable}
}

func capabilityToolResult(content string, data json.RawMessage, isFailed bool, message string, errorCode string, failureStage string, retryable bool, safeRetry bool) agent.ToolResult {
	result := agent.ToolResult{
		Output:          agent.ToolOutput{Content: content, Data: data},
		Attachments:     capabilityAttachments(data),
		RecoveryActions: capabilityRecoveryActions(data),
	}
	if !isFailed {
		return result
	}
	result.Failure = &agent.ToolFailure{
		Kind:            capabilityFailureKind(errorCode, failureStage),
		Code:            agent.CanonicalFailureCode(agent.FailureCode(firstNonEmptyString(errorCode, capabilityResultString(data, "errorCode"), agent.FailureCodes.OperationFailed.String()))),
		Stage:           firstNonEmptyString(failureStage, capabilityResultString(data, "failureStage"), "capability_invoke"),
		UserSafeSummary: firstNonEmptyString(message, capabilityResultString(data, "message"), content),
		Retryable:       retryable || capabilityResultBoolean(data, "retryable"),
		SafeRetry:       safeRetry || capabilityResultBoolean(data, "safeRetry"),
		RecoveryHints:   capabilityRecoveryHints(data),
	}
	return result
}

func capabilityFailureKind(errorCode string, failureStage string) agent.FailureKind {
	normalizedText := strings.ToLower(strings.TrimSpace(errorCode + " " + failureStage))
	if strings.Contains(normalizedText, "denied") || strings.Contains(normalizedText, "permission") || strings.Contains(normalizedText, "unauthorized") {
		return agent.FailurePermissionDenied
	}
	if strings.Contains(normalizedText, "invalid") || strings.Contains(normalizedText, "schema") || strings.Contains(normalizedText, "input") {
		return agent.FailureInvalidInput
	}
	if strings.Contains(normalizedText, "rate") || strings.Contains(normalizedText, "quota") {
		return agent.FailureRateLimited
	}
	return agent.FailureExternalService
}

func isApprovalExemptCapabilityTool(toolName string, request ToolCatalogRequest) bool {
	if strings.TrimSpace(toolName) != "message.send" {
		return false
	}
	return request.IsScheduledRun || request.IsApprovalContinuation
}

func capabilityToolRequest(toolContext context.Context, toolName string, request ToolCatalogRequest, toolInput json.RawMessage) map[string]any {
	contextDocument := map[string]any{
		"requesterPersonID":       request.RequesterPersonID,
		"requesterEmail":          request.RequesterEmail,
		"requesterName":           request.RequesterName,
		"requesterPlatformUserID": request.RequesterPlatformUserID,
		"taskSource":              string(request.TaskSource),
		"isScheduledRun":          request.IsScheduledRun,
		"isApprovalContinuation":  request.IsApprovalContinuation,
		"conversationID":          request.ConversationID,
		"conversationType":        request.ConversationType,
		"channelID":               request.ConversationChannelID,
		"channelName":             request.ConversationChannelName,
		"replyTargetID":           request.ReplyTargetID,
		"platform":                request.Platform,
	}
	if !request.ScheduledRun.IsEmpty() {
		contextDocument["scheduledRun"] = request.ScheduledRun
	}
	requestDocument := map[string]any{
		"toolName":       toolName,
		"input":          toolInput,
		"idempotencyKey": capabilityToolIdempotencyKey(toolContext, toolName),
		"context":        contextDocument,
	}
	if shouldRequireCompanionBrowser(toolContext, toolName, toolInput) {
		requestDocument["executionMode"] = "companion"
		requestDocument["requiresUserPresence"] = true
		requestDocument["privacyClass"] = "user_browser"
	}
	return requestDocument
}

func (toolCatalogBuilder *ToolCatalogBuilder) prepareCapabilityToolInput(toolContext context.Context, toolName string, request ToolCatalogRequest, toolInput json.RawMessage) (json.RawMessage, *agent.ToolResult, error) {
	if siteToolNeedsSourceBundle(toolName) {
		return toolCatalogBuilder.enrichSitePublishInput(toolContext, request, toolInput)
	}
	if capabilityToolNeedsWorkspacePath(toolName) {
		input, errorValue := toolCatalogBuilder.resolveCapabilityWorkspacePathInput(toolContext, toolName, request, toolInput)
		return input, nil, errorValue
	}
	return toolInput, nil, nil
}

func capabilityToolNeedsWorkspacePath(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "document.read", "image.read":
		return true
	default:
		return false
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) resolveCapabilityWorkspacePathInput(toolContext context.Context, toolName string, request ToolCatalogRequest, toolInput json.RawMessage) (json.RawMessage, error) {
	inputDocument := map[string]any{}
	if len(toolInput) > 0 {
		if errorValue := json.Unmarshal(toolInput, &inputDocument); errorValue != nil {
			return nil, errorValue
		}
	}
	path, _ := inputDocument["path"].(string)
	if materialID, _ := inputDocument["materialID"].(string); strings.TrimSpace(materialID) != "" {
		material, errorValue := resolveReadableAttachmentMaterial(toolContext, request, materialID)
		if errorValue != nil {
			return nil, errorValue
		}
		if errorValue := validateAttachmentMaterialTool(toolName, material); errorValue != nil {
			return nil, errorValue
		}
		path = material.Path
		delete(inputDocument, "materialID")
	}
	resolvedPath, errorValue := toolCatalogBuilder.resolveReadableCapabilityPath(request, path)
	if errorValue != nil {
		return nil, errorValue
	}
	inputDocument["path"] = toolCatalogBuilder.agentWorkspacePath(resolvedPath.ConcretePath)
	return json.Marshal(inputDocument)
}

func (toolCatalogBuilder *ToolCatalogBuilder) resolveReadableCapabilityPath(request ToolCatalogRequest, path string) (ResolvedWorkspacePath, error) {
	resolvedPath, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).Resolve(path, WorkspaceScopeForRequest(toolCatalogBuilder.workspaceRootPath, request, ""))
	if errorValue != nil {
		return ResolvedWorkspacePath{}, errorValue
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(request.PersonAccess, access.ActionRead, resolvedPath.ConcretePath) {
		return ResolvedWorkspacePath{}, errors.New("current account cannot read this file")
	}
	return resolvedPath, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) handleCapabilityToolSuccess(toolContext context.Context, toolName string, request ToolCatalogRequest, result *json.RawMessage) (*agent.ToolResult, error) {
	switch strings.TrimSpace(toolName) {
	case "site.create":
		return toolCatalogBuilder.materializeSiteCreateResult(toolContext, request, result)
	default:
		return nil, nil
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) validateCapabilityToolInputAccess(toolName string, request ToolCatalogRequest, toolInput json.RawMessage) error {
	return nil
}

func shouldRequireCompanionBrowser(toolContext context.Context, toolName string, toolInput json.RawMessage) bool {
	trimmedToolName := strings.TrimSpace(toolName)
	if !strings.HasPrefix(trimmedToolName, "browser.") {
		return false
	}
	switch trimmedToolName {
	case "browser.handoff", "browser.screenshot":
		return true
	}
	return browserInputUsesPrivateURL(toolInput)
}

func containsAny(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func browserInputUsesPrivateURL(toolInput json.RawMessage) bool {
	var input struct {
		URL      string `json:"url"`
		StartURL string `json:"startURL"`
	}
	if json.Unmarshal(toolInput, &input) != nil {
		return false
	}
	return isPrivateBrowserURL(firstNonEmptyString(input.URL, input.StartURL))
}

func isPrivateBrowserURL(value string) bool {
	parsedURL, errorValue := url.Parse(strings.TrimSpace(value))
	if errorValue != nil || parsedURL.Hostname() == "" {
		return false
	}
	hostname := strings.ToLower(strings.TrimSpace(parsedURL.Hostname()))
	if hostname == "localhost" || strings.HasSuffix(hostname, ".local") {
		return true
	}
	ipAddress := net.ParseIP(hostname)
	if ipAddress == nil {
		return false
	}
	return ipAddress.IsLoopback() || ipAddress.IsPrivate() || ipAddress.IsLinkLocalUnicast()
}

func capabilityAttachments(result json.RawMessage) []agent.FileAttachment {
	if len(result) == 0 {
		return nil
	}
	var attachment capabilityFileAttachment
	if errorValue := json.Unmarshal(result, &attachment); errorValue == nil && strings.TrimSpace(attachment.DevicePath) != "" {
		return []agent.FileAttachment{attachment.agentFileAttachment()}
	}
	var document struct {
		Attachments []capabilityFileAttachment `json:"attachments"`
	}
	if errorValue := json.Unmarshal(result, &document); errorValue != nil {
		return nil
	}
	attachments := []agent.FileAttachment{}
	for _, candidate := range document.Attachments {
		if strings.TrimSpace(candidate.DevicePath) != "" {
			attachments = append(attachments, candidate.agentFileAttachment())
		}
	}
	return attachments
}

type capabilityFileAttachment struct {
	DevicePath    string `json:"devicePath"`
	Filename      string `json:"filename,omitempty"`
	ContentType   string `json:"contentType,omitempty"`
	SizeBytes     int64  `json:"sizeBytes,omitempty"`
	Title         string `json:"title,omitempty"`
	ContentBase64 string `json:"contentBase64,omitempty"`
}

func (attachment capabilityFileAttachment) agentFileAttachment() agent.FileAttachment {
	return agent.FileAttachment{
		DevicePath:    attachment.DevicePath,
		Filename:      attachment.Filename,
		ContentType:   attachment.ContentType,
		SizeBytes:     attachment.SizeBytes,
		Title:         attachment.Title,
		ContentBase64: attachment.ContentBase64,
	}
}

func capabilityRecoveryActions(result json.RawMessage) []agent.RecoveryAction {
	var document struct {
		Recovery *agent.RecoveryAction `json:"recovery"`
	}
	if json.Unmarshal(result, &document) != nil || document.Recovery == nil {
		return nil
	}
	if strings.TrimSpace(document.Recovery.Kind) == "" {
		return nil
	}
	return []agent.RecoveryAction{*document.Recovery}
}

func capabilityRecoveryHints(result json.RawMessage) []agent.RecoveryHint {
	var document struct {
		Recovery *agent.RecoveryAction `json:"recovery"`
	}
	if json.Unmarshal(result, &document) != nil || document.Recovery == nil {
		return nil
	}
	if strings.TrimSpace(document.Recovery.Kind) == "" {
		return nil
	}
	return []agent.RecoveryHint{{
		Action: strings.TrimSpace(document.Recovery.Kind),
		Reason: "Capability returned a user-visible recovery action.",
	}}
}

func capabilityResultString(result json.RawMessage, fieldName string) string {
	if len(result) == 0 {
		return ""
	}
	var document map[string]any
	if json.Unmarshal(result, &document) != nil {
		return ""
	}
	value, isString := document[fieldName].(string)
	if !isString {
		return ""
	}
	return strings.TrimSpace(value)
}

func capabilityResultBoolean(result json.RawMessage, fieldName string) bool {
	if len(result) == 0 {
		return false
	}
	var document map[string]any
	if json.Unmarshal(result, &document) != nil {
		return false
	}
	value, isBoolean := document[fieldName].(bool)
	return isBoolean && value
}

func mcpToolDescription(toolDefinition mcp.ToolDefinition) string {
	if strings.TrimSpace(toolDefinition.Description) != "" {
		return strings.TrimSpace(toolDefinition.Description)
	}
	return "MCP tool from " + toolDefinition.ServerName
}
