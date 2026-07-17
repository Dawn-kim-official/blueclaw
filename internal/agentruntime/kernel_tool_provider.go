package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"blueclaw/internal/agent"
)

const kernelToolProviderID = "kernel"

type kernelToolDescriptorSpec struct {
	Name                 string
	Namespace            string
	PrivacyClass         string
	Visibility           string
	PolicyResource       string
	SideEffectClass      string
	RequiresApproval     bool
	CompletionMode       string
	CompletionAction     string
	CompletionTargetKind string
	Idempotency          string
	OutputSchema         json.RawMessage
}

var kernelToolDescriptorSpecs = []kernelToolDescriptorSpec{
	{
		Name:                 agent.TerminalRunToolName,
		Namespace:            "terminal",
		PrivacyClass:         "workspace",
		Visibility:           agent.ToolVisibilityModel,
		PolicyResource:       "tool:terminal.run",
		SideEffectClass:      agent.ToolSideEffectWorkspaceWrite,
		CompletionMode:       agent.ToolCompletionObservation,
		CompletionAction:     "run_command",
		CompletionTargetKind: "workspace",
		Idempotency:          agent.ToolIdempotencyNone,
		OutputSchema:         json.RawMessage(`{"type":"object"}`),
	},
	{
		Name:                 agent.FileDeliverToolName,
		Namespace:            "file",
		PrivacyClass:         "workspace",
		Visibility:           agent.ToolVisibilityModel,
		PolicyResource:       "tool:file.deliver",
		SideEffectClass:      agent.ToolSideEffectExternalWrite,
		CompletionMode:       agent.ToolCompletionObservation,
		CompletionAction:     "deliver_file",
		CompletionTargetKind: "artifact",
		Idempotency:          agent.ToolIdempotencyNone,
		OutputSchema:         json.RawMessage(`{"type":"object"}`),
	},
	{
		Name:            agent.SkillSearchToolName,
		Namespace:       "skill",
		PrivacyClass:    "workspace",
		Visibility:      agent.ToolVisibilityModel,
		PolicyResource:  "tool:skill.search",
		SideEffectClass: agent.ToolSideEffectRead,
		CompletionMode:  agent.ToolCompletionNone,
		Idempotency:     agent.ToolIdempotencyNone,
		OutputSchema:    json.RawMessage(`{"type":"object"}`),
	},
	{
		Name:            agent.FileReadToolName,
		Namespace:       "file",
		PrivacyClass:    "workspace",
		Visibility:      agent.ToolVisibilityModel,
		PolicyResource:  "tool:file.read",
		SideEffectClass: agent.ToolSideEffectRead,
		CompletionMode:  agent.ToolCompletionNone,
		Idempotency:     agent.ToolIdempotencyNone,
		OutputSchema:    json.RawMessage(`{"type":"object"}`),
	},
	{
		Name:                 agent.FileWriteToolName,
		Namespace:            "file",
		PrivacyClass:         "workspace",
		Visibility:           agent.ToolVisibilityModel,
		PolicyResource:       "tool:file.write",
		SideEffectClass:      agent.ToolSideEffectWorkspaceWrite,
		CompletionMode:       agent.ToolCompletionObservation,
		CompletionAction:     "write_file",
		CompletionTargetKind: "file",
		Idempotency:          agent.ToolIdempotencyNone,
		OutputSchema:         json.RawMessage(`{"type":"object"}`),
	},
	{
		Name:                 agent.FileDeleteToolName,
		Namespace:            "file",
		PrivacyClass:         "workspace",
		Visibility:           agent.ToolVisibilityModel,
		PolicyResource:       "tool:file.delete",
		SideEffectClass:      agent.ToolSideEffectDestructive,
		RequiresApproval:     true,
		CompletionMode:       agent.ToolCompletionObservation,
		CompletionAction:     "delete_file",
		CompletionTargetKind: "file",
		Idempotency:          agent.ToolIdempotencyNone,
		OutputSchema:         json.RawMessage(`{"type":"object"}`),
	},
	{
		Name:                 agent.FileEditToolName,
		Namespace:            "file",
		PrivacyClass:         "workspace",
		Visibility:           agent.ToolVisibilityModel,
		PolicyResource:       "tool:file.edit",
		SideEffectClass:      agent.ToolSideEffectWorkspaceWrite,
		CompletionMode:       agent.ToolCompletionObservation,
		CompletionAction:     "edit_file",
		CompletionTargetKind: "file",
		Idempotency:          agent.ToolIdempotencyNone,
		OutputSchema:         json.RawMessage(`{"type":"object"}`),
	},
	{
		Name:            agent.FilePreviewToolName,
		Namespace:       "file",
		PrivacyClass:    "workspace",
		Visibility:      agent.ToolVisibilityModel,
		PolicyResource:  "tool:file.preview",
		SideEffectClass: agent.ToolSideEffectRead,
		CompletionMode:  agent.ToolCompletionNone,
		Idempotency:     agent.ToolIdempotencyNone,
		OutputSchema:    json.RawMessage(`{"type":"object"}`),
	},
	{
		Name:            agent.ConversationHistoryToolName,
		Namespace:       "conversation",
		PrivacyClass:    "conversation",
		Visibility:      agent.ToolVisibilityModel,
		PolicyResource:  "tool:conversation.history",
		SideEffectClass: agent.ToolSideEffectRead,
		CompletionMode:  agent.ToolCompletionNone,
		Idempotency:     agent.ToolIdempotencyNone,
		OutputSchema:    json.RawMessage(`{"type":"object"}`),
	},
}

type kernelToolProvider struct {
	handlerToolSet *agent.ToolSet
}

