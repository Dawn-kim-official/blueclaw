package agentruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
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
	"blueclaw/internal/workspacepath"
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
	pinnedMemoryStore         *memory.MarkdownStore
	memoryUpdateQueue         memory.MemoryUpdateEnqueuer
	mcpRegistry               *mcp.McpRegistry
	capabilityClient          capability.Client
	capabilityToolNames       []string
	capabilityToolDescriptors []CapabilityToolDescriptor
	terminalService           *security.TerminalSessionService
	workspaceActorFactory     security.WorkspaceActorFactory
	taskRunService            *task.TaskRunService
	taskScheduleRepository    task.TaskScheduleRepository
	taskWaitTokenRepository   task.TaskWaitTokenRepository
	workspaceRootPath         string
	skillChangeHandler        func(context.Context)
	skillRetriever            agent.SkillRetriever
	instructionBundleLoader   func() agent.InstructionBundle
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
	TaskSource                TaskLaunchSource
	IsScheduledRun            bool
	IsApprovalContinuation    bool
	ConversationID            string
	ConversationType          string
	ConversationChannelID     string
	ConversationChannelName   string
	ActiveCircleID            string
	ActiveCircleConflict      bool
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

type browserHandoffOpenURLToolInput struct {
	URL string `json:"url"`
}

type askConfirmToolInput struct {
	UserFacingMessage string `json:"userFacingMessage"`
	ReasonCode        string `json:"reasonCode"`
	ReasonDetail      string `json:"reasonDetail"`
	Message           string `json:"message"`
	Reason            string `json:"reason"`
}

type askChoiceToolInput struct {
	Question             string            `json:"question"`
	Options              []askChoiceOption `json:"options"`
	RecommendedOptionKey string            `json:"recommendedOptionKey"`
	SelectionMode        string            `json:"selectionMode"`
}

type askChoiceOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value string `json:"value"`
}

type askInputToolInput struct {
	Question string `json:"question"`
}

