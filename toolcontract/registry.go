package toolcontract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

type ToolDescriptor struct {
	ID                      string              `json:"id,omitempty"`
	ProviderID              string              `json:"providerID,omitempty"`
	Namespace               string              `json:"namespace,omitempty"`
	Name                    string              `json:"name"`
	Description             string              `json:"description"`
	PrivacyClass            string              `json:"privacyClass,omitempty"`
	RequiresUserPresence    bool                `json:"requiresUserPresence,omitempty"`
	RequiresRequesterDevice bool                `json:"requiresRequesterDevice,omitempty"`
	WorksOffline            bool                `json:"worksOffline,omitempty"`
	RecoveryCard            ToolRecoveryCard    `json:"recoveryCard,omitempty"`
	InputSchema             json.RawMessage     `json:"inputSchema,omitempty"`
	InputIntentSchema       json.RawMessage     `json:"inputIntentSchema,omitempty"`
	OutputSchema            json.RawMessage     `json:"outputSchema,omitempty"`
	ResultContract          *ToolResultContract `json:"resultContract,omitempty"`
	Visibility              string              `json:"visibility,omitempty"`
	PolicyResource          string              `json:"policyResource,omitempty"`
	SideEffectClass         string              `json:"sideEffectClass,omitempty"`
	RequiresApproval        bool                `json:"requiresApproval,omitempty"`
	ApprovalScope           string              `json:"approvalScope,omitempty"`
	Completion              ToolCompletion      `json:"completion,omitempty"`
	Idempotency             string              `json:"idempotency,omitempty"`
	IdempotencyScope        string              `json:"idempotencyScope,omitempty"`
}

type ToolDefinition = ToolDescriptor

type ToolCompletion struct {
	Mode string `json:"mode,omitempty"`
}

type ToolResultContract struct {
	Schema            json.RawMessage          `json:"schema"`
	Effects           []ResourceEffectContract `json:"effects,omitempty"`
	EvidenceCondition *EvidenceCondition       `json:"evidenceCondition,omitempty"`
}

type EvidenceCondition struct {
	ResultField string          `json:"resultField"`
	Equals      json.RawMessage `json:"equals"`
}

type ResourceEffectContract struct {
	ObjectType     string             `json:"objectType"`
	Effect         string             `json:"effect"`
	ResultField    string             `json:"resultField"`
	EffectIdentity string             `json:"effectIdentity"`
	When           *EvidenceCondition `json:"when,omitempty"`
}

