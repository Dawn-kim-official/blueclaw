package agentruntime

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"blueclaw/internal/access"
	"blueclaw/internal/agent"
	"blueclaw/internal/mcp"
)

var mcpToolOutputSchema = json.RawMessage(`{"type":"object","properties":{"content":{"type":"array"},"structuredContent":{},"isError":{"type":"boolean"}},"required":["content","isError"],"additionalProperties":false}`)

type mcpToolProvider struct {
	serverName  string
	registry    mcpToolInvoker
	definitions []mcp.ToolDefinition
	request     ToolCatalogRequest
}

type mcpToolInvoker interface {
	InvokeTool(context.Context, mcp.Invocation) (string, error)
}

func (provider mcpToolProvider) ProviderID() string {
	return "mcp:" + provider.serverName
}

func (provider mcpToolProvider) ListTools(context.Context) ([]agent.BoundTool, error) {
	boundTools := make([]agent.BoundTool, 0, len(provider.definitions))
	for _, definition := range provider.definitions {
		boundTools = append(boundTools, provider.boundTool(definition))
	}
	return boundTools, nil
}

func (provider mcpToolProvider) boundTool(definition mcp.ToolDefinition) agent.BoundTool {
	return agent.BoundTool{
		Definition: agent.ToolDescriptor{
			ID:                   "mcp/" + provider.serverName + "/" + definition.Name,
			ProviderID:           provider.ProviderID(),
			Namespace:            definition.Namespace,
			Name:                 definition.Name,
			Description:          definition.Description,
			PrivacyClass:         definition.Policy.PrivacyClass,
			RequiresUserPresence: definition.Policy.RequiresUserPresence,
			WorksOffline:         definition.Policy.WorksOffline,
			InputSchema:          definition.InputSchema,
			OutputSchema:         mcpToolOutputSchema,
			Visibility:           definition.Policy.ModelVisibility,
			PolicyResource:       definition.Policy.PolicyResource,
			SideEffectClass:      definition.Policy.SideEffectClass,
			RequiresApproval:     definition.Policy.RequiresApproval,
			Completion: agent.ToolCompletion{
				Mode:       definition.Policy.CompletionMode,
				Action:     definition.Policy.CompletionAction,
				TargetKind: definition.Policy.CompletionTargetKind,
			},
			Idempotency: definition.Policy.Idempotency,
		},
		Availability: agent.ToolAvailability{Status: agent.ToolAvailabilityAvailable},
		Handler: func(toolContext context.Context, toolInvocation agent.ToolInvocation) (agent.ToolResult, error) {
			if !access.CanAccess(access.Request{PersonAccess: provider.request.PersonAccess, Action: access.ActionExecute, Resource: definition.Policy.PolicyResource}) {
				return agent.ToolFailureResult(agent.FailurePermissionDenied, agent.FailureCodes.AccessDenied, "capability_access", "current account cannot execute this tool"), nil
			}
			output, errorValue := provider.registry.InvokeTool(toolContext, mcp.Invocation{
				ServerName: provider.serverName,
				ToolName:   definition.Name,
				Input:      string(toolInvocation.Input),
			})
			if errorValue != nil {
				return agent.ToolResult{}, errorValue
			}
			result, errorValue := mcp.ParseToolResult(output)
			if errorValue != nil {
				return agent.ToolResult{}, errorValue
			}
			if result.IsError {
				return agent.ToolFailureWithOutput(
					agent.FailureExternalService,
					agent.FailureCodes.OperationFailed,
					"mcp_tool",
					"MCP tool returned an error",
					json.RawMessage(output),
				), nil
			}
			return agent.ToolSuccess(output), nil
		},
	}
}

func mcpToolProviders(registry *mcp.McpRegistry, request ToolCatalogRequest) []agent.ToolProviderRegistration {
	if registry == nil {
		return nil
	}
	definitionsByServer := map[string][]mcp.ToolDefinition{}
	for _, definition := range registry.ListTool() {
		serverName := strings.TrimSpace(definition.ServerName)
		if serverName == "" {
			continue
		}
		definitionsByServer[serverName] = append(definitionsByServer[serverName], definition)
	}
	serverNames := make([]string, 0, len(definitionsByServer))
	for serverName := range definitionsByServer {
		serverNames = append(serverNames, serverName)
	}
	sort.Strings(serverNames)
	registrations := make([]agent.ToolProviderRegistration, 0, len(serverNames))
	for _, serverName := range serverNames {
		registrations = append(registrations, agent.ToolProviderRegistration{
			Provider: mcpToolProvider{
				serverName:  serverName,
				registry:    registry,
				definitions: definitionsByServer[serverName],
				request:     request,
			},
			Trust: agent.ToolProviderExternal,
		})
	}
	return registrations
}
