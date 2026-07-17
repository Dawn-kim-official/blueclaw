package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type testToolProvider struct {
	providerID string
	tools      []BoundTool
	errorValue error
}

func (provider testToolProvider) ProviderID() string {
	return provider.providerID
}

func (provider testToolProvider) ListTools(context.Context) ([]BoundTool, error) {
	return provider.tools, provider.errorValue
}

func TestRegisterProviderAddsValidatedTools(t *testing.T) {
	toolSet := NewToolSet([]string{"task.add"})

	errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{
		providerID: "capabilityd",
		tools:      []BoundTool{validProviderTool("capabilityd/task/task.add", "task", "task.add")},
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	toolDescriptor, isFound := toolSet.ToolDefinition("task.add")
	if !isFound {
		t.Fatal("expected registered tool")
	}
	if toolDescriptor.ID != "capabilityd/task/task.add" || toolDescriptor.ProviderID != "capabilityd" {
		t.Fatalf("unexpected canonical identity: %+v", toolDescriptor)
	}
}

func TestRegisterProviderRejectsMissingSchemaAtomically(t *testing.T) {
	validTool := validProviderTool("capabilityd/task/task.add", "task", "task.add")
	invalidTool := validProviderTool("capabilityd/task/task.list", "task", "task.list")
	invalidTool.Definition.OutputSchema = nil
	toolSet := NewToolSet([]string{"task.add", "task.list"})

	errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{
		providerID: "capabilityd",
		tools:      []BoundTool{validTool, invalidTool},
	})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "outputSchema is required") {
		t.Fatalf("expected missing schema failure, got %v", errorValue)
	}
	if len(toolSet.ListRegisteredToolNames()) != 0 {
		t.Fatalf("expected atomic rejection, got %+v", toolSet.ListRegisteredToolNames())
	}
}

func TestRegisterProviderClosesObjectSchemas(t *testing.T) {
	toolSet := NewToolSet([]string{"task.add"})
	providerTool := validProviderTool("capabilityd/task/task.add", "task", "task.add")
	providerTool.Definition.InputSchema = json.RawMessage(`{"type":"object","properties":{"patch":{"type":"object","properties":{}}}}`)

	if errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{providerID: "capabilityd", tools: []BoundTool{providerTool}}); errorValue != nil {
		t.Fatal(errorValue)
	}
	descriptor, isFound := toolSet.ToolDefinition("task.add")
	if !isFound || strings.Contains(string(descriptor.InputSchema), `"additionalProperties":true`) {
		t.Fatalf("expected closed input schema, got %s", descriptor.InputSchema)
	}
	if strings.Count(string(descriptor.InputSchema), `"additionalProperties":false`) != 2 {
		t.Fatalf("expected every object schema to be closed, got %s", descriptor.InputSchema)
	}
}

func TestRegisterProviderRejectsOpenObjectSchema(t *testing.T) {
	for _, inputSchema := range []json.RawMessage{
		json.RawMessage(`{"type":"object","additionalProperties":true}`),
		json.RawMessage(`{"type":"object","additionalProperties":{}}`),
	} {
		toolSet := NewToolSet([]string{"task.add"})
		providerTool := validProviderTool("capabilityd/task/task.add", "task", "task.add")
		providerTool.Definition.InputSchema = inputSchema

		errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{providerID: "capabilityd", tools: []BoundTool{providerTool}})

		if errorValue == nil || !strings.Contains(errorValue.Error(), "must not allow additional properties") {
			t.Fatalf("expected open input schema to fail closed, got %v", errorValue)
		}
	}
}

func TestRegisterProviderRejectsNonObjectOutputSchema(t *testing.T) {
	toolSet := NewToolSet([]string{"task.add"})
	providerTool := validProviderTool("capabilityd/task/task.add", "task", "task.add")
	providerTool.Definition.OutputSchema = json.RawMessage(`{"type":"string"}`)

	errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{providerID: "capabilityd", tools: []BoundTool{providerTool}})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "outputSchema must describe an object") {
		t.Fatalf("expected non-object output schema rejection, got %v", errorValue)
	}
}