type ResourceEffect struct {
	ObjectType  string `json:"objectType"`
	Effect      string `json:"effect"`
	ID          string `json:"id,omitempty"`
	Path        string `json:"path,omitempty"`
	URL         string `json:"url,omitempty"`
	Visibility  string `json:"visibility,omitempty"`
	Durability  string `json:"durability,omitempty"`
	Filename    string `json:"filename,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Summary     string `json:"summary,omitempty"`
}

type ToolRecoveryCard struct {
	Does       string `json:"does,omitempty"`
	Produces   string `json:"produces,omitempty"`
	SideEffect string `json:"sideEffect,omitempty"`
	UseWhen    string `json:"useWhen,omitempty"`
	AvoidWhen  string `json:"avoidWhen,omitempty"`
}

const (
	ToolSideEffectNone            = "none"
	ToolSideEffectRead            = "read"
	ToolSideEffectComputation     = "computation"
	ToolSideEffectStateChange     = "state_change"
	ToolSideEffectWorkspaceWrite  = "workspace_write"
	ToolSideEffectExternalWrite   = "external_write"
	ToolSideEffectApproval        = "approval"
	ToolSideEffectConnect         = "connect"
	ToolSideEffectDestructive     = "destructive"
	ToolSideEffectExternalSend    = "external_send"
	ToolSideEffectExternalPublish = "external_publish"
	ToolSideEffectLocalFile       = "local_file"
	ToolSideEffectPlatformReply   = "platform_reply"
	ToolSideEffectSitePublish     = "site_publish"
)

func ToolDescriptorRequiresInputIntentSchema(toolDescriptor ToolDescriptor) bool {
	if toolDescriptor.Visibility != ToolVisibilityModel {
		return false
	}
	switch toolDescriptor.SideEffectClass {
	case ToolSideEffectNone, ToolSideEffectRead, ToolSideEffectComputation:
		return false
	default:
		return true
	}
}

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
	FailureInteractionRequired   FailureKind = "interaction_required"
	FailurePolicyBlocked         FailureKind = "policy_blocked"
	FailureUnknown               FailureKind = "unknown"
)

type FailureCode string

var FailureCodes = struct {
	Unavailable         FailureCode
	InvalidInput        FailureCode
	AccessDenied        FailureCode
	Conflict            FailureCode
	NotFound            FailureCode
	OperationFailed     FailureCode
	PolicyBlocked       FailureCode
	RateLimited         FailureCode
	InteractionRequired FailureCode
	ToolNameInShell     FailureCode
}{
	Unavailable:         "unavailable",
	InvalidInput:        "invalid_input",
	AccessDenied:        "access_denied",
	Conflict:            "conflict",
	NotFound:            "not_found",
	OperationFailed:     "operation_failed",
	PolicyBlocked:       "policy_blocked",
	RateLimited:         "rate_limited",
	InteractionRequired: "interaction_required",
	ToolNameInShell:     "tool_name_in_terminal",
}

func (failureCode FailureCode) String() string {
	return strings.TrimSpace(string(failureCode))
}

func CanonicalFailureCode(code FailureCode) string {
	trimmedCode := code.String()
	switch trimmedCode {
	case "unavailable", "memory_search_unavailable", "memory.search.unavailable", "tool.unavailable", "memory_queue_unavailable", "terminal_service_unavailable", "companion_bridge_unavailable", "schedule_repository_unavailable", "mattermost_unavailable":
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
	case "tool.not_registered", "not_found", "recipient_not_found":
		return FailureCodes.NotFound.String()
	case "tool.failed", "tool_failed", "operation_failed":
		return FailureCodes.OperationFailed.String()
	case "policy_blocked":
		return FailureCodes.PolicyBlocked.String()
	case "rate_limited", "too_many_requests":
		return FailureCodes.RateLimited.String()
	case "blocked_by_captcha", "interaction_required":
		return FailureCodes.InteractionRequired.String()
	case "":
		return FailureCodes.OperationFailed.String()
	default:
		return FailureCodes.OperationFailed.String()
	}
}

type ToolFailure struct {
	Kind                FailureKind          `json:"kind"`
	Code                string               `json:"code"`
	Stage               string               `json:"stage,omitempty"`
	RequiresApproval    bool                 `json:"requiresApproval,omitempty"`
	UserSafeSummary     string               `json:"userSafeSummary,omitempty"`
	Retryable           bool                 `json:"retryable,omitempty"`
	SafeRetry           bool                 `json:"safeRetry,omitempty"`
	FailureClass        string               `json:"failureClass,omitempty"`
	RetryPolicy         string               `json:"retryPolicy,omitempty"`
	RecoveryHints       []RecoveryHint       `json:"recoveryHints,omitempty"`
	DiagnosticArtifacts []DiagnosticArtifact `json:"diagnosticArtifacts,omitempty"`
	AffectedResources   []AffectedResource   `json:"affectedResources,omitempty"`
}

type ToolResult struct {
	Output          ToolOutput       `json:"output,omitempty"`
	Effects         []ResourceEffect `json:"effects,omitempty"`
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
			Kind:            NormalizeFailureKind(kind),
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

func NormalizeFailureKind(kind FailureKind) FailureKind {
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
	boundToolNameByID     map[string]string
	quarantinedProviders  []QuarantinedToolProvider
	allowsTestReplacement bool
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
		boundToolNameByID:     map[string]string{},
	}
}

func (toolSet *ToolSet) RegisterTool(toolDefinition ToolDefinition, toolHandler ToolHandler) error {
	toolName := strings.TrimSpace(toolDefinition.Name)
	if toolSet != nil && toolSet.allowsTestReplacement {
		if registeredTool, isRegistered := toolSet.boundToolByName[toolName]; isRegistered {
			registeredTool.Definition = mergeTestToolDefinition(registeredTool.Definition, toolDefinition)
			registeredTool.Handler = toolHandler
			toolSet.boundToolByName[toolName] = registeredTool
			return nil
		}
	}
	return toolSet.RegisterBoundTool(BoundTool{
		Definition:   toolDefinition,
		Availability: ToolAvailability{Status: ToolAvailabilityAvailable},
		Handler:      toolHandler,
	})
}

func mergeTestToolDefinition(currentDefinition ToolDefinition, replacementDefinition ToolDefinition) ToolDefinition {
	replacementDefinition.ID = firstNonEmptyString(replacementDefinition.ID, currentDefinition.ID)
	replacementDefinition.ProviderID = firstNonEmptyString(replacementDefinition.ProviderID, currentDefinition.ProviderID)
	replacementDefinition.Namespace = firstNonEmptyString(replacementDefinition.Namespace, currentDefinition.Namespace)
	replacementDefinition.Name = firstNonEmptyString(replacementDefinition.Name, currentDefinition.Name)
	replacementDefinition.Description = firstNonEmptyString(replacementDefinition.Description, currentDefinition.Description)
	replacementDefinition.PrivacyClass = firstNonEmptyString(replacementDefinition.PrivacyClass, currentDefinition.PrivacyClass)
	replacementDefinition.RequiresUserPresence = replacementDefinition.RequiresUserPresence || currentDefinition.RequiresUserPresence
	replacementDefinition.WorksOffline = replacementDefinition.WorksOffline || currentDefinition.WorksOffline
	replacementDefinition.InputSchema = firstNonEmptySchema(replacementDefinition.InputSchema, currentDefinition.InputSchema)
	replacementDefinition.InputIntentSchema = firstNonEmptySchema(replacementDefinition.InputIntentSchema, currentDefinition.InputIntentSchema)
	replacementDefinition.OutputSchema = firstNonEmptySchema(replacementDefinition.OutputSchema, currentDefinition.OutputSchema)
	if replacementDefinition.ResultContract == nil {
		replacementDefinition.ResultContract = currentDefinition.ResultContract
	}
	replacementDefinition.Visibility = firstNonEmptyString(replacementDefinition.Visibility, currentDefinition.Visibility)
	replacementDefinition.PolicyResource = firstNonEmptyString(replacementDefinition.PolicyResource, currentDefinition.PolicyResource)
	replacementDefinition.SideEffectClass = firstNonEmptyString(replacementDefinition.SideEffectClass, currentDefinition.SideEffectClass)
	replacementDefinition.RequiresApproval = replacementDefinition.RequiresApproval || currentDefinition.RequiresApproval
	replacementDefinition.Idempotency = firstNonEmptyString(replacementDefinition.Idempotency, currentDefinition.Idempotency)
	replacementDefinition.IdempotencyScope = firstNonEmptyString(replacementDefinition.IdempotencyScope, currentDefinition.IdempotencyScope)
	if replacementDefinition.Completion.Mode == "" {
		replacementDefinition.Completion = currentDefinition.Completion
	}
	return replacementDefinition
}

func firstNonEmptySchema(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		if len(bytes.TrimSpace(value)) > 0 {
			return value
		}
	}
	return nil
}

func (toolSet *ToolSet) RegisterTypedTool(toolDefinition ToolDefinition, toolHandler ToolHandler) error {
	return toolSet.RegisterTool(toolDefinition, toolHandler)
}

func RegisterToolFunction[Input any, Output any](toolSet *ToolSet, toolFunction ToolFunction[Input, Output]) {
	if toolSet == nil || toolFunction.Handler == nil {
		return
	}
	errorValue := toolSet.RegisterTool(toolFunction.Definition, func(toolContext context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
		var input Input
		if errorValue := unmarshalToolInput(toolInvocation.Input, &input, true); errorValue != nil {
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
	if errorValue != nil {
		panic(errorValue)
	}
}

func IdentityToolResult(toolResult ToolResult) ToolResult {
	return toolResult
}

func (toolSet *ToolSet) RegisterBoundTool(boundTool BoundTool) error {
	if toolSet == nil {
		return errors.New("tool set is unavailable")
	}
	toolDefinition := boundTool.Definition
	toolName := strings.TrimSpace(toolDefinition.Name)
	if toolName == "" {
		return errors.New("tool name is required")
	}
	if boundTool.Handler == nil {
		return errors.New("tool handler is required")
	}
	if _, isRegistered := toolSet.boundToolByName[toolName]; isRegistered {
		return errors.New("tool name is already registered: " + toolName)
	}
	toolID := strings.TrimSpace(toolDefinition.ID)
	if toolID == "" {
		toolID = toolName
	}
	if registeredToolName, isRegistered := toolSet.boundToolNameByID[toolID]; isRegistered {
		return errors.New("tool identifier is already registered by " + registeredToolName + ": " + toolID)
	}
	if strings.TrimSpace(boundTool.Availability.Status) == "" {
		boundTool.Availability.Status = ToolAvailabilityAvailable
	}
	toolDefinition.ID = toolID
	toolDefinition.Name = toolName
	boundTool.Definition = toolDefinition
	toolSet.boundToolByName[toolName] = boundTool
	toolSet.boundToolNameByID[toolID] = toolName
	return nil
}

func ToolDefinitionSideEffectClass(toolDefinition ToolDefinition) string {
	return normalizeToolSideEffectClass(firstNonEmptyString(toolDefinition.SideEffectClass, toolDefinition.RecoveryCard.SideEffect))
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
	if !toolDescriptorIsModelCallable(boundTool.Definition) {
		return false
	}
	if len(toolSet.allowedToolNameByName) > 0 && !toolSet.allowedToolNameByName[trimmedToolName] {
		return false
	}
	if IsKernelToolName(trimmedToolName) {
		return true
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
	return isRegistered && toolDescriptorIsModelCallable(boundTool.Definition) && isExposedToolAvailability(boundTool.Availability)
}

func toolDescriptorIsModelCallable(toolDescriptor ToolDefinition) bool {
	return strings.TrimSpace(toolDescriptor.Visibility) == ToolVisibilityModel && toolDescriptor.ResultContract != nil
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
		filteredToolSet.boundToolNameByID[boundTool.Definition.ID] = toolName
	}
	filteredToolSet.quarantinedProviders = append([]QuarantinedToolProvider{}, toolSet.quarantinedProviders...)
	return filteredToolSet
}

func (toolSet *ToolSet) QuarantinedProviders() []QuarantinedToolProvider {
	if toolSet == nil {
		return nil
	}
	return append([]QuarantinedToolProvider{}, toolSet.quarantinedProviders...)
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
	return toolSet.invokeRegistered(ctx, toolInvocation)
}

func (toolSet *ToolSet) invokeRegistered(ctx context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
	toolName := strings.TrimSpace(toolInvocation.ToolName)
	boundTool, isFound := toolSet.boundToolByName[toolName]
	if !isFound {
		return ToolFailureResult(FailureNotFound, FailureCodes.NotFound, "tool_registry", "tool is not registered"), nil
	}
	toolInvocation.ToolName = toolName
	toolInput, errorValue := ValidateToolInput(boundTool.Definition.InputSchema, toolInvocation.Input)
	if errorValue != nil {
		return ToolFailureResult(FailureInvalidInput, FailureCodes.InvalidInput, "tool_input_schema", errorValue.Error()), nil
	}
	toolInvocation.Input = toolInput
	result, errorValue := boundTool.Handler(ctx, toolInvocation)
	if errorValue != nil || result.Failed() {
		return result, errorValue
	}
	if boundTool.Definition.ResultContract == nil {
		if len(result.Effects) > 0 {
			return ToolFailureResult(FailureExternalService, FailureCodes.OperationFailed, "tool_result_contract", "tool returned effects without a result contract"), nil
		}
		return result, nil
	}
	if errorValue := ValidateSuccessfulToolResult(*boundTool.Definition.ResultContract, result); errorValue != nil {
		return ToolFailureResult(FailureExternalService, FailureCodes.OperationFailed, "tool_result_contract", errorValue.Error()), nil
	}
	return result, nil
}

func ValidateToolInput(schemaDocument json.RawMessage, inputDocument json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(schemaDocument)) == 0 {
		return inputDocument, nil
	}
	normalizedInput := inputDocument
	if len(bytes.TrimSpace(normalizedInput)) == 0 {
		normalizedInput = json.RawMessage(`{}`)
	}
	var input any
	if errorValue := json.Unmarshal(normalizedInput, &input); errorValue != nil {
		return nil, errors.New("tool input is not valid JSON")
	}
	var schema jsonschema.Schema
	if errorValue := json.Unmarshal(schemaDocument, &schema); errorValue != nil {
		return nil, errors.New("tool input schema is invalid")
	}
	resolvedSchema, errorValue := schema.Resolve(nil)
	if errorValue != nil {
		return nil, errors.New("tool input schema cannot be resolved")
	}
	if errorValue := resolvedSchema.Validate(input); errorValue != nil {
		return nil, errors.New("tool input does not match its descriptor schema")
	}
	return normalizedInput, nil
}

func ValidateSuccessfulToolResult(contract ToolResultContract, result ToolResult) error {
	var resultDocument any
	if errorValue := json.Unmarshal(result.Output.Data, &resultDocument); errorValue != nil {
		return errors.New("tool result is not valid JSON")
	}
	var schema jsonschema.Schema
	if errorValue := json.Unmarshal(contract.Schema, &schema); errorValue != nil {
		return errors.New("tool result schema is invalid")
	}
	resolvedSchema, errorValue := schema.Resolve(nil)
	if errorValue != nil {
		return errors.New("tool result schema cannot be resolved")
	}
	if errorValue := resolvedSchema.Validate(resultDocument); errorValue != nil {
		return errors.New("tool result does not match its descriptor schema")
	}
	return validateResourceEffects(contract.Effects, resultDocument, result.Effects)
}

func validateResourceEffects(effectContracts []ResourceEffectContract, resultDocument any, effects []ResourceEffect) error {
	document, isObject := resultDocument.(map[string]any)
	if !isObject {
		return errors.New("tool result must be an object")
	}
	expectedEffects, hasEffectIdentities := expectedResourceEffects(effectContracts, document)
	if !hasEffectIdentities {
		return errors.New("tool result effect identity field is missing")
	}
	if len(expectedEffects) != len(effects) {
		return errors.New("tool result effects do not match its descriptor contract")
	}
	unmatchedEffects := append([]ResourceEffect{}, effects...)
	for _, expectedEffect := range expectedEffects {
		matchIndex := matchingResourceEffectIndex(unmatchedEffects, expectedEffect)
		if matchIndex < 0 {
			return errors.New("tool result effect identity does not match its result")
		}
		unmatchedEffects = append(unmatchedEffects[:matchIndex], unmatchedEffects[matchIndex+1:]...)
	}
	return nil
}

type expectedResourceEffect struct {
	contract ResourceEffectContract
	identity string
}

func ProjectResourceEffects(contract *ToolResultContract, resultDocument json.RawMessage) []ResourceEffect {
	if contract == nil || len(contract.Effects) == 0 {
		return nil
	}
	var document map[string]any
	if json.Unmarshal(resultDocument, &document) != nil {
		return nil
	}
	expectedEffects, hasEffectIdentities := expectedResourceEffects(contract.Effects, document)
	if !hasEffectIdentities {
		return nil
	}
	effects := make([]ResourceEffect, 0, len(expectedEffects))
	for _, expectedEffect := range expectedEffects {
		effect, isValid := projectedResourceEffect(expectedEffect)
		if !isValid {
			return nil
		}
		effects = append(effects, effect)
	}
	return effects
}

func projectedResourceEffect(expectedEffect expectedResourceEffect) (ResourceEffect, bool) {
	contract := expectedEffect.contract
	effect := ResourceEffect{
		ObjectType: strings.TrimSpace(contract.ObjectType),
		Effect:     strings.TrimSpace(contract.Effect),
	}
	switch strings.TrimSpace(contract.EffectIdentity) {
	case "id":
		effect.ID = expectedEffect.identity
	case "path":
		effect.Path = expectedEffect.identity
	case "url":
		effect.URL = expectedEffect.identity
	default:
		return ResourceEffect{}, false
	}
	return effect, true
}

func expectedResourceEffects(effectContracts []ResourceEffectContract, document map[string]any) ([]expectedResourceEffect, bool) {
	expectedEffects := []expectedResourceEffect{}
	for _, effectContract := range effectContracts {
		if !effectConditionMatches(effectContract.When, document) {
			continue
		}
		identities, isValid := resourceEffectIdentities(document[effectContract.ResultField])
		if !isValid {
			return nil, false
		}
		for _, identity := range identities {
			expectedEffects = append(expectedEffects, expectedResourceEffect{
				contract: effectContract,
				identity: identity,
			})
		}
	}
	return expectedEffects, true
}

func effectConditionMatches(condition *EvidenceCondition, document map[string]any) bool {
	if condition == nil {
		return true
	}
	value, isPresent := document[condition.ResultField]
	if !isPresent {
		return false
	}
	var expectedValue any
	if json.Unmarshal(condition.Equals, &expectedValue) != nil {
		return false
	}
	return reflect.DeepEqual(value, expectedValue)
}

func resourceEffectIdentities(value any) ([]string, bool) {
	switch identity := value.(type) {
	case string:
		if strings.TrimSpace(identity) != "" {
			return []string{strings.TrimSpace(identity)}, true
		}
	case []any:
		if len(identity) == 0 {
			return nil, false
		}
		identities := make([]string, 0, len(identity))
		seenIdentities := map[string]bool{}
		for _, item := range identity {
			value, isString := item.(string)
			trimmedValue := strings.TrimSpace(value)
			if !isString || trimmedValue == "" || seenIdentities[trimmedValue] {
				return nil, false
			}
			seenIdentities[trimmedValue] = true
			identities = append(identities, trimmedValue)
		}
		return identities, true
	}
	return nil, false
}

func matchingResourceEffectIndex(effects []ResourceEffect, expectedEffect expectedResourceEffect) int {
	for index, effect := range effects {
		if resourceEffectMatchesContract(effect, expectedEffect.contract, expectedEffect.identity) {
			return index
		}
	}
	return -1
}

func resourceEffectMatchesContract(effect ResourceEffect, contract ResourceEffectContract, expectedIdentity string) bool {
	if strings.TrimSpace(effect.ObjectType) != strings.TrimSpace(contract.ObjectType) {
		return false
	}
	if strings.TrimSpace(effect.Effect) != strings.TrimSpace(contract.Effect) {
		return false
	}
	identity, isValid := resourceEffectIdentity(effect, contract.EffectIdentity)
	return isValid && identity == strings.TrimSpace(expectedIdentity)
}

func resourceEffectIdentity(effect ResourceEffect, identityField string) (string, bool) {
	switch strings.TrimSpace(identityField) {
	case "id":
		return strings.TrimSpace(effect.ID), strings.TrimSpace(effect.ID) != "" && strings.TrimSpace(effect.Path) == "" && strings.TrimSpace(effect.URL) == ""
	case "path":
		return strings.TrimSpace(effect.Path), strings.TrimSpace(effect.ID) == "" && strings.TrimSpace(effect.Path) != "" && strings.TrimSpace(effect.URL) == ""
	case "url":
		return strings.TrimSpace(effect.URL), strings.TrimSpace(effect.ID) == "" && strings.TrimSpace(effect.Path) == "" && strings.TrimSpace(effect.URL) != ""
	default:
		return "", false
	}
}

func (toolSet *ToolSet) InvokeInternal(ctx context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
	return toolSet.invokeRegistered(ctx, toolInvocation)
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
	for toolName, boundTool := range toolSet.boundToolByName {
		if !toolSet.IsAllowed(toolName) {
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
	lines := []string{"Available tool catalog. Call the direct tool whose typed contract matches the task. Tool availability does not make tool use mandatory:"}
	for _, toolDefinition := range toolDefinitions {
		toolName := strings.TrimSpace(toolDefinition.Name)
		if !toolSet.IsAllowed(toolName) {
			continue
		}
		lines = append(lines, "- "+toolCatalogLine(toolName, toolDefinition, toolSet))
	}
	return strings.Join(lines, "\n")
}

func toolCatalogLine(toolName string, toolDefinition ToolDefinition, toolSet *ToolSet) string {
	description := firstNonEmptyString(strings.TrimSpace(toolDefinition.Description), "No description.")
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
	return unmarshalToolInput(input, value, false)
}

func unmarshalToolInput(input json.RawMessage, value any, rejectUnknownFields bool) error {
	if len(input) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	if rejectUnknownFields {
		decoder.DisallowUnknownFields()
	}
	errorValue := decoder.Decode(value)
	if errorValue != nil {
		return errors.New("tool input is not valid json: " + errorValue.Error())
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("tool input is not valid json: multiple json values")
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

// AllowTestReplacement lets a test re-register a tool the runtime would
// otherwise refuse to overwrite. The loop lives in another package now, so its
// tests need a way in that does not open the field to production code.
func (toolSet *ToolSet) AllowTestReplacement() {
	if toolSet != nil {
		toolSet.allowsTestReplacement = true
	}
}
