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
	"time"
	"unicode/utf8"

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
const defaultFileReadMaximumBytes = 128 * 1024
const maximumFileReadBytes = 1024 * 1024
const maximumEditableTextFileBytes = 2 * 1024 * 1024
const maximumFilePreviewBytes = 200 * 1024

type HistoryProvider interface {
	FetchHistory(context.Context, string, int) (agent.VisibleContext, error)
}

type AttachmentMaterialResolver interface {
	ResolveAttachmentMaterial(context.Context, string) (agent.VisibleContextMaterial, error)
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
	ProfileName                string
	Prompt                     string
	VisibleContext             agent.VisibleContext
	RequesterPersonID          string
	RequesterName              string
	RequesterEmail             string
	RequesterPlatformUserID    string
	TaskSource                 TaskLaunchSource
	IsScheduledRun             bool
	IsApprovalContinuation     bool
	ConversationID             string
	ConversationType           string
	ConversationChannelID      string
	ConversationChannelName    string
	ActiveCircleID             string
	ActiveCircleConflict       bool
	ReplyTargetID              string
	Platform                   string
	HistoryCursor              string
	HistoryProvider            HistoryProvider
	AttachmentMaterialResolver AttachmentMaterialResolver
	PersonAccess               policy.PersonAccess
	MemoryNamespaces           []memory.MemoryNamespace
	AccessibleConversationIDs  []string
	InputParts                 []agent.AgentPart
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

type siteAppBuildToolInput struct {
	SiteID              string `json:"siteID"`
	Slug                string `json:"slug"`
	SourceWorkspacePath string `json:"sourceWorkspacePath"`
	AppWorkspacePath    string `json:"appWorkspacePath"`
	TimeoutSecond       int    `json:"timeoutSecond"`
}

type siteAppRepairToolInput struct {
	SiteID              string `json:"siteID"`
	Slug                string `json:"slug"`
	SourceWorkspacePath string `json:"sourceWorkspacePath"`
}

type siteProjectResolutionInput struct {
	SiteID              string
	SourceWorkspacePath string
}

type askConfirmToolInput struct {
	UserFacingMessage string `json:"userFacingMessage"`
	ReasonCode        string `json:"reasonCode"`
	ReasonDetail      string `json:"reasonDetail"`
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

type fileReadToolInput struct {
	Path           string `json:"path"`
	MaterialID     string `json:"materialID"`
	MaxOutputBytes int    `json:"maxOutputBytes"`
	StartLine      int    `json:"startLine"`
	LineCount      int    `json:"lineCount"`
}

type filePreviewToolInput struct {
	Path       string `json:"path"`
	MaterialID string `json:"materialID"`
}

type fileWriteToolInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    uint32 `json:"mode"`
}

type fileEditToolInput struct {
	Path    string `json:"path"`
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

type filePatchToolInput struct {
	Edits []filePatchEditInput `json:"edits"`
}

type filePatchEditInput struct {
	Path    string `json:"path"`
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

type fileAttachToolInput struct {
	Path        string                `json:"path"`
	Filename    string                `json:"filename"`
	ContentType string                `json:"contentType"`
	Title       string                `json:"title"`
	Files       []fileAttachFileInput `json:"files"`
}

type fileAttachFileInput struct {
	Path        string `json:"path"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Title       string `json:"title"`
}

type filePromoteToolInput struct {
	Path                     string `json:"path"`
	DestinationDirectoryPath string `json:"destinationDirectoryPath"`
	Overwrite                bool   `json:"overwrite"`
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
	return agent.DefaultAllowedToolNames([]string{"conversation.history", "memory.search", "memory.remember", "math.calculate", "web.search", "web.fetch", "terminal.run", "terminal.session", "browser_handoff.openURL", "ask.confirm", "ask.choice", "ask.input", "file.preview", "file.read", "document.read", "image.read", "file.write", "file.edit", "file.patch", "file.promote", "file.attach", "skill.add", "skill.remove", "skill.search", "tool.describe", "schedule.create", "schedule.cancel"})
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
			InputSchema: json.RawMessage(`{"type":"object","properties":{"expression":{"type":"string"}},"required":["expression"]}`),
		},
		Handler: toolCatalogBuilder.calculateMathTool,
		Result:  agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[security.CommandRequest, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "terminal.run",
			Description: "Run a guarded non-interactive command inside the Blueclaw workspace. Use file.write, file.edit, or file.patch for source file creation and edits instead of shell heredocs or redirection.",
			RecoveryCard: agent.ToolRecoveryCard{
				Does:       "Runs one non-interactive workspace command for inspection, dependency install, build, render, or tests.",
				Produces:   "Command stdout, stderr, exit status, and runtime diagnostics.",
				SideEffect: "workspace_write",
				UseWhen:    "You need to execute a toolchain command, build, render, test, list files, or inspect environment state.",
				AvoidWhen:  "You are creating or editing source files with heredoc, tee, or shell redirection; use file.write, file.edit, or file.patch instead.",
			},
			InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"workingDirectoryPath":{"type":"string"},"timeoutSecond":{"type":"number"}},"required":["command"]}`),
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
			InputSchema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["start","write","status","close"]},"sessionID":{"type":"string"},"command":{"type":"string"},"input":{"type":"string"},"workingDirectoryPath":{"type":"string"},"timeoutSecond":{"type":"number"}},"required":["action"]}`),
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
			InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`),
		},
		Handler: func(toolContext context.Context, input browserHandoffOpenURLToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.openBrowserHandoffTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[siteAppBuildToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "site.app.build",
			Description: "Build an editable InternKim site project from its canonical appWorkspacePath and return build evidence.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"siteID":{"type":"string"},"slug":{"type":"string"},"sourceWorkspacePath":{"type":"string"},"appWorkspacePath":{"type":"string"},"timeoutSecond":{"type":"number"}}}`),
		},
		Handler: func(toolContext context.Context, input siteAppBuildToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.buildSiteAppTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[siteAppRepairToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "site.app.repair",
			Description: "Repair a missing editable InternKim site workspace by recreating the canonical source/app scaffold without changing the published snapshot.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"siteID":{"type":"string"},"slug":{"type":"string"},"sourceWorkspacePath":{"type":"string"}}}`),
		},
		Handler: func(toolContext context.Context, input siteAppRepairToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.repairSiteAppTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[askConfirmToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "ask.confirm",
			Description: "Pause the current task while waiting for explicit user confirmation. Use only before destructive, high-risk, external-send, credential, paid-service, or capability-unlock actions. userFacingMessage is shown directly to the user and must use the same language as the original user request. reasonCode and reasonDetail are internal only.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"userFacingMessage":{"type":"string","description":"User-facing approval question shown directly to the user, written in the same language as the original user request."},"reasonCode":{"type":"string","enum":["external_send","destructive_action","credential_access","paid_action","permission_change","capability_unlock","other_sensitive_action"]},"reasonDetail":{"type":"string","description":"Optional internal diagnostic detail. Never write user-facing prose here."}},"required":["userFacingMessage","reasonCode"]}`),
		},
		Handler: toolCatalogBuilder.askConfirmTool,
		Result:  agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[askChoiceToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "ask.choice",
			Description: "Pause the current task and ask the user to choose from explicit options. Always include exactly one recommendedOptionKey. Use selectionMode single or multiple.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"},"options":{"type":"array","items":{"type":"object","properties":{"key":{"type":"string"},"label":{"type":"string"},"value":{"type":"string"}},"required":["label"]}},"recommendedOptionKey":{"type":"string"},"selectionMode":{"type":"string","enum":["single","multiple"]}},"required":["question","options","recommendedOptionKey"]}`),
		},
		Handler: toolCatalogBuilder.askChoiceTool,
		Result:  agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[askInputToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "ask.input",
			Description: "Pause the current task and ask the user for free-form input needed to continue.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"}},"required":["question"]}`),
		},
		Handler: toolCatalogBuilder.askInputTool,
		Result:  agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[fileWriteToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "file.write",
			Description: "Overwrite one UTF-8 text file under the Blueclaw workspace. Treat content as the complete file body, like terminal redirection to a file: include the text exactly as it should appear in the file, with real line breaks for multiline source.",
			RecoveryCard: agent.ToolRecoveryCard{
				Does:       "Overwrites one workspace text file with the exact content string.",
				Produces:   "A written source, document, script, or config file at the requested path.",
				SideEffect: "workspace_write",
				UseWhen:    "A source file, design document, script, or generated text artifact must be created or replaced.",
				AvoidWhen:  "You only need to inspect files, append shell output, or run commands; do not pass escaped newline sequences when writing multiline source.",
			},
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace path to create or overwrite."},"content":{"type":"string","description":"Complete file body as plain UTF-8 text. Use real line breaks for multiline files; this is the text that will be written exactly."},"mode":{"type":"number","description":"Optional POSIX file mode."}},"required":["path","content"]}`),
		},
		Handler: func(toolContext context.Context, input fileWriteToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.writeFileTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[fileReadToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "file.read",
			Description: "Read exact UTF-8 workspace text or a real file line range with honest size and truncation metadata. Use file.preview first for attached HTML, PDF, DOCX, PPTX, XLSX, or other documents.",
			RecoveryCard: agent.ToolRecoveryCard{
				Does:       "Reads a text file or requested line range from the actual workspace file; attachment materialID falls back to cached preview text.",
				Produces:   "Text content plus path, line range, original size, returned size, line count if known, and truncation metadata.",
				SideEffect: "read",
				UseWhen:    "You need current file content before file.edit, file.patch, or file.write.",
				AvoidWhen:  "The file is binary, an attached document needing conversion, or you already have the exact current text needed for an edit.",
			},
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace text file path to read."},"materialID":{"type":"string","description":"Attachment materialID from Current attachments or Previous attachments. Use file.preview first; file.read returns cached preview text if no exact workspace file is available."},"startLine":{"type":"integer","description":"Optional 1-based first line to return."},"lineCount":{"type":"integer","description":"Optional number of lines to return from startLine."}}}`),
		},
		Handler: func(toolContext context.Context, input fileReadToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.readFileTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[filePreviewToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "file.preview",
			Description: "Preview an attached or workspace file path from the conversation attachment catalog using cached AgentPart markdownPreview when available, or the existing document.read MarkItDown provider for convertible documents.",
			RecoveryCard: agent.ToolRecoveryCard{
				Does:       "Returns a document preview or file metadata without inventing content.",
				Produces:   "Path, filename, content type, size, markdown preview, conversion status, and conversion message.",
				SideEffect: "read",
				UseWhen:    "The attachment catalog lists a materialID or path for an HTML, PDF, DOCX, PPTX, XLSX, text, or data file and you need to understand it.",
				AvoidWhen:  "You need exact source lines for an edit; use file.read after previewing.",
			},
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace file path to preview. Use this when the attachment catalog has a readable path."},"materialID":{"type":"string","description":"Attachment materialID from Current attachments or Previous attachments. Use this when the catalog lists a materialID, especially if no readable path is available."}}}`),
		},
		Handler: func(toolContext context.Context, input filePreviewToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.previewFileTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[fileEditToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "file.edit",
			Description: "Apply one exact text replacement in a UTF-8 workspace file. The oldText must appear exactly once.",
			RecoveryCard: agent.ToolRecoveryCard{
				Does:       "Replaces one exact oldText occurrence with newText in one workspace text file.",
				Produces:   "A modified source, document, script, or config file with match metadata.",
				SideEffect: "workspace_write",
				UseWhen:    "A small targeted source or document change is needed and the current oldText is known.",
				AvoidWhen:  "The change creates a new file, rewrites most of a file, or oldText is missing or ambiguous; use file.read first.",
			},
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace text file path to modify."},"oldText":{"type":"string","description":"Exact existing text to replace; must appear exactly once."},"newText":{"type":"string","description":"Replacement text to write in place of oldText."}},"required":["path","oldText","newText"]}`),
		},
		Handler: func(toolContext context.Context, input fileEditToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.editFileTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[filePatchToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "file.patch",
			Description: "Apply multiple exact text replacements as one all-or-nothing workspace patch. Each oldText must appear exactly once at the point it is applied.",
			RecoveryCard: agent.ToolRecoveryCard{
				Does:       "Applies structured exact replacements across one or more workspace text files.",
				Produces:   "Modified source, document, script, or config files only after every edit validates.",
				SideEffect: "workspace_write",
				UseWhen:    "Several targeted edits should be applied together after reading current files.",
				AvoidWhen:  "You need unified diff syntax, broad file rewrites, or the current oldText snippets are not known.",
			},
			InputSchema: json.RawMessage(`{"type":"object","properties":{"edits":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string","description":"Workspace text file path to modify."},"oldText":{"type":"string","description":"Exact existing text to replace; must appear exactly once when this edit is applied."},"newText":{"type":"string","description":"Replacement text."}},"required":["path","oldText","newText"]}}},"required":["edits"]}`),
		},
		Handler: func(toolContext context.Context, input filePatchToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.patchFileTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[fileAttachToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "file.attach",
			Description: "Attach one or more existing workspace files to the final reply evidence.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"files":{"type":"array","description":"One or more finished workspace files to attach in this single call.","items":{"type":"object","properties":{"path":{"type":"string","description":"Workspace path to an existing file."},"filename":{"type":"string","description":"Optional display filename."},"contentType":{"type":"string","description":"Optional MIME type."},"title":{"type":"string","description":"Optional attachment title."}},"required":["path"]}}},"required":["files"]}`),
		},
		Handler: func(toolContext context.Context, input fileAttachToolInput) (agent.ToolResult, error) {
			return toolCatalogBuilder.attachFileTool(toolContext, input, handlerContext)
		},
		Result: agent.IdentityToolResult,
	})
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[filePromoteToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "file.promote",
			Description: "Copy one finished draft file from tmp/<slug>/build into artifacts/<slug>/ or an allowed durable circle/shared directory before attaching. Use once per output file; do not pass a directory path.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"destinationDirectoryPath":{"type":"string"},"overwrite":{"type":"boolean"}},"required":["path","destinationDirectoryPath"]}`),
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
			InputSchema: json.RawMessage(`{"type":"object","properties":{"queries":{"type":"array","items":{"type":"object","properties":{"description":{"type":"string"}},"required":["description"]}},"limit":{"type":"number"}},"required":["queries"]}`),
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
			Description: "Search or inspect available Blueclaw tools by exact name, prefix, or text query before selecting or calling them.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"toolName":{"type":"string"},"prefix":{"type":"string"},"limit":{"type":"number"}}}`),
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
		matchReason := toolDescriptionMatchReason(input, toolDefinition)
		if matchReason == "" {
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
			"matchReason":  matchReason,
		})
		if len(items) >= limit {
			break
		}
	}
	return map[string]any{"tools": items}
}

func toolDescriptionMatches(input toolDescribeToolInput, toolDefinition agent.ToolDefinition) bool {
	return toolDescriptionMatchReason(input, toolDefinition) != ""
}

func toolDescriptionMatchReason(input toolDescribeToolInput, toolDefinition agent.ToolDefinition) string {
	toolName := strings.TrimSpace(toolDefinition.Name)
	if expectedToolName := strings.TrimSpace(input.ToolName); expectedToolName != "" {
		if toolName == expectedToolName {
			return "exact_name"
		}
		return ""
	}
	if prefix := strings.TrimSpace(input.Prefix); prefix != "" && !strings.HasPrefix(toolName, prefix) {
		return ""
	} else if prefix != "" {
		return "prefix"
	}
	query := strings.ToLower(strings.TrimSpace(input.Query))
	if query == "" {
		return "all"
	}
	searchText := toolDescriptionSearchText(toolDefinition)
	if strings.Contains(searchText, query) {
		return "query"
	}
	if toolDescriptionContainsQueryTokens(searchText, query) {
		return "query_tokens"
	}
	return ""
}

func toolDescriptionSearchText(toolDefinition agent.ToolDefinition) string {
	values := []string{
		toolDefinition.Name,
		toolDefinition.Description,
		agentSpecificToolDescription(toolDefinition.Name),
		toolDefinition.RecoveryCard.Does,
		toolDefinition.RecoveryCard.Produces,
		toolDefinition.RecoveryCard.SideEffect,
		toolDefinition.RecoveryCard.UseWhen,
		toolDefinition.RecoveryCard.AvoidWhen,
	}
	return strings.ToLower(strings.Join(values, " "))
}

func toolDescriptionContainsQueryTokens(searchText string, query string) bool {
	tokens := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(tokens) == 0 {
		return true
	}
	for _, token := range tokens {
		if !strings.Contains(searchText, token) {
			return false
		}
	}
	return true
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
		if toolName == "file.read" {
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

func defaultCapabilityToolDescription(toolName string) string {
	switch strings.TrimSpace(toolName) {
	case "document.read":
		return "Read a workspace document path as Markdown using the shared document conversion pipeline. Prefer file.preview for paths listed in the conversation attachment catalog."
	case "image.read":
		return "Load an image path from the conversation attachment catalog or workspace into the model as an image input. Use only when visual inspection is needed; do not call for PDFs or text documents."
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
	if toolFailure := toolCatalogBuilder.materializeTerminalRuntimeDirectories(toolContext, workspaceActor, scope, input.EnvironmentVariables); toolFailure != nil {
		return *toolFailure, nil
	}
	if toolFailure := terminalSourceWriteMisuseFailure(input.Command); toolFailure != nil {
		return *toolFailure, nil
	}
	if toolFailure := preflightNodePackageBuild(toolContext, workspaceActor, workspacepath.Directory(workingDirectory), input.Command); toolFailure != nil {
		return *toolFailure, nil
	}
	input.ExecutionIdentity = toolCatalogBuilder.executionIdentityForRequester(handlerContext.request)
	commandResult, errorValue := workspaceActor.Run(toolContext, input)
	content := marshalToolResult(commandResult)
	if errorValue != nil {
		if security.IsCommandPathGuardrailError(errorValue) {
			return terminalPathGuardrailFailure(commandResult, content), nil
		}
		if runtimePathFailure := terminalRuntimePathFailure(input, commandResult, content); runtimePathFailure != nil {
			return *runtimePathFailure, nil
		}
		return agent.ToolFailureWithOutput(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "terminal_run", content, json.RawMessage(content)), nil
	}
	_ = toolContext
	return agent.ToolSuccess(content), nil
}

func terminalWorkspaceAccessFailure(workingDirectoryPath string) agent.ToolResult {
	message := "current account cannot use this workspace path: terminal workingDirectoryPath " + strings.TrimSpace(workingDirectoryPath) + "; recovery: use tmp/<slug> relative to the default writable directory for draft work, then promote accepted files to artifacts/<slug> or an allowed circle/shared path"
	document := json.RawMessage(marshalToolResult(map[string]any{
		"failureClass":      "workspace_permission",
		"path":              strings.TrimSpace(workingDirectoryPath),
		"requiredAccess":    "write",
		"suggestedNextTool": "site.app.status",
		"message":           message,
	}))
	result := agent.ToolFailureWithOutput(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "workspace_permission", message, document)
	result.Failure.Retryable = true
	result.Failure.SafeRetry = true
	return result
}

func (toolCatalogBuilder *ToolCatalogBuilder) buildSiteAppTool(toolContext context.Context, input siteAppBuildToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	site := siteCreateResult{}
	if shouldResolveSiteStatusForWorkspaceTool(input.SiteID, input.Slug, input.SourceWorkspacePath, input.AppWorkspacePath) {
		resolvedSite, errorValue := toolCatalogBuilder.siteStatusForWorkspaceTool(toolContext, handlerContext.request, input.SiteID, input.Slug)
		if errorValue != nil {
			return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "site_build_status", errorValue.Error()), nil
		}
		site = resolvedSite
	}
	sourceWorkspacePath := firstNonEmptyString(input.SourceWorkspacePath, site.SourceWorkspacePath)
	appWorkspacePath := firstNonEmptyString(input.AppWorkspacePath, site.AppWorkspacePath)
	if sourceWorkspacePath == "" {
		sourceWorkspacePath = sourceWorkspacePathFromSiteAppWorkspacePath(appWorkspacePath)
	}
	if strings.TrimSpace(sourceWorkspacePath) == "" && strings.TrimSpace(appWorkspacePath) == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "site_build_workspace", "appWorkspacePath could not be resolved; call site.app.status first"), nil
	}
	if strings.HasSuffix(strings.TrimSuffix(filepath.ToSlash(appWorkspacePath), "/"), "/src") {
		canonicalPath := filepath.ToSlash(filepath.Dir(filepath.ToSlash(appWorkspacePath)))
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "site_build_workspace", "site builds must run from appWorkspacePath "+canonicalPath+", not app/src"), nil
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return *actorFailure, nil
	}
	sourceWorkspace, errorValue := toolCatalogBuilder.resolveSiteProjectSourceWorkspace(toolContext, handlerContext.request, workspaceActor, siteProjectResolutionInput{
		SiteID:              firstNonEmptyString(input.SiteID, site.SiteID, siteProjectIDFromPath(sourceWorkspacePath), siteProjectIDFromPath(appWorkspacePath)),
		SourceWorkspacePath: sourceWorkspacePath,
	})
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "site_build_workspace", errorValue.Error()), nil
	}
	appWorkspace := workspacepath.Path{
		ConcretePath: filepath.Join(sourceWorkspace.ConcretePath, "app"),
		VirtualPath:  filepath.ToSlash(filepath.Join(sourceWorkspace.VirtualPath, "app")),
		Kind:         sourceWorkspace.Kind,
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionWrite, appWorkspace.ConcretePath) {
		return terminalWorkspaceAccessFailure(appWorkspace.ConcretePath), nil
	}
	appStat, errorValue := workspaceActor.Stat(toolContext, workspacepath.Path(appWorkspace))
	if errorValue != nil || !appStat.IsDirectory {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.NotFound, "site_build_workspace", "appWorkspacePath does not exist; run site.app.repair before building"), nil
	}
	if toolFailure := ensureManagedSiteBuildScript(toolContext, workspaceActor, appWorkspace); toolFailure != nil {
		return *toolFailure, nil
	}
	timeoutSecond := input.TimeoutSecond
	if timeoutSecond <= 0 {
		timeoutSecond = 180
	}
	buildResult, errorValue := toolCatalogBuilder.runTerminalTool(toolContext, security.CommandRequest{
		Command:              "bun scripts/build.ts",
		WorkingDirectoryPath: appWorkspace.VirtualPath,
		TimeoutSecond:        timeoutSecond,
	}, handlerContext)
	if errorValue != nil {
		return buildResult, errorValue
	}
	if buildResult.Failed() {
		return siteBuildCommandFailureResult(buildResult, appWorkspace), nil
	}
	qualityPath := workspacepath.Path{
		ConcretePath: filepath.Join(filepath.Dir(appWorkspace.ConcretePath), ".internkim", "build-quality.json"),
		VirtualPath:  filepath.ToSlash(filepath.Join(filepath.Dir(appWorkspace.VirtualPath), ".internkim", "build-quality.json")),
		Kind:         appWorkspace.Kind,
	}
	if toolFailure := writeSuccessfulSiteBuildQuality(toolContext, workspaceActor, qualityPath); toolFailure != nil {
		return *toolFailure, nil
	}
	result := map[string]any{
		"siteID":              firstNonEmptyString(input.SiteID, site.SiteID),
		"slug":                firstNonEmptyString(input.Slug, site.Slug),
		"sourceWorkspacePath": sourceWorkspace.VirtualPath,
		"appWorkspacePath":    appWorkspace.VirtualPath,
		"distPath":            filepath.ToSlash(filepath.Join(appWorkspace.VirtualPath, "dist")),
		"qualityPath":         filepath.ToSlash(filepath.Join(sourceWorkspace.VirtualPath, ".internkim", "build-quality.json")),
		"command":             "bun scripts/build.ts",
		"commandResult":       json.RawMessage(buildResult.ContentText()),
	}
	for key, value := range siteBuildQualityPayload(toolContext, workspaceActor, sourceWorkspace, appWorkspace) {
		result[key] = value
	}
	if deliveryBlocked, _ := result["deliveryBlocked"].(bool); deliveryBlocked {
		return siteDeliveryBlockedBuildResult(result), nil
	}
	return agent.ToolSuccess(marshalToolResult(result)), nil
}

