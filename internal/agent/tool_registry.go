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

type ToolResult struct {
	Content         string           `json:"content"`
	IsError         bool             `json:"isError"`
	Attachments     []FileAttachment `json:"attachments,omitempty"`
	RecoveryActions []RecoveryAction `json:"recoveryActions,omitempty"`
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
			return ToolResult{Content: errorValue.Error(), IsError: true}, nil
		}
		output, errorValue := toolFunction.Handler(toolContext, input)
		if errorValue != nil {
			return ToolResult{}, errorValue
		}
		if toolFunction.Result != nil {
			return toolFunction.Result(output), nil
		}
		return ToolResult{Content: marshalTypedToolOutput(output)}, nil
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

func (toolSet *ToolSet) Invoke(ctx context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
	toolName := strings.TrimSpace(toolInvocation.ToolName)
	if !toolSet.IsAllowed(toolName) {
		return ToolResult{Content: "tool is not allowed", IsError: true}, nil
	}
	boundTool, isFound := toolSet.boundToolByName[toolName]
	if !isFound {
		return ToolResult{Content: "tool is not registered", IsError: true}, nil
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
	toolDefinitions := toolSet.ListToolDefinitions()
	if len(toolDefinitions) == 0 {
		return ""
	}
	lines := []string{"Available tools:"}
	for _, toolDefinition := range toolDefinitions {
		line := "- " + toolDefinition.Name
		description := firstNonEmptyString(specificToolDescription(toolDefinition.Name), toolDefinition.Description)
		if strings.TrimSpace(description) != "" {
			line += ": " + strings.TrimSpace(description)
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