type mathCalculateToolInput struct {
	Expression string `json:"expression"`
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

type filePromoteToolInput struct {
	Path                     string   `json:"path"`
	Paths                    []string `json:"paths"`
	DestinationDirectoryPath string   `json:"destinationDirectoryPath"`
	Overwrite                bool     `json:"overwrite"`
}

type skillSearchToolInput struct {
	Queries []agent.SkillSearchQuery `json:"queries"`
	Limit   int                      `json:"limit"`
}

type toolDescribeToolInput struct {
	Query    string `json:"query"`
	ToolName string `json:"toolName"`
	Prefix   string `json:"prefix"`
	Limit    int    `json:"limit"`
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

func (toolCatalogBuilder *ToolCatalogBuilder) UsePinnedMemoryStore(pinnedMemoryStore *memory.MarkdownStore) {
	toolCatalogBuilder.pinnedMemoryStore = pinnedMemoryStore
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseMemoryUpdateQueue(memoryUpdateQueue memory.MemoryUpdateEnqueuer) {
	toolCatalogBuilder.memoryUpdateQueue = memoryUpdateQueue
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
	if terminalService != nil && toolCatalogBuilder.workspaceActorFactory == nil {
		toolCatalogBuilder.workspaceActorFactory = terminalService.WorkspaceActorFactory()
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseWorkspaceActorFactory(workspaceActorFactory security.WorkspaceActorFactory) {
	toolCatalogBuilder.workspaceActorFactory = workspaceActorFactory
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseTaskRunService(taskRunService *task.TaskRunService) {
	toolCatalogBuilder.taskRunService = taskRunService
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseTaskScheduleRepository(taskScheduleRepository task.TaskScheduleRepository) {
	toolCatalogBuilder.taskScheduleRepository = taskScheduleRepository
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseTaskWaitTokenRepository(taskWaitTokenRepository task.TaskWaitTokenRepository) {
	toolCatalogBuilder.taskWaitTokenRepository = taskWaitTokenRepository
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

func (toolCatalogBuilder *ToolCatalogBuilder) UseSkillSearch(skillRetriever agent.SkillRetriever, instructionBundleLoader func() agent.InstructionBundle) {
	toolCatalogBuilder.skillRetriever = skillRetriever
	toolCatalogBuilder.instructionBundleLoader = instructionBundleLoader
}

func (toolCatalogBuilder *ToolCatalogBuilder) WorkspaceRootPath() string {
	return strings.TrimSpace(toolCatalogBuilder.workspaceRootPath)
}

func (toolCatalogBuilder *ToolCatalogBuilder) BuildToolSet(request ToolCatalogRequest) *agent.ToolSet {
	request = withResolvedActiveCircle(request)
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
	toolCatalogBuilder.registerToolDescribeTool(toolSet, toolSet)
	toolCatalogBuilder.registerSkillSearchTool(toolSet, handlerContext, toolSet)
	return toolSet
}

func (toolCatalogBuilder *ToolCatalogBuilder) allowedToolNames(profileName string) []string {
	normalizedProfileName := normalizeProfileName(profileName)
	if allowedToolNames, isFound := toolCatalogBuilder.allowedToolNamesByProfile[normalizedProfileName]; isFound {
		return agent.DefaultAllowedToolNames(allowedToolNames)
	}
	if len(toolCatalogBuilder.fallbackAllowedToolNames) > 0 {
		return agent.DefaultAllowedToolNames(toolCatalogBuilder.fallbackAllowedToolNames)
	}
	return DefaultAllowedToolNames()
}

func DefaultAllowedToolNames() []string {
	return agent.DefaultAllowedToolNames([]string{"memory.search", "math.calculate", "terminal.run", "terminal.session", "browser_handoff.openURL", "ask.confirm", "ask.choice", "ask.input", "file.read", "file.write", "file.promote", "file.attach", "skill.add", "skill.remove", "skill.search", "tool.describe", "schedule.create", "schedule.cancel"})
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
	registerMemoryTools(toolCatalogBuilder, toolRegistry, request)
}

func fetchHistoryTool(toolContext context.Context, input historyToolInput, request ToolCatalogRequest) (agent.ToolResult, error) {
	historyCursor := firstNonEmptyString(input.HistoryCursor, request.HistoryCursor)
	if historyCursor == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "conversation_history", "history cursor is unavailable"), nil
	}
	limit := input.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	visibleContext, errorValue := request.HistoryProvider.FetchHistory(toolContext, historyCursor, limit)
	if errorValue != nil {
		return agent.ToolResult{}, errorValue
	}
	return agent.ToolSuccess(marshalToolResult(visibleContext)), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerBuiltInTools(toolRegistry *agent.ToolSet, handlerContext toolHandlerContext) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[mathCalculateToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "math.calculate",
			Description: "Evaluate a safe arithmetic expression using bc. Supports numbers, parentheses, +, -, *, /, %, ^, and **.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"expression":{"type":"string"}},"required":["expression"],"additionalProperties":false}`),
		},
		Handler: toolCatalogBuilder.calculateMathTool,
		Result:  agent.IdentityToolResult,
	})
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
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[askConfirmToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "ask.confirm",
			Description: "Pause the current task while waiting for explicit user confirmation. Use only before destructive, high-risk, external-send, credential, paid-service, or capability-unlock actions. userFacingMessage is shown directly to the user and must use the same language as the original user request. reasonCode and reasonDetail are internal only.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"userFacingMessage":{"type":"string","description":"User-facing approval question shown directly to the user, written in the same language as the original user request."},"reasonCode":{"type":"string","enum":["external_send","destructive_action","credential_access","paid_action","permission_change","capability_unlock","other_sensitive_action"]},"reasonDetail":{"type":"string","description":"Optional internal diagnostic detail. Never write user-facing prose here."},"message":{"type":"string","description":"Legacy alias for userFacingMessage."},"reason":{"type":"string","description":"Legacy internal detail. Never shown to the user."}},"required":["userFacingMessage","reasonCode"],"additionalProperties":false}`),
		},
		Handler: toolCatalogBuilder.askConfirmTool,
		Result:  agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[askChoiceToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "ask.choice",
			Description: "Pause the current task and ask the user to choose from explicit options. Always include exactly one recommendedOptionKey. Use selectionMode single or multiple.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"},"options":{"type":"array","minItems":2,"maxItems":26,"items":{"type":"object","properties":{"key":{"type":"string"},"label":{"type":"string"},"value":{"type":"string"}},"required":["label"],"additionalProperties":false}},"recommendedOptionKey":{"type":"string"},"selectionMode":{"type":"string","enum":["single","multiple"]}},"required":["question","options","recommendedOptionKey"],"additionalProperties":false}`),
		},
		Handler: toolCatalogBuilder.askChoiceTool,
		Result:  agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[askInputToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "ask.input",
			Description: "Pause the current task and ask the user for free-form input needed to continue.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"}},"required":["question"],"additionalProperties":false}`),
		},
		Handler: toolCatalogBuilder.askInputTool,
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
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[filePromoteToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "file.promote",
			Description: "Copy finished draft artifacts from tmp/<slug>/build into artifacts/<slug>/ or an allowed durable circle/shared directory before attaching.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"paths":{"type":"array","items":{"type":"string"}},"destinationDirectoryPath":{"type":"string"},"overwrite":{"type":"boolean"}},"required":["destinationDirectoryPath"],"additionalProperties":false}`),
		},
		Handler: func(toolContext context.Context, input filePromoteToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.promoteFileTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	toolCatalogBuilder.registerScheduleTools(toolRegistry, handlerContext)
	toolCatalogBuilder.registerSkillManagementTools(toolRegistry)
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerSkillSearchTool(toolRegistry *agent.ToolSet, handlerContext toolHandlerContext, availableToolSet *agent.ToolSet) {
	if toolCatalogBuilder.skillRetriever == nil || toolCatalogBuilder.instructionBundleLoader == nil {
		return
	}
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[skillSearchToolInput, agent.SkillSearchResult]{
		Definition: agent.ToolDefinition{
			Name:        "skill.search",
			Description: "Search available Blueclaw skills by concise skill-need descriptions.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"queries":{"type":"array","minItems":0,"maxItems":5,"items":{"type":"object","properties":{"description":{"type":"string"}},"required":["description"],"additionalProperties":false}},"limit":{"type":"integer"}},"required":["queries"],"additionalProperties":false}`),
		},
		Handler: func(toolContext context.Context, input skillSearchToolInput) (agent.SkillSearchResult, error) {
			return toolCatalogBuilder.searchSkills(toolContext, input, handlerContext, availableToolSet)
		},
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerToolDescribeTool(toolRegistry *agent.ToolSet, availableToolSet *agent.ToolSet) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[toolDescribeToolInput, map[string]any]{
		Definition: agent.ToolDefinition{
			Name:        "tool.describe",
			Description: "Search or inspect available Blueclaw tools by exact name, prefix, or text query before requiring or calling them.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"toolName":{"type":"string"},"prefix":{"type":"string"},"limit":{"type":"integer"}},"additionalProperties":false}`),
		},
		Handler: func(_ context.Context, input toolDescribeToolInput) (map[string]any, error) {
			return describeTools(input, availableToolSet), nil
		},
	})
}

func describeTools(input toolDescribeToolInput, toolSet *agent.ToolSet) map[string]any {
	if toolSet == nil {
		return map[string]any{"tools": []map[string]any{}}
	}
	limit := input.Limit
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	items := []map[string]any{}
	for _, toolDefinition := range toolSet.ListRegisteredToolDefinitions() {
		toolName := strings.TrimSpace(toolDefinition.Name)
		if !toolDescriptionMatches(input, toolDefinition) {
			continue
		}
		availability, _ := toolSet.ToolAvailability(toolName)
		if strings.TrimSpace(availability.Status) == agent.ToolAvailabilityDenied {
			continue
		}
		items = append(items, map[string]any{
			"name":         toolName,
			"description":  firstNonEmptyString(toolDefinition.Description, agentSpecificToolDescription(toolName)),
			"inputSchema":  toolDefinition.InputSchema,
			"availability": availability,
		})
		if len(items) >= limit {
			break
		}
	}
	return map[string]any{"tools": items}
}

func toolDescriptionMatches(input toolDescribeToolInput, toolDefinition agent.ToolDefinition) bool {
	toolName := strings.TrimSpace(toolDefinition.Name)
	if expectedToolName := strings.TrimSpace(input.ToolName); expectedToolName != "" {
		return toolName == expectedToolName
	}
	if prefix := strings.TrimSpace(input.Prefix); prefix != "" && !strings.HasPrefix(toolName, prefix) {
		return false
	}
	query := strings.ToLower(strings.TrimSpace(input.Query))
	if query == "" {
		return true
	}
	searchText := strings.ToLower(toolName + " " + toolDefinition.Description)
	return strings.Contains(searchText, query)
}

func agentSpecificToolDescription(toolName string) string {
	toolSet := agent.NewToolSet([]string{toolName})
	toolSet.RegisterTool(agent.ToolDefinition{Name: toolName}, func(context.Context, agent.ToolInvocation) (agent.ToolResult, error) {
		return agent.ToolSuccess(""), nil
	})
	for _, line := range strings.Split(toolSet.Descriptions(), "\n") {
		if strings.HasPrefix(line, "- "+toolName+": ") {
			return strings.TrimPrefix(line, "- "+toolName+": ")
		}
	}
	return ""
}

func (toolCatalogBuilder *ToolCatalogBuilder) searchSkills(toolContext context.Context, input skillSearchToolInput, handlerContext toolHandlerContext, availableToolSet *agent.ToolSet) (agent.SkillSearchResult, error) {
	limit := input.Limit
	if limit <= 0 || limit > 8 {
		limit = 5
	}
	instructionBundle := toolCatalogBuilder.instructionBundleLoader()
	agentRequest := agent.AgentRequest{
		ProfileName:       handlerContext.request.ProfileName,
		Prompt:            handlerContext.request.Prompt,
		VisibleContext:    handlerContext.request.VisibleContext,
		RequesterPersonID: handlerContext.request.RequesterPersonID,
		RequesterName:     handlerContext.request.RequesterName,
		ToolSet:           availableToolSet,
	}
	retrievalResult := toolCatalogBuilder.skillRetriever.Search(toolContext, agentRequest, instructionBundle.Skills, agent.SkillSearchQuerySet{Queries: input.Queries}, limit)
	retrievalResult = includeExactSkillNameMatches(instructionBundle.Skills, input.Queries, retrievalResult)
	return skillSearchResult(instructionBundle.Skills, retrievalResult), nil
}

func includeExactSkillNameMatches(skillInstructions []agent.SkillInstruction, queries []agent.SkillSearchQuery, retrievalResult agent.SkillRetrievalResult) agent.SkillRetrievalResult {
	for _, query := range queries {
		queryDescription := strings.TrimSpace(query.Description)
		if queryDescription == "" {
			continue
		}
		for _, skillInstruction := range skillInstructions {
			if strings.TrimSpace(skillInstruction.Name) != queryDescription {
				continue
			}
			retrievalResult.SelectedCandidates = prependSkillCandidate(retrievalResult.SelectedCandidates, agent.SkillCandidate{
				Name:   skillInstruction.Name,
				Score:  1,
				Reason: "exact_name_match",
				Source: skillInstruction.Source,
			})
		}
	}
	return retrievalResult
}

func prependSkillCandidate(candidates []agent.SkillCandidate, candidate agent.SkillCandidate) []agent.SkillCandidate {
	result := []agent.SkillCandidate{candidate}
	for _, existingCandidate := range candidates {
		if strings.TrimSpace(existingCandidate.Name) == strings.TrimSpace(candidate.Name) {
			continue
		}
		result = append(result, existingCandidate)
	}
	return result
}

func skillSearchResult(skillInstructions []agent.SkillInstruction, retrievalResult agent.SkillRetrievalResult) agent.SkillSearchResult {
	skillInstructionByName := map[string]agent.SkillInstruction{}
	for _, skillInstruction := range skillInstructions {
		skillInstructionByName[skillInstruction.Name] = skillInstruction
	}
	items := []agent.SkillSearchResultItem{}
	for _, candidate := range retrievalResult.SelectedCandidates {
		skillInstruction, isFound := skillInstructionByName[candidate.Name]
		if !isFound {
			continue
		}
		items = append(items, agent.SkillSearchResultItem{
			Name:        skillInstruction.Name,
			Description: skillInstruction.Description,
			Score:       candidate.Score,
			Tools:       append([]string{}, skillInstruction.AllowedTools...),
			SourcePath:  skillInstruction.Source.Path,
			Completion:  skillInstruction.Completion,
		})
	}
	return agent.SkillSearchResult{Skills: items}
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
		toolRegistry.RegisterBoundTool(agent.BoundTool{
			Definition: agent.ToolDefinition{
				Name:            toolName,
				Description:     firstNonEmptyString(toolDescriptor.Description, "InternKim capability tool"),
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
				errorValue = toolCatalogBuilder.capabilityClient.PostJSON(toolContext, "/v1/tools/"+url.PathEscape(toolName)+"/invoke", capabilityToolRequest(toolName, request, toolInput), &response)
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
	if strings.TrimSpace(toolName) != "platform.dm.send" {
		return false
	}
	return request.IsScheduledRun || request.IsApprovalContinuation
}

func (toolCatalogBuilder *ToolCatalogBuilder) runTerminalTool(toolContext context.Context, input security.CommandRequest, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	if toolCatalogBuilder.terminalService == nil {
		return agent.ToolFailureResult(agent.FailureDependencyUnavailable, agent.FailureCodes.Unavailable, "terminal_run", "terminal service is unavailable"), nil
	}
	scope := toolCatalogBuilder.workspaceScopeForToolContext(toolContext, handlerContext.request)
	resolver := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath)
	workingDirectory, errorValue := resolver.ResolveDirectory(input.WorkingDirectoryPath, scope)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "terminal_working_directory", errorValue.Error()), nil
	}
	input.Command = toolCatalogBuilder.resolveAgentWorkspaceReferences(input.Command)
	input.Stdin = toolCatalogBuilder.resolveAgentWorkspaceReferences(input.Stdin)
	input.EnvironmentVariables = toolCatalogBuilder.resolveAgentWorkspaceEnvironment(mergeWorkspaceEnvironment(input.EnvironmentVariables, scope.EnvironmentVariables()))
	input.WorkingDirectoryPath = workingDirectory.ConcretePath
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionWrite, input.WorkingDirectoryPath) {
		return terminalWorkspaceAccessFailure(input.WorkingDirectoryPath), nil
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return *actorFailure, nil
	}
	if errorValue := workspaceActor.MkdirAll(toolContext, workspacepath.Directory(workingDirectory), 02770); errorValue != nil {
		return actorToolFailure("mkdir_all", "terminal_working_directory", workingDirectory.VirtualPath, errorValue), nil
	}
	input.ExecutionIdentity = toolCatalogBuilder.executionIdentityForRequester(handlerContext.request)
	commandResult, errorValue := workspaceActor.Run(toolContext, input)
	content := marshalToolResult(commandResult)
	if errorValue != nil {
		if security.IsCommandPathGuardrailError(errorValue) {
			return terminalPathGuardrailFailure(commandResult, content), nil
		}
		return agent.ToolFailureWithOutput(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "terminal_run", content, json.RawMessage(content)), nil
	}
	_ = toolContext
	return agent.ToolSuccess(content), nil
}

func terminalWorkspaceAccessFailure(workingDirectoryPath string) agent.ToolResult {
	message := "current account cannot use this workspace path: terminal workingDirectoryPath " + strings.TrimSpace(workingDirectoryPath) + "; recovery: use tmp/<slug> relative to the default writable directory for draft work, then promote accepted files to artifacts/<slug> or an allowed circle/shared path"
	result := agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "terminal_working_directory_access", message)
	result.Failure.Retryable = true
	result.Failure.SafeRetry = true
	return result
}

func terminalPathGuardrailFailure(commandResult security.CommandResult, content string) agent.ToolResult {
	message := strings.TrimSpace(commandResult.Stderr)
	if message == "" {
		message = content
	}
	result := agent.ToolFailureWithOutput(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "terminal_path_guardrail", message, json.RawMessage(content))
	result.Failure.Retryable = true
	result.Failure.SafeRetry = true
	return result
}

func (toolCatalogBuilder *ToolCatalogBuilder) workspaceActorForRequest(toolContext context.Context, request ToolCatalogRequest) (security.WorkspaceActor, *agent.ToolResult) {
	if toolCatalogBuilder.workspaceActorFactory == nil {
		result := actorToolFailure("requester", "actor_runtime", "", security.WorkspaceActorError{
			Operation: "requester",
			Stage:     "factory",
			Code:      security.ActorErrorCodeRuntimeUnavailable,
			Detail:    "workspace actor factory is unavailable",
		})
		return nil, &result
	}
	personAccess := request.PersonAccess
	if strings.TrimSpace(personAccess.PersonID) == "" {
		personAccess.PersonID = strings.TrimSpace(request.RequesterPersonID)
	}
	workspaceActor, errorValue := toolCatalogBuilder.workspaceActorFactory.Requester(toolContext, security.WorkspaceActorRequest{
		PersonAccess:      personAccess,
		WorkspaceRootPath: toolCatalogBuilder.workspaceRootPath,
	})
	if errorValue != nil {
		stage := "actor_runtime"
		if actorFailureCode(errorValue) == security.ActorErrorCodeIdentityMissing {
			stage = "actor_identity_missing"
		}
		result := actorToolFailure("requester", stage, "", errorValue)
		return nil, &result
	}
	return workspaceActor, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) executionIdentityForRequester(request ToolCatalogRequest) security.ExecutionIdentity {
	personAccess := request.PersonAccess
	if strings.TrimSpace(personAccess.PersonID) == "" {
		personAccess.PersonID = strings.TrimSpace(request.RequesterPersonID)
	}
	return security.ExecutionIdentityForPersonAccess(personAccess, toolCatalogBuilder.workspaceRootPath)
}

func actorToolFailure(operation string, stage string, virtualPath string, errorValue error) agent.ToolResult {
	message := actorFailureMessage(operation, virtualPath, errorValue)
	failureKind := agent.FailureExternalService
	failureCode := agent.FailureCodes.OperationFailed
	if actorFailureCode(errorValue) == security.ActorErrorCodePermissionDenied {
		failureKind = agent.FailurePermissionDenied
		failureCode = agent.FailureCodes.AccessDenied
	}
	result := agent.ToolFailureWithOutput(failureKind, failureCode, stage, message, json.RawMessage(marshalToolResult(map[string]any{
		"operation":   operation,
		"stage":       stage,
		"virtualPath": virtualPath,
		"code":        actorFailureCode(errorValue),
		"detail":      actorFailureDetail(errorValue),
		"actorUser":   actorFailureUser(errorValue),
	})))
	result.Failure.Retryable = true
	result.Failure.SafeRetry = true
	return result
}

func actorFailureMessage(operation string, virtualPath string, errorValue error) string {
	detail := actorFailureDetail(errorValue)
	actorUser := actorFailureUser(errorValue)
	if actorUser == "" {
		actorUser = "unknown"
	}
	return fmt.Sprintf("actor.%s failed for %s as %s: %s", operation, strings.TrimSpace(virtualPath), actorUser, detail)
}

func actorFailureDetail(errorValue error) string {
	var actorError security.WorkspaceActorError
	if errors.As(errorValue, &actorError) {
		return firstNonEmptyString(strings.TrimSpace(actorError.Detail), errorValue.Error())
	}
	if errorValue == nil {
		return "operation failed"
	}
	return errorValue.Error()
}

func actorFailureCode(errorValue error) string {
	var actorError security.WorkspaceActorError
	if errors.As(errorValue, &actorError) {
		return strings.TrimSpace(actorError.Code)
	}
	return security.ActorErrorCodeOperationFailed
}

func actorFailureUser(errorValue error) string {
	var actorError security.WorkspaceActorError
	if errors.As(errorValue, &actorError) {
		return strings.TrimSpace(actorError.ActorUser)
	}
	return ""
}

func (toolCatalogBuilder *ToolCatalogBuilder) sessionTerminalTool(toolContext context.Context, input terminalSessionToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	if toolCatalogBuilder.terminalService == nil {
		return agent.ToolFailureResult(agent.FailureDependencyUnavailable, agent.FailureCodes.Unavailable, "terminal_session", "terminal service is unavailable"), nil
	}
	switch strings.TrimSpace(input.Action) {
	case "start":
		return toolCatalogBuilder.startTerminalSession(toolContext, input, handlerContext)
	case "write":
		commandResult, errorValue := toolCatalogBuilder.terminalService.WriteSessionInput(input.SessionID, input.Input)
		return terminalSessionToolResult(commandResult, errorValue), nil
	case "status":
		status, errorValue := toolCatalogBuilder.terminalService.StatusSession(input.SessionID)
		return statusToolResult(status, errorValue), nil
	case "close":
		errorValue := toolCatalogBuilder.terminalService.CloseSession(input.SessionID)
		if errorValue != nil {
			return agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "terminal_session", errorValue.Error()), nil
		}
		return agent.ToolSuccess(marshalToolResult(map[string]string{"sessionID": input.SessionID, "status": "closed"})), nil
	default:
		_ = toolContext
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "terminal_session", "terminal session action must be start, write, status, or close"), nil
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) startTerminalSession(toolContext context.Context, input terminalSessionToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	scope := toolCatalogBuilder.workspaceScopeForToolContext(toolContext, handlerContext.request)
	workingDirectory, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).ResolveDirectory(input.WorkingDirectoryPath, scope)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "terminal_session", errorValue.Error()), nil
	}
	workingDirectoryPath := workingDirectory.ConcretePath
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionWrite, workingDirectoryPath) {
		return agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "terminal_session", "current account cannot use this workspace path"), nil
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return *actorFailure, nil
	}
	if errorValue := workspaceActor.MkdirAll(toolContext, workspacepath.Directory(workingDirectory), 02770); errorValue != nil {
		return actorToolFailure("mkdir_all", "terminal_session", workingDirectory.VirtualPath, errorValue), nil
	}
	sessionID, errorValue := toolCatalogBuilder.terminalService.StartInteractiveSession(security.CommandRequest{
		Command:              toolCatalogBuilder.resolveAgentWorkspaceReferences(input.Command),
		WorkingDirectoryPath: workingDirectoryPath,
		EnvironmentVariables: toolCatalogBuilder.resolveAgentWorkspaceEnvironment(mergeWorkspaceEnvironment(input.EnvironmentVariables, scope.EnvironmentVariables())),
		TimeoutSecond:        input.TimeoutSecond,
		IsInteractive:        true,
		IsPTY:                true,
		ExecutionIdentity:    toolCatalogBuilder.executionIdentityForRequester(handlerContext.request),
	})
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "terminal_session", errorValue.Error()), nil
	}
	status, errorValue := toolCatalogBuilder.terminalService.StatusSession(sessionID)
	return statusToolResult(status, errorValue), nil
}

