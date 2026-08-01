package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Dawn-kim-official/blueclaw/internal/toolcontract"
	"strings"
)

const localToolProviderID = "local"

type localToolProvider struct {
	handlerToolSet *toolcontract.ToolSet
}

type localToolDescriptorSpec struct {
	ID                   string
	ProviderID           string
	Namespace            string
	Name                 string
	PrivacyClass         string
	RequiresUserPresence bool
	WorksOffline         bool
	InputIntentSchema    json.RawMessage
	OutputSchema         json.RawMessage
	ResultContract       *toolcontract.ToolResultContract
	Visibility           string
	PolicyResource       string
	SideEffectClass      string
	RequiresApproval     bool
	Completion           toolcontract.ToolCompletion
	Idempotency          string
	Availability         toolcontract.ToolAvailability
}

var localToolDescriptorSpecs = []localToolDescriptorSpec{
	{
		ID:              "local/memory.search",
		ProviderID:      localToolProviderID,
		Namespace:       "memory",
		Name:            "memory.search",
		PrivacyClass:    "workspace_memory",
		OutputSchema:    memorySearchOutputSchema,
		ResultContract:  &toolcontract.ToolResultContract{Schema: memorySearchOutputSchema},
		Visibility:      toolcontract.ToolVisibilityModel,
		PolicyResource:  "tool:memory.search",
		SideEffectClass: toolcontract.ToolSideEffectRead,
		Completion:      toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionNone},
		Idempotency:     toolcontract.ToolIdempotencyNone,
		Availability:    localToolAvailable,
	},
	{
		ID:           "local/memory.remember",
		ProviderID:   localToolProviderID,
		Namespace:    "memory",
		Name:         "memory.remember",
		PrivacyClass: "workspace_memory",
		OutputSchema: memoryRememberOutputSchema,
		ResultContract: &toolcontract.ToolResultContract{
			Schema: memoryRememberOutputSchema,
			Effects: []toolcontract.ResourceEffectContract{{
				ObjectType:     "memory_update",
				Effect:         "accepted",
				ResultField:    "jobID",
				EffectIdentity: "id",
			}},
			EvidenceCondition: &toolcontract.EvidenceCondition{
				ResultField: "accepted",
				Equals:      json.RawMessage(`true`),
			},
		},
		Visibility:        toolcontract.ToolVisibilityModel,
		PolicyResource:    "tool:memory.remember",
		SideEffectClass:   toolcontract.ToolSideEffectStateChange,
		InputIntentSchema: memoryRememberInputIntentSchema,
		Completion:        toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation},
		Idempotency:       toolcontract.ToolIdempotencyNone,
		Availability:      localToolAvailable,
	},
	{
		ID:                   "local/ask.input",
		ProviderID:           localToolProviderID,
		Namespace:            "ask",
		Name:                 "ask.input",
		PrivacyClass:         "user_input",
		RequiresUserPresence: true,
		WorksOffline:         true,
		OutputSchema:         askInputResultSchema,
		ResultContract:       &toolcontract.ToolResultContract{Schema: askInputResultSchema},
		Visibility:           toolcontract.ToolVisibilityModel,
		PolicyResource:       "tool:ask.input",
		SideEffectClass:      toolcontract.ToolSideEffectApproval,
		InputIntentSchema:    askInputIntentSchema,
		Completion:           toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionNone},
		Idempotency:          toolcontract.ToolIdempotencyNone,
		Availability:         localToolAvailable,
	},
	{
		ID:              "local/schedule.list",
		ProviderID:      localToolProviderID,
		Namespace:       "schedule",
		Name:            "schedule.list",
		PrivacyClass:    "workspace_schedule",
		OutputSchema:    scheduleListOutputSchema,
		ResultContract:  scheduleListResultContract(),
		Visibility:      toolcontract.ToolVisibilityModel,
		PolicyResource:  "tool:schedule.list",
		SideEffectClass: toolcontract.ToolSideEffectRead,
		Completion:      toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionNone},
		Idempotency:     toolcontract.ToolIdempotencyNone,
		Availability:    localToolAvailable,
	},
	{
		ID:                "local/schedule.create",
		ProviderID:        localToolProviderID,
		Namespace:         "schedule",
		Name:              "schedule.create",
		PrivacyClass:      "workspace_schedule",
		OutputSchema:      scheduleMutationResultSchema,
		ResultContract:    scheduleMutationResultContract("created"),
		Visibility:        toolcontract.ToolVisibilityModel,
		PolicyResource:    "tool:schedule.create",
		SideEffectClass:   toolcontract.ToolSideEffectStateChange,
		InputIntentSchema: scheduleCreateInputIntentSchema,
		Completion:        toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation},
		Idempotency:       toolcontract.ToolIdempotencyNone,
		Availability:      localToolAvailable,
	},
	{
		ID:                "local/schedule.update",
		ProviderID:        localToolProviderID,
		Namespace:         "schedule",
		Name:              "schedule.update",
		PrivacyClass:      "workspace_schedule",
		OutputSchema:      scheduleMutationResultSchema,
		ResultContract:    scheduleMutationResultContract("updated"),
		Visibility:        toolcontract.ToolVisibilityModel,
		PolicyResource:    "tool:schedule.update",
		SideEffectClass:   toolcontract.ToolSideEffectStateChange,
		InputIntentSchema: scheduleUpdateInputIntentSchema,
		Completion:        toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation},
		Idempotency:       toolcontract.ToolIdempotencyNone,
		Availability:      localToolAvailable,
	},
	{
		ID:                "local/schedule.cancel",
		ProviderID:        localToolProviderID,
		Namespace:         "schedule",
		Name:              "schedule.cancel",
		PrivacyClass:      "workspace_schedule",
		OutputSchema:      scheduleCancelResultSchema,
		ResultContract:    scheduleCancelResultContract(),
		Visibility:        toolcontract.ToolVisibilityModel,
		PolicyResource:    "tool:schedule.cancel",
		SideEffectClass:   toolcontract.ToolSideEffectStateChange,
		InputIntentSchema: scheduleCancelInputIntentSchema,
		Completion:        toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation},
		Idempotency:       toolcontract.ToolIdempotencyNone,
		Availability:      localToolAvailable,
	},
	{
		ID:           "local/skill.add",
		ProviderID:   localToolProviderID,
		Namespace:    "skill",
		Name:         "skill.add",
		PrivacyClass: "workspace_skill",
		OutputSchema: skillAddResultSchema,
		ResultContract: &toolcontract.ToolResultContract{
			Schema: skillAddResultSchema,
			Effects: []toolcontract.ResourceEffectContract{{
				ObjectType:     "skill",
				Effect:         "written",
				ResultField:    "path",
				EffectIdentity: "path",
			}},
			EvidenceCondition: &toolcontract.EvidenceCondition{
				ResultField: "written",
				Equals:      json.RawMessage(`true`),
			},
		},
		Visibility:        toolcontract.ToolVisibilityModel,
		PolicyResource:    "tool:skill.add",
		SideEffectClass:   toolcontract.ToolSideEffectWorkspaceWrite,
		InputIntentSchema: skillAddInputIntentSchema,
		Completion:        toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation},
		Idempotency:       toolcontract.ToolIdempotencyNone,
		Availability:      localToolAvailable,
	},
	{
		ID:           "local/skill.remove",
		ProviderID:   localToolProviderID,
		Namespace:    "skill",
		Name:         "skill.remove",
		PrivacyClass: "workspace_skill",
		OutputSchema: skillRemoveResultSchema,
		ResultContract: &toolcontract.ToolResultContract{
			Schema: skillRemoveResultSchema,
			Effects: []toolcontract.ResourceEffectContract{{
				ObjectType:     "skill",
				Effect:         "removed",
				ResultField:    "path",
				EffectIdentity: "path",
			}},
			EvidenceCondition: &toolcontract.EvidenceCondition{
				ResultField: "removed",
				Equals:      json.RawMessage(`true`),
			},
		},
		Visibility:        toolcontract.ToolVisibilityModel,
		PolicyResource:    "tool:skill.remove",
		SideEffectClass:   toolcontract.ToolSideEffectDestructive,
		InputIntentSchema: skillRemoveInputIntentSchema,
		Completion:        toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation},
		Idempotency:       toolcontract.ToolIdempotencyNone,
		Availability:      localToolAvailable,
	},
}

