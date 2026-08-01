package agentruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/bluecollar"
)

func TestLocalToolProviderUsesCanonicalDescriptors(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	handlerToolSet := bluecollar.NewToolSet(nil)
	toolCatalogBuilder.registerAskInputTool(handlerToolSet)

	boundTools, errorValue := (localToolProvider{handlerToolSet: handlerToolSet}).ListTools(context.Background())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(boundTools) != 1 {
		t.Fatalf("expected one local tool, got %d", len(boundTools))
	}

	inputDescriptor := boundTools[0].Definition
	if inputDescriptor.Name != "ask.input" {
		t.Fatalf("expected ask.input descriptor, got %+v", inputDescriptor)
	}
	if inputDescriptor.ID != "local/ask.input" || inputDescriptor.ProviderID != localToolProviderID || inputDescriptor.Namespace != "ask" || inputDescriptor.Visibility != bluecollar.ToolVisibilityModel {
		t.Fatalf("unexpected ask.input descriptor: %+v", inputDescriptor)
	}
	if inputDescriptor.PolicyResource != "tool:ask.input" || inputDescriptor.Completion.Mode != bluecollar.ToolCompletionNone || inputDescriptor.Idempotency != bluecollar.ToolIdempotencyNone {
		t.Fatalf("unexpected ask.input metadata: %+v", inputDescriptor)
	}
	if !inputDescriptor.RequiresUserPresence || inputDescriptor.SideEffectClass != bluecollar.ToolSideEffectApproval {
		t.Fatalf("unexpected ask.input presence metadata: %+v", inputDescriptor)
	}
	if len(inputDescriptor.InputSchema) == 0 || len(inputDescriptor.OutputSchema) == 0 {
		t.Fatal("expected ask.input schemas")
	}
	if inputDescriptor.ResultContract == nil || len(inputDescriptor.ResultContract.Effects) != 0 {
		t.Fatalf("expected ask.input result contract without effects, got %+v", inputDescriptor.ResultContract)
	}
}

func TestLocalToolProviderUsesTypedSkillMutationContracts(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	handlerToolSet := bluecollar.NewToolSet(nil)
	toolCatalogBuilder.registerSkillManagementTools(handlerToolSet)

	boundTools, errorValue := (localToolProvider{handlerToolSet: handlerToolSet}).ListTools(context.Background())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, boundTool := range boundTools {
		if boundTool.Definition.Name != "skill.add" && boundTool.Definition.Name != "skill.remove" {
			continue
		}
		if boundTool.Definition.RequiresApproval || boundTool.Definition.ResultContract == nil {
			t.Fatalf("expected direct typed result contract for %s, got %+v", boundTool.Definition.Name, boundTool.Definition)
		}
		if len(boundTool.Definition.ResultContract.Effects) != 1 {
			t.Fatalf("expected one resource effect for %s, got %+v", boundTool.Definition.Name, boundTool.Definition.ResultContract)
		}
		condition := boundTool.Definition.ResultContract.EvidenceCondition
		if condition == nil || string(condition.Equals) != "true" {
			t.Fatalf("expected explicit mutation evidence for %s, got %+v", boundTool.Definition.Name, condition)
		}
	}
}

func TestDeadCompatibilityToolsAreNotRegistered(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	deadToolNames := []string{"task.history", "browser_handoff.openURL", "db.sql"}
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, deadToolNames)
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	for _, toolName := range deadToolNames {
		if toolSet.IsRegistered(toolName) || toolSet.CanExpose(toolName) || toolSet.IsAllowed(toolName) {
			t.Fatalf("expected dead tool %s to be absent, got registered=%v exposed=%v allowed=%v", toolName, toolSet.IsRegistered(toolName), toolSet.CanExpose(toolName), toolSet.IsAllowed(toolName))
		}
		if _, isFound := toolSet.ToolDefinition(toolName); isFound {
			t.Fatalf("expected dead tool %s to have no descriptor", toolName)
		}
	}
}