func TestRegisterProviderRejectsIncompleteObservationMetadata(t *testing.T) {
	toolSet := NewToolSet([]string{"task.add"})
	providerTool := validProviderTool("capabilityd/task/task.add", "task", "task.add")
	providerTool.Definition.Completion.Action = ""

	errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{providerID: "capabilityd", tools: []BoundTool{providerTool}})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "completion.action and completion.targetKind are required") {
		t.Fatalf("expected incomplete observation metadata rejection, got %v", errorValue)
	}
}

func TestRegisterProviderRejectsCanonicalIdentityAndModelNameCollisions(t *testing.T) {
	tests := []struct {
		name  string
		tools []BoundTool
	}{
		{
			name: "identifier",
			tools: []BoundTool{
				validProviderTool("external/tasks/create", "tasks", "external.task.create"),
				validProviderTool("external/tasks/create", "tasks", "external.task.copy"),
			},
		},
		{
			name: "model name",
			tools: []BoundTool{
				validProviderTool("external/tasks/create", "tasks", "external.task.create"),
				validProviderTool("external/tasks/copy", "tasks", "external.task.create"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			toolSet := NewToolSet(nil)
			errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{
				providerID: "external",
				tools:      test.tools,
			})
			if errorValue == nil {
				t.Fatal("expected collision failure")
			}
		})
	}
}

func TestRegisterProvidersQuarantinesOnlyExternalFailure(t *testing.T) {
	toolSet := NewToolSet([]string{"task.add"})
	quarantinedProviders, errorValue := toolSet.RegisterProviders(context.Background(), []ToolProviderRegistration{
		{
			Provider: testToolProvider{providerID: "broken-mcp", errorValue: errors.New("offline")},
			Trust:    ToolProviderExternal,
		},
		{
			Provider: testToolProvider{
				providerID: "capabilityd",
				tools:      []BoundTool{validProviderTool("capabilityd/task/task.add", "task", "task.add")},
			},
			Trust: ToolProviderTrusted,
		},
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(quarantinedProviders) != 1 || quarantinedProviders[0].ProviderID != "broken-mcp" {
		t.Fatalf("unexpected quarantine result: %+v", quarantinedProviders)
	}
	if !toolSet.IsRegistered("task.add") {
		t.Fatal("expected trusted provider to remain available")
	}
}

func TestRegisterProvidersFailsOnTrustedProviderError(t *testing.T) {
	toolSet := NewToolSet(nil)

	_, errorValue := toolSet.RegisterProviders(context.Background(), []ToolProviderRegistration{{
		Provider: testToolProvider{providerID: "kernel", errorValue: errors.New("invalid descriptor")},
		Trust:    ToolProviderTrusted,
	}})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "invalid descriptor") {
		t.Fatalf("expected trusted provider failure, got %v", errorValue)
	}
}

func TestRegisterProvidersRejectsUnknownTrust(t *testing.T) {
	toolSet := NewToolSet(nil)

	_, errorValue := toolSet.RegisterProviders(context.Background(), []ToolProviderRegistration{{
		Provider: testToolProvider{providerID: "unknown"},
		Trust:    "unknown",
	}})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "trust is invalid") {
		t.Fatalf("expected unknown trust rejection, got %v", errorValue)
	}
}

