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

func TestKernelToolsHaveCanonicalResultContracts(t *testing.T) {
	provider := newKernelToolProvider(NewToolCatalogBuilder(), toolHandlerContext{
		request: ToolCatalogRequest{HistoryProvider: kernelHistoryProvider{}},
	}, agent.NewToolSet(nil))
	boundTools, errorValue := provider.ListTools(context.Background())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	expectedEffectCounts := map[string]int{
		agent.FileReadToolName:            0,
		agent.FileWriteToolName:           2,
		agent.FileDeleteToolName:          1,
		agent.FileEditToolName:            2,
		agent.FilePreviewToolName:         0,
		agent.FileDeliverToolName:         1,
		agent.ConversationHistoryToolName: 0,
	}
	for _, boundTool := range boundTools {
		expectedEffectCount, isContractedTool := expectedEffectCounts[boundTool.Definition.Name]
		if !isContractedTool {
			continue
		}
		contract := boundTool.Definition.ResultContract
		if contract == nil {
			t.Fatalf("expected %s result contract", boundTool.Definition.Name)
		}
		if len(contract.Effects) != expectedEffectCount {
			t.Fatalf("expected %s effect contracts, got %+v", boundTool.Definition.Name, contract.Effects)
		}
		if !equalJSONSchema(boundTool.Definition.OutputSchema, contract.Schema) {
			t.Fatalf("expected %s output and result schemas to match", boundTool.Definition.Name)
		}
		delete(expectedEffectCounts, boundTool.Definition.Name)
	}
	if len(expectedEffectCounts) != 0 {
		t.Fatalf("missing contracted file tools: %+v", expectedEffectCounts)
	}
}

func TestKernelToolProviderProjectsEveryResultPathEffect(t *testing.T) {
	testCases := []struct {
		toolName       string
		data           json.RawMessage
		expectedEffect []agent.ResourceEffect
	}{
		{
			toolName: agent.FileDeleteToolName,
			data:     json.RawMessage(`{"path":"tmp/obsolete.txt","deleted":true}`),
			expectedEffect: []agent.ResourceEffect{{
				ObjectType: "file",
				Effect:     "deleted",
				Path:       "tmp/obsolete.txt",
			}},
		},
		{
			toolName: agent.FileEditToolName,
			data:     json.RawMessage(`{"editedFiles":["tmp/first.md","tmp/second.md"],"editCount":2}`),
			expectedEffect: []agent.ResourceEffect{
				{ObjectType: "file", Effect: "updated", Path: "tmp/first.md"},
				{ObjectType: "file", Effect: "updated", Path: "tmp/second.md"},
				{ObjectType: "workspace", Effect: "modified", Path: "tmp/first.md"},
				{ObjectType: "workspace", Effect: "modified", Path: "tmp/second.md"},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.toolName, func(t *testing.T) {
			handlerToolSet := agent.NewToolSet(nil)
			handlerToolSet.RegisterTool(agent.ToolDefinition{
				Name:        testCase.toolName,
				Description: "test handler",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			}, func(context.Context, agent.ToolInvocation) (agent.ToolResult, error) {
				return agent.ToolSuccessData(string(testCase.data), testCase.data), nil
			})
			provider := kernelToolProvider{handlerToolSet: handlerToolSet}
			handlerDefinition, isFound := handlerToolSet.ToolDefinition(testCase.toolName)
			if !isFound {
				t.Fatal("expected handler definition")
			}
			boundTool, errorValue := provider.boundTool(handlerDefinition)
			if errorValue != nil {
				t.Fatal(errorValue)
			}

			result, errorValue := boundTool.Handler(context.Background(), agent.ToolInvocation{
				ToolName: testCase.toolName,
				Input:    json.RawMessage(`{}`),
			})

			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if len(result.Effects) != len(testCase.expectedEffect) {
				t.Fatalf("expected effects %+v, got %+v", testCase.expectedEffect, result.Effects)
			}
			for index, expectedEffect := range testCase.expectedEffect {
				if result.Effects[index] != expectedEffect {
					t.Fatalf("expected effect %+v, got %+v", expectedEffect, result.Effects[index])
				}
			}
		})
	}
}