func terminalSessionToolResult(commandResult security.CommandResult, errorValue error) agent.ToolResult {
	content := marshalToolResult(commandResult)
	if errorValue != nil {
		return agent.ToolFailureWithOutput(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "terminal_session", content, json.RawMessage(content))
	}
	return agent.ToolSuccess(content)
}

func statusToolResult(status security.TerminalSessionStatus, errorValue error) agent.ToolResult {
	content := marshalToolResult(status)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "terminal_session", errorValue.Error())
	}
	return agent.ToolSuccess(content)
}

func (toolCatalogBuilder *ToolCatalogBuilder) openBrowserHandoffTool(toolContext context.Context, input browserHandoffOpenURLToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	if toolCatalogBuilder.capabilityClient.HTTPClient == nil {
		return agent.ToolFailureResult(agent.FailureDependencyUnavailable, agent.FailureCodes.Unavailable, "browser_handoff", "companion bridge capability client is unavailable"), nil
	}
	inputDocument, errorValue := json.Marshal(map[string]string{"url": input.URL})
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "browser_handoff", errorValue.Error()), nil
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
	result := agent.ToolResult{
		Output:      agent.ToolOutput{Content: content, Data: response.Result},
		Attachments: capabilityAttachments(response.Result),
	}
	if isError {
		result.Failure = &agent.ToolFailure{
			Kind:            capabilityFailureKind("", "browser_handoff"),
			Code:            agent.FailureCodes.OperationFailed.String(),
			Stage:           "browser_handoff",
			UserSafeSummary: content,
		}
	}
	return result, nil
}

