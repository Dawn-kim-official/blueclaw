package agentruntime

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/mcp"
	"blueclaw/internal/policy"
)

type mcpToolProviderTestInvoker struct {
	output string
	error  error
	calls  *int
}

var mcpProviderOutputSchema = json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`)

func mcpProviderResultContract() *mcp.ToolResultContract {
	return &mcp.ToolResultContract{Schema: append(json.RawMessage{}, mcpProviderOutputSchema...)}
}

func TestMCPToolProviderPreservesEvidenceCondition(t *testing.T) {
	contract := mcpProviderResultContract()
	contract.EvidenceCondition = &mcp.EvidenceCondition{
		ResultField: "text",
		Equals:      json.RawMessage(`"ready"`),
	}

	boundContract := mcpToolResultContract(contract)

	if boundContract == nil || boundContract.EvidenceCondition == nil ||
		string(boundContract.EvidenceCondition.Equals) != `"ready"` {
		t.Fatalf("expected evidence condition to survive MCP binding, got %+v", boundContract)
	}
}

func (invoker mcpToolProviderTestInvoker) InvokeTool(context.Context, mcp.Invocation) (string, error) {
	if invoker.calls != nil {
		*invoker.calls = *invoker.calls + 1
	}
	return invoker.output, invoker.error
}

func TestMCPToolProviderMapsIsErrorToToolFailure(t *testing.T) {
	provider := mcpToolProvider{
		serverName: "workspace",
		registry: mcpToolProviderTestInvoker{
			output: `{"content":[],"isError":true}`,
		},
		definitions: []mcp.ToolDefinition{{
			Name:           "workspace.echo",
			Namespace:      "workspace",
			Description:    "Echo workspace text",
			InputSchema:    json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			OutputSchema:   mcpProviderOutputSchema,
			ResultContract: mcpProviderResultContract(),
			Policy: mcp.PolicyMetadata{
				PrivacyClass:     "workspace",
				ModelVisibility:  agent.ToolVisibilityModel,
				PolicyResource:   "tool:workspace.echo",
				SideEffectClass:  agent.ToolSideEffectRead,
				CompletionMode:   agent.ToolCompletionNone,
				Idempotency:      agent.ToolIdempotencySupported,
				IdempotencyScope: "operation",
			},
		}},
	}

	result, errorValue := provider.boundTool(provider.definitions[0]).Handler(context.Background(), agent.ToolInvocation{Input: json.RawMessage(`{}`)})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.Failure.Code != agent.FailureCodes.OperationFailed.String() {
		t.Fatalf("expected failed MCP result, got %+v", result)
	}
}

func TestMCPToolProviderRejectsPolicyDeniedInvocation(t *testing.T) {
	invocationCount := 0
	provider := mcpToolProvider{
		serverName: "workspace",
		registry: mcpToolProviderTestInvoker{
			output: `{"content":[],"isError":false}`,
			calls:  &invocationCount,
		},
		definitions: []mcp.ToolDefinition{{
			Name:           "workspace.echo",
			Namespace:      "workspace",
			Description:    "Echo workspace text",
			InputSchema:    json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			OutputSchema:   mcpProviderOutputSchema,
			ResultContract: mcpProviderResultContract(),
			Policy: mcp.PolicyMetadata{
				PrivacyClass:     "workspace",
				ModelVisibility:  agent.ToolVisibilityModel,
				PolicyResource:   "tool:workspace.echo",
				SideEffectClass:  agent.ToolSideEffectRead,
				CompletionMode:   agent.ToolCompletionNone,
				Idempotency:      agent.ToolIdempotencySupported,
				IdempotencyScope: "operation",
			},
		}},
		request: ToolCatalogRequest{
			PersonAccess: policy.PersonAccess{
				PersonID: "person-1",
				Circles:  []string{"staff"},
				ResourceAccessRules: []policy.ResourceAccessPolicy{{
					Resource: "tool:workspace.echo",
					Actions:  []string{"execute"},
					Circles:  []string{"admin"},
				}},
			},
		},
	}

	result, errorValue := provider.boundTool(provider.definitions[0]).Handler(context.Background(), agent.ToolInvocation{Input: json.RawMessage(`{}`)})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.Failure == nil || result.Failure.Kind != agent.FailurePermissionDenied || result.FailureCode() != agent.FailureCodes.AccessDenied.String() || result.FailureStage() != "capability_access" {
		t.Fatalf("expected capability permission failure, got %+v", result)
	}
	if invocationCount != 0 {
		t.Fatalf("expected denied MCP tool not to be invoked, got %d calls", invocationCount)
	}
}

func TestMCPToolProviderUsesCanonicalDescriptor(t *testing.T) {
	provider := mcpToolProvider{
		serverName: "workspace",
		definitions: []mcp.ToolDefinition{{
			Name:           "workspace.echo",
			Namespace:      "workspace",
			ServerName:     "workspace",
			Description:    "Echo workspace text",
			InputSchema:    json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`),
			OutputSchema:   mcpProviderOutputSchema,
			ResultContract: mcpProviderResultContract(),
			Policy: mcp.PolicyMetadata{
				PrivacyClass:     "workspace",
				ModelVisibility:  agent.ToolVisibilityModel,
				PolicyResource:   "tool:workspace.echo",
				SideEffectClass:  agent.ToolSideEffectRead,
				CompletionMode:   agent.ToolCompletionNone,
				Idempotency:      agent.ToolIdempotencySupported,
				IdempotencyScope: "operation",
			},
		}},
	}
	toolSet := agent.NewToolSet([]string{"workspace.echo"})

	errorValue := toolSet.RegisterProvider(context.Background(), provider)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	descriptor, isFound := toolSet.ToolDefinition("workspace.echo")
	if !isFound {
		t.Fatal("expected MCP tool")
	}
	if descriptor.ID != "mcp/workspace/workspace.echo" || descriptor.ProviderID != "mcp:workspace" {
		t.Fatalf("unexpected identity: %+v", descriptor)
	}
	if !equalJSONSchema(descriptor.OutputSchema, mcpProviderOutputSchema) ||
		descriptor.ResultContract == nil ||
		!equalJSONSchema(descriptor.ResultContract.Schema, mcpProviderOutputSchema) ||
		descriptor.IdempotencyScope != "operation" ||
		descriptor.Visibility != agent.ToolVisibilityModel {
		t.Fatalf("expected complete MCP descriptor: %+v", descriptor)
	}
}