func siteBuildCommandFailureResult(buildResult agent.ToolResult, appWorkspace workspacepath.Path) agent.ToolResult {
	payload := siteBuildFailurePayload(buildResult, appWorkspace)
	buildResult.Output.Data = json.RawMessage(marshalToolResult(payload))
	if siteBuildFailureLooksSourceFixable(buildResult.ContentText()) {
		buildResult.Failure = &agent.ToolFailure{
			Kind:                  agent.FailureInvalidInput,
			Code:                  agent.FailureCodes.InvalidInput.String(),
			Stage:                 "site_build_source",
			UserSafeSummary:       siteBuildSourceFailureSummary(buildResult.ContentText()),
			Retryable:             true,
			SafeRetry:             true,
			FailureClass:          "fixable_artifact_quality",
			RetryPolicy:           "after_precondition",
			RequiredPreconditions: []string{"source_changed"},
			RecoveryHints: []agent.RecoveryHint{{
				Action:    "edit_resource",
				ToolNames: []string{"file.read", "file.edit", "file.patch", "file.write"},
				Reason:    "Inspect the source file named in the build error, fix the syntax or content, then run site.app.build again.",
			}},
			AffectedResources: siteBuildFailureAffectedResources(buildResult.ContentText(), appWorkspace),
		}
	}
	return buildResult
}

func siteBuildFailurePayload(buildResult agent.ToolResult, appWorkspace workspacepath.Path) map[string]any {
	content := buildResult.ContentText()
	sourceWorkspacePath := filepath.Dir(appWorkspace.ConcretePath)
	return map[string]any{
		"target":                  appWorkspace.VirtualPath,
		"stderrTail":              terminalFailureTail(content),
		"likelyCause":             siteBuildLikelyCause(content),
		"suggestedSourceFiles":    siteBuildFailureSuggestedSourceFiles(content, appWorkspace),
		"canUseExistingFreshDist": siteBuildStatus(sourceWorkspacePath) == "fresh",
		"buildStatus":             siteBuildStatus(sourceWorkspacePath),
		"command":                 "bun scripts/build.ts",
		"commandResult":           content,
	}
}

func terminalFailureTail(content string) string {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) <= 24 {
		return strings.TrimSpace(content)
	}
	return strings.TrimSpace(strings.Join(lines[len(lines)-24:], "\n"))
}

func siteBuildLikelyCause(content string) string {
	lowerContent := strings.ToLower(content)
	switch {
	case strings.Contains(lowerContent, "command not found") && strings.Contains(lowerContent, "bun"):
		return "managed runtime PATH does not include bun"
	case strings.Contains(lowerContent, "permission denied") || strings.Contains(lowerContent, "eacces"):
		return "workspace permission problem"
	case siteBuildFailureLooksSourceFixable(content):
		return "editable source compile error"
	case strings.Contains(lowerContent, "cannot find module") || strings.Contains(lowerContent, "could not resolve"):
		return "dependency or import resolution error"
	default:
		return "site build command failed"
	}
}

func siteBuildFailureSuggestedSourceFiles(content string, appWorkspace workspacepath.Path) []string {
	files := []string{}
	for _, resource := range siteBuildFailureAffectedResources(content, appWorkspace) {
		files = append(files, resource.Path)
	}
	if len(files) > 0 {
		return files
	}
	return []string{
		filepath.ToSlash(filepath.Join(appWorkspace.VirtualPath, "src", "App.tsx")),
		filepath.ToSlash(filepath.Join(appWorkspace.VirtualPath, "src", "index.css")),
		filepath.ToSlash(filepath.Join(appWorkspace.VirtualPath, "src", "prototype-data.ts")),
	}
}

func siteBuildFailureLooksSourceFixable(content string) bool {
	lowerContent := strings.ToLower(content)
	return strings.Contains(lowerContent, "syntax error") ||
		strings.Contains(lowerContent, "transform failed") ||
		strings.Contains(lowerContent, "vite:esbuild") ||
		strings.Contains(lowerContent, "/src/")
}

func siteBuildSourceFailureSummary(content string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmedLine := strings.TrimSpace(line)
		if strings.Contains(strings.ToLower(trimmedLine), "syntax error") {
			return trimmedLine
		}
	}
	return "site source failed to compile; inspect and fix the affected source file."
}

func siteBuildFailureAffectedResources(content string, appWorkspace workspacepath.Path) []agent.AffectedResource {
	resources := []agent.AffectedResource{}
	for _, resourcePath := range siteBuildFailureSourcePaths(content, appWorkspace) {
		resources = append(resources, agent.AffectedResource{
			Path:   resourcePath,
			Role:   "source",
			Reason: "Build reported a source compile error here.",
		})
	}
	return resources
}

func siteBuildFailureSourcePaths(content string, appWorkspace workspacepath.Path) []string {
	paths := []string{}
	for _, line := range strings.Split(content, "\n") {
		if !strings.Contains(line, "/src/") {
			continue
		}
		for _, field := range strings.Fields(line) {
			cleanField := strings.Trim(field, "\"'`,:;()[]{}")
			sourceIndex := strings.Index(cleanField, "/src/")
			if sourceIndex < 0 {
				continue
			}
			sourcePath := siteBuildFailureSourcePathFromField(cleanField[sourceIndex+1:])
			paths = append(paths, filepath.ToSlash(filepath.Join(appWorkspace.VirtualPath, sourcePath)))
		}
	}
	return stableUniqueStrings(paths)
}

