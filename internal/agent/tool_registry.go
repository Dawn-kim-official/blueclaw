package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

type ToolDefinition struct {
	Name             string           `json:"name"`
	Description      string           `json:"description"`
	RecoveryCard     ToolRecoveryCard `json:"recoveryCard,omitempty"`
	InputSchema      json.RawMessage  `json:"inputSchema,omitempty"`
	OutputSchema     json.RawMessage  `json:"outputSchema,omitempty"`
	PolicyResource   string           `json:"policyResource,omitempty"`
	SideEffectClass  string           `json:"sideEffectClass,omitempty"`
	RequiresApproval bool             `json:"requiresApproval,omitempty"`
}

type ToolRecoveryCard struct {
	Does       string `json:"does,omitempty"`
	Produces   string `json:"produces,omitempty"`
	SideEffect string `json:"sideEffect,omitempty"`
	UseWhen    string `json:"useWhen,omitempty"`
	AvoidWhen  string `json:"avoidWhen,omitempty"`
}

const (
	ToolSideEffectNone           = "none"
	ToolSideEffectRead           = "read"
	ToolSideEffectComputation    = "computation"
	ToolSideEffectStateChange    = "state_change"
	ToolSideEffectWorkspaceWrite = "workspace_write"
	ToolSideEffectExternalWrite  = "external_write"
)

type ToolInvocation struct {
	ToolName string          `json:"toolName"`
	Input    json.RawMessage `json:"input"`
}

type FileAttachment struct {
	DevicePath    string `json:"devicePath"`
	Filename      string `json:"filename,omitempty"`
	ContentType   string `json:"contentType,omitempty"`
	SizeBytes     int64  `json:"sizeBytes,omitempty"`
	Title         string `json:"title,omitempty"`
	ContentBase64 string `json:"-"`
}

type RecoveryAction struct {
	Kind           string `json:"kind"`
	Delivery       string `json:"delivery"`
	DownloadURL    string `json:"downloadURL,omitempty"`
	ConnectCommand string `json:"connectCommand,omitempty"`
	PlatformUserID string `json:"platformUserID,omitempty"`
}

type RecoveryHint struct {
	Action        string   `json:"action,omitempty"`
	ToolNames     []string `json:"toolNames,omitempty"`
	Reason        string   `json:"reason,omitempty"`
	Preconditions []string `json:"preconditions,omitempty"`
}

