package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"

	"blueclaw/internal/access"
	"blueclaw/internal/agent"
	"blueclaw/internal/mcp"
)

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
		if request.IsScheduledRun && isInteractiveCapabilityTool(toolName) {
			continue
		}
		toolDescription := firstNonEmptyString(toolDescriptor.Description, defaultCapabilityToolDescription(toolName))
		toolRegistry.RegisterBoundTool(agent.BoundTool{
			Definition: agent.ToolDefinition{
				Name:            toolName,
				Description:     toolDescription,
				InputSchema:     toolDescriptor.InputSchema,
				OutputSchema:    toolDescriptor.OutputSchema,
				PolicyResource:  toolDescriptor.PolicyResource,
				SideEffectClass: toolDescriptor.SideEffectClass,
			},
			Availability: capabilityToolAvailability(toolDescriptor, request),
			Handler: func(toolContext context.Context, toolInvocation agent.ToolInvocation) (agent.ToolResult, error) {
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
				policyResource := firstNonEmptyString(toolDescriptor.PolicyResource, "tool:"+toolName)
				if !access.CanAccess(access.Request{PersonAccess: request.PersonAccess, Action: access.ActionExecute, Resource: policyResource}) {
					return agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "capability_access", "current account cannot execute this tool"), nil
				}
				toolInput, toolFailure, errorValue := toolCatalogBuilder.prepareCapabilityToolInput(toolContext, toolName, request, json.RawMessage(toolInvocation.Input))
				if toolFailure != nil {
					return *toolFailure, nil
				}
				if errorValue != nil {
					return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "capability_input", errorValue.Error()), nil
				}
				if errorValue := toolCatalogBuilder.validateCapabilityToolInputAccess(toolName, request, toolInput); errorValue != nil {
					return agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "file_read_access", errorValue.Error()), nil
				}
				errorValue = toolCatalogBuilder.capabilityClient.PostJSON(toolContext, "/v1/tools/"+url.PathEscape(toolName)+"/invoke", capabilityToolRequest(toolContext, toolName, request, toolInput), &response)
				if errorValue != nil {
					return agent.ToolResult{}, errorValue
				}
				if !response.IsError && response.Status != "error" && response.Status != "denied" {
					toolFailure, errorValue := toolCatalogBuilder.handleCapabilityToolSuccess(toolContext, toolName, request, &response.Result)
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
			},
		})
	}
}

func isInteractiveCapabilityTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "user.confirm", "user.input", "ask.confirm", "ask.choice", "ask.input":
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
	case "platform.message.context":
		return "Read the current platform conversation, thread, channel, requester, and bot message context."
	case "platform.message.search":
		return "Search platform messages by scope, author, and queries. queries is an OR list; use one item for a single keyword. Returns compact messageIDs before previews. Use before deleting or editing messages described in natural language."
	case "platform.message.delete":
		return "Delete InternKim bot messages by exact messageIDs returned from platform.message.search. Deletes one selected page at a time and never searches internally."
	case "platform.message.send":
		return "Send a platform message to a direct message, current thread, current channel, or named channel. Recipient resolution and ambiguity are handled by this tool."
	case "platform.message.update":
		return "Update an InternKim bot message text or pin state. Use only for platform messages that should be edited or pinned."
	case "site.app.status":
		return siteAppStatusToolDescription
	default:
		return "InternKim capability tool"
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
		return agent.ToolAvailability{Status: agent.ToolAvailabilityAsk, Reason: "requires approval"}
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
	if strings.TrimSpace(toolName) != "platform.message.send" {
		return false
	}
	return request.IsScheduledRun || request.IsApprovalContinuation
}

func capabilityToolRequest(toolContext context.Context, toolName string, request ToolCatalogRequest, toolInput json.RawMessage) map[string]any {
	requestDocument := map[string]any{
		"toolName": toolName,
		"input":    toolInput,
		"context": map[string]any{
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
		},
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
		toolInput, toolFailure, errorValue := toolCatalogBuilder.enrichSitePublishInput(toolContext, request, toolInput)
		return toolInput, toolFailure, errorValue
	}
	if capabilityToolNeedsWorkspacePath(toolName) {
		input, errorValue := toolCatalogBuilder.resolveCapabilityWorkspacePathInput(toolContext, toolName, request, toolInput)
		return input, nil, errorValue
	}
	toolInput, errorValue := toolCatalogBuilder.enrichCapabilityToolInput(toolName, request, toolInput)
	return toolInput, nil, errorValue
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

func (toolCatalogBuilder *ToolCatalogBuilder) enrichCapabilityToolInput(toolName string, request ToolCatalogRequest, toolInput json.RawMessage) (json.RawMessage, error) {
	if !siteToolNeedsSourceBundle(toolName) {
		return toolInput, nil
	}
	toolInput, toolFailure, errorValue := toolCatalogBuilder.enrichSitePublishInput(context.Background(), request, toolInput)
	if toolFailure != nil {
		return nil, errors.New(toolFailure.ContentText())
	}
	return toolInput, errorValue
}

func (toolCatalogBuilder *ToolCatalogBuilder) handleCapabilityToolSuccess(toolContext context.Context, toolName string, request ToolCatalogRequest, result *json.RawMessage) (*agent.ToolResult, error) {
	switch strings.TrimSpace(toolName) {
	case "site.app.create":
		return toolCatalogBuilder.materializeSiteCreateResult(toolContext, request, result)
	case "site.app.status":
		return toolCatalogBuilder.annotateSiteStatusResult(toolContext, request, result)
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
	for _, workKind := range agent.WorkKindsFromContext(toolContext) {
		if workKind == agent.WorkKindUserBrowser {
			return true
		}
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
	toolNames := []string{}
	if strings.TrimSpace(document.Recovery.ConnectCommand) != "" {
		toolNames = append(toolNames, "ask.confirm")
	}
	return []agent.RecoveryHint{{
		Action:    strings.TrimSpace(document.Recovery.Kind),
		ToolNames: toolNames,
		Reason:    "Capability returned a user-visible recovery action.",
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