func siteBuildFailureSourcePathFromField(field string) string {
	sourcePath := strings.Trim(field, "\"'`,:;()[]{}")
	for _, extension := range []string{".tsx", ".ts", ".jsx", ".js", ".css"} {
		if index := strings.Index(sourcePath, extension); index >= 0 {
			return sourcePath[:index+len(extension)]
		}
	}
	return sourcePath
}

func siteDeliveryBlockedBuildResult(result map[string]any) agent.ToolResult {
	return agent.ToolResult{
		Output: agent.ToolOutput{Content: marshalToolResult(result)},
		Failure: &agent.ToolFailure{
			Kind:                  agent.FailureInvalidInput,
			Code:                  agent.FailureCodes.InvalidInput.String(),
			Stage:                 "site_build_delivery",
			UserSafeSummary:       siteDeliveryBlockedSummary(result),
			Retryable:             true,
			SafeRetry:             true,
			FailureClass:          "fixable_artifact_quality",
			RetryPolicy:           "after_precondition",
			RequiredPreconditions: []string{"source_changed"},
			RecoveryHints: []agent.RecoveryHint{{
				Action:    "edit_resource",
				ToolNames: []string{"file.read", "file.edit", "file.patch", "file.write"},
				Reason:    "Replace starter scaffold content in editableTargets, then run site.app.build again.",
			}},
			AffectedResources: siteDeliveryBlockedAffectedResources(result),
		},
	}
}

func siteDeliveryBlockedSummary(result map[string]any) string {
	if blockers, isSlice := result["deliveryBlockers"].([]string); isSlice && len(blockers) > 0 {
		return "site build produced a non-deliverable starter scaffold: " + strings.Join(blockers, "; ")
	}
	if blockers, isSlice := result["deliveryBlockers"].([]any); isSlice && len(blockers) > 0 {
		values := []string{}
		for _, blocker := range blockers {
			if text, isString := blocker.(string); isString && strings.TrimSpace(text) != "" {
				values = append(values, strings.TrimSpace(text))
			}
		}
		if len(values) > 0 {
			return "site build produced a non-deliverable starter scaffold: " + strings.Join(values, "; ")
		}
	}
	return "site build produced a non-deliverable starter scaffold; replace starter content before publishing."
}

func siteDeliveryBlockedAffectedResources(result map[string]any) []agent.AffectedResource {
	targets, isSlice := result["editableTargets"].([]string)
	if !isSlice {
		if values, ok := result["editableTargets"].([]any); ok {
			for _, value := range values {
				if text, isString := value.(string); isString {
					targets = append(targets, text)
				}
			}
		}
	}
	resources := []agent.AffectedResource{}
	for _, target := range targets {
		if strings.TrimSpace(target) == "" {
			continue
		}
		resources = append(resources, agent.AffectedResource{
			Path:   strings.TrimSpace(target),
			Role:   "source",
			Reason: "Starter scaffold content must be replaced before delivery.",
		})
	}
	return resources
}

func ensureManagedSiteBuildScript(ctx context.Context, workspaceActor security.WorkspaceActor, appWorkspace workspacepath.Path) *agent.ToolResult {
	buildScriptContent := siteManagedBuildScriptContent()
	if len(buildScriptContent) == 0 {
		result := agent.ToolFailureResult(agent.FailureDependencyUnavailable, agent.FailureCodes.Unavailable, "site_build_scaffold", "managed site build script is unavailable")
		return &result
	}
	buildScriptPath := workspacepath.Path{
		ConcretePath: filepath.Join(appWorkspace.ConcretePath, "scripts", "build.ts"),
		VirtualPath:  filepath.ToSlash(filepath.Join(appWorkspace.VirtualPath, "scripts", "build.ts")),
		Kind:         appWorkspace.Kind,
	}
	if errorValue := workspaceActor.WriteFile(ctx, buildScriptPath, buildScriptContent, 0660); errorValue != nil {
		result := actorToolFailure("write_file", "site_build_scaffold", buildScriptPath.VirtualPath, errorValue)
		return &result
	}
	return nil
}

func siteBuildQualityPayload(ctx context.Context, workspaceActor security.WorkspaceActor, sourceWorkspace workspacepath.Path, appWorkspace workspacepath.Path) map[string]any {
	qualityPath := workspacepath.Path{
		ConcretePath: filepath.Join(sourceWorkspace.ConcretePath, ".internkim", "build-quality.json"),
		VirtualPath:  filepath.ToSlash(filepath.Join(sourceWorkspace.VirtualPath, ".internkim", "build-quality.json")),
		Kind:         sourceWorkspace.Kind,
	}
	payload := map[string]any{
		"qualityStatus":      "missing_report",
		"qualityIssues":      []any{},
		"qualityIssueCount":  0,
		"blockingIssueCount": 0,
		"editableTargets":    []string{},
	}
	qualityDocument, errorValue := workspaceActor.ReadFile(ctx, qualityPath, 1024*1024)
	if errorValue != nil || !json.Valid(qualityDocument) {
		return payload
	}
	var quality map[string]any
	if errorValue := json.Unmarshal(qualityDocument, &quality); errorValue != nil {
		payload["qualityStatus"] = "invalid_report"
		return payload
	}
	issues, _ := quality["issues"].([]any)
	blockingIssueCount, _ := siteBlockingIssueCount(quality)
	payload["qualityIssues"] = issues
	payload["qualityIssueCount"] = len(issues)
	payload["blockingIssueCount"] = blockingIssueCount
	payload["editableTargets"] = siteQualityEditableTargets(quality, appWorkspace)
	deliveryBlockers := siteDeliveryBlockerSummaries(quality)
	if len(deliveryBlockers) > 0 {
		payload["qualityStatus"] = "delivery_blocked"
		payload["deliveryBlocked"] = true
		payload["deliveryBlockers"] = deliveryBlockers
		payload["recommendedNextActions"] = []string{
			"Do not publish while deliveryBlocked is true.",
			"Edit the listed editableTargets to replace starter scaffold content.",
			"Run site.app.build again after source changes.",
		}
		return payload
	}
	if len(issues) > 0 {
		payload["qualityStatus"] = "needs_improvement"
		payload["recommendedNextActions"] = []string{
			"Inspect qualityIssues and editableTargets.",
			"Revise the affected source files when improvement budget remains.",
			"Publish with the report if build output is acceptable and improvement budget is exhausted.",
		}
		return payload
	}
	payload["qualityStatus"] = "passed"
	return payload
}

func siteDeliveryBlockerSummaries(quality map[string]any) []string {
	issues, isSlice := quality["issues"].([]any)
	if !isSlice {
		return nil
	}
	summaries := []string{}
	for _, issue := range issues {
		issueDocument, isMap := issue.(map[string]any)
		if !isMap {
			continue
		}
		if !siteQualityIssueBlocksDelivery(issueDocument) {
			continue
		}
		target, _ := issueDocument["target"].(string)
		message, _ := issueDocument["message"].(string)
		suggestedFix, _ := issueDocument["suggestedFix"].(string)
		text := firstNonEmptyString(strings.TrimSpace(message), strings.TrimSpace(suggestedFix), "Starter scaffold remains.")
		summaries = append(summaries, firstNonEmptyString(strings.TrimSpace(target), "site")+": "+text)
	}
	return summaries
}

func siteQualityIssueBlocksDelivery(issue map[string]any) bool {
	category, _ := issue["category"].(string)
	if strings.TrimSpace(category) == "templateSmell" {
		return true
	}
	return siteQualityIssueContainsStarterMarker(issue)
}

func siteQualityIssueContainsStarterMarker(issue map[string]any) bool {
	for _, value := range issue {
		text, isString := value.(string)
		if !isString {
			continue
		}
		if siteTextContainsStarterMarker(text) {
			return true
		}
	}
	return false
}

func siteTextContainsStarterMarker(value string) bool {
	for _, marker := range []string{
		"INTERNKIM_SITE_STARTER_REPLACE_ME",
		"Replace this starter",
		"Beautiful default scaffold",
		"InternKim React prototype",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func siteQualityAffectedResources(quality map[string]any, appWorkspace workspacepath.Path) []agent.AffectedResource {
	issues, isSlice := quality["issues"].([]any)
	if !isSlice {
		return nil
	}
	resources := []agent.AffectedResource{}
	for _, issue := range issues {
		issueDocument, isMap := issue.(map[string]any)
		if !isMap {
			continue
		}
		target, _ := issueDocument["target"].(string)
		message, _ := issueDocument["message"].(string)
		suggestedFix, _ := issueDocument["suggestedFix"].(string)
		if strings.TrimSpace(target) == "" {
			continue
		}
		reason := strings.TrimSpace(message)
		if strings.TrimSpace(suggestedFix) != "" {
			reason = strings.TrimSpace(reason + " " + strings.TrimSpace(suggestedFix))
		}
		resources = append(resources, agent.AffectedResource{
			Path:   siteQualityEditableTargetPath(appWorkspace, target),
			Role:   "source",
			Reason: reason,
		})
	}
	return resources
}

func siteQualityIssueSummaries(quality map[string]any) []string {
	issues, isSlice := quality["issues"].([]any)
	if !isSlice {
		return nil
	}
	summaries := []string{}
	for _, issue := range issues {
		issueDocument, isMap := issue.(map[string]any)
		if !isMap {
			continue
		}
		target, _ := issueDocument["target"].(string)
		message, _ := issueDocument["message"].(string)
		suggestedFix, _ := issueDocument["suggestedFix"].(string)
		category, _ := issueDocument["category"].(string)
		text := firstNonEmptyString(strings.TrimSpace(message), strings.TrimSpace(suggestedFix), strings.TrimSpace(category))
		if text == "" {
			continue
		}
		summaries = append(summaries, firstNonEmptyString(strings.TrimSpace(target), "site")+": "+text)
		if len(summaries) >= 3 {
			return summaries
		}
	}
	return summaries
}

func siteQualityEditableTargets(quality map[string]any, appWorkspace workspacepath.Path) []string {
	resources := siteQualityAffectedResources(quality, appWorkspace)
	targets := []string{}
	for _, resource := range resources {
		if strings.TrimSpace(resource.Path) != "" {
			targets = append(targets, strings.TrimSpace(resource.Path))
		}
	}
	return stableUniqueStrings(targets)
}

func siteQualityEditableTargetPath(appWorkspace workspacepath.Path, target string) string {
	cleanTarget := filepath.ToSlash(filepath.Clean(strings.TrimSpace(target)))
	cleanTarget = strings.TrimPrefix(cleanTarget, "/")
	if cleanTarget == "." || cleanTarget == "" {
		return appWorkspace.VirtualPath
	}
	if strings.HasPrefix(cleanTarget, "app/") {
		return filepath.ToSlash(filepath.Join(filepath.Dir(appWorkspace.VirtualPath), cleanTarget))
	}
	return filepath.ToSlash(filepath.Join(appWorkspace.VirtualPath, cleanTarget))
}

func siteBlockingIssueCount(quality map[string]any) (int, bool) {
	value, exists := quality["blockingIssueCount"]
	if !exists {
		return 0, false
	}
	switch typedValue := value.(type) {
	case float64:
		return int(typedValue), true
	case int:
		return typedValue, true
	default:
		return 0, false
	}
}

func writeSuccessfulSiteBuildQuality(ctx context.Context, workspaceActor security.WorkspaceActor, qualityPath workspacepath.Path) *agent.ToolResult {
	quality := map[string]any{
		"generatedAt":         time.Now().UTC().Format(time.RFC3339),
		"generatedBy":         "site.app.build",
		"blockingIssueCount":  0,
		"issues":              []any{},
		"postBuildNormalized": true,
	}
	document, errorValue := workspaceActor.ReadFile(ctx, qualityPath, 1024*1024)
	if errorValue == nil {
		var existing map[string]any
		if json.Unmarshal(document, &existing) == nil {
			quality = existing
			quality["generatedAt"] = time.Now().UTC().Format(time.RFC3339)
			if generatedBy, _ := quality["generatedBy"].(string); strings.TrimSpace(generatedBy) != "" {
				quality["generatedBy"] = strings.TrimSpace(generatedBy)
			} else {
				quality["generatedBy"] = "site.app.build"
			}
			quality["postBuildNormalized"] = true
			if _, exists := quality["blockingIssueCount"]; !exists {
				quality["blockingIssueCount"] = 0
			}
			if _, exists := quality["issues"]; !exists {
				quality["issues"] = []any{}
			}
		}
	}
	qualityDocument, errorValue := json.MarshalIndent(quality, "", "  ")
	if errorValue != nil {
		return toolResultPointer(agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "site_build_quality", errorValue.Error()))
	}
	if errorValue := workspaceActor.MkdirAll(ctx, qualityPath.Parent(), 02770); errorValue != nil {
		toolFailure := actorToolFailure("mkdir_all", "site_build_quality", qualityPath.VirtualPath, errorValue)
		return &toolFailure
	}
	if errorValue := workspaceActor.WriteFile(ctx, qualityPath, append(qualityDocument, '\n'), 0660); errorValue != nil {
		toolFailure := actorToolFailure("write_file", "site_build_quality", qualityPath.VirtualPath, errorValue)
		return &toolFailure
	}
	return nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) repairSiteAppTool(toolContext context.Context, input siteAppRepairToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	site := siteCreateResult{}
	if shouldResolveSiteStatusForWorkspaceTool(input.SiteID, input.Slug, input.SourceWorkspacePath, "") {
		resolvedSite, errorValue := toolCatalogBuilder.siteStatusForWorkspaceTool(toolContext, handlerContext.request, input.SiteID, input.Slug)
		if errorValue != nil {
			return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "site_repair_status", errorValue.Error()), nil
		}
		site = resolvedSite
	}
	sourceWorkspacePath := firstNonEmptyString(input.SourceWorkspacePath, site.SourceWorkspacePath)
	if strings.TrimSpace(sourceWorkspacePath) == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "site_repair_workspace", "sourceWorkspacePath could not be resolved; call site.app.status first"), nil
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return *actorFailure, nil
	}
	sourceWorkspace, errorValue := toolCatalogBuilder.resolveSiteProjectSourceWorkspace(toolContext, handlerContext.request, workspaceActor, siteProjectResolutionInput{
		SiteID:              firstNonEmptyString(input.SiteID, site.SiteID, siteProjectIDFromPath(sourceWorkspacePath)),
		SourceWorkspacePath: sourceWorkspacePath,
	})
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "site_repair_workspace", errorValue.Error()), nil
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionWrite, sourceWorkspace.ConcretePath) {
		return terminalWorkspaceAccessFailure(sourceWorkspace.ConcretePath), nil
	}
	if errorValue := workspaceActor.MkdirAll(toolContext, workspacepath.Directory(sourceWorkspace), 02770); errorValue != nil {
		return actorToolFailure("mkdir_all", "site_repair_workspace", sourceWorkspace.VirtualPath, errorValue), nil
	}
	site.SourceWorkspacePath = sourceWorkspace.VirtualPath
	site.WorkspacePath = canonicalSiteProjectWorkspacePath(firstNonEmptyString(input.SiteID, site.SiteID, siteProjectIDFromPath(sourceWorkspace.VirtualPath)))
	site.AppWorkspacePath = filepath.ToSlash(filepath.Join(sourceWorkspace.VirtualPath, "app"))
	if toolFailure := writeSiteStarterFiles(toolContext, workspaceActor, workspacepath.Directory(sourceWorkspace), site); toolFailure != nil {
		return *toolFailure, nil
	}
	return agent.ToolSuccess(marshalToolResult(map[string]any{
		"siteID":              site.SiteID,
		"slug":                site.Slug,
		"workspacePath":       site.WorkspacePath,
		"sourceWorkspacePath": site.SourceWorkspacePath,
		"draftPath":           site.SourceWorkspacePath,
		"appWorkspacePath":    site.AppWorkspacePath,
		"workspaceHealth":     "ready",
		"publishedUnchanged":  true,
	})), nil
}