func browserHandoffEventName(isError bool) string {
	if isError {
		return "browser_handoff.failed"
	}
	return "browser_handoff.opened"
}

func (toolCatalogBuilder *ToolCatalogBuilder) askConfirmTool(toolContext context.Context, input askConfirmToolInput) (agent.ToolResult, error) {
	taskRunID := agent.TaskRunIDFromContext(toolContext)
	if taskRunID == "" || toolCatalogBuilder.taskRunService == nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "ask_confirm", "ask.confirm requires an active task run"), nil
	}
	userFacingMessage := askConfirmUserFacingMessage(input)
	if userFacingMessage == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "ask_confirm", "ask.confirm requires userFacingMessage"), nil
	}
	reasonCode := normalizeApprovalReasonCode(input.ReasonCode)
	reasonDetail := askConfirmReasonDetail(input)
	_, errorValue := toolCatalogBuilder.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusWaitingApproval, approvalInternalReason(reasonCode, reasonDetail))
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "approval_request", errorValue.Error()), nil
	}
	toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "confirmation.requested", marshalToolResult(map[string]string{
		"userFacingMessage": userFacingMessage,
		"message":           userFacingMessage,
		"reasonCode":        reasonCode,
		"reasonDetail":      reasonDetail,
		"responseLanguage":  agent.ResponseLanguageFromContext(toolContext),
	}))
	toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "ask.requested", marshalToolResult(map[string]any{
		"kind":             "confirm",
		"message":          userFacingMessage,
		"reasonCode":       reasonCode,
		"reasonDetail":     reasonDetail,
		"responseLanguage": agent.ResponseLanguageFromContext(toolContext),
	}))
	return agent.ToolSuccess(marshalToolResult(map[string]string{"taskRunID": taskRunID, "status": string(task.TaskStatusWaitingApproval), "userFacingMessage": userFacingMessage, "message": userFacingMessage, "reasonCode": reasonCode})), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) askChoiceTool(toolContext context.Context, input askChoiceToolInput) (agent.ToolResult, error) {
	taskRunID := agent.TaskRunIDFromContext(toolContext)
	if taskRunID == "" || toolCatalogBuilder.taskRunService == nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "ask_choice", "ask.choice requires an active task run"), nil
	}
	choiceRequest, errorValue := normalizeAskChoiceRequest(input, agent.ResponseLanguageFromContext(toolContext))
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "ask_choice", errorValue.Error()), nil
	}
	_, errorValue = toolCatalogBuilder.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusWaitingUserInput, choiceRequest.Question)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "ask_choice", errorValue.Error()), nil
	}
	toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "ask.requested", marshalToolResult(choiceRequest))
	return agent.ToolSuccess(marshalToolResult(map[string]any{"taskRunID": taskRunID, "status": string(task.TaskStatusWaitingUserInput), "question": choiceRequest.Question, "kind": choiceRequest.Kind})), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) askInputTool(toolContext context.Context, input askInputToolInput) (agent.ToolResult, error) {
	taskRunID := agent.TaskRunIDFromContext(toolContext)
	if taskRunID == "" || toolCatalogBuilder.taskRunService == nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "ask_input", "ask.input requires an active task run"), nil
	}
	question := strings.TrimSpace(input.Question)
	if question == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "ask_input", "ask.input requires question"), nil
	}
	_, errorValue := toolCatalogBuilder.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusWaitingUserInput, question)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "ask_input", errorValue.Error()), nil
	}
	toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "ask.requested", marshalToolResult(map[string]string{
		"kind":             "input",
		"question":         question,
		"message":          question,
		"responseLanguage": agent.ResponseLanguageFromContext(toolContext),
	}))
	return agent.ToolSuccess(marshalToolResult(map[string]string{"taskRunID": taskRunID, "status": string(task.TaskStatusWaitingUserInput), "question": question, "kind": "input"})), nil
}

