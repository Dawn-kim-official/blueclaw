package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

type ToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ToolInvocation struct {
	ToolName string          `json:"toolName"`
	Input    json.RawMessage `json:"input"`
}

type ToolResult struct {
	Content string `json:"content"`
	IsError bool   `json:"isError"`
}

type ToolHandler func(context.Context, ToolInvocation) (ToolResult, error)

type ToolRegistry struct {
	allowedToolNameByName map[string]bool
	definitionByName      map[string]ToolDefinition
	handlerByName         map[string]ToolHandler
}

func NewToolRegistry(allowedToolNames []string) *ToolRegistry {
	allowedToolNameByName := map[string]bool{}
	for _, allowedToolName := range allowedToolNames {
		trimmedToolName := strings.TrimSpace(allowedToolName)
		if trimmedToolName != "" {
			allowedToolNameByName[trimmedToolName] = true
		}
	}
	return &ToolRegistry{
		allowedToolNameByName: allowedToolNameByName,
		definitionByName:      map[string]ToolDefinition{},
		handlerByName:         map[string]ToolHandler{},
	}
}

func (toolRegistry *ToolRegistry) RegisterTool(toolDefinition ToolDefinition, toolHandler ToolHandler) {
	toolName := strings.TrimSpace(toolDefinition.Name)
	if toolName == "" || toolHandler == nil {
		return
	}
	toolDefinition.Name = toolName
	toolRegistry.definitionByName[toolName] = toolDefinition
	toolRegistry.handlerByName[toolName] = toolHandler
}

func (toolRegistry *ToolRegistry) IsAllowed(toolName string) bool {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" {
		return false
	}
	if len(toolRegistry.allowedToolNameByName) == 0 {
		_, isRegistered := toolRegistry.handlerByName[trimmedToolName]
		return isRegistered
	}
	return toolRegistry.allowedToolNameByName[trimmedToolName]
}

func (toolRegistry *ToolRegistry) InvokeTool(ctx context.Context, toolInvocation ToolInvocation) (ToolResult, error) {
	toolName := strings.TrimSpace(toolInvocation.ToolName)
	if !toolRegistry.IsAllowed(toolName) {
		return ToolResult{Content: "tool is not allowed", IsError: true}, nil
	}
	toolHandler, isFound := toolRegistry.handlerByName[toolName]
	if !isFound {
		return ToolResult{Content: "tool is not registered", IsError: true}, nil
	}
	toolInvocation.ToolName = toolName
	return toolHandler(ctx, toolInvocation)
}

func (toolRegistry *ToolRegistry) ListToolDefinitions() []ToolDefinition {
	toolDefinitions := []ToolDefinition{}
	for toolName, toolDefinition := range toolRegistry.definitionByName {
		if toolRegistry.IsAllowed(toolName) {
			toolDefinitions = append(toolDefinitions, toolDefinition)
		}
	}
	return toolDefinitions
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