func shouldResolveSiteStatusForWorkspaceTool(siteID string, slug string, sourceWorkspacePath string, appWorkspacePath string) bool {
	return strings.TrimSpace(siteID) != "" ||
		strings.TrimSpace(slug) != "" ||
		(strings.TrimSpace(sourceWorkspacePath) == "" && strings.TrimSpace(appWorkspacePath) == "")
}

func (toolCatalogBuilder *ToolCatalogBuilder) siteStatusForWorkspaceTool(toolContext context.Context, request ToolCatalogRequest, siteID string, slug string) (siteCreateResult, error) {
	if toolCatalogBuilder.capabilityClient.Endpoint == "" {
		return siteCreateResult{}, errors.New("site.app.status capability is unavailable")
	}
	input := map[string]string{}
	if strings.TrimSpace(siteID) != "" {
		input["siteID"] = strings.TrimSpace(siteID)
	}
	if strings.TrimSpace(slug) != "" {
		input["slug"] = strings.TrimSpace(slug)
	}
	inputDocument, _ := json.Marshal(input)
	var response struct {
		Result  json.RawMessage `json:"result"`
		IsError bool            `json:"isError"`
		Status  string          `json:"status"`
		Message string          `json:"message"`
	}
	errorValue := toolCatalogBuilder.capabilityClient.PostJSON(toolContext, "/v1/tools/site.app.status/invoke", capabilityToolRequest("site.app.status", request, inputDocument), &response)
	if errorValue != nil {
		return siteCreateResult{}, errorValue
	}
	if response.IsError || response.Status == "error" || response.Status == "denied" {
		return siteCreateResult{}, errors.New(firstNonEmptyString(response.Message, string(response.Result)))
	}
	if len(bytes.TrimSpace(response.Result)) == 0 {
		return siteCreateResult{}, nil
	}
	return decodeSiteCreateResult(response.Result)
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

func terminalRuntimePathFailure(commandRequest security.CommandRequest, commandResult security.CommandResult, content string) *agent.ToolResult {
	combinedText := strings.ToLower(commandResult.Stderr + "\n" + commandResult.Stdout + "\n" + content)
	if !strings.Contains(combinedText, "not found in $path") && !strings.Contains(combinedText, "command not found") && !strings.Contains(combinedText, "executable file not found") {
		return nil
	}
	document := json.RawMessage(marshalToolResult(map[string]any{
		"failureClass":      "terminal_runtime_path",
		"command":           commandRequest.Command,
		"actualPATH":        commandRequest.EnvironmentVariables["PATH"],
		"canonicalPATH":     security.CanonicalRuntimePATH,
		"executionUser":     commandRequest.ExecutionIdentity.UserName,
		"workingDirectory":  commandRequest.WorkingDirectoryPath,
		"commandResult":     commandResult,
		"recommendedAction": "Fix Blueclaw runtime PATH propagation; do not change site source or ask the user to use external hosting.",
	}))
	result := agent.ToolFailureWithOutput(agent.FailureDependencyUnavailable, agent.FailureCode("terminal_runtime_path"), "terminal_runtime_path", "terminal runtime PATH did not expose a managed executable", document)
	result.Failure.Retryable = true
	result.Failure.SafeRetry = false
	return &result
}

func terminalSourceWriteMisuseFailure(command string) *agent.ToolResult {
	target := terminalSourceWriteTarget(command)
	if target == "" {
		return nil
	}
	message := "terminal.run is for commands, builds, renders, and inspection; use file.write, file.edit, or file.patch to create or edit source files"
	document := json.RawMessage(marshalToolResult(map[string]any{
		"failureClass":       "source_edit_tool_misuse",
		"command":            command,
		"detectedTarget":     target,
		"suggestedNextTools": []string{"file.write", "file.edit", "file.patch"},
		"message":            message,
	}))
	result := agent.ToolFailureWithOutput(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "terminal_source_write", message, document)
	result.Failure.Retryable = true
	result.Failure.SafeRetry = true
	result.Failure.RetryPolicy = "different_tool"
	result.Failure.RecoveryHints = []agent.RecoveryHint{{
		Action:    "edit_resource",
		ToolNames: []string{"file.write", "file.edit", "file.patch"},
		Reason:    "Write source content through file tools so the runtime can preserve context, permissions, and recovery state.",
	}}
	return &result
}

func terminalSourceWriteTarget(command string) string {
	if strings.Contains(command, "<<") {
		return "shell heredoc"
	}
	fields := strings.Fields(command)
	for index, field := range fields {
		if sourcePath := shellRedirectSourcePath(field, nextShellField(fields, index)); sourcePath != "" {
			return sourcePath
		}
	}
	return ""
}

func nextShellField(fields []string, index int) string {
	nextIndex := index + 1
	if nextIndex >= len(fields) {
		return ""
	}
	return fields[nextIndex]
}

func shellRedirectSourcePath(field string, nextField string) string {
	if strings.Contains(field, ">&") || strings.Contains(field, "<&") {
		return ""
	}
	switch field {
	case ">", ">>", "1>", "1>>":
		if terminalWritePathLooksLikeSource(nextField) {
			return nextField
		}
		return ""
	}
	for _, prefix := range []string{">>", ">", "1>>", "1>"} {
		if strings.HasPrefix(field, prefix) {
			candidatePath := strings.TrimPrefix(field, prefix)
			if terminalWritePathLooksLikeSource(candidatePath) {
				return candidatePath
			}
		}
	}
	return ""
}

func terminalWritePathLooksLikeSource(path string) bool {
	cleanPath := strings.Trim(strings.TrimSpace(path), `"'`)
	if cleanPath == "" {
		return false
	}
	slashPath := filepath.ToSlash(cleanPath)
	if strings.HasPrefix(slashPath, "dist/") || strings.HasPrefix(slashPath, "build/") || strings.Contains(slashPath, "/dist/") || strings.Contains(slashPath, "/build/") {
		return false
	}
	extension := strings.ToLower(filepath.Ext(cleanPath))
	if !terminalWriteExtensionLooksLikeSource(extension) {
		return false
	}
	baseName := strings.ToLower(filepath.Base(cleanPath))
	if terminalWriteBaseNameLooksLikeSource(baseName) {
		return true
	}
	return strings.HasPrefix(slashPath, "src/") || strings.HasPrefix(slashPath, "app/src/") || strings.Contains(slashPath, "/src/")
}

func terminalWriteExtensionLooksLikeSource(extension string) bool {
	switch extension {
	case ".ts", ".tsx", ".js", ".jsx", ".css", ".scss", ".html", ".md", ".mdx", ".json", ".toml", ".yaml", ".yml", ".svelte", ".vue", ".go", ".py", ".sh":
		return true
	default:
		return false
	}
}

func terminalWriteBaseNameLooksLikeSource(baseName string) bool {
	switch baseName {
	case "package.json", "vite.config.ts", "vite.config.js", "tsconfig.json", "tailwind.config.ts", "tailwind.config.js", "design.md", "presentation.md":
		return true
	default:
		return false
	}
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
	environmentVariables := toolCatalogBuilder.resolveAgentWorkspaceEnvironment(mergeWorkspaceEnvironment(input.EnvironmentVariables, scope.EnvironmentVariables()))
	if toolFailure := toolCatalogBuilder.materializeTerminalRuntimeDirectories(toolContext, workspaceActor, scope, environmentVariables); toolFailure != nil {
		return *toolFailure, nil
	}
	sessionID, errorValue := toolCatalogBuilder.terminalService.StartInteractiveSession(security.CommandRequest{
		Command:              toolCatalogBuilder.resolveAgentWorkspaceReferences(input.Command),
		WorkingDirectoryPath: workingDirectoryPath,
		EnvironmentVariables: environmentVariables,
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

func preflightNodePackageBuild(ctx context.Context, workspaceActor security.WorkspaceActor, workingDirectory workspacepath.Directory, command string) *agent.ToolResult {
	if !commandRunsNodePackageBuild(command) {
		return nil
	}
	packagePath := workingDirectory.JoinVirtualFile("package.json")
	document, errorValue := workspaceActor.ReadFile(ctx, packagePath, 256*1024)
	if errorValue != nil {
		return packageManifestInvalidFailure(packagePath.VirtualPath, "package.json is missing or unreadable: "+errorValue.Error())
	}
	var packageDocument map[string]any
	if errorValue := json.Unmarshal(document, &packageDocument); errorValue != nil {
		return packageManifestInvalidFailure(packagePath.VirtualPath, "package.json is not valid JSON: "+errorValue.Error())
	}
	scripts, isObject := packageDocument["scripts"].(map[string]any)
	if !isObject {
		return packageManifestInvalidFailure(packagePath.VirtualPath, "package.json has no scripts object")
	}
	buildScript, isString := scripts["build"].(string)
	if !isString || strings.TrimSpace(buildScript) == "" {
		return packageManifestInvalidFailure(packagePath.VirtualPath, "package.json has no scripts.build command")
	}
	return nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) materializeTerminalRuntimeDirectories(ctx context.Context, workspaceActor security.WorkspaceActor, scope WorkspaceScope, environmentVariables map[string]string) *agent.ToolResult {
	for _, directory := range terminalRuntimeDirectories(scope, environmentVariables) {
		if errorValue := workspaceActor.MkdirAll(ctx, directory, 02770); errorValue != nil {
			result := actorToolFailure("mkdir_all", "terminal_runtime_environment", directory.VirtualPath, errorValue)
			return &result
		}
	}
	return nil
}

func terminalRuntimeDirectories(scope WorkspaceScope, environmentVariables map[string]string) []workspacepath.Directory {
	seenDirectoryPaths := map[string]bool{}
	var directories []workspacepath.Directory
	for _, name := range []string{
		"TMPDIR",
		"TMP",
		"TEMP",
		"XDG_CACHE_HOME",
		"XDG_CONFIG_HOME",
		"XDG_RUNTIME_DIR",
		"BUN_TMPDIR",
		"BUN_INSTALL",
		"BUN_INSTALL_CACHE_DIR",
		"npm_config_cache",
	} {
		directoryPath := filepath.Clean(strings.TrimSpace(environmentVariables[name]))
		if directoryPath == "." || !strings.HasPrefix(directoryPath, filepath.Clean(scope.RequesterRootPath)+string(filepath.Separator)) {
			continue
		}
		if seenDirectoryPaths[directoryPath] {
			continue
		}
		seenDirectoryPaths[directoryPath] = true
		directories = append(directories, requesterOwnedRuntimeDirectory(scope, directoryPath))
	}
	return directories
}

func requesterOwnedRuntimeDirectory(scope WorkspaceScope, directoryPath string) workspacepath.Directory {
	virtualPath := filepath.ToSlash(directoryPath)
	if relativePath, errorValue := filepath.Rel(scope.RequesterRootPath, directoryPath); errorValue == nil && relativePath != "." && !strings.HasPrefix(relativePath, "..") {
		virtualPath = filepath.ToSlash(filepath.Join("home", relativePath))
	}
	return workspacepath.Directory{
		ConcretePath: directoryPath,
		VirtualPath:  virtualPath,
		Kind:         workspacepath.KindWorkspace,
	}
}

func commandRunsNodePackageBuild(command string) bool {
	tokens := terminalCommandWords(command)
	for index := 0; index < len(tokens); index++ {
		switch tokens[index] {
		case "bun", "npm", "pnpm":
			if index+2 < len(tokens) && tokens[index+1] == "run" && tokens[index+2] == "build" {
				return true
			}
		case "yarn":
			if index+1 < len(tokens) && tokens[index+1] == "build" {
				return true
			}
			if index+2 < len(tokens) && tokens[index+1] == "run" && tokens[index+2] == "build" {
				return true
			}
		}
	}
	return false
}

func terminalCommandWords(command string) []string {
	replacer := strings.NewReplacer("\n", " ", "\t", " ", ";", " ", "&&", " ", "||", " ")
	fields := strings.Fields(replacer.Replace(command))
	words := make([]string, 0, len(fields))
	for _, field := range fields {
		word := strings.Trim(field, `"'`)
		if word != "" {
			words = append(words, word)
		}
	}
	return words
}

func packageManifestInvalidFailure(path string, detail string) *agent.ToolResult {
	content := marshalToolResult(map[string]string{
		"code":   "package_manifest_invalid",
		"path":   strings.TrimSpace(path),
		"detail": strings.TrimSpace(detail),
	})
	return &agent.ToolResult{
		Output: agent.ToolOutput{Content: content, Data: json.RawMessage(content)},
		Failure: &agent.ToolFailure{
			Kind:            agent.FailureInvalidInput,
			Code:            "package_manifest_invalid",
			Stage:           "package_manifest_preflight",
			UserSafeSummary: content,
			Retryable:       true,
			SafeRetry:       true,
		},
	}
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
	return strings.TrimSpace(input.UserFacingMessage)
}

func askConfirmReasonDetail(input askConfirmToolInput) string {
	return strings.TrimSpace(input.ReasonDetail)
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
	path := strings.TrimSpace(input.Path)
	if path == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_write", "path is required"), nil
	}
	if input.Content == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_write", "content is required"), nil
	}
	resolvedPath, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).Resolve(path, scope)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_write", errorValue.Error()), nil
	}
	if isManagedSitePackageManifestPath(resolvedPath.VirtualPath) {
		return managedSiteManifestProtectedFailure(resolvedPath.VirtualPath), nil
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

func (toolCatalogBuilder *ToolCatalogBuilder) readFileTool(toolContext context.Context, input fileReadToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	scope := toolCatalogBuilder.workspaceScopeForToolContext(toolContext, handlerContext.request)
	path := strings.TrimSpace(input.Path)
	materialID := strings.TrimSpace(input.MaterialID)
	if materialID != "" {
		if result, isCached := cachedFileReadResultByMaterialID(handlerContext.request.InputParts, materialID, input); isCached {
			return result, nil
		}
	}
	if path == "" {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_read", "path or materialID is required"), nil
	}
	resolvedPath, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).Resolve(path, scope)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_read", errorValue.Error()), nil
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionRead, resolvedPath.ConcretePath) {
		return agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "file_read", "current account cannot read this file"), nil
	}
	maxOutputBytes := input.MaxOutputBytes
	if maxOutputBytes <= 0 || maxOutputBytes > maximumFileReadBytes {
		maxOutputBytes = defaultFileReadMaximumBytes
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return *actorFailure, nil
	}
	fileInformation, errorValue := workspaceActor.Stat(toolContext, resolvedPath)
	if errorValue != nil {
		if result, isCached := cachedFileReadResult(handlerContext.request.InputParts, path, input); isCached {
			return result, nil
		}
		if result, fallbackError, isFound := toolCatalogBuilder.fileReadFallbackFromAttachmentMaterial(toolContext, resolvedPath.VirtualPath, input, handlerContext); isFound {
			return result, fallbackError
		}
		return actorToolFailure("stat", "file_read", resolvedPath.VirtualPath, errorValue), nil
	}
	if !fileInformation.IsRegular {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_read", "path is not a regular file"), nil
	}
	readMaximumBytes := maximumFileReadBytes
	if maxOutputBytes > readMaximumBytes {
		readMaximumBytes = maxOutputBytes
	}
	content, errorValue := workspaceActor.ReadFile(toolContext, resolvedPath, int64(readMaximumBytes+1))
	if errorValue != nil {
		if fileInformation.SizeBytes > int64(readMaximumBytes) {
			return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_read", "file is too large for exact text read; use file.preview for document or attachment understanding"), nil
		}
		return actorToolFailure("read_file", "file_read", resolvedPath.VirtualPath, errorValue), nil
	}
	isFileTruncated := len(content) > readMaximumBytes
	if isFileTruncated {
		content = content[:readMaximumBytes]
	}
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_read", "file.read supports UTF-8 text files; use file.preview or a specialized document tool for binary files"), nil
	}
	readResult := fileReadResult(string(content), input.StartLine, input.LineCount, maxOutputBytes)
	return agent.ToolSuccess(marshalToolResult(map[string]any{
		"path":              resolvedPath.VirtualPath,
		"content":           readResult.Content,
		"startLine":         readResult.StartLine,
		"endLine":           readResult.EndLine,
		"totalLines":        readResult.TotalLines,
		"totalLinesKnown":   !isFileTruncated,
		"originalSizeBytes": fileInformation.SizeBytes,
		"returnedBytes":     len([]byte(readResult.Content)),
		"sizeBytes":         fileInformation.SizeBytes,
		"isTruncated":       isFileTruncated || readResult.IsTruncated,
	})), nil
}