func askConfirmUserFacingMessage(input askConfirmToolInput) string {
	return firstNonEmptyString(input.UserFacingMessage, input.Message)
}

func askConfirmReasonDetail(input askConfirmToolInput) string {
	return firstNonEmptyString(input.ReasonDetail, input.Reason)
}

type normalizedAskChoiceRequest struct {
	Kind                 string            `json:"kind"`
	Question             string            `json:"question"`
	Options              []askChoiceOption `json:"options"`
	RecommendedOptionKey string            `json:"recommendedOptionKey"`
	SelectionMode        string            `json:"selectionMode"`
	ResponseLanguage     string            `json:"responseLanguage"`
}

func normalizeAskChoiceRequest(input askChoiceToolInput, responseLanguage string) (normalizedAskChoiceRequest, error) {
	question := strings.TrimSpace(input.Question)
	if question == "" {
		return normalizedAskChoiceRequest{}, errors.New("ask.choice requires question")
	}
	selectionMode := strings.TrimSpace(input.SelectionMode)
	if selectionMode == "" {
		selectionMode = "single"
	}
	if selectionMode != "single" && selectionMode != "multiple" {
		return normalizedAskChoiceRequest{}, errors.New("ask.choice selectionMode must be single or multiple")
	}
	options := normalizedAskChoiceOptions(input.Options)
	if len(options) < 2 {
		return normalizedAskChoiceRequest{}, errors.New("ask.choice requires at least two options")
	}
	recommendedOptionKey := strings.TrimSpace(input.RecommendedOptionKey)
	if !askChoiceOptionKeyExists(options, recommendedOptionKey) {
		return normalizedAskChoiceRequest{}, errors.New("ask.choice recommendedOptionKey must match an option key")
	}
	kind := "choice_single"
	if selectionMode == "multiple" {
		kind = "choice_multiple"
	}
	return normalizedAskChoiceRequest{
		Kind:                 kind,
		Question:             question,
		Options:              options,
		RecommendedOptionKey: recommendedOptionKey,
		SelectionMode:        selectionMode,
		ResponseLanguage:     responseLanguage,
	}, nil
}

func normalizedAskChoiceOptions(options []askChoiceOption) []askChoiceOption {
	normalizedOptions := []askChoiceOption{}
	for index, option := range options {
		label := strings.TrimSpace(option.Label)
		if label == "" {
			continue
		}
		key := strings.TrimSpace(option.Key)
		if key == "" {
			key = askChoiceKey(index)
		}
		value := strings.TrimSpace(option.Value)
		if value == "" {
			value = label
		}
		normalizedOptions = append(normalizedOptions, askChoiceOption{Key: key, Label: label, Value: value})
	}
	return normalizedOptions
}

func askChoiceKey(index int) string {
	if index >= 0 && index < 26 {
		return string(rune('A' + index))
	}
	return fmt.Sprintf("O%d", index+1)
}

func askChoiceOptionKeyExists(options []askChoiceOption, key string) bool {
	for _, option := range options {
		if option.Key == key {
			return true
		}
	}
	return false
}

func approvalInternalReason(reasonCode string, reasonDetail string) string {
	if strings.TrimSpace(reasonDetail) == "" {
		return reasonCode
	}
	return reasonCode + ": " + strings.TrimSpace(reasonDetail)
}

