package agentruntime

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/internal/access"
	"github.com/Dawn-kim-official/blueclaw/internal/agent"
	"github.com/Dawn-kim-official/blueclaw/internal/mcp"
)

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
		if !mcpToolDefinitionIsRegistered(definition, provider.request) {
			continue
		}
		boundTools = append(boundTools, provider.boundTool(definition))
	}
	return boundTools, nil
}

func mcpToolDefinitionIsRegistered(definition mcp.ToolDefinition, request ToolCatalogRequest) bool {
	return !request.IsScheduledRun || !definition.Policy.RequiresUserPresence
}

func (provider mcpToolProvider) boundTool(definition mcp.ToolDefinition) agent.BoundTool {
	resultContract := mcpToolResultContract(definition.ResultContract)
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
			InputIntentSchema:    definition.InputIntentSchema,
			OutputSchema:         definition.OutputSchema,
			ResultContract:       resultContract,
			Visibility:           definition.Policy.ModelVisibility,
			PolicyResource:       definition.Policy.PolicyResource,
			SideEffectClass:      definition.Policy.SideEffectClass,
			RequiresApproval:     definition.Policy.RequiresApproval,
			Completion: agent.ToolCompletion{
				Mode: definition.Policy.CompletionMode,
			},
			Idempotency:      definition.Policy.Idempotency,
			IdempotencyScope: definition.Policy.IdempotencyScope,
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
			toolResult := agent.ToolSuccessData(output, result.StructuredContent)
			toolResult.Effects = agent.ProjectResourceEffects(resultContract, result.StructuredContent)
			return toolResult, nil
		},
	}
}

func mcpToolResultContract(contract *mcp.ToolResultContract) *agent.ToolResultContract {
	if contract == nil {
		return nil
	}
	effects := make([]agent.ResourceEffectContract, 0, len(contract.Effects))
	for _, effect := range contract.Effects {
		effects = append(effects, agent.ResourceEffectContract{
			ObjectType:     strings.TrimSpace(effect.ObjectType),
			Effect:         strings.TrimSpace(effect.Effect),
			ResultField:    strings.TrimSpace(effect.ResultField),
			EffectIdentity: strings.TrimSpace(effect.EffectIdentity),
		})
	}
	return &agent.ToolResultContract{
		Schema:            append(json.RawMessage{}, contract.Schema...),
		Effects:           effects,
		EvidenceCondition: mcpEvidenceCondition(contract.EvidenceCondition),
	}
}

func mcpEvidenceCondition(condition *mcp.EvidenceCondition) *agent.EvidenceCondition {
	if condition == nil {
		return nil
	}
	return &agent.EvidenceCondition{
		ResultField: strings.TrimSpace(condition.ResultField),
		Equals:      append(json.RawMessage{}, condition.Equals...),
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