func TestMCPToolProviderExcludesUserPresenceToolsFromScheduledRuns(t *testing.T) {
	definition := mcp.ToolDefinition{
		Name:           "workspace.echo",
		Namespace:      "workspace",
		Description:    "Echo workspace text",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		OutputSchema:   mcpProviderOutputSchema,
		ResultContract: mcpProviderResultContract(),
		Policy: mcp.PolicyMetadata{
			PrivacyClass:         "workspace",
			RequiresUserPresence: true,
			ModelVisibility:      agent.ToolVisibilityModel,
			PolicyResource:       "tool:workspace.echo",
			SideEffectClass:      agent.ToolSideEffectRead,
			CompletionMode:       agent.ToolCompletionNone,
			Idempotency:          agent.ToolIdempotencySupported,
			IdempotencyScope:     "operation",
		},
	}
	provider := mcpToolProvider{
		serverName:  "workspace",
		definitions: []mcp.ToolDefinition{definition},
		request:     ToolCatalogRequest{IsScheduledRun: true},
	}
	toolSet := agent.NewToolSet([]string{"workspace.echo"})

	if errorValue := toolSet.RegisterProvider(context.Background(), provider); errorValue != nil {
		t.Fatal(errorValue)
	}
	if toolSet.IsRegistered("workspace.echo") {
		t.Fatal("scheduled runs must not register MCP tools that require user presence")
	}

	provider.request.IsScheduledRun = false
	if errorValue := toolSet.RegisterProvider(context.Background(), provider); errorValue != nil {
		t.Fatal(errorValue)
	}
	if !toolSet.IsRegistered("workspace.echo") {
		t.Fatal("interactive runs must register MCP tools that require user presence")
	}
}