func cachedFileReadResultByMaterialID(parts []agent.AgentPart, materialID string, input fileReadToolInput) (agent.ToolResult, bool) {
	preview, isCached := cachedFilePreviewResultByMaterialID(parts, materialID)
	if !isCached {
		return agent.ToolResult{}, false
	}
	return cachedFileReadResultFromPreview(preview, input), true
}

func cachedFileReadResult(parts []agent.AgentPart, path string, input fileReadToolInput) (agent.ToolResult, bool) {
	preview, isCached := cachedFilePreviewResult(parts, path)
	if !isCached {
		return agent.ToolResult{}, false
	}
	return cachedFileReadResultFromPreview(preview, input), true
}

func cachedFileReadResultFromPreview(preview map[string]any, input fileReadToolInput) agent.ToolResult {
	content := stringMapValue(preview, "markdownPreview")
	readResult := fileReadResult(content, input.StartLine, input.LineCount, defaultFileReadMaximumBytes)
	return agent.ToolSuccess(marshalToolResult(map[string]any{
		"path":              stringMapValue(preview, "path"),
		"content":           readResult.Content,
		"startLine":         readResult.StartLine,
		"endLine":           readResult.EndLine,
		"totalLines":        readResult.TotalLines,
		"totalLinesKnown":   true,
		"originalSizeBytes": int64MapValue(preview, "sizeBytes"),
		"returnedBytes":     len([]byte(readResult.Content)),
		"sizeBytes":         int64MapValue(preview, "sizeBytes"),
		"isTruncated":       readResult.IsTruncated,
		"source":            "attachmentPreview",
		"isExactFileRead":   false,
	}))
}

func (toolCatalogBuilder *ToolCatalogBuilder) fileReadFallbackFromAttachmentMaterial(toolContext context.Context, path string, input fileReadToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error, bool) {
	material, isFound := visibleAttachmentMaterialForPath(handlerContext.request.VisibleContext, path)
	if !isFound {
		return agent.ToolResult{}, nil, false
	}
	materialID := strings.TrimSpace(material.MaterialID)
	if materialID == "" {
		return agent.ToolResult{}, nil, false
	}
	resolvedMaterial, errorValue := resolveReadableAttachmentMaterial(toolContext, handlerContext.request, materialID)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_read", errorValue.Error()), nil, true
	}
	if attachmentMaterialLooksLikeImage(resolvedMaterial) {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_read", "attachment material is an image; use image.read"), nil, true
	}
	if preview, hasPreview := filePreviewResultFromVisibleMaterial(resolvedMaterial); hasPreview {
		return cachedFileReadResultFromPreview(preview, input), nil, true
	}
	fallbackPath := strings.TrimSpace(resolvedMaterial.Path)
	if fallbackPath == "" || fallbackPath == strings.TrimSpace(path) {
		return agent.ToolResult{}, nil, false
	}
	fallbackInput := input
	fallbackInput.Path = fallbackPath
	fallbackInput.MaterialID = ""
	result, readError := toolCatalogBuilder.readFileTool(toolContext, fallbackInput, handlerContext)
	return result, readError, true
}

func stringMapValue(document map[string]any, key string) string {
	value, _ := document[key].(string)
	return strings.TrimSpace(value)
}

func int64MapValue(document map[string]any, key string) int64 {
	switch value := document[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	default:
		return 0
	}
}

type fileReadOutput struct {
	Content     string
	StartLine   int
	EndLine     int
	TotalLines  int
	IsTruncated bool
}

func fileReadResult(content string, startLine int, lineCount int, maxOutputBytes int) fileReadOutput {
	lines := splitFileLines(content)
	totalLines := len(lines)
	if totalLines == 0 {
		return fileReadOutput{}
	}
	if startLine <= 0 {
		content, isTruncated := truncateTextByBytes(content, maxOutputBytes)
		return fileReadOutput{
			Content:     content,
			StartLine:   1,
			EndLine:     totalLines,
			TotalLines:  totalLines,
			IsTruncated: isTruncated,
		}
	}
	if startLine > totalLines {
		return fileReadOutput{
			StartLine:  startLine,
			EndLine:    startLine - 1,
			TotalLines: totalLines,
		}
	}
	if lineCount <= 0 {
		lineCount = 200
	}
	endLine := startLine + lineCount - 1
	if endLine > totalLines {
		endLine = totalLines
	}
	content, isTruncated := truncateTextByBytes(strings.Join(lines[startLine-1:endLine], "\n"), maxOutputBytes)
	return fileReadOutput{
		Content:     content,
		StartLine:   startLine,
		EndLine:     endLine,
		TotalLines:  totalLines,
		IsTruncated: isTruncated,
	}
}

func splitFileLines(content string) []string {
	if content == "" {
		return nil
	}
	normalizedContent := strings.TrimSuffix(content, "\n")
	if normalizedContent == "" {
		return []string{""}
	}
	return strings.Split(normalizedContent, "\n")
}

func (toolCatalogBuilder *ToolCatalogBuilder) previewFileTool(toolContext context.Context, input filePreviewToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	scope := toolCatalogBuilder.workspaceScopeForToolContext(toolContext, handlerContext.request)
	if cachedPreview, isCached := cachedFilePreviewResultForInput(handlerContext.request.InputParts, input); isCached {
		return agent.ToolSuccess(marshalToolResult(cachedPreview)), nil
	}
	if materialPreview, isResolved := toolCatalogBuilder.filePreviewResolvedMaterial(toolContext, input, handlerContext.request); isResolved {
		return materialPreview, nil
	}
	previewPath, materialFailure := toolCatalogBuilder.filePreviewPath(toolContext, input, handlerContext.request)
	if materialFailure != nil {
		return *materialFailure, nil
	}
	if cachedPreview, isCached := cachedFilePreviewResult(handlerContext.request.InputParts, previewPath); isCached {
		return agent.ToolSuccess(marshalToolResult(cachedPreview)), nil
	}
	resolvedPath, failureResult, errorValue := toolCatalogBuilder.resolveReadableWorkspacePath(previewPath, scope, handlerContext.request, "file_preview")
	if failureResult != nil || errorValue != nil {
		return firstToolFailureResult(failureResult, errorValue, "file_preview"), nil
	}
	if cachedPreview, isCached := cachedFilePreviewResult(handlerContext.request.InputParts, resolvedPath.VirtualPath); isCached {
		return agent.ToolSuccess(marshalToolResult(cachedPreview)), nil
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return *actorFailure, nil
	}
	fileInformation, errorValue := workspaceActor.Stat(toolContext, resolvedPath)
	if errorValue != nil {
		if fallbackPath, fallbackFailure, isFound := toolCatalogBuilder.filePreviewFallbackPath(toolContext, resolvedPath.VirtualPath, handlerContext.request); isFound {
			if fallbackFailure != nil {
				return *fallbackFailure, nil
			}
			if strings.TrimSpace(fallbackPath) != "" && strings.TrimSpace(fallbackPath) != strings.TrimSpace(resolvedPath.VirtualPath) {
				return toolCatalogBuilder.previewFileTool(toolContext, filePreviewToolInput{Path: fallbackPath}, handlerContext)
			}
		}
		return actorToolFailure("stat", "file_preview", resolvedPath.VirtualPath, errorValue), nil
	}
	contentType := previewContentType(resolvedPath.VirtualPath)
	if strings.HasPrefix(contentType, "image/") {
		return agent.ToolSuccess(marshalToolResult(filePreviewResult(resolvedPath.VirtualPath, contentType, fileInformation.SizeBytes, "", "image", "use the image input part or image.read for visual inspection"))), nil
	}
	if toolCatalogBuilder.capabilityClient.HTTPClient != nil {
		if result, isConverted := toolCatalogBuilder.convertFilePreviewWithCapability(toolContext, handlerContext.request, resolvedPath.VirtualPath, contentType, fileInformation.SizeBytes); isConverted {
			return result, nil
		}
	}
	return toolCatalogBuilder.previewTextFile(toolContext, workspaceActor, resolvedPath, contentType, fileInformation.SizeBytes), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) filePreviewResolvedMaterial(toolContext context.Context, input filePreviewToolInput, request ToolCatalogRequest) (agent.ToolResult, bool) {
	if strings.TrimSpace(input.Path) != "" || strings.TrimSpace(input.MaterialID) == "" {
		return agent.ToolResult{}, false
	}
	material, errorValue := resolveReadableAttachmentMaterial(toolContext, request, input.MaterialID)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_preview", errorValue.Error()), true
	}
	if attachmentMaterialLooksLikeImage(material) {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_preview", "attachment material is an image; use image.read"), true
	}
	if result, hasPreview := filePreviewResultFromVisibleMaterial(material); hasPreview {
		return agent.ToolSuccess(marshalToolResult(result)), true
	}
	return agent.ToolResult{}, false
}

func filePreviewResultFromVisibleMaterial(material agent.VisibleContextMaterial) (map[string]any, bool) {
	preview := strings.TrimSpace(material.MarkdownPreview)
	status := strings.TrimSpace(material.ConversionStatus)
	message := strings.TrimSpace(material.ConversionMessage)
	if preview == "" && status == "" && message == "" {
		return nil, false
	}
	contentType := firstNonEmptyString(strings.TrimSpace(material.ContentType), previewContentType(material.Path))
	return filePreviewResult(material.Path, contentType, material.SizeBytes, preview, status, message), true
}

func (toolCatalogBuilder *ToolCatalogBuilder) filePreviewFallbackPath(toolContext context.Context, path string, request ToolCatalogRequest) (string, *agent.ToolResult, bool) {
	material, isFound := visibleAttachmentMaterialForPath(request.VisibleContext, path)
	if !isFound {
		return "", nil, false
	}
	materialID := strings.TrimSpace(material.MaterialID)
	if materialID == "" {
		return "", nil, false
	}
	resolvedMaterial, errorValue := resolveReadableAttachmentMaterial(toolContext, request, materialID)
	if errorValue != nil {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_preview", errorValue.Error())
		return "", &result, true
	}
	if attachmentMaterialLooksLikeImage(resolvedMaterial) {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_preview", "attachment material is an image; use image.read")
		return "", &result, true
	}
	return resolvedMaterial.Path, nil, true
}

func visibleAttachmentMaterialForPath(visibleContext agent.VisibleContext, path string) (agent.VisibleContextMaterial, bool) {
	candidates := visibleAttachmentMaterials(visibleContext)
	if material, isFound := visibleAttachmentMaterialWithExactPath(candidates, path); isFound {
		return material, true
	}
	return visibleAttachmentMaterialWithFilename(candidates, filepath.Base(strings.TrimSpace(path)))
}

func visibleAttachmentMaterials(visibleContext agent.VisibleContext) []agent.VisibleContextMaterial {
	materials := append([]agent.VisibleContextMaterial{}, visibleContext.CurrentMaterials...)
	materials = append(materials, visibleContext.Materials...)
	for _, message := range visibleContext.Messages {
		materials = append(materials, message.Materials...)
	}
	return uniqueVisibleAttachmentMaterials(materials)
}

func uniqueVisibleAttachmentMaterials(materials []agent.VisibleContextMaterial) []agent.VisibleContextMaterial {
	seen := map[string]bool{}
	result := make([]agent.VisibleContextMaterial, 0, len(materials))
	for _, material := range materials {
		key := visibleAttachmentMaterialKey(material)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, material)
	}
	return result
}

func visibleAttachmentMaterialKey(material agent.VisibleContextMaterial) string {
	if materialID := strings.TrimSpace(material.MaterialID); materialID != "" {
		return "material:" + materialID
	}
	if path := strings.TrimSpace(material.Path); path != "" {
		return "path:" + path
	}
	if filename := strings.TrimSpace(material.Filename); filename != "" {
		return "filename:" + filename
	}
	return ""
}

func visibleAttachmentMaterialWithExactPath(materials []agent.VisibleContextMaterial, path string) (agent.VisibleContextMaterial, bool) {
	trimmedPath := strings.TrimSpace(path)
	for _, material := range materials {
		if strings.TrimSpace(material.Path) == trimmedPath {
			return material, true
		}
	}
	return agent.VisibleContextMaterial{}, false
}

