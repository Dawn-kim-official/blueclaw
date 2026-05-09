package agentruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
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
const siteSourceBundleMaximumBytes = 64 * 1024 * 1024

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
	capabilityToolDescriptors []CapabilityToolDescriptor
	terminalService           *security.TerminalSessionService
	taskRunService            *task.TaskRunService
	taskScheduleRepository    task.TaskScheduleRepository
	workspaceRootPath         string
	skillChangeHandler        func(context.Context)
}

type toolHandlerContext struct {
	request           ToolCatalogRequest
	conversationScope ConversationResourceScope
}

type ToolCatalogRequest struct {
	ProfileName               string
	Prompt                    string
	VisibleContext            agent.VisibleContext
	RequesterPersonID         string
	RequesterName             string
	RequesterEmail            string
	RequesterPlatformUserID   string
	ConversationID            string
	ConversationType          string
	ConversationChannelID     string
	ConversationChannelName   string
	ReplyTargetID             string
	Platform                  string
	HistoryCursor             string
	HistoryProvider           HistoryProvider
	PersonAccess              policy.PersonAccess
	MemoryNamespaces          []memory.MemoryNamespace
	AccessibleConversationIDs []string
}

type CapabilityToolDescriptor struct {
	Name             string
	Description      string
	InputSchema      json.RawMessage
	OutputSchema     json.RawMessage
	PolicyResource   string
	SideEffectClass  string
	RequiresApproval bool
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

type historyToolInput struct {
	HistoryCursor string `json:"historyCursor"`
	Limit         int    `json:"limit"`
	Direction     string `json:"direction"`
}

type memorySearchToolInput struct {
	Query string `json:"query"`
}

type browserHandoffOpenURLToolInput struct {
	URL string `json:"url"`
}

type approvalRequestToolInput struct {
	Message string `json:"message"`
	Reason  string `json:"reason"`
}

type fileWriteToolInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    uint32 `json:"mode"`
}