func (provider kernelToolProvider) ProviderID() string {
	return kernelToolProviderID
}

func (provider kernelToolProvider) ListTools(context.Context) ([]agent.BoundTool, error) {
	registeredToolNames := provider.handlerToolSet.ListRegisteredToolNames()
	for _, toolName := range registeredToolNames {
		if _, isFound := kernelToolDescriptorSpecForName(toolName); !isFound {
			return nil, fmt.Errorf("kernel provider registered unexpected tool %s", toolName)
		}
	}
	boundTools := make([]agent.BoundTool, 0, len(registeredToolNames))
	for _, toolName := range localKernelToolNames() {
		toolDefinition, isFound := provider.handlerToolSet.ToolDefinition(toolName)
		if !isFound {
			continue
		}
		boundTool, errorValue := provider.boundTool(toolDefinition)
		if errorValue != nil {
			return nil, errorValue
		}
		boundTools = append(boundTools, boundTool)
	}
	return boundTools, nil
}

func (provider kernelToolProvider) boundTool(toolDefinition agent.ToolDefinition) (agent.BoundTool, error) {
	canonicalDefinition, errorValue := canonicalKernelToolDescriptor(toolDefinition)
	if errorValue != nil {
		return agent.BoundTool{}, errorValue
	}
	return agent.BoundTool{
		Definition: canonicalDefinition,
		Availability: agent.ToolAvailability{
			Status: agent.ToolAvailabilityAvailable,
		},
		Handler: func(toolContext context.Context, invocation agent.ToolInvocation) (agent.ToolResult, error) {
			invocation.ToolName = canonicalDefinition.Name
			return provider.handlerToolSet.InvokeInternal(toolContext, invocation)
		},
	}, nil
}

func localKernelToolNames() []string {
	toolNames := make([]string, 0, len(kernelToolDescriptorSpecs))
	for _, descriptorSpec := range kernelToolDescriptorSpecs {
		toolNames = append(toolNames, descriptorSpec.Name)
	}
	return toolNames
}

func kernelToolDescriptorSpecForName(toolName string) (kernelToolDescriptorSpec, bool) {
	for _, descriptorSpec := range kernelToolDescriptorSpecs {
		if descriptorSpec.Name == strings.TrimSpace(toolName) {
			return descriptorSpec, true
		}
	}
	return kernelToolDescriptorSpec{}, false
}

func canonicalKernelToolDescriptor(toolDefinition agent.ToolDefinition) (agent.ToolDefinition, error) {
	descriptorSpec, isFound := kernelToolDescriptorSpecForName(toolDefinition.Name)
	if !isFound {
		return agent.ToolDefinition{}, errors.New("kernel descriptor is not registered: " + strings.TrimSpace(toolDefinition.Name))
	}
	if descriptorSpec.Namespace == "" || descriptorSpec.PrivacyClass == "" || descriptorSpec.Visibility == "" || descriptorSpec.PolicyResource == "" || descriptorSpec.SideEffectClass == "" || descriptorSpec.CompletionMode == "" || descriptorSpec.Idempotency == "" || len(descriptorSpec.OutputSchema) == 0 {
		return agent.ToolDefinition{}, errors.New("kernel descriptor is incomplete: " + descriptorSpec.Name)
	}
	if strings.TrimSpace(toolDefinition.Description) == "" || len(toolDefinition.InputSchema) == 0 {
		return agent.ToolDefinition{}, errors.New("kernel handler definition is incomplete: " + descriptorSpec.Name)
	}
	toolDefinition.ID = kernelToolProviderID + "/" + descriptorSpec.Name
	toolDefinition.ProviderID = kernelToolProviderID
	toolDefinition.Namespace = descriptorSpec.Namespace
	toolDefinition.Name = descriptorSpec.Name
	toolDefinition.PrivacyClass = descriptorSpec.PrivacyClass
	toolDefinition.Visibility = descriptorSpec.Visibility
	toolDefinition.PolicyResource = descriptorSpec.PolicyResource
	toolDefinition.SideEffectClass = descriptorSpec.SideEffectClass
	toolDefinition.RequiresApproval = descriptorSpec.RequiresApproval
	toolDefinition.Completion = agent.ToolCompletion{
		Mode:       descriptorSpec.CompletionMode,
		Action:     descriptorSpec.CompletionAction,
		TargetKind: descriptorSpec.CompletionTargetKind,
	}
	toolDefinition.Idempotency = descriptorSpec.Idempotency
	toolDefinition.OutputSchema = append(json.RawMessage{}, descriptorSpec.OutputSchema...)
	return toolDefinition, nil
}

func newKernelToolProvider(toolCatalogBuilder *ToolCatalogBuilder, handlerContext toolHandlerContext, availableToolSet *agent.ToolSet) kernelToolProvider {
	handlerToolSet := agent.NewToolSet(nil)
	toolCatalogBuilder.registerHistoryTool(handlerToolSet, handlerContext.request)
	toolCatalogBuilder.registerTerminalTools(handlerToolSet, handlerContext)
	toolCatalogBuilder.registerFileTools(handlerToolSet, handlerContext)
	toolCatalogBuilder.registerSkillSearchTool(handlerToolSet, handlerContext, availableToolSet)
	return kernelToolProvider{handlerToolSet: handlerToolSet}
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerKernelTools(toolSet *agent.ToolSet, handlerContext toolHandlerContext) {
	provider := newKernelToolProvider(toolCatalogBuilder, handlerContext, toolSet)
	if errorValue := toolSet.RegisterProvider(context.Background(), provider); errorValue != nil {
		panic(fmt.Errorf("register trusted kernel tool provider: %w", errorValue))
	}
}
