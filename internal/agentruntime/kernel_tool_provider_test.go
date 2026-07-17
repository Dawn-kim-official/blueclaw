package agentruntime

import (
	"context"
	"encoding/json"
	"testing"

	"blueclaw/internal/agent"
)

type kernelHistoryProvider struct{}

func (kernelHistoryProvider) FetchHistory(context.Context, string, int) (agent.VisibleContext, error) {
	return agent.VisibleContext{}, nil
}

func TestKernelToolProviderUsesCanonicalDescriptors(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	provider := newKernelToolProvider(toolCatalogBuilder, toolHandlerContext{
		request: ToolCatalogRequest{HistoryProvider: kernelHistoryProvider{}},
	}, agent.NewToolSet(nil))

	boundTools, errorValue := provider.ListTools(context.Background())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(boundTools) != len(localKernelToolNames())-1 {
		t.Fatalf("expected local kernel palette, got %d tools", len(boundTools))
	}

	expectedToolNames := map[string]bool{}
	for _, toolName := range localKernelToolNames() {
		if toolName != agent.SkillSearchToolName {
			expectedToolNames[toolName] = true
		}
	}
	for _, boundTool := range boundTools {
		descriptor := boundTool.Definition
		if !expectedToolNames[descriptor.Name] {
			t.Fatalf("unexpected local kernel tool %q", descriptor.Name)
		}
		delete(expectedToolNames, descriptor.Name)
		descriptorSpec, isFound := kernelToolDescriptorSpecForName(descriptor.Name)
		if !isFound {
			t.Fatalf("missing descriptor spec for %q", descriptor.Name)
		}
		if descriptor.ProviderID != kernelToolProviderID || descriptor.ID != kernelToolProviderID+"/"+descriptor.Name {
			t.Fatalf("expected canonical kernel identity, got %+v", descriptor)
		}
		if descriptor.Namespace != descriptorSpec.Namespace || descriptor.PrivacyClass != descriptorSpec.PrivacyClass || descriptor.Visibility != descriptorSpec.Visibility || descriptor.PolicyResource != descriptorSpec.PolicyResource {
			t.Fatalf("expected model-visible policy metadata, got %+v", descriptor)
		}
		if descriptor.SideEffectClass != descriptorSpec.SideEffectClass || descriptor.RequiresApproval != descriptorSpec.RequiresApproval {
			t.Fatalf("expected explicit side-effect metadata, got %+v", descriptor)
		}
		if descriptor.Completion.Mode != descriptorSpec.CompletionMode || descriptor.Idempotency != descriptorSpec.Idempotency {
			t.Fatalf("expected complete lifecycle metadata, got %+v", descriptor)
		}
		if len(descriptor.Description) == 0 || len(descriptor.InputSchema) == 0 || len(descriptor.OutputSchema) == 0 {
			t.Fatalf("expected schemas in descriptor, got %+v", descriptor)
		}
		var inputSchema map[string]any
		if errorValue := json.Unmarshal(descriptor.InputSchema, &inputSchema); errorValue != nil {
			t.Fatalf("expected valid input schema: %v", errorValue)
		}
		if inputSchema["type"] != "object" {
			t.Fatalf("expected object input schema, got %s", descriptor.InputSchema)
		}
		var outputSchema map[string]any
		if errorValue := json.Unmarshal(descriptor.OutputSchema, &outputSchema); errorValue != nil || outputSchema["type"] != "object" {
			t.Fatalf("expected object output schema, got %s", descriptor.OutputSchema)
		}
	}
	if len(expectedToolNames) != 0 {
		t.Fatalf("missing local kernel tools: %+v", expectedToolNames)
	}
}

func TestLocalKernelToolNamesExcludeCapabilityBackedImageReader(t *testing.T) {
	expectedKernelToolNames := []string{
		agent.TerminalRunToolName,
		agent.FileDeliverToolName,
		agent.SkillSearchToolName,
		agent.FileReadToolName,
		agent.FileWriteToolName,
		agent.FileDeleteToolName,
		agent.FileEditToolName,
		agent.FilePreviewToolName,
		agent.ConversationHistoryToolName,
	}
	if len(agent.KernelToolNames()) != len(expectedKernelToolNames)+1 {
		t.Fatalf("expected exactly 10 kernel tool names, got %+v", agent.KernelToolNames())
	}
	if len(localKernelToolNames()) != len(expectedKernelToolNames) {
		t.Fatalf("expected nine locally bound kernel tools, got %+v", localKernelToolNames())
	}
	for index, toolName := range localKernelToolNames() {
		if toolName != expectedKernelToolNames[index] {
			t.Fatalf("unexpected local kernel membership: %+v", localKernelToolNames())
		}
	}
}