type fileAttachToolInput struct {
	Path        string   `json:"path"`
	Paths       []string `json:"paths"`
	Filename    string   `json:"filename"`
	ContentType string   `json:"contentType"`
	Title       string   `json:"title"`
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

func (toolCatalogBuilder *ToolCatalogBuilder) UseCapabilityToolDescriptors(capabilityClient capability.Client, toolDescriptors []CapabilityToolDescriptor) {
	toolCatalogBuilder.capabilityClient = capabilityClient
	toolCatalogBuilder.capabilityToolDescriptors = copyCapabilityToolDescriptors(toolDescriptors)
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseTerminalService(terminalService *security.TerminalSessionService) {
	toolCatalogBuilder.terminalService = terminalService
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseTaskRunService(taskRunService *task.TaskRunService) {
	toolCatalogBuilder.taskRunService = taskRunService
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseTaskScheduleRepository(taskScheduleRepository task.TaskScheduleRepository) {
	toolCatalogBuilder.taskScheduleRepository = taskScheduleRepository
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

func (toolCatalogBuilder *ToolCatalogBuilder) BuildToolSet(request ToolCatalogRequest) *agent.ToolSet {
	toolSet := agent.NewToolSet(toolCatalogBuilder.allowedToolNames(request.ProfileName))
	handlerContext := toolHandlerContext{
		request:           request,
		conversationScope: toolCatalogBuilder.conversationScope(request),
	}
	toolCatalogBuilder.registerHistoryTool(toolSet, request)
	toolCatalogBuilder.registerMemoryTool(toolSet, request)
	toolCatalogBuilder.registerBuiltInTools(toolSet, handlerContext)
	toolCatalogBuilder.registerMCPTools(toolSet)
	toolCatalogBuilder.registerCapabilityTools(toolSet, request)
	return toolSet
}

func (toolCatalogBuilder *ToolCatalogBuilder) allowedToolNames(profileName string) []string {
	normalizedProfileName := normalizeProfileName(profileName)
	if allowedToolNames, isFound := toolCatalogBuilder.allowedToolNamesByProfile[normalizedProfileName]; isFound {
		return append([]string{}, allowedToolNames...)
	}
	if len(toolCatalogBuilder.fallbackAllowedToolNames) > 0 {
		return append([]string{}, toolCatalogBuilder.fallbackAllowedToolNames...)
	}
	return []string{"memory.search", "terminal.run", "terminal.session", "browser_handoff.openURL", "approval.request", "file.write", "file.attach", "skill.add", "skill.remove", "schedule.create"}
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerHistoryTool(toolRegistry *agent.ToolSet, request ToolCatalogRequest) {
	if request.HistoryProvider == nil {
		return
	}
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[historyToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "conversation.history",
			Description: "Fetch earlier visible messages for this conversation using the opaque history cursor.",
		},
		Handler: func(toolContext context.Context, input historyToolInput) (agent.ToolResult, error) {
			return fetchHistoryTool(toolContext, input, request)
		},
		Result: agent.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerMemoryTool(toolRegistry *agent.ToolSet, request ToolCatalogRequest) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[memorySearchToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "memory.search",
			Description: "Search Blueclaw graph memory allowed for this requester and conversation.",
		},
		Handler: func(toolContext context.Context, input memorySearchToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.searchMemoryTool(toolContext, input, request)
		},
		Result: agent.IdentityToolResult,
	})
}

func fetchHistoryTool(toolContext context.Context, input historyToolInput, request ToolCatalogRequest) (agent.ToolResult, error) {
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
}

func (toolCatalogBuilder *ToolCatalogBuilder) searchMemoryTool(toolContext context.Context, input memorySearchToolInput, request ToolCatalogRequest) (agent.ToolResult, error) {
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

func (toolCatalogBuilder *ToolCatalogBuilder) registerBuiltInTools(toolRegistry *agent.ToolSet, handlerContext toolHandlerContext) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[security.CommandRequest, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "terminal.run",
			Description: "Run a guarded non-interactive command inside the Blueclaw workspace.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"workingDirectoryPath":{"type":"string"},"environmentVariables":{"type":"object","additionalProperties":{"type":"string"}},"timeoutSecond":{"type":"integer"}},"required":["command"],"additionalProperties":false}`),
		},
		Handler: func(toolContext context.Context, input security.CommandRequest) (agent.ToolResult, error) {
			return toolCatalogBuilder.runTerminalTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[terminalSessionToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "terminal.session",
			Description: "Manage a PTY terminal session inside the Blueclaw workspace with action start, write, status, or close.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["start","write","status","close"]},"sessionID":{"type":"string"},"command":{"type":"string"},"input":{"type":"string"},"workingDirectoryPath":{"type":"string"},"environmentVariables":{"type":"object","additionalProperties":{"type":"string"}},"timeoutSecond":{"type":"integer"}},"required":["action"],"additionalProperties":false}`),
		},
		Handler: func(toolContext context.Context, input terminalSessionToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.sessionTerminalTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[browserHandoffOpenURLToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "browser_handoff.openURL",
			Description: "Ask the Companion bridge to open a URL on the user's computer without running shell commands.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"],"additionalProperties":false}`),
		},
		Handler: func(toolContext context.Context, input browserHandoffOpenURLToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.openBrowserHandoffTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[approvalRequestToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "approval.request",
			Description: "Pause the current task while waiting for explicit user approval. Use only before destructive, high-risk, external-send, credential, paid-service, or tool-availability ask actions. Do not use for ordinary non-destructive writes such as calendar.event.add, flow.task.add, site.app.create, or site.app.publish.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"},"reason":{"type":"string"}},"required":["message"],"additionalProperties":false}`),
		},
		Handler: toolCatalogBuilder.requestApprovalTool,
		Result:  agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[fileWriteToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "file.write",
			Description: "Write a UTF-8 text file under the Blueclaw workspace. Use this for markdown, scripts, and source files instead of shell redirection.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"},"mode":{"type":"integer"}},"required":["path","content"],"additionalProperties":false}`),
		},
		Handler: func(toolContext context.Context, input fileWriteToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.writeFileTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[fileAttachToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "file.attach",
			Description: "Attach one or more existing workspace files to the final reply evidence. Use paths for related artifact sets.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"paths":{"type":"array","items":{"type":"string"}},"filename":{"type":"string"},"contentType":{"type":"string"},"title":{"type":"string"}},"additionalProperties":false}`),
		},
		Handler: func(toolContext context.Context, input fileAttachToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.attachFileTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	toolCatalogBuilder.registerScheduleTools(toolRegistry, handlerContext)
	toolCatalogBuilder.registerSkillManagementTools(toolRegistry)
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
			return agent.ToolResult{Content: output}, nil
		})
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerCapabilityTools(toolRegistry *agent.ToolSet, request ToolCatalogRequest) {
	for _, capabilityToolDescriptor := range toolCatalogBuilder.capabilityToolDefinitions() {
		toolDescriptor := capabilityToolDescriptor
		toolName := toolDescriptor.Name
		toolRegistry.RegisterBoundTool(agent.BoundTool{
			Definition: agent.ToolDefinition{
				Name:            toolName,
				Description:     firstNonEmptyString(toolDescriptor.Description, "InternKim capability tool"),
				InputSchema:     toolDescriptor.InputSchema,
				OutputSchema:    toolDescriptor.OutputSchema,
				PolicyResource:  toolDescriptor.PolicyResource,
				SideEffectClass: toolDescriptor.SideEffectClass,
			},
			Availability: capabilityToolAvailability(toolDescriptor),
			Handler: func(toolContext context.Context, toolInvocation agent.ToolInvocation) (agent.ToolResult, error) {
				var response struct {
					Content string          `json:"content"`
					IsError bool            `json:"isError"`
					Status  string          `json:"status"`
					Result  json.RawMessage `json:"result"`
				}
				policyResource := firstNonEmptyString(toolDescriptor.PolicyResource, "tool:"+toolName)
				if !access.CanAccess(access.Request{PersonAccess: request.PersonAccess, Action: access.ActionExecute, Resource: policyResource}) {
					return agent.ToolResult{Content: "current account cannot execute this tool", IsError: true}, nil
				}
				toolInput, errorValue := toolCatalogBuilder.enrichCapabilityToolInput(toolName, request, json.RawMessage(toolInvocation.Input))
				if errorValue != nil {
					return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
				}
				errorValue = toolCatalogBuilder.capabilityClient.PostJSON(toolContext, "/v1/tools/"+url.PathEscape(toolName)+"/invoke", capabilityToolRequest(toolName, request, toolInput), &response)
				if errorValue != nil {
					return agent.ToolResult{}, errorValue
				}
				content := strings.TrimSpace(response.Content)
				if content == "" && len(response.Result) > 0 {
					content = string(response.Result)
				}
				isError := response.IsError || response.Status == "error" || response.Status == "denied"
				return agent.ToolResult{Content: content, IsError: isError, Attachments: capabilityAttachments(response.Result), RecoveryActions: capabilityRecoveryActions(response.Result)}, nil
			},
		})
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

func capabilityToolAvailability(toolDescriptor CapabilityToolDescriptor) agent.ToolAvailability {
	if toolDescriptor.RequiresApproval {
		return agent.ToolAvailability{Status: agent.ToolAvailabilityAsk, Reason: "requires approval"}
	}
	return agent.ToolAvailability{Status: agent.ToolAvailabilityAvailable}
}

func (toolCatalogBuilder *ToolCatalogBuilder) runTerminalTool(toolContext context.Context, input security.CommandRequest, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	if toolCatalogBuilder.terminalService == nil {
		return agent.ToolResult{Content: "terminal service is unavailable", IsError: true}, nil
	}
	input.Command = toolCatalogBuilder.resolveAgentWorkspaceReferences(input.Command)
	input.Stdin = toolCatalogBuilder.resolveAgentWorkspaceReferences(input.Stdin)
	input.EnvironmentVariables = toolCatalogBuilder.resolveAgentWorkspaceEnvironment(input.EnvironmentVariables)
	if strings.TrimSpace(input.WorkingDirectoryPath) == "" {
		input.WorkingDirectoryPath = handlerContext.conversationScope.DefaultDirectoryPath
	} else {
		input.WorkingDirectoryPath = toolCatalogBuilder.resolveAgentWorkspacePath(input.WorkingDirectoryPath)
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionWrite, input.WorkingDirectoryPath) {
		return agent.ToolResult{Content: "current account cannot use this workspace path", IsError: true}, nil
	}
	input.ExecutionIdentity = security.ExecutionIdentityForPersonAccess(handlerContext.request.PersonAccess, toolCatalogBuilder.workspaceRootPath)
	commandResult, errorValue := toolCatalogBuilder.terminalService.RunCommand(input)
	content := marshalToolResult(commandResult)
	if errorValue != nil {
		return agent.ToolResult{Content: content, IsError: true}, nil
	}
	_ = toolContext
	return agent.ToolResult{Content: content}, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) sessionTerminalTool(toolContext context.Context, input terminalSessionToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	if toolCatalogBuilder.terminalService == nil {
		return agent.ToolResult{Content: "terminal service is unavailable", IsError: true}, nil
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
	workingDirectoryPath := firstNonEmptyString(toolCatalogBuilder.resolveAgentWorkspacePath(input.WorkingDirectoryPath), handlerContext.conversationScope.DefaultDirectoryPath)
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
		ExecutionIdentity:    security.ExecutionIdentityForPersonAccess(handlerContext.request.PersonAccess, toolCatalogBuilder.workspaceRootPath),
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

func (toolCatalogBuilder *ToolCatalogBuilder) openBrowserHandoffTool(toolContext context.Context, input browserHandoffOpenURLToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	if toolCatalogBuilder.capabilityClient.HTTPClient == nil {
		return agent.ToolResult{Content: "companion bridge capability client is unavailable", IsError: true}, nil
	}
	inputDocument, errorValue := json.Marshal(map[string]string{"url": input.URL})
	if errorValue != nil {
		return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
	}
	requestDocument := capabilityToolRequest("browser.handoff", handlerContext.request, inputDocument)
	requestDocument["executionMode"] = "companion"
	requestDocument["requiresUserPresence"] = true
	requestDocument["privacyClass"] = "user_browser"
	var response struct {
		Content string          `json:"content"`
		IsError bool            `json:"isError"`
		Status  string          `json:"status"`
		Result  json.RawMessage `json:"result"`
	}
	errorValue = toolCatalogBuilder.capabilityClient.PostJSON(toolContext, "/v1/tools/browser.handoff/invoke", requestDocument, &response)
	if errorValue != nil {
		return agent.ToolResult{}, errorValue
	}
	content := firstNonEmptyString(response.Content, string(response.Result))
	isError := response.IsError || response.Status == "error" || response.Status == "denied"
	if response.Status == "waiting_for_user" {
		if taskRunID := agent.TaskRunIDFromContext(toolContext); taskRunID != "" && toolCatalogBuilder.taskRunService != nil {
			_, _ = toolCatalogBuilder.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusWaitingUserInput, content)
		}
	}
	if taskRunID := agent.TaskRunIDFromContext(toolContext); taskRunID != "" && toolCatalogBuilder.taskRunService != nil {
		toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, browserHandoffEventName(isError), marshalToolResult(map[string]string{"url": input.URL, "content": content}))
	}
	return agent.ToolResult{Content: content, IsError: isError, Attachments: capabilityAttachments(response.Result)}, nil
}

func browserHandoffEventName(isError bool) string {
	if isError {
		return "browser_handoff.failed"
	}
	return "browser_handoff.opened"
}

func (toolCatalogBuilder *ToolCatalogBuilder) requestApprovalTool(toolContext context.Context, input approvalRequestToolInput) (agent.ToolResult, error) {
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

func (toolCatalogBuilder *ToolCatalogBuilder) writeFileTool(toolContext context.Context, input fileWriteToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	resolvedPath, errorValue := toolCatalogBuilder.resolveWorkspaceFilePathForConversation(input.Path, handlerContext.conversationScope)
	if errorValue != nil {
		return agent.ToolResult{Content: errorValue.Error(), IsError: true}, nil
	}
	if isImmutableSkillPath(toolCatalogBuilder.workspaceRootPath, resolvedPath) {
		return agent.ToolResult{Content: "file.write cannot modify built-in skill files", IsError: true}, nil
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionWrite, resolvedPath) {
		return agent.ToolResult{Content: "current account cannot write this file", IsError: true}, nil
	}
	fileMode := os.FileMode(0660)
	if input.Mode != 0 {
		fileMode = os.FileMode(input.Mode)
	}
	if errorValue := os.MkdirAll(filepath.Dir(resolvedPath), 0770); errorValue != nil {
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

func (toolCatalogBuilder *ToolCatalogBuilder) attachFileTool(toolContext context.Context, input fileAttachToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
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

func (toolCatalogBuilder *ToolCatalogBuilder) fileAttachment(path string, input fileAttachToolInput, handlerContext toolHandlerContext) (agent.FileAttachment, error) {
	resolvedPath, errorValue := toolCatalogBuilder.resolveWorkspaceFilePathForConversation(path, handlerContext.conversationScope)
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

func attachmentFilename(input fileAttachToolInput, resolvedPath string) string {
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

func (toolCatalogBuilder *ToolCatalogBuilder) resolveWorkspaceFilePathForConversation(value string, conversationScope ConversationResourceScope) (string, error) {
	trimmedPath := toolCatalogBuilder.resolveAgentWorkspacePath(value)
	if trimmedPath == "" {
		return "", errors.New("path is required")
	}
	if filepath.IsAbs(trimmedPath) {
		return toolCatalogBuilder.resolveWorkspaceFilePath(trimmedPath)
	}
	defaultDirectoryPath := firstNonEmptyString(conversationScope.DefaultDirectoryPath, toolCatalogBuilder.workspaceRootPath)
	return toolCatalogBuilder.resolveWorkspaceFilePath(filepath.Join(defaultDirectoryPath, trimmedPath))
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
	requestDocument := map[string]any{
		"toolName": toolName,
		"input":    toolInput,
		"context": map[string]any{
			"requesterPersonID":       request.RequesterPersonID,
			"requesterEmail":          request.RequesterEmail,
			"requesterName":           request.RequesterName,
			"requesterPlatformUserID": request.RequesterPlatformUserID,
			"conversationID":          request.ConversationID,
			"conversationType":        request.ConversationType,
			"channelID":               request.ConversationChannelID,
			"channelName":             request.ConversationChannelName,
			"platform":                request.Platform,
		},
	}
	if shouldRequireCompanionBrowser(toolName, request, toolInput) {
		requestDocument["executionMode"] = "companion"
		requestDocument["requiresUserPresence"] = true
		requestDocument["privacyClass"] = "user_browser"
	}
	return requestDocument
}

func (toolCatalogBuilder *ToolCatalogBuilder) enrichCapabilityToolInput(toolName string, request ToolCatalogRequest, toolInput json.RawMessage) (json.RawMessage, error) {
	if strings.TrimSpace(toolName) != "site.app.publish" {
		return toolInput, nil
	}
	inputDocument := map[string]any{}
	if len(toolInput) > 0 {
		if errorValue := json.Unmarshal(toolInput, &inputDocument); errorValue != nil {
			return nil, errorValue
		}
	}
	sourceWorkspacePath := siteSourceWorkspacePath(inputDocument)
	if sourceWorkspacePath == "" {
		sourceWorkspacePath = defaultSiteSourceWorkspacePath(inputDocument)
	}
	if sourceWorkspacePath == "" {
		return toolInput, nil
	}
	resolvedSourcePath, errorValue := toolCatalogBuilder.resolveWorkspaceFilePath(sourceWorkspacePath)
	if errorValue != nil {
		return nil, errorValue
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(request.PersonAccess, access.ActionRead, resolvedSourcePath) {
		return nil, errors.New("current account cannot publish this site workspace path")
	}
	sourceBundleBase64, errorValue := buildSiteSourceBundleBase64(resolvedSourcePath)
	if errorValue != nil {
		return nil, errorValue
	}
	inputDocument["sourceWorkspacePath"] = toolCatalogBuilder.agentWorkspacePath(resolvedSourcePath)
	inputDocument["sourceBundleBase64"] = sourceBundleBase64
	inputDocument["sourceBundleFormat"] = "tar.gz"
	return json.Marshal(inputDocument)
}

func siteSourceWorkspacePath(inputDocument map[string]any) string {
	value, isString := inputDocument["sourceWorkspacePath"].(string)
	if !isString {
		return ""
	}
	return strings.TrimSpace(value)
}

func defaultSiteSourceWorkspacePath(inputDocument map[string]any) string {
	siteID, isString := inputDocument["siteID"].(string)
	if !isString || strings.TrimSpace(siteID) == "" {
		return ""
	}
	return filepath.Join("/workspace", "circles", "staff", "sites", strings.TrimSpace(siteID))
}

func buildSiteSourceBundleBase64(sourceWorkspacePath string) (string, error) {
	buffer := bytes.Buffer{}
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	errorValue := filepath.Walk(sourceWorkspacePath, func(path string, information os.FileInfo, walkError error) error {
		if walkError != nil {
			return walkError
		}
		relativePath, errorValue := filepath.Rel(sourceWorkspacePath, path)
		if errorValue != nil || relativePath == "." {
			return errorValue
		}
		if shouldSkipSiteSourceBundlePath(relativePath, information) {
			if information.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		return writeSiteSourceBundleEntry(tarWriter, path, relativePath, information)
	})
	closeError := tarWriter.Close()
	gzipCloseError := gzipWriter.Close()
	if errorValue != nil {
		return "", errorValue
	}
	if closeError != nil {
		return "", closeError
	}
	if gzipCloseError != nil {
		return "", gzipCloseError
	}
	if buffer.Len() > siteSourceBundleMaximumBytes {
		return "", errors.New("site source bundle is too large")
	}
	return base64.StdEncoding.EncodeToString(buffer.Bytes()), nil
}

func shouldSkipSiteSourceBundlePath(relativePath string, information os.FileInfo) bool {
	_ = information
	for _, component := range strings.Split(filepath.Clean(relativePath), string(os.PathSeparator)) {
		switch component {
		case ".git", "node_modules":
			return true
		}
	}
	return false
}

func writeSiteSourceBundleEntry(tarWriter *tar.Writer, path string, relativePath string, information os.FileInfo) error {
	header, errorValue := tar.FileInfoHeader(information, "")
	if errorValue != nil {
		return errorValue
	}
	header.Name = filepath.ToSlash(relativePath)
	if errorValue := tarWriter.WriteHeader(header); errorValue != nil {
		return errorValue
	}
	if information.IsDir() {
		return nil
	}
	file, errorValue := os.Open(path)
	if errorValue != nil {
		return errorValue
	}
	defer file.Close()
	_, errorValue = io.Copy(tarWriter, file)
	return errorValue
}

func shouldRequireCompanionBrowser(toolName string, request ToolCatalogRequest, toolInput json.RawMessage) bool {
	trimmedToolName := strings.TrimSpace(toolName)
	if !strings.HasPrefix(trimmedToolName, "browser.") {
		return false
	}
	switch trimmedToolName {
	case "browser.handoff", "browser.screenshot":
		return true
	}
	return promptRequiresUserBrowser(request.Prompt) || browserFollowUpRequiresUserBrowser(request) || browserInputUsesPrivateURL(toolInput)
}

func promptRequiresUserBrowser(prompt string) bool {
	normalizedPrompt := strings.ToLower(strings.TrimSpace(prompt))
	if normalizedPrompt == "" {
		return false
	}
	if containsAny(normalizedPrompt, []string{"브라우저 열", "브라우저 켜", "open browser", "open the browser"}) {
		return true
	}
	return containsAny(normalizedPrompt, []string{
		"로그인", "login", "sign in", "signin", "account", "계정",
		"mfa", "2fa", "otp", "쿠키", "cookie", "세션", "session",
		"결제", "payment", "관리자", "admin", "내 컴퓨터", "my computer",
		"내 브라우저", "my browser", "localhost", "local url", "private network",
		"credential", "credentials", "자격 증명", "인증 정보",
		"google cloud console", "cloud console", "구글 클라우드 콘솔",
	})
}

func browserFollowUpRequiresUserBrowser(request ToolCatalogRequest) bool {
	if !looksLikeBrowserFollowUp(request.Prompt) {
		return false
	}
	return visibleContextMentionsUserBrowser(request.VisibleContext)
}

func looksLikeBrowserFollowUp(prompt string) bool {
	normalizedPrompt := strings.ToLower(strings.TrimSpace(prompt))
	if normalizedPrompt == "" {
		return false
	}
	return containsAny(normalizedPrompt, []string{
		"다시 해", "다시 열", "다시 시도", "계속해", "진행해", "이제 연결", "연결했",
		"try again", "open it again", "do it again", "continue", "go ahead", "connected now",
	})
}

func visibleContextMentionsUserBrowser(visibleContext agent.VisibleContext) bool {
	for _, message := range visibleContext.Messages {
		text := strings.ToLower(strings.TrimSpace(message.Text))
		if text == "" {
			continue
		}
		if containsAny(text, []string{
			"browser", "브라우저", "companion", "컴패니언", "login", "로그인",
			"credential", "credentials", "자격 증명", "인증 정보",
			"google cloud console", "cloud console", "구글 클라우드 콘솔",
		}) {
			return true
		}
	}
	return false
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

func copyCapabilityToolDescriptors(toolDescriptors []CapabilityToolDescriptor) []CapabilityToolDescriptor {
	copiedToolDescriptors := []CapabilityToolDescriptor{}
	for _, toolDescriptor := range toolDescriptors {
		trimmedName := strings.TrimSpace(toolDescriptor.Name)
		if trimmedName == "" {
			continue
		}
		toolDescriptor.Name = trimmedName
		copiedToolDescriptors = append(copiedToolDescriptors, toolDescriptor)
	}
	return copiedToolDescriptors
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