func normalizeApprovalReasonCode(reasonCode string) string {
	switch strings.TrimSpace(reasonCode) {
	case "external_send", "destructive_action", "credential_access", "paid_action", "permission_change", "capability_unlock", "other_sensitive_action":
		return strings.TrimSpace(reasonCode)
	default:
		return "other_sensitive_action"
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) writeFileTool(toolContext context.Context, input fileWriteToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	scope := toolCatalogBuilder.workspaceScopeForToolContext(toolContext, handlerContext.request)
	resolvedPath, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).Resolve(input.Path, scope)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_write", errorValue.Error()), nil
	}
	if isImmutableSkillPath(toolCatalogBuilder.workspaceRootPath, resolvedPath.ConcretePath) {
		return agent.ToolFailureResult(agent.FailurePolicyBlocked, agent.FailureCodes.PolicyBlocked, "file_write", "file.write cannot modify built-in skill files"), nil
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionWrite, resolvedPath.ConcretePath) {
		return agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "file_write", "current account cannot write this file"), nil
	}
	fileMode := os.FileMode(0660)
	if input.Mode != 0 {
		fileMode = os.FileMode(input.Mode)
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return *actorFailure, nil
	}
	if errorValue := workspaceActor.MkdirAll(toolContext, resolvedPath.Parent(), 02770); errorValue != nil {
		return actorToolFailure("mkdir_all", "file_write", resolvedPath.VirtualPath, errorValue), nil
	}
	if errorValue := workspaceActor.WriteFile(toolContext, resolvedPath, []byte(input.Content), fileMode); errorValue != nil {
		return actorToolFailure("write_file", "file_write", resolvedPath.VirtualPath, errorValue), nil
	}
	return agent.ToolSuccess(marshalToolResult(map[string]any{
		"path":      resolvedPath.VirtualPath,
		"sizeBytes": len(input.Content),
	})), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) attachFileTool(toolContext context.Context, input fileAttachToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	attachmentPaths := requestedAttachmentPaths(input.Path, input.Paths)
	if len(attachmentPaths) == 0 {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_attach", "path is required"), nil
	}
	attachments := []agent.FileAttachment{}
	scope := toolCatalogBuilder.workspaceScopeForToolContext(toolContext, handlerContext.request)
	for _, attachmentPath := range attachmentPaths {
		attachment, failureResult, errorValue := toolCatalogBuilder.fileAttachment(toolContext, attachmentPath, input, handlerContext, scope)
		if failureResult != nil {
			return *failureResult, nil
		}
		if errorValue != nil {
			return agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "file_attach", errorValue.Error()), nil
		}
		attachments = append(attachments, attachment)
	}
	_ = toolContext
	return agent.ToolResult{
		Output:      agent.ToolOutput{Content: "file attached"},
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

func (toolCatalogBuilder *ToolCatalogBuilder) promoteFileTool(toolContext context.Context, input filePromoteToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	sourcePaths := requestedAttachmentPaths(input.Path, input.Paths)
	if len(sourcePaths) == 0 {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_promote", "path is required"), nil
	}
	scope := toolCatalogBuilder.workspaceScopeForToolContext(toolContext, handlerContext.request)
	resolver := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath)
	destinationDirectory, errorValue := resolver.ResolveDirectory(input.DestinationDirectoryPath, scope)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_promote", errorValue.Error()), nil
	}
	if !destinationDirectory.IsDurableArtifact {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_promote", "destinationDirectoryPath must be artifacts/<slug>, /workspace/circles/<circleID>/..., or /workspace/shared/public/..."), nil
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionWrite, destinationDirectory.ConcretePath) {
		return agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "file_promote", "current account cannot write the promotion destination"), nil
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return *actorFailure, nil
	}
	if errorValue := workspaceActor.MkdirAll(toolContext, workspacepath.Directory(destinationDirectory), 02770); errorValue != nil {
		return actorToolFailure("mkdir_all", "file_promote", destinationDirectory.VirtualPath, errorValue), nil
	}
	promotedPaths := []string{}
	for _, sourcePath := range sourcePaths {
		source, errorValue := resolver.Resolve(sourcePath, scope)
		if errorValue != nil {
			return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_promote", errorValue.Error()), nil
		}
		if source.Kind != workspacePathKindDraft {
			return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_promote", "source paths must come from tmp/<slug> draft work"), nil
		}
		if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionRead, source.ConcretePath) {
			return agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "file_promote", "current account cannot read the promotion source"), nil
		}
		sourceInformation, errorValue := workspaceActor.Stat(toolContext, source)
		if errorValue != nil {
			return actorToolFailure("stat", "file_promote", source.VirtualPath, errorValue), nil
		}
		if !sourceInformation.IsRegular {
			return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_promote", "source path is not a regular file"), nil
		}
		destination := workspacepath.Directory(destinationDirectory).JoinVirtualFile(source.BaseName())
		if !input.Overwrite {
			if _, errorValue := workspaceActor.Stat(toolContext, destination); errorValue == nil {
				return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_promote", "destination already exists; set overwrite=true to replace it"), nil
			} else if !security.IsActorNotFoundError(errorValue) {
				return actorToolFailure("stat", "file_promote", destination.VirtualPath, errorValue), nil
			}
		}
		if errorValue := workspaceActor.CopyFile(toolContext, source, destination, 0660, input.Overwrite); errorValue != nil {
			return actorToolFailure("copy_file", "file_promote", destination.VirtualPath, errorValue), nil
		}
		promotedPaths = append(promotedPaths, destination.VirtualPath)
	}
	return agent.ToolSuccess(marshalToolResult(map[string]any{
		"paths": promotedPaths,
	})), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) fileAttachment(toolContext context.Context, path string, input fileAttachToolInput, handlerContext toolHandlerContext, scope WorkspaceScope) (agent.FileAttachment, *agent.ToolResult, error) {
	resolvedPath, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).Resolve(path, scope)
	if errorValue != nil {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_attach", errorValue.Error())
		return agent.FileAttachment{}, &result, nil
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionRead, resolvedPath.ConcretePath) {
		result := agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "file_attach", "current account cannot read this file")
		return agent.FileAttachment{}, &result, nil
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return agent.FileAttachment{}, actorFailure, nil
	}
	fileInformation, errorValue := workspaceActor.Stat(toolContext, resolvedPath)
	if errorValue != nil {
		result := actorToolFailure("stat", "file_attach", resolvedPath.VirtualPath, errorValue)
		return agent.FileAttachment{}, &result, nil
	}
	if !fileInformation.IsRegular {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_attach", "attachment path is not a regular file")
		return agent.FileAttachment{}, &result, nil
	}
	document, errorValue := workspaceActor.ReadFile(toolContext, resolvedPath, inlineAttachmentMaximumBytes)
	if errorValue != nil {
		result := actorToolFailure("read_file", "file_attach", resolvedPath.VirtualPath, errorValue)
		return agent.FileAttachment{}, &result, nil
	}
	filename := attachmentFilename(input, resolvedPath.ConcretePath)
	contentType := firstNonEmptyString(input.ContentType, mime.TypeByExtension(filepath.Ext(filename)), "application/octet-stream")
	return agent.FileAttachment{
		DevicePath:    toolCatalogBuilder.agentWorkspacePath(resolvedPath.ConcretePath),
		Filename:      filename,
		ContentType:   contentType,
		SizeBytes:     fileInformation.SizeBytes,
		Title:         strings.TrimSpace(input.Title),
		ContentBase64: base64.StdEncoding.EncodeToString(document),
	}, nil, nil
}

func mergeWorkspaceEnvironment(environmentVariables map[string]string, workspaceEnvironment map[string]string) map[string]string {
	result := map[string]string{}
	for name, value := range environmentVariables {
		result[name] = value
	}
	for name, value := range workspaceEnvironment {
		if strings.TrimSpace(result[name]) == "" {
			result[name] = value
		}
	}
	return result
}