func visibleAttachmentMaterialWithFilename(materials []agent.VisibleContextMaterial, filename string) (agent.VisibleContextMaterial, bool) {
	trimmedFilename := strings.TrimSpace(filename)
	if trimmedFilename == "" || trimmedFilename == "." {
		return agent.VisibleContextMaterial{}, false
	}
	matches := []agent.VisibleContextMaterial{}
	for _, material := range materials {
		if strings.TrimSpace(material.Filename) == trimmedFilename || filepath.Base(strings.TrimSpace(material.Path)) == trimmedFilename {
			matches = append(matches, material)
		}
	}
	if len(matches) != 1 {
		return agent.VisibleContextMaterial{}, false
	}
	return matches[0], true
}

func (toolCatalogBuilder *ToolCatalogBuilder) filePreviewPath(toolContext context.Context, input filePreviewToolInput, request ToolCatalogRequest) (string, *agent.ToolResult) {
	path := strings.TrimSpace(input.Path)
	materialID := strings.TrimSpace(input.MaterialID)
	if path != "" {
		return path, nil
	}
	if materialID == "" {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_preview", "path or materialID is required")
		return "", &result
	}
	material, errorValue := resolveReadableAttachmentMaterial(toolContext, request, materialID)
	if errorValue != nil {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_preview", errorValue.Error())
		return "", &result
	}
	if attachmentMaterialLooksLikeImage(material) {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_preview", "attachment material is an image; use image.read")
		return "", &result
	}
	return material.Path, nil
}

func attachmentMaterialLooksLikeImage(material agent.VisibleContextMaterial) bool {
	contentType := strings.ToLower(strings.TrimSpace(material.ContentType))
	if strings.HasPrefix(contentType, "image/") {
		return true
	}
	filename := strings.ToLower(strings.TrimSpace(material.Filename))
	return strings.HasSuffix(filename, ".png") ||
		strings.HasSuffix(filename, ".jpg") ||
		strings.HasSuffix(filename, ".jpeg") ||
		strings.HasSuffix(filename, ".gif") ||
		strings.HasSuffix(filename, ".webp")
}

func (toolCatalogBuilder *ToolCatalogBuilder) resolveReadableWorkspacePath(path string, scope WorkspaceScope, request ToolCatalogRequest, stage string) (workspacepath.Path, *agent.ToolResult, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, stage, "path is required")
		return workspacepath.Path{}, &result, nil
	}
	resolvedPath, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).Resolve(trimmedPath, scope)
	if errorValue != nil {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, stage, errorValue.Error())
		return workspacepath.Path{}, &result, nil
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(request.PersonAccess, access.ActionRead, resolvedPath.ConcretePath) {
		result := agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, stage, "current account cannot read this file")
		return workspacepath.Path{}, &result, nil
	}
	return resolvedPath, nil, nil
}

func firstToolFailureResult(failureResult *agent.ToolResult, errorValue error, stage string) agent.ToolResult {
	if failureResult != nil {
		return *failureResult
	}
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, stage, errorValue.Error())
	}
	return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, stage, "invalid file preview request")
}

func cachedFilePreviewResultForInput(parts []agent.AgentPart, input filePreviewToolInput) (map[string]any, bool) {
	if materialID := strings.TrimSpace(input.MaterialID); materialID != "" {
		return cachedFilePreviewResultByMaterialID(parts, materialID)
	}
	return cachedFilePreviewResult(parts, strings.TrimSpace(input.Path))
}

func cachedFilePreviewResultByMaterialID(parts []agent.AgentPart, materialID string) (map[string]any, bool) {
	trimmedMaterialID := strings.TrimSpace(materialID)
	for _, part := range parts {
		if agentPartMaterialID(part) != trimmedMaterialID {
			continue
		}
		return cachedFilePreviewResultFromPart(part)
	}
	return nil, false
}

func cachedFilePreviewResult(parts []agent.AgentPart, path string) (map[string]any, bool) {
	for _, part := range parts {
		if part.File == nil || strings.TrimSpace(part.File.Path) != strings.TrimSpace(path) {
			continue
		}
		return cachedFilePreviewResultFromPart(part)
	}
	return nil, false
}

func cachedFilePreviewResultFromPart(part agent.AgentPart) (map[string]any, bool) {
	if part.File == nil {
		return nil, false
	}
	if strings.TrimSpace(part.File.MarkdownPreview) == "" && strings.TrimSpace(part.File.ConversionStatus) == "" {
		return nil, false
	}
	return filePreviewResult(
		part.File.Path,
		part.File.ContentType,
		part.File.SizeBytes,
		part.File.MarkdownPreview,
		firstNonEmptyString(part.File.ConversionStatus, "cached"),
		part.File.ConversionMessage,
	), true
}

func agentPartMaterialID(part agent.AgentPart) string {
	fileID := strings.TrimSpace(part.Source.FileID)
	if fileID == "" {
		return ""
	}
	return firstNonEmptyString(strings.TrimSpace(part.Source.Platform), "attachment") + ":" + fileID
}

func (toolCatalogBuilder *ToolCatalogBuilder) convertFilePreviewWithCapability(toolContext context.Context, request ToolCatalogRequest, path string, contentType string, sizeBytes int64) (agent.ToolResult, bool) {
	var response struct {
		Content      string          `json:"content"`
		IsError      bool            `json:"isError"`
		Status       string          `json:"status"`
		Message      string          `json:"message"`
		ErrorCode    string          `json:"errorCode"`
		FailureStage string          `json:"failureStage"`
		Result       json.RawMessage `json:"result"`
	}
	input := agent.MarshalToolInput(map[string]any{"path": path, "maxOutputBytes": maximumFilePreviewBytes})
	errorValue := toolCatalogBuilder.capabilityClient.PostJSON(toolContext, "/v1/tools/document.read/invoke", capabilityToolRequest("document.read", request, input), &response)
	if errorValue != nil || response.IsError || response.Status == "error" || response.Status == "denied" {
		return agent.ToolResult{}, false
	}
	var document struct {
		Content   string `json:"content"`
		Truncated bool   `json:"truncated"`
		Format    string `json:"format"`
	}
	if json.Unmarshal(response.Result, &document) != nil {
		document.Content = response.Content
	}
	conversionStatus := "converted"
	if document.Truncated {
		conversionStatus = "truncated"
	}
	result := filePreviewResult(path, contentType, sizeBytes, document.Content, conversionStatus, "")
	if strings.TrimSpace(document.Format) != "" {
		result["previewFormat"] = strings.TrimSpace(document.Format)
	}
	return agent.ToolSuccess(marshalToolResult(result)), true
}

func (toolCatalogBuilder *ToolCatalogBuilder) previewTextFile(toolContext context.Context, workspaceActor security.WorkspaceActor, path workspacepath.Path, contentType string, sizeBytes int64) agent.ToolResult {
	document, errorValue := workspaceActor.ReadFile(toolContext, path, maximumFilePreviewBytes+1)
	if errorValue != nil {
		if sizeBytes > maximumFilePreviewBytes {
			return agent.ToolSuccess(marshalToolResult(filePreviewResult(path.VirtualPath, contentType, sizeBytes, "", "unsupported", "file is too large for local text preview; use document.read/MarkItDown provider when available")))
		}
		return actorToolFailure("read_file", "file_preview", path.VirtualPath, errorValue)
	}
	isTruncated := len(document) > maximumFilePreviewBytes
	if isTruncated {
		document = document[:maximumFilePreviewBytes]
	}
	if !utf8.Valid(document) || bytes.IndexByte(document, 0) >= 0 {
		return agent.ToolSuccess(marshalToolResult(filePreviewResult(path.VirtualPath, contentType, sizeBytes, "", "unsupported", "file is not UTF-8 text and no MarkItDown preview is available")))
	}
	content, isContentTruncated := truncateTextByBytes(string(document), maximumFilePreviewBytes)
	conversionStatus := "converted"
	if isTruncated || isContentTruncated {
		conversionStatus = "truncated"
	}
	return agent.ToolSuccess(marshalToolResult(filePreviewResult(path.VirtualPath, contentType, sizeBytes, content, conversionStatus, "")))
}

func filePreviewResult(path string, contentType string, sizeBytes int64, markdownPreview string, conversionStatus string, conversionMessage string) map[string]any {
	return map[string]any{
		"path":              strings.TrimSpace(path),
		"filename":          filepath.Base(strings.TrimSpace(path)),
		"contentType":       strings.TrimSpace(contentType),
		"sizeBytes":         sizeBytes,
		"previewFormat":     "markdown",
		"markdownPreview":   strings.TrimSpace(markdownPreview),
		"conversionStatus":  strings.TrimSpace(conversionStatus),
		"conversionMessage": strings.TrimSpace(conversionMessage),
	}
}

func previewContentType(path string) string {
	if contentType := mime.TypeByExtension(filepath.Ext(strings.TrimSpace(path))); strings.TrimSpace(contentType) != "" {
		return strings.TrimSpace(contentType)
	}
	return "application/octet-stream"
}

func truncateTextByBytes(content string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len([]byte(content)) <= maxBytes {
		return content, false
	}
	document := []byte(content)
	if maxBytes > len(document) {
		maxBytes = len(document)
	}
	truncatedDocument := document[:maxBytes]
	for len(truncatedDocument) > 0 && !utf8.Valid(truncatedDocument) {
		truncatedDocument = truncatedDocument[:len(truncatedDocument)-1]
	}
	return string(truncatedDocument), true
}

func (toolCatalogBuilder *ToolCatalogBuilder) editFileTool(toolContext context.Context, input fileEditToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	edit := filePatchEditInput{Path: input.Path, OldText: input.OldText, NewText: input.NewText}
	patchInput := filePatchToolInput{Edits: []filePatchEditInput{edit}}
	result, errorValue := toolCatalogBuilder.patchFileTool(toolContext, patchInput, handlerContext)
	if result.Failed() && result.Failure.Stage == "file_patch" {
		result.Failure.Stage = "file_edit"
	}
	return result, errorValue
}

func (toolCatalogBuilder *ToolCatalogBuilder) patchFileTool(toolContext context.Context, input filePatchToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	if len(input.Edits) == 0 {
		return fileExactEditFailure("file_patch", "", -1, 0, "edits is required"), nil
	}
	if len(input.Edits) > 100 {
		return fileExactEditFailure("file_patch", "", -1, len(input.Edits), "too many edits; split the patch into smaller groups"), nil
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, handlerContext.request)
	if actorFailure != nil {
		return *actorFailure, nil
	}
	patchState := newFilePatchState()
	for editIndex, edit := range input.Edits {
		if result := toolCatalogBuilder.validatePatchEdit(toolContext, handlerContext, workspaceActor, patchState, edit, editIndex); result != nil {
			return *result, nil
		}
	}
	if result := writePatchState(toolContext, workspaceActor, patchState); result != nil {
		return *result, nil
	}
	return agent.ToolSuccess(marshalToolResult(map[string]any{
		"editedFiles": patchState.virtualPaths(),
		"editCount":   len(input.Edits),
	})), nil
}

type filePatchState struct {
	originalContents map[string]string
	currentContents  map[string]string
	resolvedPaths    map[string]ResolvedWorkspacePath
	pathOrder        []string
}

func newFilePatchState() *filePatchState {
	return &filePatchState{
		originalContents: map[string]string{},
		currentContents:  map[string]string{},
		resolvedPaths:    map[string]ResolvedWorkspacePath{},
	}
}

func (patchState *filePatchState) virtualPaths() []string {
	paths := []string{}
	for _, key := range patchState.pathOrder {
		paths = append(paths, patchState.resolvedPaths[key].VirtualPath)
	}
	return paths
}

func (toolCatalogBuilder *ToolCatalogBuilder) validatePatchEdit(toolContext context.Context, handlerContext toolHandlerContext, workspaceActor security.WorkspaceActor, patchState *filePatchState, edit filePatchEditInput, editIndex int) *agent.ToolResult {
	if strings.TrimSpace(edit.Path) == "" {
		result := fileExactEditFailure("file_patch", "", editIndex, 0, "path is required")
		return &result
	}
	if edit.OldText == "" {
		result := fileExactEditFailure("file_patch", strings.TrimSpace(edit.Path), editIndex, 0, "oldText is required")
		return &result
	}
	resolvedPath, result := toolCatalogBuilder.resolveEditableFilePath(toolContext, handlerContext, workspaceActor, strings.TrimSpace(edit.Path), patchState)
	if result != nil {
		return result
	}
	key := resolvedPath.ConcretePath
	currentContent := patchState.currentContents[key]
	matchCount := strings.Count(currentContent, edit.OldText)
	if matchCount != 1 {
		result := fileExactEditFailure("file_patch", resolvedPath.VirtualPath, editIndex, matchCount, "oldText must match exactly once; read the file and retry with a more specific snippet")
		return &result
	}
	patchState.currentContents[key] = strings.Replace(currentContent, edit.OldText, edit.NewText, 1)
	return nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) resolveEditableFilePath(toolContext context.Context, handlerContext toolHandlerContext, workspaceActor security.WorkspaceActor, path string, patchState *filePatchState) (ResolvedWorkspacePath, *agent.ToolResult) {
	scope := toolCatalogBuilder.workspaceScopeForToolContext(toolContext, handlerContext.request)
	resolvedPath, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).Resolve(path, scope)
	if errorValue != nil {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_patch", errorValue.Error())
		return ResolvedWorkspacePath{}, &result
	}
	if isManagedSitePackageManifestPath(resolvedPath.VirtualPath) {
		result := agent.ToolFailureResult(agent.FailurePolicyBlocked, agent.FailureCodes.PolicyBlocked, "file_patch", "site.app.create manages this build manifest; edit DESIGN.md and app source files instead of app/package.json")
		return ResolvedWorkspacePath{}, &result
	}
	if isImmutableSkillPath(toolCatalogBuilder.workspaceRootPath, resolvedPath.ConcretePath) {
		result := agent.ToolFailureResult(agent.FailurePolicyBlocked, agent.FailureCodes.PolicyBlocked, "file_patch", "file.patch cannot modify built-in skill files")
		return ResolvedWorkspacePath{}, &result
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionRead, resolvedPath.ConcretePath) || !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionWrite, resolvedPath.ConcretePath) {
		result := agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "file_patch", "current account cannot edit this file")
		return ResolvedWorkspacePath{}, &result
	}
	if _, isLoaded := patchState.currentContents[resolvedPath.ConcretePath]; isLoaded {
		return resolvedPath, nil
	}
	content, errorValue := workspaceActor.ReadFile(toolContext, resolvedPath, maximumEditableTextFileBytes+1)
	if errorValue != nil {
		result := actorToolFailure("read_file", "file_patch", resolvedPath.VirtualPath, errorValue)
		return ResolvedWorkspacePath{}, &result
	}
	if len(content) > maximumEditableTextFileBytes {
		result := fileExactEditFailure("file_patch", resolvedPath.VirtualPath, -1, 0, "file is too large for exact edit; rewrite a smaller generated file or use a more specific workflow")
		return ResolvedWorkspacePath{}, &result
	}
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_patch", "file.patch supports UTF-8 text files; use a specialized document or artifact tool for binary files")
		return ResolvedWorkspacePath{}, &result
	}
	patchState.originalContents[resolvedPath.ConcretePath] = string(content)
	patchState.currentContents[resolvedPath.ConcretePath] = string(content)
	patchState.resolvedPaths[resolvedPath.ConcretePath] = resolvedPath
	patchState.pathOrder = append(patchState.pathOrder, resolvedPath.ConcretePath)
	return resolvedPath, nil
}

