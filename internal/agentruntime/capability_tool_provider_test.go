package agentruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"blueclaw/internal/agent"
)

func TestCapabilityToolProviderRegistersCanonicalDescriptor(t *testing.T) {
	provider := capabilityToolProvider{
		toolCatalogBuilder: NewToolCatalogBuilder(),
		request:            ToolCatalogRequest{},
		descriptors: []CapabilityToolDescriptor{{
			Name:               "task.add",
			CanonicalName:      "task.add",
			Namespace:          "task",
			ModelName:          "task.add",
			ModelVisibility:    agent.ToolVisibilityModel,
			Description:        "Create a task.",
			PrivacyClass:       "workspace_task",
			InputSchema:        json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`),
			OutputSchema:       json.RawMessage(`{"type":"object","properties":{"result":{}},"additionalProperties":false}`),
			PolicyResource:     "tool:task.add",
			SideEffectClass:    agent.ToolSideEffectWorkspaceWrite,
			CompletionEvidence: &CapabilityCompletionEvidence{Mode: "success", Action: "write_task", TargetKind: "task"},
			Availability:       CapabilityAvailability{State: "ok"},
			Idempotency:        CapabilityIdempotency{Scope: "operation"},
		}},
	}
	toolSet := agent.NewToolSet([]string{"task.add"})

	if errorValue := toolSet.RegisterProvider(context.Background(), provider); errorValue != nil {
		t.Fatal(errorValue)
	}
	descriptor, isFound := toolSet.ToolDefinition("task.add")
	if !isFound {
		t.Fatal("expected task.add")
	}
	if descriptor.ID != "capabilityd/task.add" || descriptor.ProviderID != "capabilityd" || descriptor.Completion.Mode != agent.ToolCompletionObservation {
		t.Fatalf("unexpected descriptor: %+v", descriptor)
	}
}

func TestCapabilityToolProviderPreservesCanonicalReadResultContract(t *testing.T) {
	provider := capabilityToolProvider{
		toolCatalogBuilder: NewToolCatalogBuilder(),
		request:            ToolCatalogRequest{},
		descriptors:        []CapabilityToolDescriptor{canonicalReadDescriptor("document.read")},
	}
	toolSet := agent.NewToolSet([]string{"document.read"})

	if errorValue := toolSet.RegisterProvider(context.Background(), provider); errorValue != nil {
		t.Fatal(errorValue)
	}
	descriptor, isFound := toolSet.ToolDefinition("document.read")
	if !isFound || descriptor.ResultContract == nil {
		t.Fatalf("expected canonical read result contract, found=%v descriptor=%+v", isFound, descriptor)
	}
	if len(descriptor.ResultContract.Effects) != 0 {
		t.Fatalf("expected read result contract to prohibit effects, got %+v", descriptor.ResultContract.Effects)
	}
	if !strings.Contains(string(descriptor.ResultContract.Schema), `"format"`) || !strings.Contains(string(descriptor.ResultContract.Schema), `"truncated"`) {
		t.Fatalf("expected document result schema to survive provider binding, got %s", descriptor.ResultContract.Schema)
	}
}

func TestCapabilityToolProviderRejectsIncompleteDescriptor(t *testing.T) {
	provider := capabilityToolProvider{
		toolCatalogBuilder: NewToolCatalogBuilder(),
		descriptors:        []CapabilityToolDescriptor{{Name: "task.add"}},
	}

	errorValue := agent.NewToolSet(nil).RegisterProvider(context.Background(), provider)

	if errorValue == nil || !strings.Contains(errorValue.Error(), "required") {
		t.Fatalf("expected fail-closed descriptor validation, got %v", errorValue)
	}
}

func TestCapabilityToolProviderRejectsScalarSchema(t *testing.T) {
	descriptor := completeTestCapabilityToolDescriptor(CapabilityToolDescriptor{Name: "task.add"})
	descriptor.InputSchema = json.RawMessage(`{"type":"string"}`)
	provider := capabilityToolProvider{
		toolCatalogBuilder: NewToolCatalogBuilder(),
		descriptors:        []CapabilityToolDescriptor{descriptor},
	}

	errorValue := agent.NewToolSet(nil).RegisterProvider(context.Background(), provider)

	if errorValue == nil || !strings.Contains(errorValue.Error(), "must describe objects") {
		t.Fatalf("expected scalar schema rejection, got %v", errorValue)
	}
}

func TestCapabilityToolProviderRejectsIncompleteCompletionEvidence(t *testing.T) {
	descriptor := completeTestCapabilityToolDescriptor(CapabilityToolDescriptor{Name: "task.add"})
	descriptor.CompletionEvidence = &CapabilityCompletionEvidence{Mode: "success"}
	provider := capabilityToolProvider{
		toolCatalogBuilder: NewToolCatalogBuilder(),
		descriptors:        []CapabilityToolDescriptor{descriptor},
	}

	errorValue := agent.NewToolSet(nil).RegisterProvider(context.Background(), provider)

	if errorValue == nil || !strings.Contains(errorValue.Error(), "action and targetKind are required") {
		t.Fatalf("expected incomplete completion evidence rejection, got %v", errorValue)
	}
}