func (toolCatalogBuilder *ToolCatalogBuilder) canAccessWorkspacePath(personAccess policy.PersonAccess, action string, path string) bool {
	resource := access.ResourceForWorkspacePath(toolCatalogBuilder.workspaceRootPath, path)
	return access.CanAccess(access.Request{
		PersonAccess: personAccess,
		Action:       action,
		Resource:     resource,
	})
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

func (toolCatalogBuilder *ToolCatalogBuilder) resolveTerminalWorkingDirectoryPath(value string, conversationScope ConversationResourceScope) string {
	trimmedPath := toolCatalogBuilder.resolveAgentWorkspacePath(value)
	defaultDirectoryPath := firstNonEmptyString(conversationScope.DefaultDirectoryPath, toolCatalogBuilder.workspaceRootPath)
	if trimmedPath == "" {
		return defaultDirectoryPath
	}
	if filepath.IsAbs(trimmedPath) {
		return trimmedPath
	}
	return filepath.Join(defaultDirectoryPath, trimmedPath)
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
	if shouldRequireCompanionBrowser(toolName, request, toolInput) {
		requestDocument["executionMode"] = "companion"
		requestDocument["requiresUserPresence"] = true
		requestDocument["privacyClass"] = "user_browser"
	}
	return requestDocument
}

func (toolCatalogBuilder *ToolCatalogBuilder) prepareCapabilityToolInput(toolContext context.Context, toolName string, request ToolCatalogRequest, toolInput json.RawMessage) (json.RawMessage, *agent.ToolResult, error) {
	if strings.TrimSpace(toolName) == "site.app.publish" {
		toolInput, toolFailure, errorValue := toolCatalogBuilder.enrichSitePublishInput(toolContext, request, toolInput)
		return toolInput, toolFailure, errorValue
	}
	toolInput, errorValue := toolCatalogBuilder.enrichCapabilityToolInput(toolName, request, toolInput)
	return toolInput, nil, errorValue
}

func (toolCatalogBuilder *ToolCatalogBuilder) enrichCapabilityToolInput(toolName string, request ToolCatalogRequest, toolInput json.RawMessage) (json.RawMessage, error) {
	if strings.TrimSpace(toolName) != "site.app.publish" {
		return toolInput, nil
	}
	toolInput, toolFailure, errorValue := toolCatalogBuilder.enrichSitePublishInput(context.Background(), request, toolInput)
	if toolFailure != nil {
		return nil, errors.New(toolFailure.ContentText())
	}
	return toolInput, errorValue
}

func (toolCatalogBuilder *ToolCatalogBuilder) enrichSitePublishInput(toolContext context.Context, request ToolCatalogRequest, toolInput json.RawMessage) (json.RawMessage, *agent.ToolResult, error) {
	inputDocument := map[string]any{}
	if len(toolInput) > 0 {
		if errorValue := json.Unmarshal(toolInput, &inputDocument); errorValue != nil {
			return nil, nil, errorValue
		}
	}
	sourceWorkspacePath := siteSourceWorkspacePath(inputDocument)
	if sourceWorkspacePath == "" {
		sourceWorkspacePath = defaultSiteSourceWorkspacePath(inputDocument)
	}
	if sourceWorkspacePath == "" {
		return toolInput, nil, nil
	}
	scope := WorkspaceScopeForRequest(toolCatalogBuilder.workspaceRootPath, request, agent.TaskRunIDFromContext(toolContext))
	resolvedSourcePath, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).Resolve(sourceWorkspacePath, scope)
	if errorValue != nil {
		return nil, nil, errorValue
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(request.PersonAccess, access.ActionRead, resolvedSourcePath.ConcretePath) {
		return nil, nil, errors.New("current account cannot publish this site workspace path")
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, request)
	if actorFailure != nil {
		return nil, actorFailure, nil
	}
	sourceBundle, errorValue := workspaceActor.BundleDirectory(toolContext, workspacepath.Directory(resolvedSourcePath), security.WorkspaceActorBundleOptions{
		Format:       "tar.gz",
		MaxBytes:     siteSourceBundleMaximumBytes,
		ExcludeNames: siteSourceBundleExcludeNames(),
	})
	if errorValue != nil {
		toolFailure := actorToolFailure("bundle_directory", "site_publish", resolvedSourcePath.VirtualPath, errorValue)
		return nil, &toolFailure, nil
	}
	inputDocument["sourceWorkspacePath"] = resolvedSourcePath.VirtualPath
	inputDocument["sourceBundleBase64"] = sourceBundle.ContentBase64
	inputDocument["sourceBundleFormat"] = sourceBundle.Format
	document, errorValue := json.Marshal(inputDocument)
	return document, nil, errorValue
}

func (toolCatalogBuilder *ToolCatalogBuilder) handleCapabilityToolSuccess(toolContext context.Context, toolName string, request ToolCatalogRequest, result *json.RawMessage) (*agent.ToolResult, error) {
	if strings.TrimSpace(toolName) != "site.app.create" {
		return nil, nil
	}
	return toolCatalogBuilder.materializeSiteCreateResult(toolContext, request, result)
}

func (toolCatalogBuilder *ToolCatalogBuilder) materializeSiteCreateResult(toolContext context.Context, request ToolCatalogRequest, result *json.RawMessage) (*agent.ToolResult, error) {
	site, errorValue := decodeSiteCreateResult(*result)
	if errorValue != nil {
		return nil, errorValue
	}
	if strings.TrimSpace(site.SourceWorkspacePath) == "" {
		site.SourceWorkspacePath = defaultSiteSourceWorkspacePath(map[string]any{"siteID": site.SiteID})
	}
	scope := WorkspaceScopeForRequest(toolCatalogBuilder.workspaceRootPath, request, agent.TaskRunIDFromContext(toolContext))
	sourceWorkspace, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).Resolve(site.SourceWorkspacePath, scope)
	if errorValue != nil {
		return nil, errorValue
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(request.PersonAccess, access.ActionWrite, sourceWorkspace.ConcretePath) {
		return toolResultPointer(agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "site_source_workspace", "current account cannot write this site workspace path")), nil
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, request)
	if actorFailure != nil {
		return actorFailure, nil
	}
	if toolFailure := writeSiteStarterFiles(toolContext, workspaceActor, workspacepath.Directory(sourceWorkspace), site); toolFailure != nil {
		return toolFailure, nil
	}
	site.SourceWorkspacePath = sourceWorkspace.VirtualPath
	site.WorkspacePath = sourceWorkspace.VirtualPath
	document, errorValue := json.Marshal(site)
	if errorValue != nil {
		return nil, errorValue
	}
	*result = document
	return nil, nil
}

type siteCreateResult struct {
	SiteID              string `json:"siteID"`
	Slug                string `json:"slug"`
	Title               string `json:"title"`
	PublishedURL        string `json:"publishedURL"`
	WorkspacePath       string `json:"workspacePath"`
	SourceWorkspacePath string `json:"sourceWorkspacePath"`
	Status              string `json:"status"`
}

func decodeSiteCreateResult(document json.RawMessage) (siteCreateResult, error) {
	if len(bytes.TrimSpace(document)) == 0 {
		return siteCreateResult{}, errors.New("site.app.create returned no result")
	}
	var site siteCreateResult
	if errorValue := json.Unmarshal(document, &site); errorValue != nil {
		return siteCreateResult{}, errorValue
	}
	if strings.TrimSpace(site.SiteID) == "" {
		return siteCreateResult{}, errors.New("site.app.create result is missing siteID")
	}
	return site, nil
}

func writeSiteStarterFiles(ctx context.Context, workspaceActor security.WorkspaceActor, sourceWorkspace workspacepath.Directory, site siteCreateResult) *agent.ToolResult {
	if errorValue := workspaceActor.MkdirAll(ctx, sourceWorkspace, 02770); errorValue != nil {
		toolFailure := actorToolFailure("mkdir_all", "site_create", sourceWorkspace.VirtualPath, errorValue)
		return &toolFailure
	}
	for _, siteFile := range siteStarterFiles(site) {
		path := workspacePathForSiteStarterFile(sourceWorkspace, siteFile.Path)
		if errorValue := workspaceActor.MkdirAll(ctx, path.Parent(), 02770); errorValue != nil {
			toolFailure := actorToolFailure("mkdir_all", "site_create", path.VirtualPath, errorValue)
			return &toolFailure
		}
		if errorValue := workspaceActor.WriteFile(ctx, path, []byte(siteFile.Content), 0660); errorValue != nil {
			toolFailure := actorToolFailure("write_file", "site_create", path.VirtualPath, errorValue)
			return &toolFailure
		}
	}
	return nil
}

type siteStarterFile struct {
	Path    string
	Content string
}

func workspacePathForSiteStarterFile(sourceWorkspace workspacepath.Directory, relativePath string) workspacepath.Path {
	cleanPath := filepath.Clean(relativePath)
	return workspacepath.Path{
		ConcretePath:      filepath.Join(sourceWorkspace.ConcretePath, cleanPath),
		VirtualPath:       filepath.ToSlash(filepath.Join(sourceWorkspace.VirtualPath, cleanPath)),
		Kind:              sourceWorkspace.Kind,
		IsDurableArtifact: sourceWorkspace.IsDurableArtifact,
	}
}

