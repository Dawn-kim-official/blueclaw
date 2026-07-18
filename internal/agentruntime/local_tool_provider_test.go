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
	if mathDescriptor.ResultContract == nil || len(mathDescriptor.ResultContract.Effects) != 0 {
		t.Fatalf("expected math result contract without effects, got %+v", mathDescriptor.ResultContract)
	}

	inputDescriptor, isFound := descriptors["ask.input"]
	if !isFound {
		t.Fatal("expected ask.input descriptor")
	}
	if !inputDescriptor.RequiresUserPresence || inputDescriptor.SideEffectClass != agent.ToolSideEffectApproval {
		t.Fatalf("unexpected ask.input metadata: %+v", inputDescriptor)
	}
	if inputDescriptor.ResultContract == nil || len(inputDescriptor.ResultContract.Effects) != 0 {
		t.Fatalf("expected ask.input result contract without effects, got %+v", inputDescriptor.ResultContract)
	}
}

func TestLocalToolProviderUsesTypedSkillMutationContracts(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	handlerToolSet := agent.NewToolSet(nil)
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

func TestLocalToolProviderHidesDatabaseSQLFromModels(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"db.sql"})
	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	if !toolSet.IsRegistered("db.sql") {
		t.Fatal("expected db.sql to remain internally registered")
	}
	if toolSet.CanExpose("db.sql") || toolSet.IsAllowed("db.sql") {
		t.Fatal("expected db.sql to remain hidden from model exposure")
	}
	definition, isFound := toolSet.ToolDefinition("db.sql")
	if !isFound || definition.Visibility != agent.ToolVisibilityInternal {
		t.Fatalf("expected internal db.sql descriptor, got found=%v definition=%+v", isFound, definition)
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

func TestLocalToolProviderPreservesMemoryRememberContract(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	handlerToolSet := agent.NewToolSet(nil)
	registerMemoryTools(toolCatalogBuilder, handlerToolSet, ToolCatalogRequest{})

	boundTools, errorValue := (localToolProvider{handlerToolSet: handlerToolSet}).ListTools(context.Background())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var descriptor agent.ToolDescriptor
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
