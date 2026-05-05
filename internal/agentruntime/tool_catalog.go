package agentruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"blueclaw/internal/access"
	"blueclaw/internal/agent"
	"blueclaw/internal/capability"
	"blueclaw/internal/mcp"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
	"blueclaw/internal/security"
	"blueclaw/internal/task"
)

const inlineAttachmentMaximumBytes = 25 * 1024 * 1024

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
	taskRunService            *task.TaskRunService
	workspaceRootPath         string
	skillChangeHandler        func(context.Context)
}

type toolHandlerContext struct {
	request ToolCatalogRequest
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

type terminalSessionToolInput struct {
	Action               string            `json:"action"`
	SessionID            string            `json:"sessionID"`
	Command              string            `json:"command"`
	Input                string            `json:"input"`
	WorkingDirectoryPath string            `json:"workingDirectoryPath"`
	EnvironmentVariables map[string]string `json:"environmentVariables"`
	TimeoutSecond        int               `json:"timeoutSecond"`
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

func (toolCatalogBuilder *ToolCatalogBuilder) UseTaskRunService(taskRunService *task.TaskRunService) {
	toolCatalogBuilder.taskRunService = taskRunService
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseWorkspaceRootPath(workspaceRootPath string) {
	trimmedWorkspaceRootPath := strings.TrimSpace(workspaceRootPath)
	if trimmedWorkspaceRootPath != "" {
		toolCatalogBuilder.workspaceRootPath = trimmedWorkspaceRootPath
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseSkillChangeHandler(skillChangeHandler func(context.Context)) {
	toolCatalogBuilder.skillChangeHandler = skillChangeHandler
}

func (toolCatalogBuilder *ToolCatalogBuilder) WorkspaceRootPath() string {
	return strings.TrimSpace(toolCatalogBuilder.workspaceRootPath)
}

func (toolCatalogBuilder *ToolCatalogBuilder) BuildToolRegistry(request ToolCatalogRequest) *agent.ToolRegistry {
	toolRegistry := agent.NewToolRegistry(toolCatalogBuilder.allowedToolNames(request.ProfileName))
	toolCatalogBuilder.registerHistoryTool(toolRegistry, request)
	toolCatalogBuilder.registerMemoryTool(toolRegistry, request)
	toolCatalogBuilder.registerBuiltInTools(toolRegistry, toolHandlerContext{request: request})
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
	return []string{"memory.search", "terminal.run", "terminal.session", "browser_handoff.openURL", "approval.request", "file.write", "file.attach", "skill.add", "skill.remove"}
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
		ReaderCircles:             request.PersonAccess.Circles,
		ResourceAccessRules:       request.PersonAccess.ResourceAccessRules,
		ReaderSecurityLevelRank:   request.PersonAccess.SecurityLevelRank,
		ReaderGrantedClasses:      request.PersonAccess.GrantedClasses,
		ConversationID:            request.ConversationID,
		AccessibleConversationIDs: request.AccessibleConversationIDs,
		Namespaces:                request.MemoryNamespaces,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerBuiltInTools(toolRegistry *agent.ToolRegistry, handlerContext toolHandlerContext) {
	toolRegistry.RegisterTool(agent.ToolDefinition{
		Name:        "terminal.run",
		Description: "Run a guarded non-interactive command inside the Blueclaw workspace.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"workingDirectoryPath":{"type":"string"},"environmentVariables":{"type":"object","additionalProperties":{"type":"string"}},"timeoutSecond":{"type":"integer"}},"required":["command"],"additionalProperties":false}`),
	}, func(toolContext context.Context, toolInvocation agent.ToolInvocation) (agent.ToolResult, error) {
		return toolCatalogBuilder.runTerminalTool(toolContext, toolInvocation, handlerContext)
	})
	toolRegistry.RegisterTool(agent.ToolDefinition{
		Name:        "terminal.session",
		Description: "Manage a PTY terminal session inside the Blueclaw workspace with action start, write, status, or close.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["start","write","status","close"]},"sessionID":{"type":"string"},"command":{"type":"string"},"input":{"type":"string"},"workingDirectoryPath":{"type":"string"},"environmentVariables":{"type":"object","additionalProperties":{"type":"string"}},"timeoutSecond":{"type":"integer"}},"required":["action"],"additionalProperties":false}`),
	}, func(toolContext context.Context, toolInvocation agent.ToolInvocation) (agent.ToolResult, error) {
		return toolCatalogBuilder.sessionTerminalTool(toolContext, toolInvocation, handlerContext)
	})
	toolRegistry.RegisterTool(agent.ToolDefinition{
		Name:        "browser_handoff.openURL",
		Description: "Ask the Companion bridge to open a URL on the user's computer without running shell commands.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"],"additionalProperties":false}`),
	}, toolCatalogBuilder.openBrowserHandoffTool)
	toolRegistry.RegisterTool(agent.ToolDefinition{
		Name:        "approval.request",
		Description: "Pause the current task while waiting for explicit user approval.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"},"reason":{"type":"string"}},"required":["message"],"additionalProperties":false}`),
	}, toolCatalogBuilder.requestApprovalTool)
	toolRegistry.RegisterTool(agent.ToolDefinition{
		Name:        "file.write",
		Description: "Write a UTF-8 text file under the Blueclaw workspace. Use this for markdown, scripts, and source files instead of shell redirection.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"},"mode":{"type":"integer"}},"required":["path","content"],"additionalProperties":false}`),
	}, func(toolContext context.Context, toolInvocation agent.ToolInvocation) (agent.ToolResult, error) {
		return toolCatalogBuilder.writeFileTool(toolContext, toolInvocation, handlerContext)
	})
	toolRegistry.RegisterTool(agent.ToolDefinition{
		Name:        "file.attach",
		Description: "Attach one or more existing workspace files to the final reply evidence. Use paths for related artifact sets.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"paths":{"type":"array","items":{"type":"string"}},"filename":{"type":"string"},"contentType":{"type":"string"},"title":{"type":"string"}},"additionalProperties":false}`),
	}, func(toolContext context.Context, toolInvocation agent.ToolInvocation) (agent.ToolResult, error) {
		return toolCatalogBuilder.attachFileTool(toolContext, toolInvocation, handlerContext)
	})
	toolCatalogBuilder.registerSkillManagementTools(toolRegistry)
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
			if !access.CanAccess(access.Request{PersonAccess: request.PersonAccess, Action: access.ActionExecute, Resource: "tool:" + toolName}) {
				return agent.ToolResult{Content: "current account cannot execute this tool", IsError: true}, nil
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

func (toolCatalogBuilder *ToolCatalogBuilder) runTerminalTool(toolContext context.Context, toolInvocation agent.ToolInvocation, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	if toolCatalogBuilder.terminalService == nil {
		return agent.ToolResult{Content: "terminal service is unavailable", IsError: true}, nil
	}
	var input security.CommandRequest
	if errorValue := agent.UnmarshalToolInput(toolInvocation.Input, &input); errorValue != nil {
		return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
	}
	input.Command = toolCatalogBuilder.resolveAgentWorkspaceReferences(input.Command)
	input.Stdin = toolCatalogBuilder.resolveAgentWorkspaceReferences(input.Stdin)
	input.EnvironmentVariables = toolCatalogBuilder.resolveAgentWorkspaceEnvironment(input.EnvironmentVariables)
	if strings.TrimSpace(input.WorkingDirectoryPath) == "" {
		input.WorkingDirectoryPath = toolCatalogBuilder.workspaceRootPath
	} else {
		input.WorkingDirectoryPath = toolCatalogBuilder.resolveAgentWorkspacePath(input.WorkingDirectoryPath)
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionWrite, input.WorkingDirectoryPath) {
		return agent.ToolResult{Content: "current account cannot use this workspace path", IsError: true}, nil
	}
	commandResult, errorValue := toolCatalogBuilder.terminalService.RunCommand(input)
	content := marshalToolResult(commandResult)
	if errorValue != nil {
		return agent.ToolResult{Content: content, IsError: true}, nil
	}
	_ = toolContext
	return agent.ToolResult{Content: content}, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) sessionTerminalTool(toolContext context.Context, toolInvocation agent.ToolInvocation, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	if toolCatalogBuilder.terminalService == nil {
		return agent.ToolResult{Content: "terminal service is unavailable", IsError: true}, nil
	}
	var input terminalSessionToolInput
	if errorValue := agent.UnmarshalToolInput(toolInvocation.Input, &input); errorValue != nil {
		return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
	}
	switch strings.TrimSpace(input.Action) {
	case "start":
		return toolCatalogBuilder.startTerminalSession(input, handlerContext)
	case "write":
		commandResult, errorValue := toolCatalogBuilder.terminalService.WriteSessionInput(input.SessionID, input.Input)
		return terminalSessionToolResult(commandResult, errorValue), nil
	case "status":
		status, errorValue := toolCatalogBuilder.terminalService.StatusSession(input.SessionID)
		return statusToolResult(status, errorValue), nil
	case "close":
		errorValue := toolCatalogBuilder.terminalService.CloseSession(input.SessionID)
		if errorValue != nil {
			return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
		}
		return agent.ToolResult{Content: marshalToolResult(map[string]string{"sessionID": input.SessionID, "status": "closed"})}, nil
	default:
		_ = toolContext
		return agent.ToolResult{Content: "terminal session action must be start, write, status, or close", IsError: true}, nil
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) startTerminalSession(input terminalSessionToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	workingDirectoryPath := firstNonEmptyString(toolCatalogBuilder.resolveAgentWorkspacePath(input.WorkingDirectoryPath), toolCatalogBuilder.workspaceRootPath)
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionWrite, workingDirectoryPath) {
		return agent.ToolResult{Content: "current account cannot use this workspace path", IsError: true}, nil
	}
	sessionID, errorValue := toolCatalogBuilder.terminalService.StartInteractiveSession(security.CommandRequest{
		Command:              toolCatalogBuilder.resolveAgentWorkspaceReferences(input.Command),
		WorkingDirectoryPath: workingDirectoryPath,
		EnvironmentVariables: toolCatalogBuilder.resolveAgentWorkspaceEnvironment(input.EnvironmentVariables),
		TimeoutSecond:        input.TimeoutSecond,
		IsInteractive:        true,
		IsPTY:                true,
	})
	if errorValue != nil {
		return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
	}
	status, errorValue := toolCatalogBuilder.terminalService.StatusSession(sessionID)
	return statusToolResult(status, errorValue), nil
}

func terminalSessionToolResult(commandResult security.CommandResult, errorValue error) agent.ToolResult {
	content := marshalToolResult(commandResult)
	if errorValue != nil {
		return agent.ToolResult{Content: content, IsError: true}
	}
	return agent.ToolResult{Content: content}
}

func statusToolResult(status security.TerminalSessionStatus, errorValue error) agent.ToolResult {
	content := marshalToolResult(status)
	if errorValue != nil {
		return agent.ToolResult{Content: errorValue.Error(), IsError: true}
	}
	return agent.ToolResult{Content: content}
}

func (toolCatalogBuilder *ToolCatalogBuilder) openBrowserHandoffTool(toolContext context.Context, toolInvocation agent.ToolInvocation) (agent.ToolResult, error) {
	if toolCatalogBuilder.capabilityClient.HTTPClient == nil {
		return agent.ToolResult{Content: "companion bridge capability client is unavailable", IsError: true}, nil
	}
	var input struct {
		URL string `json:"url"`
	}
	if errorValue := agent.UnmarshalToolInput(toolInvocation.Input, &input); errorValue != nil {
		return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
	}
	var response struct {
		Content string          `json:"content"`
		IsError bool            `json:"isError"`
		Status  string          `json:"status"`
		Result  json.RawMessage `json:"result"`
	}
	errorValue := toolCatalogBuilder.capabilityClient.PostJSON(toolContext, "/v1/tools/browser.open/invoke", map[string]any{
		"toolName": "browser.open",
		"input":    map[string]string{"url": input.URL},
	}, &response)
	if errorValue != nil {
		return agent.ToolResult{}, errorValue
	}
	content := firstNonEmptyString(response.Content, string(response.Result))
	if taskRunID := agent.TaskRunIDFromContext(toolContext); taskRunID != "" && toolCatalogBuilder.taskRunService != nil {
		toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "browser_handoff.opened", marshalToolResult(map[string]string{"url": input.URL, "content": content}))
	}
	return agent.ToolResult{Content: content, IsError: response.IsError || response.Status == "error" || response.Status == "denied", Attachments: capabilityAttachments(response.Result)}, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) requestApprovalTool(toolContext context.Context, toolInvocation agent.ToolInvocation) (agent.ToolResult, error) {
	var input struct {
		Message string `json:"message"`
		Reason  string `json:"reason"`
	}
	if errorValue := agent.UnmarshalToolInput(toolInvocation.Input, &input); errorValue != nil {
		return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
	}
	taskRunID := agent.TaskRunIDFromContext(toolContext)
	if taskRunID == "" || toolCatalogBuilder.taskRunService == nil {
		return agent.ToolResult{Content: "approval requires an active task run", IsError: true}, nil
	}
	reason := firstNonEmptyString(input.Reason, input.Message, "approval requested")
	_, errorValue := toolCatalogBuilder.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusWaitingApproval, reason)
	if errorValue != nil {
		return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
	}
	toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "approval.requested", marshalToolResult(input))
	return agent.ToolResult{Content: marshalToolResult(map[string]string{"taskRunID": taskRunID, "status": string(task.TaskStatusWaitingApproval), "message": input.Message})}, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) writeFileTool(toolContext context.Context, toolInvocation agent.ToolInvocation, handlerContext toolHandlerContext) (agent.ToolResult, error) {
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
	if isImmutableSkillPath(toolCatalogBuilder.workspaceRootPath, resolvedPath) {
		return agent.ToolResult{Content: "file.write cannot modify built-in skill files", IsError: true}, nil
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionWrite, resolvedPath) {
		return agent.ToolResult{Content: "current account cannot write this file", IsError: true}, nil
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
		"path":      toolCatalogBuilder.agentWorkspacePath(resolvedPath),
		"sizeBytes": len(input.Content),
	})}, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) attachFileTool(toolContext context.Context, toolInvocation agent.ToolInvocation, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	var input struct {
		Path        string   `json:"path"`
		Paths       []string `json:"paths"`
		Filename    string   `json:"filename"`
		ContentType string   `json:"contentType"`
		Title       string   `json:"title"`
	}
	if errorValue := agent.UnmarshalToolInput(toolInvocation.Input, &input); errorValue != nil {
		return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
	}
	attachmentPaths := requestedAttachmentPaths(input.Path, input.Paths)
	if len(attachmentPaths) == 0 {
		return agent.ToolResult{Content: "path is required", IsError: true}, nil
	}
	attachments := []agent.FileAttachment{}
	for _, attachmentPath := range attachmentPaths {
		attachment, errorValue := toolCatalogBuilder.fileAttachment(attachmentPath, input, handlerContext)
		if errorValue != nil {
			return agent.ToolResult{}, errorValue
		}
		attachments = append(attachments, attachment)
	}
	_ = toolContext
	return agent.ToolResult{
		Content:     "file attached",
		Attachments: attachments,
	}, nil
}

