package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	ScheduledRun               agent.ScheduledRunContext
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

type historyToolInput struct {
	HistoryCursor string `json:"historyCursor"`
	Limit         int    `json:"limit"`
	Direction     string `json:"direction"`
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
	toolCatalogBuilder.registerTaskHistoryTool(toolSet, request)
	toolCatalogBuilder.registerMemoryTool(toolSet, request)
	toolCatalogBuilder.registerBuiltInTools(toolSet, handlerContext)
	toolCatalogBuilder.registerMCPTools(toolSet)
	toolCatalogBuilder.registerCapabilityTools(toolSet, request)
	toolCatalogBuilder.registerSkillSearchTool(toolSet, handlerContext, toolSet)
	return toolSet
}

func (toolCatalogBuilder *ToolCatalogBuilder) allowedToolNames(profileName string) []string {
	normalizedProfileName := normalizeProfileName(profileName)
	if allowedToolNames, isFound := toolCatalogBuilder.allowedToolNamesByProfile[normalizedProfileName]; isFound {
		return trimNonEmptyStrings(allowedToolNames)
	}
	if len(toolCatalogBuilder.fallbackAllowedToolNames) > 0 {
		return trimNonEmptyStrings(toolCatalogBuilder.fallbackAllowedToolNames)
	}
	return DefaultAllowedToolNames()
}

func DefaultAllowedToolNames() []string {
	return agent.KernelToolNames()
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
	toolCatalogBuilder.registerMathTool(toolRegistry)
	toolCatalogBuilder.registerTerminalTools(toolRegistry, handlerContext)
	toolCatalogBuilder.registerBrowserHandoffTool(toolRegistry, handlerContext)
	toolCatalogBuilder.registerSiteTools(toolRegistry, handlerContext)
	toolCatalogBuilder.registerAskTools(toolRegistry, handlerContext)
	toolCatalogBuilder.registerFileTools(toolRegistry, handlerContext)
	toolCatalogBuilder.registerArtifactDeliveryTool(toolRegistry, handlerContext)
	toolCatalogBuilder.registerScheduleTools(toolRegistry, handlerContext)
	toolCatalogBuilder.registerSkillManagementTools(toolRegistry)
	toolCatalogBuilder.registerDatabaseTools(toolRegistry, handlerContext)
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
	switch actorFailureCode(errorValue) {
	case security.ActorErrorCodePermissionDenied:
		failureKind = agent.FailurePermissionDenied
		failureCode = agent.FailureCodes.AccessDenied
	case security.ActorErrorCodeNotFound:
		failureKind = agent.FailureNotFound
		failureCode = agent.FailureCodes.NotFound
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