func writePatchState(toolContext context.Context, workspaceActor security.WorkspaceActor, patchState *filePatchState) *agent.ToolResult {
	writtenKeys := []string{}
	for _, key := range patchState.pathOrder {
		resolvedPath := patchState.resolvedPaths[key]
		if errorValue := workspaceActor.WriteFile(toolContext, resolvedPath, []byte(patchState.currentContents[key]), 0660); errorValue != nil {
			rollbackPatchWrites(toolContext, workspaceActor, patchState, writtenKeys)
			result := actorToolFailure("write_file", "file_patch", resolvedPath.VirtualPath, errorValue)
			return &result
		}
		writtenKeys = append(writtenKeys, key)
	}
	return nil
}

func rollbackPatchWrites(toolContext context.Context, workspaceActor security.WorkspaceActor, patchState *filePatchState, writtenKeys []string) {
	for _, key := range writtenKeys {
		resolvedPath := patchState.resolvedPaths[key]
		_ = workspaceActor.WriteFile(toolContext, resolvedPath, []byte(patchState.originalContents[key]), 0660)
	}
}

func fileExactEditFailure(stage string, path string, editIndex int, matchCount int, guidance string) agent.ToolResult {
	content := marshalToolResult(map[string]any{
		"path":       strings.TrimSpace(path),
		"editIndex":  editIndex,
		"matchCount": matchCount,
		"guidance":   strings.TrimSpace(guidance),
	})
	result := agent.ToolFailureWithOutput(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, stage, content, json.RawMessage(content))
	result.Failure.Retryable = true
	result.Failure.SafeRetry = true
	result.Failure.RetryPolicy = "different_input"
	result.Failure.RecoveryHints = []agent.RecoveryHint{{
		Action:    "inspect_or_edit_text",
		ToolNames: []string{"file.read", "file.edit", "file.patch", "file.write"},
		Reason:    "Read the current file content, then retry with an exact oldText snippet or rewrite the full file.",
	}}
	return result
}

func isManagedSitePackageManifestPath(virtualPath string) bool {
	cleanPath := filepath.ToSlash(filepath.Clean(strings.TrimPrefix(strings.TrimSpace(virtualPath), "/workspace/")))
	parts := strings.Split(cleanPath, "/")
	if len(parts) == 5 &&
		parts[0] == "home" &&
		parts[1] == "sites" &&
		parts[3] == "app" &&
		parts[4] == "package.json" {
		return true
	}
	return len(parts) == 6 &&
		parts[0] == "home" &&
		parts[1] == "sites" &&
		parts[3] == "draft" &&
		parts[4] == "app" &&
		parts[5] == "package.json"
}