func TestToolCatalogReportsEveryMCPQuarantine(t *testing.T) {
	reportedProviders := []agent.QuarantinedToolProvider{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMCPQuarantineReporter(func(quarantinedProvider agent.QuarantinedToolProvider) {
		reportedProviders = append(reportedProviders, quarantinedProvider)
	})
	expectedProviders := []agent.QuarantinedToolProvider{
		{ProviderID: "mcp:first", Reason: "invalid metadata"},
		{ProviderID: "mcp:second", Reason: "tool name collision"},
	}

	toolCatalogBuilder.reportMCPQuarantines(expectedProviders)

	if !reflect.DeepEqual(reportedProviders, expectedProviders) {
		t.Fatalf("expected every MCP quarantine report, got %+v", reportedProviders)
	}
}

func equalJSONSchema(firstSchema json.RawMessage, secondSchema json.RawMessage) bool {
	var firstDocument any
	var secondDocument any
	if json.Unmarshal(firstSchema, &firstDocument) != nil || json.Unmarshal(secondSchema, &secondDocument) != nil {
		return false
	}
	return reflect.DeepEqual(firstDocument, secondDocument)
}

func TestMCPToolProviderValidatesStructuredSuccess(t *testing.T) {
	testCases := []struct {
		name      string
		output    string
		isSuccess bool
	}{
		{
			name:      "typed result",
			output:    `{"content":[],"structuredContent":{"text":"blueclaw"},"isError":false}`,
			isSuccess: true,
		},
		{
			name:   "text only result",
			output: `{"content":[{"type":"text","text":"blueclaw"}],"isError":false}`,
		},
		{
			name:   "generic structured result",
			output: `{"content":[],"structuredContent":{"value":"blueclaw"},"isError":false}`,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			definition := mcp.ToolDefinition{
				Name:           "workspace.echo",
				Namespace:      "workspace",
				ServerName:     "workspace",
				Description:    "Echo workspace text",
				InputSchema:    json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
				OutputSchema:   mcpProviderOutputSchema,
				ResultContract: mcpProviderResultContract(),
				Policy: mcp.PolicyMetadata{
					PrivacyClass:     "workspace",
					ModelVisibility:  agent.ToolVisibilityModel,
					PolicyResource:   "tool:workspace.echo",
					SideEffectClass:  agent.ToolSideEffectRead,
					CompletionMode:   agent.ToolCompletionNone,
					Idempotency:      agent.ToolIdempotencySupported,
					IdempotencyScope: "operation",
				},
			}
			provider := mcpToolProvider{
				serverName:  "workspace",
				registry:    mcpToolProviderTestInvoker{output: testCase.output},
				definitions: []mcp.ToolDefinition{definition},
			}
			toolSet := agent.NewToolSet([]string{"workspace.echo"})
			if errorValue := toolSet.RegisterProvider(context.Background(), provider); errorValue != nil {
				t.Fatal(errorValue)
			}

			result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{ToolName: "workspace.echo", Input: json.RawMessage(`{}`)})

			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if testCase.isSuccess && result.Failed() {
				t.Fatalf("expected typed success, got %+v", result)
			}
			if !testCase.isSuccess && (!result.Failed() || result.FailureStage() != "tool_result_contract") {
				t.Fatalf("expected fail-closed result contract, got %+v", result)
			}
		})
	}
}

func TestMCPToolProviderProjectsExactResultEvidence(t *testing.T) {
	resultSchema := json.RawMessage(`{"type":"object","properties":{"siteID":{"type":"string"}},"required":["siteID"],"additionalProperties":false}`)
	definition := mcp.ToolDefinition{
		Name:         "site.publish",
		Namespace:    "site",
		ServerName:   "workspace",
		Description:  "Publish a site",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		OutputSchema: resultSchema,
		ResultContract: &mcp.ToolResultContract{
			Schema: resultSchema,
			Effects: []mcp.ResourceEffectContract{{
				ObjectType:     "site",
				Effect:         "published",
				ResultField:    "siteID",
				EffectIdentity: "id",
			}},
		},
		Policy: mcp.PolicyMetadata{
			PrivacyClass:         "workspace",
			ModelVisibility:      agent.ToolVisibilityModel,
			PolicyResource:       "tool:site.publish",
			SideEffectClass:      agent.ToolSideEffectExternalPublish,
			CompletionMode:       agent.ToolCompletionObservation,
			CompletionAction:     "publish_site",
			CompletionTargetKind: "site",
			Idempotency:          agent.ToolIdempotencySupported,
			IdempotencyScope:     "operation",
		},
	}
	provider := mcpToolProvider{
		serverName:  "workspace",
		registry:    mcpToolProviderTestInvoker{output: `{"content":[],"structuredContent":{"siteID":"site-1"},"isError":false}`},
		definitions: []mcp.ToolDefinition{definition},
	}
	toolSet := agent.NewToolSet([]string{"site.publish"})
	if errorValue := toolSet.RegisterProvider(context.Background(), provider); errorValue != nil {
		t.Fatal(errorValue)
	}

	result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{ToolName: "site.publish", Input: json.RawMessage(`{}`)})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() || !reflect.DeepEqual(result.Effects, []agent.ResourceEffect{{
		ObjectType: "site",
		Effect:     "published",
		ID:         "site-1",
	}}) {
		t.Fatalf("expected exact site evidence, got %+v", result)
	}
}