var (
	localToolAvailable = toolcontract.ToolAvailability{Status: toolcontract.ToolAvailabilityAvailable}
)

func (provider localToolProvider) ProviderID() string {
	return localToolProviderID
}

func (provider localToolProvider) ListTools(context.Context) ([]toolcontract.BoundTool, error) {
	if provider.handlerToolSet == nil {
		return nil, errors.New("local tool registry is unavailable")
	}
	boundTools := make([]toolcontract.BoundTool, 0, len(provider.handlerToolSet.ListRegisteredToolNames()))
	for _, toolName := range provider.handlerToolSet.ListRegisteredToolNames() {
		spec, found := localToolDescriptorSpecForName(toolName)
		if !found {
			return nil, fmt.Errorf("local tool %s has no canonical descriptor", toolName)
		}
		if errorValue := validateLocalToolDescriptorSpec(spec); errorValue != nil {
			return nil, fmt.Errorf("invalid local tool descriptor %s: %w", toolName, errorValue)
		}
		handlerDefinition, found := provider.handlerToolSet.ToolDefinition(toolName)
		if !found || strings.TrimSpace(handlerDefinition.Description) == "" || len(handlerDefinition.InputSchema) == 0 {
			return nil, fmt.Errorf("local tool %s has an incomplete handler definition", toolName)
		}
		boundTools = append(boundTools, provider.boundTool(spec, handlerDefinition))
	}
	return boundTools, nil
}

