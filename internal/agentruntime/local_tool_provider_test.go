package agentruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"blueclaw/internal/agent"
)

func TestLocalToolProviderUsesCanonicalDescriptors(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	handlerToolSet := agent.NewToolSet(nil)
	toolCatalogBuilder.registerMathTool(handlerToolSet)
	toolCatalogBuilder.registerAskInputTool(handlerToolSet)

	boundTools, errorValue := (localToolProvider{handlerToolSet: handlerToolSet}).ListTools(context.Background())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(boundTools) != 2 {
		t.Fatalf("expected two local tools, got %d", len(boundTools))
	}

	descriptors := map[string]agent.ToolDescriptor{}
	for _, boundTool := range boundTools {
		descriptors[boundTool.Definition.Name] = boundTool.Definition
	}

	mathDescriptor, isFound := descriptors["math.calculate"]
	if !isFound {
		t.Fatal("expected math.calculate descriptor")
	}
	if mathDescriptor.ID != "local/math.calculate" || mathDescriptor.ProviderID != localToolProviderID || mathDescriptor.Namespace != "math" || mathDescriptor.Visibility != agent.ToolVisibilityModel {
		t.Fatalf("unexpected math descriptor: %+v", mathDescriptor)
	}
	if mathDescriptor.PolicyResource != "tool:math.calculate" || mathDescriptor.SideEffectClass != agent.ToolSideEffectComputation || mathDescriptor.Completion.Mode != agent.ToolCompletionNone || mathDescriptor.Idempotency != agent.ToolIdempotencyNone {
		t.Fatalf("unexpected math metadata: %+v", mathDescriptor)
	}
	if len(mathDescriptor.InputSchema) == 0 || len(mathDescriptor.OutputSchema) == 0 {
		t.Fatal("expected math schemas")
	}

	inputDescriptor, isFound := descriptors["ask.input"]
	if !isFound {
		t.Fatal("expected ask.input descriptor")
	}
	if !inputDescriptor.RequiresUserPresence || inputDescriptor.SideEffectClass != agent.ToolSideEffectApproval {
		t.Fatalf("unexpected ask.input metadata: %+v", inputDescriptor)
	}
}

func TestLocalToolProviderPreservesMemorySearchContract(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	handlerToolSet := agent.NewToolSet(nil)
	registerMemoryTools(toolCatalogBuilder, handlerToolSet, ToolCatalogRequest{})

	boundTools, errorValue := (localToolProvider{handlerToolSet: handlerToolSet}).ListTools(context.Background())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var descriptor agent.ToolDescriptor
	for _, boundTool := range boundTools {
		if boundTool.Definition.Name == "memory.search" {
			descriptor = boundTool.Definition
		}
	}
	if descriptor.Name == "" || descriptor.ResultContract == nil {
		t.Fatalf("expected memory.search result contract, got %+v", descriptor)
	}
	if !equalJSONSchema(descriptor.OutputSchema, memorySearchOutputSchema) || !equalJSONSchema(descriptor.ResultContract.Schema, memorySearchOutputSchema) {
		t.Fatalf("expected canonical output schema preservation, got %+v", descriptor)
	}
	if len(descriptor.ResultContract.Effects) != 0 || descriptor.ResultContract.EvidenceCondition != nil {
		t.Fatalf("expected schema-only memory.search contract, got %+v", descriptor.ResultContract)
	}
	if !strings.Contains(string(descriptor.InputSchema), `"required":["query"]`) || !strings.Contains(string(descriptor.InputSchema), `"pattern":"\\S"`) {
		t.Fatalf("expected strict query schema preservation, got %s", descriptor.InputSchema)
	}
}

func TestLocalToolProviderRejectsMalformedMemorySearchResult(t *testing.T) {
	handlerToolSet := agent.NewToolSet(nil)
	if errorValue := handlerToolSet.RegisterTool(agent.ToolDefinition{
		Name:        "memory.search",
		Description: "Return a malformed memory result.",
		InputSchema: memorySearchInputSchema,
	}, func(context.Context, agent.ToolInvocation) (agent.ToolResult, error) {
		document := json.RawMessage(`{"facts":[],"searchStatus":"complete","sources":[]}`)
		return agent.ToolSuccessData(string(document), document), nil
	}); errorValue != nil {
		t.Fatal(errorValue)
	}
	toolSet := agent.NewToolSet([]string{"memory.search"})
	if errorValue := toolSet.RegisterProvider(context.Background(), localToolProvider{handlerToolSet: handlerToolSet}); errorValue != nil {
		t.Fatal(errorValue)
	}

	result, errorValue := toolSet.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "memory.search",
		Input:    agent.MarshalToolInput(map[string]string{"query": "release notes"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureStage() != "tool_result_contract" {
		t.Fatalf("expected malformed result to fail closed, got %+v", result)
	}
}

func TestLocalToolProviderRejectsUnregisteredDescriptor(t *testing.T) {
	handlerToolSet := agent.NewToolSet(nil)
	if errorValue := handlerToolSet.RegisterTool(agent.ToolDefinition{
		Name:        "ad_hoc.tool",
		Description: "Unregistered tool",
	}, func(context.Context, agent.ToolInvocation) (agent.ToolResult, error) {
		return agent.ToolSuccess("ok"), nil
	}); errorValue != nil {
		t.Fatal(errorValue)
	}

	_, errorValue := (localToolProvider{handlerToolSet: handlerToolSet}).ListTools(context.Background())
	if errorValue == nil || !strings.Contains(errorValue.Error(), "no canonical descriptor") {
		t.Fatalf("expected missing canonical descriptor failure, got %v", errorValue)
	}
}

func TestTaskHistoryDescriptorIsInternal(t *testing.T) {
	descriptor, isFound := localToolDescriptorSpecForName("task.history")
	if !isFound {
		t.Fatal("expected task.history descriptor")
	}
	if descriptor.Visibility != agent.ToolVisibilityInternal {
		t.Fatalf("expected internal task.history visibility, got %q", descriptor.Visibility)
	}
}

func TestLegacyBrowserHandoffIsRegisteredButHiddenFromModels(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"browser_handoff.openURL"},
	}, nil)
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	descriptor, isFound := toolSet.ToolDefinition("browser_handoff.openURL")
	if !isFound || descriptor.Visibility != agent.ToolVisibilityInternal {
		t.Fatalf("expected registered internal browser handoff descriptor, found=%v descriptor=%+v", isFound, descriptor)
	}
	if toolSet.CanExpose("browser_handoff.openURL") || containsString(toolSet.ListToolNames(), "browser_handoff.openURL") {
		t.Fatalf("expected browser handoff to stay outside model exposure, got %+v", toolSet.ListToolNames())
	}
}

func TestLocalToolDescriptorsAreComplete(t *testing.T) {
	identifiers := map[string]string{}
	names := map[string]string{}
	for _, descriptor := range localToolDescriptorSpecs {
		if errorValue := validateLocalToolDescriptorSpec(descriptor); errorValue != nil {
			t.Fatalf("descriptor %s is incomplete: %v", descriptor.Name, errorValue)
		}
		if previousName := identifiers[descriptor.ID]; previousName != "" {
			t.Fatalf("descriptor identifier %s repeats %s and %s", descriptor.ID, previousName, descriptor.Name)
		}
		if previousID := names[descriptor.Name]; previousID != "" {
			t.Fatalf("descriptor name %s repeats %s and %s", descriptor.Name, previousID, descriptor.ID)
		}
		identifiers[descriptor.ID] = descriptor.Name
		names[descriptor.Name] = descriptor.ID
	}
}