func requestedAttachmentPaths(path string, paths []string) []string {
	attachmentPaths := trimNonEmptyStrings(paths)
	if strings.TrimSpace(path) != "" {
		attachmentPaths = append([]string{strings.TrimSpace(path)}, attachmentPaths...)
	}
	return attachmentPaths
}

func (toolCatalogBuilder *ToolCatalogBuilder) fileAttachment(path string, input struct {
	Path        string   `json:"path"`
	Paths       []string `json:"paths"`
	Filename    string   `json:"filename"`
	ContentType string   `json:"contentType"`
	Title       string   `json:"title"`
}, handlerContext toolHandlerContext) (agent.FileAttachment, error) {
	resolvedPath, errorValue := toolCatalogBuilder.resolveWorkspaceFilePath(path)
	if errorValue != nil {
		return agent.FileAttachment{}, errorValue
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionRead, resolvedPath) {
		return agent.FileAttachment{}, errors.New("current account cannot read this file")
	}
	fileInformation, errorValue := os.Stat(resolvedPath)
	if errorValue != nil {
		return agent.FileAttachment{}, errorValue
	}
	if !fileInformation.Mode().IsRegular() {
		return agent.FileAttachment{}, errors.New("attachment path is not a regular file")
	}
	contentBase64, errorValue := inlineAttachmentContent(resolvedPath, fileInformation.Size())
	if errorValue != nil {
		return agent.FileAttachment{}, errorValue
	}
	filename := attachmentFilename(input, resolvedPath)
	contentType := firstNonEmptyString(input.ContentType, mime.TypeByExtension(filepath.Ext(filename)), "application/octet-stream")
	return agent.FileAttachment{
		DevicePath:    toolCatalogBuilder.agentWorkspacePath(resolvedPath),
		Filename:      filename,
		ContentType:   contentType,
		SizeBytes:     fileInformation.Size(),
		Title:         strings.TrimSpace(input.Title),
		ContentBase64: contentBase64,
	}, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) canAccessWorkspacePath(personAccess policy.PersonAccess, action string, path string) bool {
	resource := access.ResourceForWorkspacePath(toolCatalogBuilder.workspaceRootPath, path)
	return access.CanAccess(access.Request{
		PersonAccess: personAccess,
		Action:       action,
		Resource:     resource,
	})
}