func (provider localToolProvider) boundTool(spec localToolDescriptorSpec, handlerDefinition toolcontract.ToolDefinition) toolcontract.BoundTool {
	toolName := spec.Name
	return toolcontract.BoundTool{
		Definition: toolcontract.ToolDescriptor{
			ID:                   spec.ID,
			ProviderID:           spec.ProviderID,
			Namespace:            spec.Namespace,
			Name:                 spec.Name,
			Description:          handlerDefinition.Description,
			PrivacyClass:         spec.PrivacyClass,
			RequiresUserPresence: spec.RequiresUserPresence,
			WorksOffline:         spec.WorksOffline,
			InputSchema:          handlerDefinition.InputSchema,
			InputIntentSchema:    spec.InputIntentSchema,
			OutputSchema:         spec.OutputSchema,
			ResultContract:       spec.ResultContract,
			Visibility:           spec.Visibility,
			PolicyResource:       spec.PolicyResource,
			SideEffectClass:      spec.SideEffectClass,
			RequiresApproval:     spec.RequiresApproval,
			Completion:           spec.Completion,
			Idempotency:          spec.Idempotency,
		},
		Availability: spec.Availability,
		Handler: func(toolContext context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			invocation.ToolName = toolName
			result, errorValue := provider.handlerToolSet.InvokeInternal(toolContext, invocation)
			if errorValue == nil && !result.Failed() {
				result.Effects = toolcontract.ProjectResourceEffects(spec.ResultContract, result.Output.Data)
			}
			return result, errorValue
		},
	}
}

func localToolDescriptorSpecForName(toolName string) (localToolDescriptorSpec, bool) {
	trimmedToolName := strings.TrimSpace(toolName)
	for _, spec := range localToolDescriptorSpecs {
		if spec.Name == trimmedToolName {
			return spec, true
		}
	}
	return localToolDescriptorSpec{}, false
}

func validateLocalToolDescriptorSpec(spec localToolDescriptorSpec) error {
	if strings.TrimSpace(spec.ID) == "" || strings.TrimSpace(spec.ProviderID) == "" || strings.TrimSpace(spec.Namespace) == "" || strings.TrimSpace(spec.Name) == "" {
		return errors.New("identity is required")
	}
	if spec.ProviderID != localToolProviderID {
		return errors.New("provider identifier is invalid")
	}
	if strings.TrimSpace(spec.PrivacyClass) == "" || strings.TrimSpace(spec.Visibility) == "" || strings.TrimSpace(spec.PolicyResource) == "" || strings.TrimSpace(spec.SideEffectClass) == "" || strings.TrimSpace(spec.Idempotency) == "" {
		return errors.New("privacy, visibility, policy, side effect, and idempotency metadata are required")
	}
	if strings.TrimSpace(spec.Completion.Mode) == "" || len(spec.OutputSchema) == 0 || strings.TrimSpace(spec.Availability.Status) == "" {
		return errors.New("completion, output schema, and availability metadata are required")
	}
	if spec.Visibility == toolcontract.ToolVisibilityModel && spec.ResultContract == nil {
		return errors.New("model-visible result contract is required")
	}
	return nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerLocalTools(toolSet *toolcontract.ToolSet, request ToolCatalogRequest, handlerContext toolHandlerContext) {
	handlerToolSet := toolcontract.NewToolSet(nil)
	toolCatalogBuilder.registerMemoryTool(handlerToolSet, request)
	toolCatalogBuilder.registerBuiltInTools(handlerToolSet, handlerContext)
	provider := localToolProvider{handlerToolSet: handlerToolSet}
	if errorValue := toolSet.RegisterProvider(context.Background(), provider); errorValue != nil {
		panic(fmt.Errorf("register trusted local tool provider: %w", errorValue))
	}
}