func TestLocalToolProviderPreservesMemorySearchContract(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	handlerToolSet := bluecollar.NewToolSet(nil)
	registerMemoryTools(toolCatalogBuilder, handlerToolSet, ToolCatalogRequest{})

	boundTools, errorValue := (localToolProvider{handlerToolSet: handlerToolSet}).ListTools(context.Background())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var descriptor bluecollar.ToolDescriptor
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

func TestLocalToolProviderPreservesMemoryRememberContract(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	handlerToolSet := bluecollar.NewToolSet(nil)
	registerMemoryTools(toolCatalogBuilder, handlerToolSet, ToolCatalogRequest{})

	boundTools, errorValue := (localToolProvider{handlerToolSet: handlerToolSet}).ListTools(context.Background())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var descriptor bluecollar.ToolDescriptor
	for _, boundTool := range boundTools {
		if boundTool.Definition.Name == "memory.remember" {
			descriptor = boundTool.Definition
		}
	}
	if descriptor.Name == "" || descriptor.ResultContract == nil {
		t.Fatalf("expected memory.remember result contract, got %+v", descriptor)
	}
	if !equalJSONSchema(descriptor.OutputSchema, memoryRememberOutputSchema) || !equalJSONSchema(descriptor.ResultContract.Schema, memoryRememberOutputSchema) {
		t.Fatalf("expected canonical output schema preservation, got %+v", descriptor)
	}
	if len(descriptor.ResultContract.Effects) != 1 || descriptor.ResultContract.Effects[0].ResultField != "jobID" {
		t.Fatalf("expected exact memory update effect, got %+v", descriptor.ResultContract)
	}
	condition := descriptor.ResultContract.EvidenceCondition
	if condition == nil || condition.ResultField != "accepted" || string(condition.Equals) != "true" {
		t.Fatalf("expected accepted memory evidence condition, got %+v", condition)
	}
}

func TestLocalToolProviderRejectsMalformedMemorySearchResult(t *testing.T) {
	handlerToolSet := bluecollar.NewToolSet(nil)
	if errorValue := handlerToolSet.RegisterTool(bluecollar.ToolDefinition{
		Name:        "memory.search",
		Description: "Return a malformed memory result.",
		InputSchema: memorySearchInputSchema,
	}, func(context.Context, bluecollar.ToolInvocation) (bluecollar.ToolResult, error) {
		document := json.RawMessage(`{"facts":[],"searchStatus":"complete","sources":[]}`)
		return bluecollar.ToolSuccessData(string(document), document), nil
	}); errorValue != nil {
		t.Fatal(errorValue)
	}
	toolSet := bluecollar.NewToolSet([]string{"memory.search"})
	if errorValue := toolSet.RegisterProvider(context.Background(), localToolProvider{handlerToolSet: handlerToolSet}); errorValue != nil {
		t.Fatal(errorValue)
	}

	result, errorValue := toolSet.Invoke(context.Background(), bluecollar.ToolInvocation{
		ToolName: "memory.search",
		Input:    bluecollar.MarshalToolInput(map[string]string{"query": "release notes"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureStage() != "tool_result_contract" {
		t.Fatalf("expected malformed result to fail closed, got %+v", result)
	}
}

func TestLocalToolProviderRejectsUnregisteredDescriptor(t *testing.T) {
	handlerToolSet := bluecollar.NewToolSet(nil)
	if errorValue := handlerToolSet.RegisterTool(bluecollar.ToolDefinition{
		Name:        "ad_hoc.tool",
		Description: "Unregistered tool",
	}, func(context.Context, bluecollar.ToolInvocation) (bluecollar.ToolResult, error) {
		return bluecollar.ToolSuccess("ok"), nil
	}); errorValue != nil {
		t.Fatal(errorValue)
	}

	_, errorValue := (localToolProvider{handlerToolSet: handlerToolSet}).ListTools(context.Background())
	if errorValue == nil || !strings.Contains(errorValue.Error(), "no canonical descriptor") {
		t.Fatalf("expected missing canonical descriptor failure, got %v", errorValue)
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

func TestLocalToolProviderPreservesScheduleContracts(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(&memoryTaskScheduleRepository{})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.list", "schedule.create", "schedule.update", "schedule.cancel"})
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-1",
	})

	for _, toolName := range []string{"schedule.list", "schedule.create", "schedule.update", "schedule.cancel"} {
		descriptor, isFound := toolSet.ToolDefinition(toolName)
		if !isFound || descriptor.ResultContract == nil {
			t.Fatalf("expected %s result contract, found=%v descriptor=%+v", toolName, isFound, descriptor)
		}
		if !equalJSONSchema(descriptor.OutputSchema, descriptor.ResultContract.Schema) {
			t.Fatalf("expected %s output and result schemas to match", toolName)
		}
	}

	createDescriptor, _ := toolSet.ToolDefinition("schedule.create")
	if len(createDescriptor.ResultContract.Effects) != 1 || createDescriptor.ResultContract.Effects[0].ResultField != "scheduleID" {
		t.Fatalf("expected exact schedule create effect, got %+v", createDescriptor.ResultContract)
	}
	updateDescriptor, _ := toolSet.ToolDefinition("schedule.update")
	if len(updateDescriptor.ResultContract.Effects) != 1 || updateDescriptor.ResultContract.Effects[0].ResultField != "scheduleID" {
		t.Fatalf("expected exact schedule update effect, got %+v", updateDescriptor.ResultContract)
	}
	cancelDescriptor, _ := toolSet.ToolDefinition("schedule.cancel")
	condition := cancelDescriptor.ResultContract.EvidenceCondition
	if condition == nil || condition.ResultField != "cancelled" || string(condition.Equals) != "true" {
		t.Fatalf("expected explicit schedule cancel evidence, got %+v", cancelDescriptor.ResultContract)
	}
}