func siteStarterFiles(site siteCreateResult) []siteStarterFile {
	return []siteStarterFile{
		{Path: ".internkim/site.json", Content: siteWorkspaceMetadata(site)},
		{Path: "DESIGN.md", Content: siteDesignDocument(site)},
		{Path: "app/package.json", Content: sitePackageDocument(site)},
		{Path: "app/index.html", Content: siteIndexDocument(site)},
		{Path: "app/src/main.tsx", Content: siteMainTSXDocument()},
		{Path: "app/src/App.tsx", Content: siteAppTSXDocument(site)},
		{Path: "app/src/styles.css", Content: siteStylesDocument()},
		{Path: "pocketbase/pb_migrations/.gitkeep", Content: ""},
		{Path: "pocketbase/pb_hooks/.gitkeep", Content: ""},
	}
}

func siteWorkspaceMetadata(site siteCreateResult) string {
	document, errorValue := json.MarshalIndent(map[string]any{
		"siteID":         site.SiteID,
		"slug":           site.Slug,
		"title":          site.Title,
		"publishedURL":   site.PublishedURL,
		"purpose":        "prototype for idea validation",
		"stack":          "React + Vite + PocketBase",
		"designDefault":  "starter scaffold only; customize through DESIGN.md before publish",
		"sourceContract": "editable source is owned by the requester actor",
	}, "", "  ")
	if errorValue != nil {
		return "{}\n"
	}
	return string(document) + "\n"
}

func sitePackageDocument(site siteCreateResult) string {
	document, errorValue := json.MarshalIndent(map[string]any{
		"scripts": map[string]string{
			"build":   "vite build",
			"dev":     "vite --host 0.0.0.0",
			"preview": "vite preview --host 0.0.0.0",
		},
		"dependencies": map[string]string{
			"@vitejs/plugin-react": "latest",
			"lucide-react":         "latest",
			"pocketbase":           "latest",
			"react":                "latest",
			"react-dom":            "latest",
			"typescript":           "latest",
			"vite":                 "latest",
		},
		"devDependencies": map[string]string{},
		"name":            sanitizeWorkspaceSlug(site.Slug),
		"private":         true,
		"type":            "module",
		"version":         "0.0.0",
	}, "", "  ")
	if errorValue != nil {
		return "{}\n"
	}
	return string(document) + "\n"
}

func siteDesignDocument(site siteCreateResult) string {
	title := html.EscapeString(firstNonEmptyString(site.Title, site.Slug))
	return "# " + title + " DESIGN.md\n\n## Product\n\nEditable scaffold for a website prototype. Replace this file with a request-specific design system before publishing user-facing work.\n\n## Audience\n\nDefine the primary user and what they are trying to accomplish.\n\n## Prototype Scope\n\nDescribe what works in the first publish and what is intentionally deferred.\n\n## Visual Direction\n\nChoose typography, color, spacing, layout density, interaction feel, and responsive behavior for this specific request.\n\n## Screens\n\nList the screens and states included in the prototype.\n\n## Workflows\n\nDescribe the main interaction paths the user can try.\n\n## Data Model\n\nDefine local state, fake data, PocketBase collections, files, or realtime behavior.\n\n## Implemented Now\n\nReplace this scaffold with the implemented feature set before publishing.\n\n## Next Iterations\n\nRecord follow-up work for longer projects.\n\n## Acceptance Criteria\n\nList the checks that must pass before publish.\n"
}

func siteIndexDocument(site siteCreateResult) string {
	title := html.EscapeString(firstNonEmptyString(site.Title, site.Slug))
	return "<!doctype html>\n<html lang=\"ko\">\n<head>\n<meta charset=\"UTF-8\" />\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\" />\n<title>" + title + "</title>\n</head>\n<body>\n<div id=\"root\"></div>\n<script type=\"module\" src=\"/src/main.tsx\"></script>\n</body>\n</html>\n"
}

func siteMainTSXDocument() string {
	return "import React from 'react';\nimport { createRoot } from 'react-dom/client';\nimport App from './App';\nimport './styles.css';\n\nconst rootElement = document.getElementById('root');\n\nif (rootElement) {\n  createRoot(rootElement).render(\n    <React.StrictMode>\n      <App />\n    </React.StrictMode>,\n  );\n}\n"
}

func siteAppTSXDocument(site siteCreateResult) string {
	title := html.EscapeString(firstNonEmptyString(site.Title, site.Slug))
	return "import PocketBase from 'pocketbase';\n\nconst pocketBase = new PocketBase(window.location.origin);\n\nexport default function App() {\n  return (\n    <main className=\"scaffold-shell\">\n      <section className=\"scaffold-panel\">\n        <p className=\"scaffold-label\">Editable scaffold</p>\n        <h1>" + title + "</h1>\n        <p className=\"scaffold-copy\">\n          이 사이트는 아직 사용자 요청에 맞게 제작되기 전의 기본 작업 공간입니다. DESIGN.md를 작성하고 React 소스를 구현한 뒤 빌드해서 배포하세요.\n        </p>\n        <span className=\"scaffold-origin\">{pocketBase.baseURL}</span>\n      </section>\n    </main>\n  );\n}\n"
}

func siteStylesDocument() string {
	return ":root {\n  color: #111827;\n  background: #f8fafc;\n  font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, \"Apple SD Gothic Neo\", \"Segoe UI\", sans-serif;\n}\n\n* {\n  box-sizing: border-box;\n}\n\nbody {\n  margin: 0;\n  min-width: 320px;\n  min-height: 100vh;\n  background: #f8fafc;\n}\n\n.scaffold-shell {\n  display: grid;\n  min-height: 100vh;\n  place-items: center;\n  padding: 24px;\n}\n\n.scaffold-panel {\n  width: min(640px, 100%);\n  border: 1px solid #d1d5db;\n  border-radius: 8px;\n  background: #ffffff;\n  padding: 28px;\n}\n\n.scaffold-label {\n  margin: 0 0 12px;\n  color: #6b7280;\n  font-size: 13px;\n  font-weight: 700;\n}\n\nh1 {\n  margin: 0;\n  font-size: 32px;\n  line-height: 1.15;\n  letter-spacing: 0;\n}\n\n.scaffold-copy {\n  margin: 16px 0 0;\n  color: #4b5563;\n  font-size: 15px;\n  line-height: 1.65;\n}\n\n.scaffold-origin {\n  display: inline-block;\n  margin-top: 18px;\n  color: #6b7280;\n  font-size: 13px;\n}\n\n@media (max-width: 680px) {\n  .scaffold-panel {\n    padding: 22px;\n  }\n\n  h1 {\n    font-size: 28px;\n  }\n}\n"
}

func toolResultPointer(result agent.ToolResult) *agent.ToolResult {
	return &result
}

func (toolCatalogBuilder *ToolCatalogBuilder) validateCapabilityToolInputAccess(toolName string, request ToolCatalogRequest, toolInput json.RawMessage) error {
	if strings.TrimSpace(toolName) != "file.read" {
		return nil
	}
	inputDocument := map[string]any{}
	if len(toolInput) > 0 {
		if errorValue := json.Unmarshal(toolInput, &inputDocument); errorValue != nil {
			return errorValue
		}
	}
	path, _ := inputDocument["path"].(string)
	resolvedPath, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).Resolve(path, WorkspaceScopeForRequest(toolCatalogBuilder.workspaceRootPath, request, ""))
	if errorValue != nil {
		return errorValue
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(request.PersonAccess, access.ActionRead, resolvedPath.ConcretePath) {
		return errors.New("current account cannot read this file")
	}
	return nil
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

func siteSourceBundleExcludeNames() []string {
	return []string{".git", "node_modules", ".vite", ".turbo", ".cache"}
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