func TestMCPToolProviderProjectsEveryArrayResultEffect(t *testing.T) {
	resultSchema := json.RawMessage(`{"type":"object","properties":{"paths":{"type":"array","items":{"type":"string"},"minItems":1,"uniqueItems":true}},"required":["paths"],"additionalProperties":false}`)
	definition := mcp.ToolDefinition{
		Name:         "workspace.edit",
		Namespace:    "workspace",
		ServerName:   "workspace",
		Description:  "Edit workspace files",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		OutputSchema: resultSchema,
		ResultContract: &mcp.ToolResultContract{
			Schema: resultSchema,
			Effects: []mcp.ResourceEffectContract{{
				ObjectType:     "file",
				Effect:         "updated",
				ResultField:    "paths",
				EffectIdentity: "path",
			}},
		},
		Policy: mcp.PolicyMetadata{
			PrivacyClass:     "workspace",
			ModelVisibility:  agent.ToolVisibilityModel,
			PolicyResource:   "tool:workspace.edit",
			SideEffectClass:  agent.ToolSideEffectWorkspaceWrite,
			CompletionMode:   agent.ToolCompletionNone,
			Idempotency:      agent.ToolIdempotencySupported,
			IdempotencyScope: "operation",
		},
	}
	provider := mcpToolProvider{
		serverName:  "workspace",
		registry:    mcpToolProviderTestInvoker{output: `{"content":[],"structuredContent":{"paths":["/workspace/one.md","/workspace/two.md"]},"isError":false}`},
		definitions: []mcp.ToolDefinition{definition},
	}
	toolSet := agent.NewToolSet([]string{"workspace.edit"})
	if errorValue := toolSet.RegisterProvider(context.Background(), provider); errorValue != nil {
		t.Fatal(errorValue)
	}
	result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{ToolName: "workspace.edit", Input: json.RawMessage(`{}`)})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	expectedEffects := []agent.ResourceEffect{
		{ObjectType: "file", Effect: "updated", Path: "/workspace/one.md"},
		{ObjectType: "file", Effect: "updated", Path: "/workspace/two.md"},
	}
	if result.Failed() || !reflect.DeepEqual(result.Effects, expectedEffects) {
		t.Fatalf("expected every array identity to become evidence, got %+v", result)
	}
}

func TestMCPToolProviderCollisionQuarantinesExternalServer(t *testing.T) {
	toolSet := agent.NewToolSet([]string{"file.read"})
	toolSet.RegisterTool(agent.ToolDefinition{Name: "file.read"}, func(context.Context, agent.ToolInvocation) (agent.ToolResult, error) {
		return agent.ToolSuccess("ok"), nil
	})
	provider := mcpToolProvider{
		serverName: "collision",
		definitions: []mcp.ToolDefinition{{
			Name:           "file.read",
			Namespace:      "file",
			ServerName:     "collision",
			Description:    "Colliding file reader",
			InputSchema:    json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			OutputSchema:   mcpProviderOutputSchema,
			ResultContract: mcpProviderResultContract(),
			Policy: mcp.PolicyMetadata{
				PrivacyClass:     "workspace",
				ModelVisibility:  agent.ToolVisibilityModel,
				PolicyResource:   "tool:file.read",
				SideEffectClass:  agent.ToolSideEffectRead,
				CompletionMode:   agent.ToolCompletionNone,
				Idempotency:      agent.ToolIdempotencySupported,
				IdempotencyScope: "operation",
			},
		}},
	}

	quarantinedProviders, errorValue := toolSet.RegisterProviders(context.Background(), []agent.ToolProviderRegistration{{
		Provider: provider,
		Trust:    agent.ToolProviderExternal,
	}})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(quarantinedProviders) != 1 || quarantinedProviders[0].ProviderID != "mcp:collision" {
		t.Fatalf("expected collision quarantine, got %+v", quarantinedProviders)
	}
	if len(toolSet.QuarantinedProviders()) != 1 {
		t.Fatalf("expected quarantine evidence on tool set, got %+v", toolSet.QuarantinedProviders())
	}
}