func managedSiteManifestProtectedFailure(path string) agent.ToolResult {
	content := marshalToolResult(map[string]string{
		"code":   "managed_manifest_protected",
		"path":   strings.TrimSpace(path),
		"detail": "site.app.create manages this build manifest; edit DESIGN.md and app source files instead of overwriting app/package.json",
	})
	return agent.ToolResult{
		Output: agent.ToolOutput{Content: content, Data: json.RawMessage(content)},
		Failure: &agent.ToolFailure{
			Kind:            agent.FailurePolicyBlocked,
			Code:            "managed_manifest_protected",
			Stage:           "file_write",
			UserSafeSummary: content,
			Retryable:       true,
			SafeRetry:       true,
		},
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) attachFileTool(toolContext context.Context, input fileAttachToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	scope := toolCatalogBuilder.workspaceScopeForToolContext(toolContext, handlerContext.request)
	attachmentInputs := normalizeFileAttachInputs(input)
	if len(attachmentInputs) == 0 {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_attach", "files must contain at least one path"), nil
	}
	attachments := []agent.FileAttachment{}
	for _, attachmentInput := range attachmentInputs {
		attachment, failureResult, errorValue := toolCatalogBuilder.fileAttachment(toolContext, attachmentInput, handlerContext, scope)
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
		Output:      agent.ToolOutput{Content: "files attached"},
		Attachments: attachments,
	}, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) promoteFileTool(toolContext context.Context, input filePromoteToolInput, handlerContext toolHandlerContext) (agent.ToolResult, error) {
	sourcePath := strings.TrimSpace(input.Path)
	if sourcePath == "" {
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
	source, errorValue := resolver.Resolve(sourcePath, scope)
	if errorValue != nil {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_promote", errorValue.Error()), nil
	}
	if source.Kind != workspacePathKindDraft {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_promote", "source path must come from tmp/<slug> draft work"), nil
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(handlerContext.request.PersonAccess, access.ActionRead, source.ConcretePath) {
		return agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "file_promote", "current account cannot read the promotion source"), nil
	}
	sourceInformation, errorValue := workspaceActor.Stat(toolContext, source)
	if errorValue != nil {
		return actorToolFailure("stat", "file_promote", source.VirtualPath, errorValue), nil
	}
	if !sourceInformation.IsRegular {
		return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_promote", "source path is a directory or non-file; promote each output file separately, for example tmp/<slug>/build/deck.html and tmp/<slug>/build/deck.pptx"), nil
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
	return agent.ToolSuccess(marshalToolResult(map[string]any{
		"path": destination.VirtualPath,
	})), nil
}

func normalizeFileAttachInputs(input fileAttachToolInput) []fileAttachFileInput {
	if len(input.Files) > 0 {
		return input.Files
	}
	if strings.TrimSpace(input.Path) == "" {
		return nil
	}
	return []fileAttachFileInput{{
		Path:        input.Path,
		Filename:    input.Filename,
		ContentType: input.ContentType,
		Title:       input.Title,
	}}
}

func (toolCatalogBuilder *ToolCatalogBuilder) fileAttachment(toolContext context.Context, input fileAttachFileInput, handlerContext toolHandlerContext, scope WorkspaceScope) (agent.FileAttachment, *agent.ToolResult, error) {
	path := strings.TrimSpace(input.Path)
	if path == "" {
		result := agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, "file_attach", "attachment path is required")
		return agent.FileAttachment{}, &result, nil
	}
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
		if isWorkspaceManagedEnvironmentName(name) || strings.TrimSpace(result[name]) == "" {
			result[name] = value
		}
	}
	return result
}

func isWorkspaceManagedEnvironmentName(name string) bool {
	switch name {
	case "BLUECLAW_REQUESTER_TMP",
		"BLUECLAW_TASK_TMP",
		"BLUECLAW_REQUESTER_ARTIFACTS",
		"BLUECLAW_DEPENDENCY_CACHE",
		"HOME",
		"PATH",
		"TMPDIR",
		"TMP",
		"TEMP",
		"XDG_CACHE_HOME",
		"XDG_CONFIG_HOME",
		"XDG_RUNTIME_DIR",
		"BUN_TMPDIR",
		"BUN_INSTALL",
		"BUN_INSTALL_CACHE_DIR",
		"npm_config_cache":
		return true
	default:
		return false
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) canAccessWorkspacePath(personAccess policy.PersonAccess, action string, path string) bool {
	resource := access.ResourceForWorkspacePath(toolCatalogBuilder.workspaceRootPath, path)
	return access.CanAccess(access.Request{
		PersonAccess: personAccess,
		Action:       action,
		Resource:     resource,
	})
}

func attachmentFilename(input fileAttachFileInput, resolvedPath string) string {
	if strings.TrimSpace(input.Filename) != "" {
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

func resolveReadableAttachmentMaterial(toolContext context.Context, request ToolCatalogRequest, materialID string) (agent.VisibleContextMaterial, error) {
	if request.AttachmentMaterialResolver == nil {
		return agent.VisibleContextMaterial{}, errors.New("attachment material resolver is unavailable")
	}
	material, errorValue := request.AttachmentMaterialResolver.ResolveAttachmentMaterial(toolContext, materialID)
	if errorValue != nil {
		return agent.VisibleContextMaterial{}, errorValue
	}
	if strings.TrimSpace(material.Path) == "" {
		return agent.VisibleContextMaterial{}, errors.New("attachment material has no readable workspace path")
	}
	return material, nil
}

func validateAttachmentMaterialTool(toolName string, material agent.VisibleContextMaterial) error {
	contentType := strings.ToLower(strings.TrimSpace(material.ContentType))
	switch strings.TrimSpace(toolName) {
	case "image.read":
		if contentType != "" && !strings.HasPrefix(contentType, "image/") {
			return errors.New("attachment material is not an image; use document.read")
		}
	case "document.read":
		if strings.HasPrefix(contentType, "image/") {
			return errors.New("attachment material is an image; use image.read")
		}
	}
	return nil
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

func siteToolNeedsSourceBundle(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "site.app.publish", "site.app.preview":
		return true
	default:
		return false
	}
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
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, request)
	if actorFailure != nil {
		return nil, actorFailure, nil
	}
	resolvedSourcePath, errorValue := toolCatalogBuilder.resolveSiteProjectSourceWorkspace(toolContext, request, workspaceActor, siteProjectResolutionInput{
		SiteID:              firstNonEmptyString(siteIDFromInputDocument(inputDocument), siteProjectIDFromPath(sourceWorkspacePath)),
		SourceWorkspacePath: sourceWorkspacePath,
	})
	if errorValue != nil {
		return nil, nil, errorValue
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(request.PersonAccess, access.ActionRead, resolvedSourcePath.ConcretePath) {
		return nil, nil, errors.New("current account cannot publish this site workspace path")
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
	switch strings.TrimSpace(toolName) {
	case "site.app.create":
		return toolCatalogBuilder.materializeSiteCreateResult(toolContext, request, result)
	case "site.app.status":
		return toolCatalogBuilder.annotateSiteStatusResult(toolContext, request, result)
	default:
		return nil, nil
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) materializeSiteCreateResult(toolContext context.Context, request ToolCatalogRequest, result *json.RawMessage) (*agent.ToolResult, error) {
	site, errorValue := decodeSiteCreateResult(*result)
	if errorValue != nil {
		return nil, errorValue
	}
	if strings.TrimSpace(site.SourceWorkspacePath) == "" {
		site.SourceWorkspacePath = defaultSiteSourceWorkspacePath(map[string]any{"siteID": site.SiteID})
	}
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, request)
	if actorFailure != nil {
		return actorFailure, nil
	}
	sourceWorkspace, errorValue := toolCatalogBuilder.resolveSiteProjectSourceWorkspace(toolContext, request, workspaceActor, siteProjectResolutionInput{
		SiteID:              site.SiteID,
		SourceWorkspacePath: site.SourceWorkspacePath,
	})
	if errorValue != nil {
		return nil, errorValue
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(request.PersonAccess, access.ActionWrite, sourceWorkspace.ConcretePath) {
		return toolResultPointer(agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "site_source_workspace", "current account cannot write this site workspace path")), nil
	}
	if toolFailure := writeSiteStarterFiles(toolContext, workspaceActor, workspacepath.Directory(sourceWorkspace), site); toolFailure != nil {
		return toolFailure, nil
	}
	site.SourceWorkspacePath = sourceWorkspace.VirtualPath
	site.WorkspacePath = sourceWorkspace.VirtualPath
	site.AppWorkspacePath = filepath.ToSlash(filepath.Join(sourceWorkspace.VirtualPath, "app"))
	site.UIPrimitiveImports = siteUIPrimitiveImports()
	site.SourceGuidance = siteSourceGuidance()
	document, errorValue := json.Marshal(site)
	if errorValue != nil {
		return nil, errorValue
	}
	*result = document
	return nil, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) annotateSiteStatusResult(toolContext context.Context, request ToolCatalogRequest, result *json.RawMessage) (*agent.ToolResult, error) {
	document := map[string]any{}
	if len(bytes.TrimSpace(*result)) == 0 {
		return nil, nil
	}
	if errorValue := json.Unmarshal(*result, &document); errorValue != nil {
		return nil, errorValue
	}
	sourceWorkspacePath, _ := document["sourceWorkspacePath"].(string)
	if strings.TrimSpace(sourceWorkspacePath) == "" {
		return nil, nil
	}
	siteID, _ := document["siteID"].(string)
	health := toolCatalogBuilder.siteWorkspaceHealth(toolContext, request, siteID, sourceWorkspacePath)
	if resolvedSourceWorkspacePath, _ := health["sourceWorkspacePath"].(string); strings.TrimSpace(resolvedSourceWorkspacePath) != "" {
		document["sourceWorkspacePath"] = resolvedSourceWorkspacePath
		document["draftPath"] = resolvedSourceWorkspacePath
		if siteID := firstNonEmptyString(siteID, siteProjectIDFromPath(resolvedSourceWorkspacePath)); siteID != "" {
			document["workspacePath"] = filepath.ToSlash(filepath.Join("home", "sites", siteID))
		}
	}
	if resolvedAppWorkspacePath, _ := health["appWorkspacePath"].(string); strings.TrimSpace(resolvedAppWorkspacePath) != "" {
		document["appWorkspacePath"] = resolvedAppWorkspacePath
	}
	document["workspaceHealth"] = health["status"]
	document["workspaceHealthDetails"] = health
	annotatedDocument, errorValue := json.Marshal(document)
	if errorValue != nil {
		return nil, errorValue
	}
	*result = annotatedDocument
	return nil, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) siteWorkspaceHealth(toolContext context.Context, request ToolCatalogRequest, siteID string, sourceWorkspacePath string) map[string]any {
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, request)
	if actorFailure != nil {
		return map[string]any{"status": "permission_problem", "path": sourceWorkspacePath, "suggestedNextTool": "site.app.repair"}
	}
	resolvedSourcePath, errorValue := toolCatalogBuilder.resolveSiteProjectSourceWorkspace(toolContext, request, workspaceActor, siteProjectResolutionInput{
		SiteID:              firstNonEmptyString(siteID, siteProjectIDFromPath(sourceWorkspacePath)),
		SourceWorkspacePath: sourceWorkspacePath,
	})
	if errorValue != nil {
		return map[string]any{"status": "unknown", "reason": errorValue.Error(), "suggestedNextTool": "site.app.status"}
	}
	if !toolCatalogBuilder.canAccessWorkspacePath(request.PersonAccess, access.ActionWrite, resolvedSourcePath.ConcretePath) {
		return map[string]any{"status": "permission_problem", "path": resolvedSourcePath.VirtualPath, "sourceWorkspacePath": resolvedSourcePath.VirtualPath, "suggestedNextTool": "site.app.repair"}
	}
	sourceStat, errorValue := workspaceActor.Stat(toolContext, workspacepath.Path(resolvedSourcePath))
	if errorValue != nil || !sourceStat.IsDirectory {
		return map[string]any{"status": "missing", "path": resolvedSourcePath.VirtualPath, "sourceWorkspacePath": resolvedSourcePath.VirtualPath, "suggestedNextTool": "site.app.repair"}
	}
	appPath := workspacepath.Path{
		ConcretePath: filepath.Join(resolvedSourcePath.ConcretePath, "app"),
		VirtualPath:  filepath.ToSlash(filepath.Join(resolvedSourcePath.VirtualPath, "app")),
		Kind:         resolvedSourcePath.Kind,
	}
	appStat, errorValue := workspaceActor.Stat(toolContext, appPath)
	if errorValue != nil || !appStat.IsDirectory {
		return map[string]any{"status": "missing", "path": appPath.VirtualPath, "sourceWorkspacePath": resolvedSourcePath.VirtualPath, "appWorkspacePath": appPath.VirtualPath, "suggestedNextTool": "site.app.repair"}
	}
	buildStatus := siteBuildStatus(resolvedSourcePath.ConcretePath)
	if buildStatus != "fresh" {
		return map[string]any{"status": "stale_build", "path": appPath.VirtualPath, "sourceWorkspacePath": resolvedSourcePath.VirtualPath, "appWorkspacePath": appPath.VirtualPath, "buildStatus": buildStatus, "suggestedNextTool": "site.app.build"}
	}
	return map[string]any{"status": "ready", "path": appPath.VirtualPath, "sourceWorkspacePath": resolvedSourcePath.VirtualPath, "appWorkspacePath": appPath.VirtualPath, "buildStatus": buildStatus, "suggestedNextTool": ""}
}

func siteBuildStatus(sourceWorkspacePath string) string {
	distPath := filepath.Join(sourceWorkspacePath, "app", "dist")
	qualityPath := filepath.Join(sourceWorkspacePath, ".internkim", "build-quality.json")
	if !isLocalDirectory(distPath) {
		return "missing_dist"
	}
	if !isLocalRegularFile(filepath.Join(distPath, "index.html")) {
		return "missing_index"
	}
	if !isLocalRegularFile(qualityPath) {
		return "missing_quality"
	}
	latestSourceModTime := latestLocalModTime(filepath.Join(sourceWorkspacePath, "app", "src"))
	qualityInformation, errorValue := os.Stat(qualityPath)
	if errorValue != nil {
		return "missing_quality"
	}
	indexInformation, errorValue := os.Stat(filepath.Join(distPath, "index.html"))
	if errorValue != nil {
		return "missing_index"
	}
	if latestSourceModTime.After(qualityInformation.ModTime()) || latestSourceModTime.After(indexInformation.ModTime()) {
		return "stale"
	}
	return "fresh"
}

func latestLocalModTime(path string) time.Time {
	latestModTime := time.Time{}
	_ = filepath.Walk(path, func(currentPath string, information os.FileInfo, walkError error) error {
		if walkError != nil || information == nil || information.IsDir() {
			return nil
		}
		if information.ModTime().After(latestModTime) {
			latestModTime = information.ModTime()
		}
		_ = currentPath
		return nil
	})
	return latestModTime
}

func isLocalDirectory(path string) bool {
	information, errorValue := os.Stat(path)
	return errorValue == nil && information.IsDir()
}

func isLocalRegularFile(path string) bool {
	information, errorValue := os.Stat(path)
	return errorValue == nil && information.Mode().IsRegular()
}

type siteCreateResult struct {
	SiteID              string           `json:"siteID"`
	Slug                string           `json:"slug"`
	Title               string           `json:"title"`
	Description         string           `json:"description"`
	Idea                string           `json:"idea"`
	OriginalPrompt      string           `json:"originalPrompt"`
	Purpose             string           `json:"purpose"`
	Audience            string           `json:"audience"`
	Archetype           string           `json:"archetype"`
	DomainKeywords      []string         `json:"domainKeywords"`
	CreatedBy           map[string]any   `json:"createdBy"`
	OwnerIdentity       map[string]any   `json:"ownerIdentity"`
	Collaborators       []map[string]any `json:"collaborators"`
	PublishedURL        string           `json:"publishedURL"`
	WorkspacePath       string           `json:"workspacePath"`
	SourceWorkspacePath string           `json:"sourceWorkspacePath"`
	AppWorkspacePath    string           `json:"appWorkspacePath"`
	UIPrimitiveImports  []string         `json:"uiPrimitiveImports,omitempty"`
	SourceGuidance      string           `json:"sourceGuidance,omitempty"`
	Status              string           `json:"status"`
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
	files := []siteStarterFile{
		{Path: ".internkim/site.json", Content: siteWorkspaceMetadata(site)},
		{Path: ".internkim/idea.md", Content: siteIdeaMarkdown(site)},
		{Path: "DESIGN.md", Content: siteDesignDocument(site)},
	}
	files = append(files, siteAppScaffoldFiles(site)...)
	files = append(files,
		siteStarterFile{Path: "pocketbase/pb_migrations/.gitkeep", Content: ""},
	)
	return files
}

func siteWorkspaceMetadata(site siteCreateResult) string {
	document, errorValue := json.MarshalIndent(map[string]any{
		"siteID":         site.SiteID,
		"slug":           site.Slug,
		"title":          site.Title,
		"publishedURL":   site.PublishedURL,
		"description":    firstNonEmptyString(site.Description, "Editable website prototype project."),
		"idea":           firstNonEmptyString(site.Idea, site.OriginalPrompt, "Original site idea should be refined before publish."),
		"originalPrompt": site.OriginalPrompt,
		"purpose":        firstNonEmptyString(site.Purpose, "prototype for idea validation"),
		"audience":       site.Audience,
		"archetype":      firstNonEmptyString(site.Archetype, "landing"),
		"domainKeywords": site.DomainKeywords,
		"createdBy":      site.CreatedBy,
		"owner":          site.OwnerIdentity,
		"collaborators":  site.Collaborators,
		"stack":          "React + Vite + TypeScript + Tailwind + shadcn/ui scaffold with Stitch DESIGN.md",
		"uiPrimitives":   siteUIPrimitiveImports(),
		"designDefault":  "starter scaffold only; customize through a black-on-white DESIGN.md before publish",
		"sourceContract": "editable source is owned by the requester actor",
	}, "", "  ")
	if errorValue != nil {
		return "{}\n"
	}
	return string(document) + "\n"
}

func siteUIPrimitiveImports() []string {
	return []string{
		"Badge from ./components/ui/badge",
		"Button from ./components/ui/button",
		"Card, CardContent, CardDescription, CardHeader, CardTitle from ./components/ui/card",
		"Input from ./components/ui/input",
		"Textarea from ./components/ui/textarea",
		"Separator from ./components/ui/separator",
		"Tabs, TabsContent, TabsList, TabsTrigger from ./components/ui/tabs",
		"Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger from ./components/ui/dialog",
	}
}

func siteSourceGuidance() string {
	return "Use local shadcn-style primitives from ./components/ui/* and customize App.tsx, prototype-data.ts, and index.css. Default to black-on-white minimal styling unless the user asks for another direction. Keep package.json, Vite, Tailwind, and scripts managed."
}

func siteIdeaMarkdown(site siteCreateResult) string {
	title := firstNonEmptyString(site.Title, site.Slug)
	summary := firstNonEmptyString(site.Description, title)
	idea := firstNonEmptyString(site.Idea, site.OriginalPrompt, "Refine this file with the user's site idea before publish.")
	return "# Site Idea\n\n## Summary\n" + summary + "\n\n## Original Idea\n" + idea + "\n\n## Audience\n" + firstNonEmptyString(site.Audience, "Unspecified") + "\n\n## Purpose\n" + firstNonEmptyString(site.Purpose, "prototype") + "\n\n## Archetype\n" + firstNonEmptyString(site.Archetype, "landing") + "\n"
}

func siteDesignDocument(site siteCreateResult) string {
	title := html.EscapeString(firstNonEmptyString(site.Title, site.Slug))
	return "# " + title + " DESIGN.md\n\n## Product\n\nEditable scaffold for a website prototype. Replace this file with a request-specific design system before publishing user-facing work.\n\n## Audience\n\nDefine the primary user and what they are trying to accomplish.\n\n## Prototype Scope\n\nDescribe what works in the first publish and what is intentionally deferred.\n\n## Visual Direction\n\nDefault to black-on-white minimal styling: white background, near-black text, quiet gray borders, restrained monochrome controls, and color only when the domain clearly benefits from it. Choose typography, spacing, layout density, interaction feel, and responsive behavior for this specific request.\n\n## Screens\n\nList the screens and states included in the prototype.\n\n## Workflows\n\nDescribe the main interaction paths the user can try.\n\n## Data Model\n\nDefine local state, fake data, PocketBase collections, files, or realtime behavior.\n\n## Implemented Now\n\nReplace this scaffold with the implemented feature set before publishing.\n\n## Next Iterations\n\nRecord follow-up work for longer projects.\n\n## Acceptance Criteria\n\nList the checks that must pass before publish.\n"
}

func toolResultPointer(result agent.ToolResult) *agent.ToolResult {
	return &result
}

func (toolCatalogBuilder *ToolCatalogBuilder) validateCapabilityToolInputAccess(toolName string, request ToolCatalogRequest, toolInput json.RawMessage) error {
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
	return canonicalSiteSourceWorkspacePath(strings.TrimSpace(siteID))
}

func siteIDFromInputDocument(inputDocument map[string]any) string {
	siteID, isString := inputDocument["siteID"].(string)
	if !isString {
		return ""
	}
	return strings.TrimSpace(siteID)
}

func canonicalSiteSourceWorkspacePath(siteID string) string {
	if strings.TrimSpace(siteID) == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Join(canonicalSiteProjectWorkspacePath(siteID), "draft"))
}

func canonicalSiteProjectWorkspacePath(siteID string) string {
	if strings.TrimSpace(siteID) == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Join("home", "sites", strings.TrimSpace(siteID)))
}

func sourceWorkspacePathFromSiteAppWorkspacePath(appWorkspacePath string) string {
	cleanPath := strings.TrimSuffix(filepath.ToSlash(strings.TrimSpace(appWorkspacePath)), "/")
	if cleanPath == "" {
		return ""
	}
	if strings.HasSuffix(cleanPath, "/app") {
		return strings.TrimSuffix(cleanPath, "/app")
	}
	return ""
}

func siteProjectIDFromPath(path string) string {
	cleanPath := strings.Trim(strings.TrimSpace(filepath.ToSlash(path)), "/")
	for _, prefix := range []string{"home/sites/", "workspace/sites/", "sites/"} {
		if siteID := pathSegmentAfterPrefix(cleanPath, prefix); siteID != "" {
			return siteID
		}
	}
	parts := strings.Split(cleanPath, "/")
	for index := 0; index+1 < len(parts); index++ {
		if parts[index] == "sites" && parts[index+1] != "" {
			return parts[index+1]
		}
	}
	return ""
}

func pathSegmentAfterPrefix(path string, prefix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	remainder := strings.TrimPrefix(path, prefix)
	segment, _, _ := strings.Cut(remainder, "/")
	return strings.TrimSpace(segment)
}

func siteWorkspacePathCandidates(sourceWorkspacePath string, siteID string) []string {
	candidates := []string{}
	canonicalPath := canonicalSiteSourceWorkspacePath(siteID)
	if canonicalPath != "" && shouldPreferCanonicalSiteWorkspacePath(sourceWorkspacePath) {
		candidates = append(candidates, canonicalPath)
	}
	if strings.TrimSpace(sourceWorkspacePath) != "" {
		candidates = append(candidates, strings.TrimSpace(sourceWorkspacePath))
	}
	if canonicalPath != "" && !shouldPreferCanonicalSiteWorkspacePath(sourceWorkspacePath) {
		candidates = append(candidates, canonicalPath)
	}
	return stableUniqueStrings(candidates)
}

func shouldPreferCanonicalSiteWorkspacePath(path string) bool {
	cleanPath := strings.Trim(strings.TrimSpace(filepath.ToSlash(path)), "/")
	return cleanPath == "" ||
		strings.HasPrefix(cleanPath, "home/sites/") ||
		strings.HasPrefix(cleanPath, "workspace/sites/") ||
		strings.HasPrefix(cleanPath, "workspace/private/people/") ||
		strings.HasPrefix(cleanPath, "sites/")
}

func stableUniqueStrings(values []string) []string {
	seenValues := map[string]bool{}
	uniqueValues := []string{}
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" || seenValues[trimmedValue] {
			continue
		}
		seenValues[trimmedValue] = true
		uniqueValues = append(uniqueValues, trimmedValue)
	}
	return uniqueValues
}

func (toolCatalogBuilder *ToolCatalogBuilder) resolveSiteProjectSourceWorkspace(toolContext context.Context, request ToolCatalogRequest, workspaceActor security.WorkspaceActor, input siteProjectResolutionInput) (ResolvedWorkspacePath, error) {
	scope := WorkspaceScopeForRequest(toolCatalogBuilder.workspaceRootPath, request, agent.TaskRunIDFromContext(toolContext))
	candidates := siteWorkspacePathCandidates(input.SourceWorkspacePath, firstNonEmptyString(input.SiteID, siteProjectIDFromPath(input.SourceWorkspacePath)))
	if len(candidates) == 0 {
		return ResolvedWorkspacePath{}, errors.New("site sourceWorkspacePath could not be resolved")
	}
	var firstResolvedPath *ResolvedWorkspacePath
	var lastError error
	for _, candidate := range candidates {
		resolvedPath, errorValue := NewWorkspacePathResolver(toolCatalogBuilder.workspaceRootPath).ResolveDirectory(candidate, scope)
		if errorValue != nil {
			lastError = errorValue
			continue
		}
		if !toolCatalogBuilder.canAccessWorkspacePath(request.PersonAccess, access.ActionWrite, resolvedPath.ConcretePath) {
			lastError = errors.New("current account cannot use this site workspace path: " + resolvedPath.VirtualPath)
			continue
		}
		if firstResolvedPath == nil {
			resolvedPathCopy := resolvedPath
			firstResolvedPath = &resolvedPathCopy
		}
		sourceStat, errorValue := workspaceActor.Stat(toolContext, workspacepath.Path(resolvedPath))
		if errorValue == nil && sourceStat.IsDirectory {
			return resolvedPath, nil
		}
	}
	if firstResolvedPath != nil {
		return *firstResolvedPath, nil
	}
	if lastError != nil {
		return ResolvedWorkspacePath{}, lastError
	}
	return ResolvedWorkspacePath{}, errors.New("site sourceWorkspacePath could not be resolved")
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