func inlineAttachmentContent(path string, sizeBytes int64) (string, error) {
	if sizeBytes > inlineAttachmentMaximumBytes {
		return "", errors.New("attachment file is too large")
	}
	document, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return "", errorValue
	}
	return base64.StdEncoding.EncodeToString(document), nil
}

func attachmentFilename(input struct {
	Path        string   `json:"path"`
	Paths       []string `json:"paths"`
	Filename    string   `json:"filename"`
	ContentType string   `json:"contentType"`
	Title       string   `json:"title"`
}, resolvedPath string) string {
	if len(input.Paths) == 0 && strings.TrimSpace(input.Filename) != "" {
		return strings.TrimSpace(input.Filename)
	}
	return filepath.Base(resolvedPath)
}

func (toolCatalogBuilder *ToolCatalogBuilder) resolveWorkspaceFilePath(value string) (string, error) {
	trimmedPath := toolCatalogBuilder.resolveAgentWorkspacePath(value)
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

func (toolCatalogBuilder *ToolCatalogBuilder) resolveAgentWorkspacePath(value string) string {
	trimmedPath := strings.TrimSpace(value)
	if trimmedPath == "" {
		return ""
	}
	if toolCatalogBuilder.workspaceRootPath == "/workspace" {
		return trimmedPath
	}
	if trimmedPath == "/workspace" {
		return toolCatalogBuilder.workspaceRootPath
	}
	if strings.HasPrefix(trimmedPath, "/workspace/") {
		return filepath.Join(toolCatalogBuilder.workspaceRootPath, strings.TrimPrefix(trimmedPath, "/workspace/"))
	}
	return trimmedPath
}

func (toolCatalogBuilder *ToolCatalogBuilder) resolveAgentWorkspaceReferences(value string) string {
	if strings.TrimSpace(value) == "" || toolCatalogBuilder.workspaceRootPath == "/workspace" {
		return value
	}
	return strings.ReplaceAll(value, "/workspace", toolCatalogBuilder.workspaceRootPath)
}

func (toolCatalogBuilder *ToolCatalogBuilder) resolveAgentWorkspaceEnvironment(environmentVariables map[string]string) map[string]string {
	if len(environmentVariables) == 0 {
		return environmentVariables
	}
	resolvedEnvironmentVariables := map[string]string{}
	for key, value := range environmentVariables {
		resolvedEnvironmentVariables[key] = toolCatalogBuilder.resolveAgentWorkspaceReferences(value)
	}
	return resolvedEnvironmentVariables
}

func (toolCatalogBuilder *ToolCatalogBuilder) agentWorkspacePath(path string) string {
	relativePath, errorValue := filepath.Rel(toolCatalogBuilder.workspaceRootPath, path)
	if errorValue != nil || relativePath == "." || strings.HasPrefix(relativePath, "../") || relativePath == ".." {
		return path
	}
	return filepath.ToSlash(filepath.Join("/workspace", relativePath))
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
