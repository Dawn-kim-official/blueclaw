package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

type ToolDefinition struct {
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	InputSchema     json.RawMessage `json:"inputSchema,omitempty"`
	OutputSchema    json.RawMessage `json:"outputSchema,omitempty"`
	PolicyResource  string          `json:"policyResource,omitempty"`
	SideEffectClass string          `json:"sideEffectClass,omitempty"`
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

type FailureCodeParts struct {
	Domain string
	Action string
	Reason string
}

var FailureCodes = struct {
	MemorySearchUnavailable FailureCode
	ToolFailed              FailureCode
	ToolInputInvalid        FailureCode
	ToolUnavailable         FailureCode
	ToolNotAllowed          FailureCode
	ToolNotRegistered       FailureCode
}{
	MemorySearchUnavailable: NewFailureCode(FailureCodeParts{Domain: "memory", Action: "search", Reason: "unavailable"}),
	ToolFailed:              NewFailureCode(FailureCodeParts{Domain: "tool", Reason: "failed"}),
	ToolInputInvalid:        NewFailureCode(FailureCodeParts{Domain: "tool", Action: "input", Reason: "invalid"}),
	ToolUnavailable:         NewFailureCode(FailureCodeParts{Domain: "tool", Reason: "unavailable"}),
	ToolNotAllowed:          NewFailureCode(FailureCodeParts{Domain: "tool", Action: "not", Reason: "allowed"}),
	ToolNotRegistered:       NewFailureCode(FailureCodeParts{Domain: "tool", Action: "not", Reason: "registered"}),
}

func NewFailureCode(parts FailureCodeParts) FailureCode {
	return FailureCode(strings.Join(nonEmptyFailureCodeParts(parts.Domain, parts.Action, parts.Reason), "."))
}

func FailureCodeLiteral(value string) FailureCode {
	return FailureCode(strings.TrimSpace(value))
}

func (failureCode FailureCode) String() string {
	return strings.TrimSpace(string(failureCode))
}

func normalizeFailureCode(code FailureCode) string {
	trimmedCode := code.String()
	switch trimmedCode {
	case "memory_search_unavailable":
		return FailureCodes.MemorySearchUnavailable.String()
	case "":
		return FailureCodes.ToolFailed.String()
	default:
		return trimmedCode
	}
}

func nonEmptyFailureCodeParts(parts ...string) []string {
	result := []string{}
	for _, part := range parts {
		trimmedPart := strings.TrimSpace(part)
		if trimmedPart == "" {
			continue
		}
		result = append(result, trimmedPart)
	}
	return result
}

type ToolFailure struct {
	Kind            FailureKind `json:"kind"`
	Code            string      `json:"code"`
	Stage           string      `json:"stage,omitempty"`
	UserSafeSummary string      `json:"userSafeSummary,omitempty"`
	Retryable       bool        `json:"retryable,omitempty"`
	SafeRetry       bool        `json:"safeRetry,omitempty"`
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
			Code:            normalizeFailureCode(code),
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

func ToolInputFailure(message string) ToolResult {
	return ToolFailureResult(FailureInvalidInput, FailureCodes.ToolInputInvalid, "tool_input", message)
}

func ToolUnavailableFailure(toolName string, message string) ToolResult {
	return ToolFailureResult(FailureDependencyUnavailable, FailureCodes.ToolUnavailable, firstNonEmptyString(strings.TrimSpace(toolName), "tool"), message)
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
	boundTool.Definition = toolDefinition
	toolSet.boundToolByName[toolName] = boundTool
}

func (toolSet *ToolSet) IsAllowed(toolName string) bool {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" {
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

func (toolSet *ToolSet) Invoke(ctx context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
	toolName := strings.TrimSpace(toolInvocation.ToolName)
	if !toolSet.IsAllowed(toolName) {
		return ToolFailureResult(FailurePolicyBlocked, FailureCodes.ToolNotAllowed, "tool_availability", "tool is not allowed"), nil
	}
	boundTool, isFound := toolSet.boundToolByName[toolName]
	if !isFound {
		return ToolFailureResult(FailureNotFound, FailureCodes.ToolNotRegistered, "tool_registry", "tool is not registered"), nil
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
	toolNames := toolSet.ListToolNames()
	if len(toolNames) == 0 {
		return ""
	}
	lines := []string{"Available tools. Use them only when they fit the current user goal; tool availability does not make tool use mandatory:"}
	for _, toolName := range toolNames {
		boundTool := toolSet.boundToolByName[toolName]
		toolDefinition := boundTool.Definition
		line := "- " + toolDefinition.Name
		description := firstNonEmptyString(specificToolDescription(toolDefinition.Name), toolDefinition.Description)
		if strings.TrimSpace(description) != "" {
			line += ": " + strings.TrimSpace(description)
		}
		if strings.TrimSpace(boundTool.Availability.Status) == ToolAvailabilityAsk {
			line += " Availability: ask approval before invoking"
			if strings.TrimSpace(boundTool.Availability.Reason) != "" {
				line += " (" + strings.TrimSpace(boundTool.Availability.Reason) + ")"
			}
		}
		if inputSchema := toolDefinitionInputSchema(toolDefinition); len(inputSchema) > 0 {
			line += " Input schema: " + strings.TrimSpace(string(inputSchema))
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
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