type DiagnosticArtifact struct {
	Path        string `json:"path,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Description string `json:"description,omitempty"`
}

type AffectedResource struct {
	Path   string `json:"path,omitempty"`
	Role   string `json:"role,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type ToolOutput struct {
	Content string          `json:"content,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type FailureKind string

const (
	FailureDependencyUnavailable FailureKind = "dependency_unavailable"
	FailurePermissionDenied      FailureKind = "permission_denied"
	FailureInvalidInput          FailureKind = "invalid_input"
	FailureNotFound              FailureKind = "not_found"
	FailureRateLimited           FailureKind = "rate_limited"
	FailureExternalService       FailureKind = "external_service"
	FailurePolicyBlocked         FailureKind = "policy_blocked"
	FailureUnknown               FailureKind = "unknown"
)

type FailureCode string

var FailureCodes = struct {
	Unavailable     FailureCode
	InvalidInput    FailureCode
	AccessDenied    FailureCode
	Conflict        FailureCode
	NotFound        FailureCode
	OperationFailed FailureCode
	PolicyBlocked   FailureCode
	RateLimited     FailureCode
	ToolNameInShell FailureCode
}{
	Unavailable:     "unavailable",
	InvalidInput:    "invalid_input",
	AccessDenied:    "access_denied",
	Conflict:        "conflict",
	NotFound:        "not_found",
	OperationFailed: "operation_failed",
	PolicyBlocked:   "policy_blocked",
	RateLimited:     "rate_limited",
	ToolNameInShell: "tool_name_in_terminal",
}

func (failureCode FailureCode) String() string {
	return strings.TrimSpace(string(failureCode))
}

func CanonicalFailureCode(code FailureCode) string {
	trimmedCode := code.String()
	switch trimmedCode {
	case "unavailable", "memory_search_unavailable", "memory.search.unavailable", "tool.unavailable", "memory_queue_unavailable", "terminal_service_unavailable", "companion_bridge_unavailable", "schedule_repository_unavailable":
		return FailureCodes.Unavailable.String()
	case "tool.input.invalid", "invalid_input", "approval_message_required":
		return FailureCodes.InvalidInput.String()
	case "tool_name_in_terminal":
		return FailureCodes.ToolNameInShell.String()
	case "tool.not_allowed":
		return FailureCodes.PolicyBlocked.String()
	case "access_denied", "permission_denied", "workspace_path_denied":
		return FailureCodes.AccessDenied.String()
	case "conflict":
		return FailureCodes.Conflict.String()
	case "tool.not_registered", "not_found":
		return FailureCodes.NotFound.String()
	case "tool.failed", "tool_failed", "operation_failed":
		return FailureCodes.OperationFailed.String()
	case "policy_blocked":
		return FailureCodes.PolicyBlocked.String()
	case "rate_limited", "too_many_requests":
		return FailureCodes.RateLimited.String()
	case "":
		return FailureCodes.OperationFailed.String()
	default:
		return classifyFailureCodeText(trimmedCode)
	}
}

func classifyFailureCodeText(code string) string {
	normalizedCode := strings.ToLower(strings.TrimSpace(code))
	if strings.Contains(normalizedCode, "unavailable") {
		return FailureCodes.Unavailable.String()
	}
	if strings.Contains(normalizedCode, "not_found") || strings.Contains(normalizedCode, "not.found") {
		return FailureCodes.NotFound.String()
	}
	if strings.Contains(normalizedCode, "denied") || strings.Contains(normalizedCode, "permission") || strings.Contains(normalizedCode, "unauthorized") {
		return FailureCodes.AccessDenied.String()
	}
	if strings.Contains(normalizedCode, "invalid") {
		return FailureCodes.InvalidInput.String()
	}
	if strings.Contains(normalizedCode, "conflict") || strings.Contains(normalizedCode, "duplicate") {
		return FailureCodes.Conflict.String()
	}
	if strings.Contains(normalizedCode, "rate_limited") || strings.Contains(normalizedCode, "too_many") {
		return FailureCodes.RateLimited.String()
	}
	return FailureCodes.OperationFailed.String()
}

type ToolFailure struct {
	Kind                  FailureKind          `json:"kind"`
	Code                  string               `json:"code"`
	Stage                 string               `json:"stage,omitempty"`
	UserSafeSummary       string               `json:"userSafeSummary,omitempty"`
	Retryable             bool                 `json:"retryable,omitempty"`
	SafeRetry             bool                 `json:"safeRetry,omitempty"`
	FailureClass          string               `json:"failureClass,omitempty"`
	RetryPolicy           string               `json:"retryPolicy,omitempty"`
	RequiredPreconditions []string             `json:"requiredPreconditions,omitempty"`
	RecoveryHints         []RecoveryHint       `json:"recoveryHints,omitempty"`
	DiagnosticArtifacts   []DiagnosticArtifact `json:"diagnosticArtifacts,omitempty"`
	AffectedResources     []AffectedResource   `json:"affectedResources,omitempty"`
}

type ToolResult struct {
	Output          ToolOutput       `json:"output,omitempty"`
	Failure         *ToolFailure     `json:"failure,omitempty"`
	Attachments     []FileAttachment `json:"attachments,omitempty"`
	RecoveryActions []RecoveryAction `json:"recoveryActions,omitempty"`
}

func ToolSuccess(content string) ToolResult {
	return ToolResult{Output: ToolOutput{Content: content}}
}

func ToolSuccessData(content string, data json.RawMessage) ToolResult {
	return ToolResult{Output: ToolOutput{Content: content, Data: data}}
}

func ToolFailureResult(kind FailureKind, code FailureCode, stage string, summary string) ToolResult {
	return ToolResult{
		Output: ToolOutput{Content: summary},
		Failure: &ToolFailure{
			Kind:            normalizeFailureKind(kind),
			Code:            CanonicalFailureCode(code),
			Stage:           strings.TrimSpace(stage),
			UserSafeSummary: strings.TrimSpace(summary),
		},
	}
}

func ToolFailureWithOutput(kind FailureKind, code FailureCode, stage string, summary string, data json.RawMessage) ToolResult {
	result := ToolFailureResult(kind, code, stage, summary)
	result.Output.Data = data
	return result
}

func ToolFailureData(kind FailureKind, code FailureCode, stage string, summary string, data json.RawMessage) ToolResult {
	return ToolFailureWithOutput(kind, code, stage, summary, data)
}

func ToolInputFailure(message string) ToolResult {
	return ToolFailureResult(FailureInvalidInput, FailureCodes.InvalidInput, "tool_input", message)
}

func ToolUnavailableFailure(toolName string, message string) ToolResult {
	return ToolFailureResult(FailureDependencyUnavailable, FailureCodes.Unavailable, firstNonEmptyString(strings.TrimSpace(toolName), "tool"), message)
}

func (toolResult ToolResult) Failed() bool {
	return toolResult.Failure != nil
}

func (toolResult ToolResult) ContentText() string {
	if strings.TrimSpace(toolResult.Output.Content) != "" {
		return toolResult.Output.Content
	}
	if len(toolResult.Output.Data) > 0 {
		return string(toolResult.Output.Data)
	}
	return ""
}

func (toolResult ToolResult) FailureCode() string {
	if toolResult.Failure == nil {
		return ""
	}
	return strings.TrimSpace(toolResult.Failure.Code)
}

func (toolResult ToolResult) FailureStage() string {
	if toolResult.Failure == nil {
		return ""
	}
	return strings.TrimSpace(toolResult.Failure.Stage)
}

func (toolResult ToolResult) UserSafeFailureSummary() string {
	if toolResult.Failure == nil {
		return ""
	}
	return strings.TrimSpace(toolResult.Failure.UserSafeSummary)
}

func normalizeFailureKind(kind FailureKind) FailureKind {
	if strings.TrimSpace(string(kind)) == "" {
		return FailureUnknown
	}
	return kind
}

type ToolHandler func(context.Context, ToolInvocation) (ToolResult, error)

const (
	ToolAvailabilityAvailable   = "available"
	ToolAvailabilityAsk         = "ask"
	ToolAvailabilityUnavailable = "unavailable"
	ToolAvailabilityDenied      = "denied"
)

type ToolAvailability struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type BoundTool struct {
	Definition   ToolDefinition
	Availability ToolAvailability
	Handler      ToolHandler
}

type ToolFunction[Input any, Output any] struct {
	Definition ToolDefinition
	Handler    func(context.Context, Input) (Output, error)
	Result     func(Output) ToolResult
}

type ToolSet struct {
	allowedToolNameByName map[string]bool
	boundToolByName       map[string]BoundTool
}

func NewToolSet(allowedToolNames []string) *ToolSet {
	allowedToolNameByName := map[string]bool{}
	for _, allowedToolName := range allowedToolNames {
		trimmedToolName := strings.TrimSpace(allowedToolName)
		if trimmedToolName != "" {
			allowedToolNameByName[trimmedToolName] = true
		}
	}
	return &ToolSet{
		allowedToolNameByName: allowedToolNameByName,
		boundToolByName:       map[string]BoundTool{},
	}
}

func (toolSet *ToolSet) RegisterTool(toolDefinition ToolDefinition, toolHandler ToolHandler) {
	toolSet.RegisterBoundTool(BoundTool{
		Definition:   toolDefinition,
		Availability: ToolAvailability{Status: ToolAvailabilityAvailable},
		Handler:      toolHandler,
	})
}

func (toolSet *ToolSet) RegisterTypedTool(toolDefinition ToolDefinition, toolHandler ToolHandler) {
	toolSet.RegisterTool(toolDefinition, toolHandler)
}

func RegisterToolFunction[Input any, Output any](toolSet *ToolSet, toolFunction ToolFunction[Input, Output]) {
	if toolSet == nil || toolFunction.Handler == nil {
		return
	}
	toolSet.RegisterTool(toolFunction.Definition, func(toolContext context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		var input Input
		if errorValue := UnmarshalToolInput(toolInvocation.Input, &input); errorValue != nil {
			return ToolInputFailure(errorValue.Error()), nil
		}
		output, errorValue := toolFunction.Handler(toolContext, input)
		if errorValue != nil {
			return ToolResult{}, errorValue
		}
		if toolFunction.Result != nil {
			return toolFunction.Result(output), nil
		}
		return ToolSuccess(marshalTypedToolOutput(output)), nil
	})
}

func IdentityToolResult(toolResult ToolResult) ToolResult {
	return toolResult
}

func (toolSet *ToolSet) RegisterBoundTool(boundTool BoundTool) {
	if toolSet == nil {
		return
	}
	toolDefinition := boundTool.Definition
	toolName := strings.TrimSpace(toolDefinition.Name)
	if toolName == "" || boundTool.Handler == nil {
		return
	}
	if strings.TrimSpace(boundTool.Availability.Status) == "" {
		boundTool.Availability.Status = ToolAvailabilityAvailable
	}
	toolDefinition.Name = toolName
	if strings.TrimSpace(toolDefinition.SideEffectClass) == "" && strings.TrimSpace(toolDefinition.RecoveryCard.SideEffect) == "" {
		toolDefinition.SideEffectClass = DefaultToolSideEffectClass(toolName)
	}
	boundTool.Definition = toolDefinition
	toolSet.boundToolByName[toolName] = boundTool
}

func DefaultToolSideEffectClass(toolName string) string {
	normalizedToolName := strings.ToLower(strings.TrimSpace(toolName))
	if normalizedToolName == "" {
		return ""
	}
	for _, suffix := range []string{".list", ".read", ".search", ".status", ".context", ".preview", ".snapshot", ".screenshot"} {
		if strings.HasSuffix(normalizedToolName, suffix) {
			return ToolSideEffectRead
		}
	}
	for _, suffix := range []string{".calculate", ".count", ".compare", ".classify"} {
		if strings.HasSuffix(normalizedToolName, suffix) {
			return ToolSideEffectComputation
		}
	}
	for _, suffix := range []string{".send", ".reply", ".post", ".publish"} {
		if strings.HasSuffix(normalizedToolName, suffix) {
			return ToolSideEffectExternalWrite
		}
	}
	for _, suffix := range []string{".add", ".create", ".update", ".delete", ".remove", ".cancel", ".write", ".edit", ".patch", ".promote", ".attach", ".run", ".fill", ".click", ".press", ".select"} {
		if strings.HasSuffix(normalizedToolName, suffix) {
			return ToolSideEffectStateChange
		}
	}
	return ""
}

func ToolDefinitionSideEffectClass(toolDefinition ToolDefinition) string {
	return normalizeToolSideEffectClass(firstNonEmptyString(toolDefinition.SideEffectClass, toolDefinition.RecoveryCard.SideEffect, DefaultToolSideEffectClass(toolDefinition.Name)))
}

func ToolDefinitionRequiresSideEffectEvidence(toolDefinition ToolDefinition) bool {
	switch ToolDefinitionSideEffectClass(toolDefinition) {
	case "", ToolSideEffectNone, ToolSideEffectRead, ToolSideEffectComputation:
		return false
	default:
		return true
	}
}

func normalizeToolSideEffectClass(sideEffectClass string) string {
	normalizedSideEffectClass := strings.ToLower(strings.TrimSpace(sideEffectClass))
	switch normalizedSideEffectClass {
	case "readonly", "read_only", "inspect", "inspection":
		return ToolSideEffectRead
	case "compute", "calculation", "pure":
		return ToolSideEffectComputation
	case "write", "mutation", "mutating":
		return ToolSideEffectStateChange
	default:
		return normalizedSideEffectClass
	}
}

func (toolSet *ToolSet) IsAllowed(toolName string) bool {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" {
		return false
	}
	if !toolIsModelCallable(trimmedToolName) {
		return false
	}
	boundTool, isRegistered := toolSet.boundToolByName[trimmedToolName]
	if !isRegistered {
		return false
	}
	if len(toolSet.allowedToolNameByName) > 0 && !toolSet.allowedToolNameByName[trimmedToolName] {
		return false
	}
	return isExposedToolAvailability(boundTool.Availability)
}

func (toolSet *ToolSet) IsRegistered(toolName string) bool {
	if toolSet == nil {
		return false
	}
	_, isRegistered := toolSet.boundToolByName[strings.TrimSpace(toolName)]
	return isRegistered
}

func (toolSet *ToolSet) CanExpose(toolName string) bool {
	if toolSet == nil {
		return false
	}
	if !toolIsModelCallable(toolName) {
		return false
	}
	boundTool, isRegistered := toolSet.boundToolByName[strings.TrimSpace(toolName)]
	return isRegistered && isExposedToolAvailability(boundTool.Availability)
}

func (toolSet *ToolSet) ToolDefinition(toolName string) (ToolDefinition, bool) {
	if toolSet == nil {
		return ToolDefinition{}, false
	}
	boundTool, isRegistered := toolSet.boundToolByName[strings.TrimSpace(toolName)]
	if !isRegistered {
		return ToolDefinition{}, false
	}
	return boundTool.Definition, true
}

func (toolSet *ToolSet) ToolAvailability(toolName string) (ToolAvailability, bool) {
	if toolSet == nil {
		return ToolAvailability{}, false
	}
	boundTool, isRegistered := toolSet.boundToolByName[strings.TrimSpace(toolName)]
	if !isRegistered {
		return ToolAvailability{}, false
	}
	return boundTool.Availability, true
}

func (toolSet *ToolSet) WithAllowedToolNames(toolNames []string) *ToolSet {
	if toolSet == nil {
		return nil
	}
	filteredToolSet := NewToolSet(toolNames)
	for toolName, boundTool := range toolSet.boundToolByName {
		filteredToolSet.boundToolByName[toolName] = boundTool
	}
	return filteredToolSet
}

func (toolSet *ToolSet) WithAdditionalAllowedToolNames(toolNames []string) *ToolSet {
	if toolSet == nil {
		return nil
	}
	allowedToolNames := toolSet.ListToolNames()
	for _, toolName := range toolNames {
		trimmedToolName := strings.TrimSpace(toolName)
		if trimmedToolName == "" || !toolSet.CanExpose(trimmedToolName) {
			continue
		}
		allowedToolNames = appendUniqueStrings(allowedToolNames, trimmedToolName)
	}
	return toolSet.WithAllowedToolNames(allowedToolNames)
}

func (toolSet *ToolSet) Invoke(ctx context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
	toolName := strings.TrimSpace(toolInvocation.ToolName)
	if !toolSet.IsAllowed(toolName) {
		return ToolFailureResult(FailurePolicyBlocked, FailureCodes.PolicyBlocked, "tool_availability", "tool is not allowed"), nil
	}
	boundTool, isFound := toolSet.boundToolByName[toolName]
	if !isFound {
		return ToolFailureResult(FailureNotFound, FailureCodes.NotFound, "tool_registry", "tool is not registered"), nil
	}
	toolInvocation.ToolName = toolName
	return boundTool.Handler(ctx, toolInvocation)
}

func (toolSet *ToolSet) ListToolDefinitions() []ToolDefinition {
	toolDefinitions := []ToolDefinition{}
	for toolName, boundTool := range toolSet.boundToolByName {
		if toolSet.IsAllowed(toolName) {
			toolDefinitions = append(toolDefinitions, boundTool.Definition)
		}
	}
	sort.SliceStable(toolDefinitions, func(leftIndex int, rightIndex int) bool {
		return toolDefinitions[leftIndex].Name < toolDefinitions[rightIndex].Name
	})
	return toolDefinitions
}

func (toolSet *ToolSet) ListRegisteredToolDefinitions() []ToolDefinition {
	toolDefinitions := []ToolDefinition{}
	if toolSet == nil {
		return toolDefinitions
	}
	for _, boundTool := range toolSet.boundToolByName {
		toolDefinitions = append(toolDefinitions, boundTool.Definition)
	}
	sort.SliceStable(toolDefinitions, func(leftIndex int, rightIndex int) bool {
		return toolDefinitions[leftIndex].Name < toolDefinitions[rightIndex].Name
	})
	return toolDefinitions
}

func (toolSet *ToolSet) ListDescribedToolDefinitions() []ToolDefinition {
	toolDefinitions := []ToolDefinition{}
	if toolSet == nil {
		return toolDefinitions
	}
	for _, boundTool := range toolSet.boundToolByName {
		if !toolIsModelCallable(boundTool.Definition.Name) {
			continue
		}
		if strings.TrimSpace(boundTool.Availability.Status) == ToolAvailabilityDenied {
			continue
		}
		toolDefinitions = append(toolDefinitions, boundTool.Definition)
	}
	sort.SliceStable(toolDefinitions, func(leftIndex int, rightIndex int) bool {
		return toolDefinitions[leftIndex].Name < toolDefinitions[rightIndex].Name
	})
	return toolDefinitions
}

func (toolSet *ToolSet) ListRegisteredToolNames() []string {
	toolNames := []string{}
	if toolSet == nil {
		return toolNames
	}
	for toolName := range toolSet.boundToolByName {
		toolNames = append(toolNames, toolName)
	}
	sort.Strings(toolNames)
	return toolNames
}

func (toolSet *ToolSet) ListDescribedToolNames() []string {
	toolNames := []string{}
	for _, toolDefinition := range toolSet.ListDescribedToolDefinitions() {
		toolNames = append(toolNames, toolDefinition.Name)
	}
	return toolNames
}

func (toolSet *ToolSet) ListHiddenDescribedToolNames() []string {
	toolNames := []string{}
	if toolSet == nil {
		return toolNames
	}
	for _, toolDefinition := range toolSet.ListDescribedToolDefinitions() {
		toolName := strings.TrimSpace(toolDefinition.Name)
		if toolName != "" && !toolSet.IsAllowed(toolName) {
			toolNames = append(toolNames, toolName)
		}
	}
	sort.Strings(toolNames)
	return toolNames
}

func (toolSet *ToolSet) ListToolNames() []string {
	toolNames := []string{}
	for toolName := range toolSet.boundToolByName {
		if toolSet.IsAllowed(toolName) {
			toolNames = append(toolNames, toolName)
		}
	}
	sort.Strings(toolNames)
	return toolNames
}

func (toolSet *ToolSet) Descriptions() string {
	if toolSet == nil {
		return ""
	}
	toolDefinitions := toolSet.ListDescribedToolDefinitions()
	if len(toolDefinitions) == 0 {
		return ""
	}
	lines := []string{"Available tool catalog. These tools are known to Blueclaw; exposed tools can be called now, hidden tools must be named in the requestTools field of your action to expose them for the next step before calling. Tool availability does not make tool use mandatory:"}
	for _, toolDefinition := range toolDefinitions {
		toolName := strings.TrimSpace(toolDefinition.Name)
		lines = append(lines, "- "+toolCatalogLine(toolName, toolDefinition, toolSet))
	}
	return strings.Join(lines, "\n")
}

func toolCatalogLine(toolName string, toolDefinition ToolDefinition, toolSet *ToolSet) string {
	description := firstNonEmptyString(specificToolDescription(toolName), strings.TrimSpace(toolDefinition.Description), "No description.")
	visibility := "hidden"
	if toolSet.IsAllowed(toolName) {
		visibility = "exposed"
	}
	availability, _ := toolSet.ToolAvailability(toolName)
	availabilityStatus := firstNonEmptyString(strings.TrimSpace(availability.Status), ToolAvailabilityAvailable)
	if availabilityStatus == ToolAvailabilityAsk {
		availabilityStatus = "ask approval before invoking"
	}
	if strings.TrimSpace(availability.Reason) != "" {
		return toolName + ": " + description + " [" + visibility + ", " + availabilityStatus + ": " + strings.TrimSpace(availability.Reason) + "]"
	}
	return toolName + ": " + description + " [" + visibility + ", " + availabilityStatus + "]"
}

func MarshalToolInput(value any) json.RawMessage {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return json.RawMessage(`{}`)
	}
	return document
}

func UnmarshalToolInput(input json.RawMessage, value any) error {
	if len(input) == 0 {
		return nil
	}
	errorValue := json.Unmarshal(input, value)
	if errorValue != nil {
		return errors.New("tool input is not valid json: " + errorValue.Error())
	}
	return nil
}

func isExposedToolAvailability(toolAvailability ToolAvailability) bool {
	switch strings.TrimSpace(toolAvailability.Status) {
	case "", ToolAvailabilityAvailable, ToolAvailabilityAsk:
		return true
	default:
		return false
	}
}

func marshalTypedToolOutput(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return ""
	}
	return string(document)
}