func TestRegisterProvidersQuarantinesEveryExternalProviderInAModelNameCollision(t *testing.T) {
	toolSet := NewToolSet([]string{"workspace.echo"})
	quarantinedProviders, errorValue := toolSet.RegisterProviders(context.Background(), []ToolProviderRegistration{
		{
			Provider: testToolProvider{
				providerID: "mcp:first",
				tools:      []BoundTool{validProviderTool("mcp/first/echo", "workspace", "workspace.echo")},
			},
			Trust: ToolProviderExternal,
		},
		{
			Provider: testToolProvider{
				providerID: "mcp:second",
				tools:      []BoundTool{validProviderTool("mcp/second/echo", "workspace", "workspace.echo")},
			},
			Trust: ToolProviderExternal,
		},
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(quarantinedProviders) != 2 {
		t.Fatalf("expected both conflicting providers to be quarantined, got %+v", quarantinedProviders)
	}
	if toolSet.IsRegistered("workspace.echo") {
		t.Fatal("expected a colliding external tool name to remain unregistered")
	}
}

func TestRegisterProvidersQuarantinesExternalCollisionWithTrustedTool(t *testing.T) {
	toolSet := NewToolSet([]string{"task.add"})
	quarantinedProviders, errorValue := toolSet.RegisterProviders(context.Background(), []ToolProviderRegistration{
		{
			Provider: testToolProvider{
				providerID: "mcp:tasks",
				tools:      []BoundTool{validProviderTool("mcp/tasks/add", "task", "task.add")},
			},
			Trust: ToolProviderExternal,
		},
		{
			Provider: testToolProvider{
				providerID: "capabilityd",
				tools:      []BoundTool{validProviderTool("capabilityd/task/add", "task", "task.add")},
			},
			Trust: ToolProviderTrusted,
		},
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(quarantinedProviders) != 1 || quarantinedProviders[0].ProviderID != "mcp:tasks" {
		t.Fatalf("expected only the external provider to be quarantined, got %+v", quarantinedProviders)
	}
	descriptor, isFound := toolSet.ToolDefinition("task.add")
	if !isFound || descriptor.ProviderID != "capabilityd" {
		t.Fatalf("expected the trusted tool to remain registered, got %+v", descriptor)
	}
}

func TestRegisterBoundToolRejectsOverwrite(t *testing.T) {
	toolSet := NewToolSet([]string{"task.add"})
	firstTool := validProviderTool("capabilityd/task/task.add", "task", "task.add")
	secondTool := firstTool

	if errorValue := toolSet.RegisterBoundTool(firstTool); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := toolSet.RegisterBoundTool(secondTool); errorValue == nil {
		t.Fatal("expected duplicate registration failure")
	}
}

func TestProviderVisibilityControlsModelExposure(t *testing.T) {
	hiddenTool := validProviderTool("capabilityd/internal/llm.text", "internal", "llm.text")
	hiddenTool.Definition.Visibility = ToolVisibilityInternal
	toolSet := NewToolSet([]string{"llm.text"})

	errorValue := toolSet.RegisterProvider(context.Background(), testToolProvider{
		providerID: "capabilityd",
		tools:      []BoundTool{hiddenTool},
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if toolSet.IsAllowed("llm.text") {
		t.Fatal("expected hidden descriptor to stay out of model exposure")
	}
	if !toolSet.IsRegistered("llm.text") {
		t.Fatal("expected hidden descriptor to remain internally registered")
	}
}

func validProviderTool(toolID string, namespace string, name string) BoundTool {
	return BoundTool{
		Definition: ToolDescriptor{
			ID:              toolID,
			Namespace:       namespace,
			Name:            name,
			Description:     "Execute " + name,
			PrivacyClass:    "workspace",
			InputSchema:     json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			OutputSchema:    json.RawMessage(`{"type":"object","properties":{}}`),
			Visibility:      ToolVisibilityModel,
			PolicyResource:  "tool:" + name,
			SideEffectClass: ToolSideEffectStateChange,
			Completion:      ToolCompletion{Mode: ToolCompletionObservation, Action: "write_task", TargetKind: "task"},
			Idempotency:     ToolIdempotencyNone,
		},
		Availability: ToolAvailability{Status: ToolAvailabilityAvailable},
		Handler: func(context.Context, ToolInvocation) (ToolResult, error) {
			return ToolSuccess("ok"), nil
		},
	}
}
