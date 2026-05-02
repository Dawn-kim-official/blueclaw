package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"blueclaw/internal/agent"
	"blueclaw/internal/capability"
	"blueclaw/internal/mcp"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
	"blueclaw/internal/security"
)

type HistoryProvider interface {
	FetchHistory(context.Context, string, int) (agent.VisibleContext, error)
}

type ToolCatalogBuilder struct {
	allowedToolNamesByProfile map[string][]string
	fallbackAllowedToolNames  []string
	memoryService             *memory.MemoryService
	mcpRegistry               *mcp.McpRegistry
	capabilityClient          capability.Client
	capabilityToolNames       []string
	terminalService           *security.TerminalSessionService
	workspaceRootPath         string
}

type ToolCatalogRequest struct {
	ProfileName               string
	Prompt                    string
	RequesterPersonID         string
	RequesterEmail            string
	ConversationID            string
	Platform                  string
	HistoryCursor             string
	HistoryProvider           HistoryProvider
	PersonAccess              policy.PersonAccess
	MemoryNamespaces          []memory.MemoryNamespace
	AccessibleConversationIDs []string
}

func NewToolCatalogBuilder() *ToolCatalogBuilder {
	return &ToolCatalogBuilder{
		workspaceRootPath: "/workspace",
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseAllowedToolNamesByProfile(allowedToolNamesByProfile map[string][]string, fallbackAllowedToolNames []string) {
	toolCatalogBuilder.allowedToolNamesByProfile = copyAllowedToolNamesByProfile(allowedToolNamesByProfile)
	toolCatalogBuilder.fallbackAllowedToolNames = trimNonEmptyStrings(fallbackAllowedToolNames)
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseMemoryService(memoryService *memory.MemoryService) {
	toolCatalogBuilder.memoryService = memoryService
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseMCPRegistry(mcpRegistry *mcp.McpRegistry) {
	toolCatalogBuilder.mcpRegistry = mcpRegistry
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseCapabilityTools(capabilityClient capability.Client, toolNames []string) {
	toolCatalogBuilder.capabilityClient = capabilityClient
	toolCatalogBuilder.capabilityToolNames = trimNonEmptyStrings(toolNames)
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseTerminalService(terminalService *security.TerminalSessionService) {
	toolCatalogBuilder.terminalService = terminalService
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseWorkspaceRootPath(workspaceRootPath string) {
	trimmedWorkspaceRootPath := strings.TrimSpace(workspaceRootPath)
	if trimmedWorkspaceRootPath != "" {
		toolCatalogBuilder.workspaceRootPath = trimmedWorkspaceRootPath
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) BuildToolRegistry(request ToolCatalogRequest) *agent.ToolRegistry {
	toolRegistry := agent.NewToolRegistry(toolCatalogBuilder.allowedToolNames(request.ProfileName))
	toolCatalogBuilder.registerHistoryTool(toolRegistry, request)
	toolCatalogBuilder.registerMemoryTool(toolRegistry, request)
	toolCatalogBuilder.registerBuiltInTools(toolRegistry)
	toolCatalogBuilder.registerMCPTools(toolRegistry)
	toolCatalogBuilder.registerCapabilityTools(toolRegistry, request)
	return toolRegistry
}

func (toolCatalogBuilder *ToolCatalogBuilder) allowedToolNames(profileName string) []string {
	normalizedProfileName := normalizeProfileName(profileName)
	if allowedToolNames, isFound := toolCatalogBuilder.allowedToolNamesByProfile[normalizedProfileName]; isFound {
		return append([]string{}, allowedToolNames...)
	}
	if len(toolCatalogBuilder.fallbackAllowedToolNames) > 0 {
		return append([]string{}, toolCatalogBuilder.fallbackAllowedToolNames...)
	}
	return []string{"memory.search", "terminal.run", "file.write", "file.attach"}
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerHistoryTool(toolRegistry *agent.ToolRegistry, request ToolCatalogRequest) {
	if request.HistoryProvider == nil {
		return
	}
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
		historyCursor := firstNonEmptyString(input.HistoryCursor, request.HistoryCursor)
		if historyCursor == "" {
			return agent.ToolResult{Content: "history cursor is unavailable", IsError: true}, nil
		}
		limit := input.Limit
		if limit <= 0 || limit > 50 {
			limit = 20
		}
		visibleContext, errorValue := request.HistoryProvider.FetchHistory(toolContext, historyCursor, limit)
		if errorValue != nil {
			return agent.ToolResult{}, errorValue
		}
		return agent.ToolResult{Content: marshalToolResult(visibleContext)}, nil
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerMemoryTool(toolRegistry *agent.ToolRegistry, request ToolCatalogRequest) {
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
		query := firstNonEmptyString(input.Query, request.Prompt)
		memoryFacts, errorValue := toolCatalogBuilder.SearchMemory(toolContext, TaskMemoryRequest{
			Query:                     query,
			RequesterPersonID:         request.RequesterPersonID,
			ConversationID:            request.ConversationID,
			PersonAccess:              request.PersonAccess,
			MemoryNamespaces:          request.MemoryNamespaces,
			AccessibleConversationIDs: request.AccessibleConversationIDs,
		})
		if errorValue != nil {
			return agent.ToolResult{}, errorValue
		}
		return agent.ToolResult{Content: marshalToolResult(memoryFacts)}, nil
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) SearchMemory(ctx context.Context, request TaskMemoryRequest) ([]memory.MemoryFact, error) {
	if toolCatalogBuilder.memoryService == nil {
		return nil, nil
	}
	return toolCatalogBuilder.memoryService.SearchMemory(ctx, memory.MemorySearchRequest{
		Query:                     request.Query,
		ReaderPersonID:            request.RequesterPersonID,
		ReaderSecurityLevelRank:   request.PersonAccess.SecurityLevelRank,
		ReaderGrantedClasses:      request.PersonAccess.GrantedClasses,
		ConversationID:            request.ConversationID,
		AccessibleConversationIDs: request.AccessibleConversationIDs,
		Namespaces:                request.MemoryNamespaces,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerBuiltInTools(toolRegistry *agent.ToolRegistry) {
	toolRegistry.RegisterTool(agent.ToolDefinition{
		Name:        "terminal.run",
		Description: "Run a guarded non-interactive command inside the Blueclaw workspace.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"executableName":{"type":"string"},"arguments":{"type":"array","items":{"type":"string"}},"workingDirectoryPath":{"type":"string"},"environmentVariables":{"type":"object","additionalProperties":{"type":"string"}}},"required":["executableName"],"additionalProperties":false}`),
	}, toolCatalogBuilder.runTerminalTool)
	toolRegistry.RegisterTool(agent.ToolDefinition{
		Name:        "file.write",
		Description: "Write a UTF-8 text file under the Blueclaw workspace.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"},"mode":{"type":"integer"}},"required":["path","content"],"additionalProperties":false}`),
	}, toolCatalogBuilder.writeFileTool)
	toolRegistry.RegisterTool(agent.ToolDefinition{
		Name:        "file.attach",
		Description: "Attach an existing workspace file to the final reply evidence.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"filename":{"type":"string"},"contentType":{"type":"string"},"title":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}, toolCatalogBuilder.attachFileTool)
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerMCPTools(toolRegistry *agent.ToolRegistry) {
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
			return agent.ToolResult{Content: output}, nil
		})
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerCapabilityTools(toolRegistry *agent.ToolRegistry, request ToolCatalogRequest) {
	for _, capabilityToolName := range toolCatalogBuilder.capabilityToolNames {
		toolName := capabilityToolName
		toolRegistry.RegisterTool(agent.ToolDefinition{
			Name:        toolName,
			Description: "InternKim capability tool",
		}, func(toolContext context.Context, toolInvocation agent.ToolInvocation) (agent.ToolResult, error) {
			var response struct {
				Content string          `json:"content"`
				IsError bool            `json:"isError"`
				Status  string          `json:"status"`
				Result  json.RawMessage `json:"result"`
			}
			errorValue := toolCatalogBuilder.capabilityClient.PostJSON(toolContext, "/v1/tools/"+url.PathEscape(toolName)+"/invoke", capabilityToolRequest(toolName, request, json.RawMessage(toolInvocation.Input)), &response)
			if errorValue != nil {
				return agent.ToolResult{}, errorValue
			}
			content := strings.TrimSpace(response.Content)
			if content == "" && len(response.Result) > 0 {
				content = string(response.Result)
			}
			isError := response.IsError || response.Status == "error" || response.Status == "denied"
			return agent.ToolResult{Content: content, IsError: isError, Attachments: capabilityAttachments(response.Result)}, nil
		})
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) runTerminalTool(toolContext context.Context, toolInvocation agent.ToolInvocation) (agent.ToolResult, error) {
	if toolCatalogBuilder.terminalService == nil {
		return agent.ToolResult{Content: "terminal service is unavailable", IsError: true}, nil
	}
	var input security.CommandRequest
	if errorValue := agent.UnmarshalToolInput(toolInvocation.Input, &input); errorValue != nil {
		return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
	}
	if strings.TrimSpace(input.WorkingDirectoryPath) == "" {
		input.WorkingDirectoryPath = toolCatalogBuilder.workspaceRootPath
	}
	commandResult, errorValue := toolCatalogBuilder.terminalService.RunCommand(input)
	content := marshalToolResult(commandResult)
	if errorValue != nil {
		return agent.ToolResult{Content: content, IsError: true}, nil
	}
	_ = toolContext
	return agent.ToolResult{Content: content}, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) writeFileTool(toolContext context.Context, toolInvocation agent.ToolInvocation) (agent.ToolResult, error) {
	var input struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Mode    uint32 `json:"mode"`
	}
	if errorValue := agent.UnmarshalToolInput(toolInvocation.Input, &input); errorValue != nil {
		return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
	}
	resolvedPath, errorValue := toolCatalogBuilder.resolveWorkspaceFilePath(input.Path)
	if errorValue != nil {
		return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
	}
	fileMode := os.FileMode(0600)
	if input.Mode != 0 {
		fileMode = os.FileMode(input.Mode)
	}
	if errorValue := os.MkdirAll(filepath.Dir(resolvedPath), 0700); errorValue != nil {
		return agent.ToolResult{}, errorValue
	}
	if errorValue := os.WriteFile(resolvedPath, []byte(input.Content), fileMode); errorValue != nil {
		return agent.ToolResult{}, errorValue
	}
	_ = toolContext
	return agent.ToolResult{Content: marshalToolResult(map[string]any{
		"path":      resolvedPath,
		"sizeBytes": len(input.Content),
	})}, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) attachFileTool(toolContext context.Context, toolInvocation agent.ToolInvocation) (agent.ToolResult, error) {
	var input struct {
		Path        string `json:"path"`
		Filename    string `json:"filename"`
		ContentType string `json:"contentType"`
		Title       string `json:"title"`
	}
	if errorValue := agent.UnmarshalToolInput(toolInvocation.Input, &input); errorValue != nil {
		return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
	}
	resolvedPath, errorValue := toolCatalogBuilder.resolveWorkspaceFilePath(input.Path)
	if errorValue != nil {
		return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
	}
	fileInformation, errorValue := os.Stat(resolvedPath)
	if errorValue != nil {
		return agent.ToolResult{}, errorValue
	}
	if !fileInformation.Mode().IsRegular() {
		return agent.ToolResult{Content: "attachment path is not a regular file", IsError: true}, nil
	}
	filename := firstNonEmptyString(input.Filename, filepath.Base(resolvedPath))
	contentType := firstNonEmptyString(input.ContentType, mime.TypeByExtension(filepath.Ext(filename)), "application/octet-stream")
	_ = toolContext
	return agent.ToolResult{
		Content: "file attached",
		Attachments: []agent.FileAttachment{{
			DevicePath:  resolvedPath,
			Filename:    filename,
			ContentType: contentType,
			SizeBytes:   fileInformation.Size(),
			Title:       strings.TrimSpace(input.Title),
		}},
	}, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) resolveWorkspaceFilePath(value string) (string, error) {
	trimmedPath := strings.TrimSpace(value)
	if trimmedPath == "" {
		return "", errors.New("path is required")
	}
	if !filepath.IsAbs(trimmedPath) {
		trimmedPath = filepath.Join(toolCatalogBuilder.workspaceRootPath, trimmedPath)
	}
	cleanWorkspaceRootPath, errorValue := filepath.Abs(toolCatalogBuilder.workspaceRootPath)
	if errorValue != nil {
		return "", errorValue
	}
	cleanPath, errorValue := filepath.Abs(filepath.Clean(trimmedPath))
	if errorValue != nil {
		return "", errorValue
	}
	relativePath, errorValue := filepath.Rel(cleanWorkspaceRootPath, cleanPath)
	if errorValue != nil {
		return "", errorValue
	}
	if relativePath == ".." || strings.HasPrefix(relativePath, "../") {
		return "", errors.New("path must stay under the workspace root")
	}
	return cleanPath, nil
}

func capabilityToolRequest(toolName string, request ToolCatalogRequest, toolInput json.RawMessage) map[string]any {
	return map[string]any{
		"toolName": toolName,
		"input":    toolInput,
		"context": map[string]any{
			"requesterPersonID": request.RequesterPersonID,
			"requesterEmail":    request.RequesterEmail,
			"conversationID":    request.ConversationID,
			"platform":          request.Platform,
		},
	}
}

func capabilityAttachments(result json.RawMessage) []agent.FileAttachment {
	if len(result) == 0 {
		return nil
	}
	var attachment agent.FileAttachment
	if errorValue := json.Unmarshal(result, &attachment); errorValue == nil && strings.TrimSpace(attachment.DevicePath) != "" {
		return []agent.FileAttachment{attachment}
	}
	var document struct {
		Attachments []agent.FileAttachment `json:"attachments"`
	}
	if errorValue := json.Unmarshal(result, &document); errorValue != nil {
		return nil
	}
	attachments := []agent.FileAttachment{}
	for _, candidate := range document.Attachments {
		if strings.TrimSpace(candidate.DevicePath) != "" {
			attachments = append(attachments, candidate)
		}
	}
	return attachments
}

func mcpToolDescription(toolDefinition mcp.ToolDefinition) string {
	if strings.TrimSpace(toolDefinition.Description) != "" {
		return strings.TrimSpace(toolDefinition.Description)
	}
	return "MCP tool from " + toolDefinition.ServerName
}

func marshalToolResult(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return fmt.Sprint(value)
	}
	return string(document)
}

func copyAllowedToolNamesByProfile(allowedToolNamesByProfile map[string][]string) map[string][]string {
	copiedAllowedToolNamesByProfile := map[string][]string{}
	for profileName, allowedToolNames := range allowedToolNamesByProfile {
		copiedAllowedToolNamesByProfile[normalizeProfileName(profileName)] = trimNonEmptyStrings(allowedToolNames)
	}
	return copiedAllowedToolNamesByProfile
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

func normalizeProfileName(profileName string) string {
	trimmedProfileName := strings.TrimSpace(profileName)
	if trimmedProfileName == "" {
		return "default"
	}
	return trimmedProfileName
}
