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
	InputIntentSchema    json.RawMessage
	OutputSchema         json.RawMessage
	ResultContract       *agent.ToolResultContract
}

var (
	fileReadResultSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"path":{"type":"string"},
			"content":{"type":"string"},
			"startLine":{"type":"integer","minimum":0},
			"endLine":{"type":"integer","minimum":0},
			"totalLines":{"type":"integer","minimum":0},
			"returnedBytes":{"type":"integer","minimum":0},
			"startByte":{"type":"integer","minimum":0},
			"endByte":{"type":"integer","minimum":0},
			"nextByte":{"type":"integer","minimum":0},
			"totalBytes":{"type":"integer","minimum":0},
			"isEndOfFile":{"type":"boolean"},
			"totalLinesKnown":{"type":"boolean"},
			"originalSizeBytes":{"type":"integer","minimum":0},
			"sizeBytes":{"type":"integer","minimum":0},
			"isTruncated":{"type":"boolean"},
			"exists":{"type":"boolean"},
			"optional":{"type":"boolean"},
			"recommendedWritePath":{"type":"string"},
			"readHint":{"type":"string"},
			"source":{"type":"string"},
			"isExactFileRead":{"type":"boolean"}
		},
		"required":["path","content","startLine","endLine","totalLines","returnedBytes","startByte","endByte","nextByte","totalBytes","isEndOfFile","totalLinesKnown","originalSizeBytes","sizeBytes","isTruncated"],
		"additionalProperties":false
		}`)
	fileWriteResultSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"path":{"type":"string","minLength":1},
			"terminalPath":{"type":"string","description":"Absolute filesystem path for terminal.run commands. Quote it: paths may contain spaces."},
			"sizeBytes":{"type":"integer","minimum":0}
		},
		"required":["path","sizeBytes"],
		"additionalProperties":false
		}`)
	fileDeleteResultSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"path":{"type":"string","minLength":1},
			"deleted":{"const":true}
		},
		"required":["path","deleted"],
		"additionalProperties":false
		}`)
	fileEditResultSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"editedFiles":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"uniqueItems":true},
			"editCount":{"type":"integer","minimum":1}
		},
		"required":["editedFiles","editCount"],
		"additionalProperties":false
		}`)
	filePreviewResultSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"path":{"type":"string"},
			"filename":{"type":"string"},
			"contentType":{"type":"string"},
			"sizeBytes":{"type":"integer","minimum":0},
			"previewFormat":{"type":"string","minLength":1},
			"markdownPreview":{"type":"string"},
			"conversionStatus":{"type":"string"},
			"conversionMessage":{"type":"string"}
		},
		"required":["path","filename","contentType","sizeBytes","previewFormat","markdownPreview","conversionStatus","conversionMessage"],
		"additionalProperties":false
		}`)
	fileDeliverResultSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"deliveredPaths":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"uniqueItems":true},
			"attachmentCount":{"type":"integer","minimum":1}
		},
		"required":["deliveredPaths","attachmentCount"],
		"additionalProperties":false
		}`)
	fileWriteInputIntentSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"path":{"type":"string"},
			"content":{"type":"string"}
		},
		"additionalProperties":false
	}`)
	fileDeleteInputIntentSchema = json.RawMessage(`{
		"type":"object",
		"properties":{"path":{"type":"string"}},
		"additionalProperties":false
	}`)
	fileEditInputIntentSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"edits":{
				"type":"array",
				"minItems":1,
				"items":{
					"type":"object",
					"properties":{
						"path":{"type":"string"},
						"oldText":{"type":"string"},
						"newText":{"type":"string"}
					},
					"additionalProperties":false
				}
			}
		},
		"additionalProperties":false
	}`)
	fileDeliverInputIntentSchema = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
)

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
		InputIntentSchema:    terminalRunInputIntentSchema,
		OutputSchema:         terminalRunResultSchema,
		ResultContract: &agent.ToolResultContract{
			Schema: terminalRunResultSchema,
			EvidenceCondition: &agent.EvidenceCondition{
				ResultField: "completed",
				Equals:      json.RawMessage(`true`),
			},
		},
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
		InputIntentSchema:    fileDeliverInputIntentSchema,
		OutputSchema:         fileDeliverResultSchema,
		ResultContract: &agent.ToolResultContract{
			Schema: fileDeliverResultSchema,
			Effects: []agent.ResourceEffectContract{{
				ObjectType:     "file",
				Effect:         "attached",
				ResultField:    "deliveredPaths",
				EffectIdentity: "path",
			}},
		},
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
		OutputSchema:    skillSearchResultSchema,
		ResultContract: &agent.ToolResultContract{
			Schema: skillSearchResultSchema,
		},
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
		OutputSchema:    fileReadResultSchema,
		ResultContract: &agent.ToolResultContract{
			Schema: fileReadResultSchema,
		},
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
		InputIntentSchema:    fileWriteInputIntentSchema,
		OutputSchema:         fileWriteResultSchema,
		ResultContract: &agent.ToolResultContract{
			Schema: fileWriteResultSchema,
			Effects: []agent.ResourceEffectContract{
				{
					ObjectType:     "file",
					Effect:         "created",
					ResultField:    "path",
					EffectIdentity: "path",
				},
				{
					ObjectType:     "workspace",
					Effect:         "modified",
					ResultField:    "path",
					EffectIdentity: "path",
				},
			},
		},
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
		InputIntentSchema:    fileDeleteInputIntentSchema,
		OutputSchema:         fileDeleteResultSchema,
		ResultContract: &agent.ToolResultContract{
			Schema: fileDeleteResultSchema,
			Effects: []agent.ResourceEffectContract{{
				ObjectType:     "file",
				Effect:         "deleted",
				ResultField:    "path",
				EffectIdentity: "path",
			}},
		},
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
		InputIntentSchema:    fileEditInputIntentSchema,
		OutputSchema:         fileEditResultSchema,
		ResultContract: &agent.ToolResultContract{
			Schema: fileEditResultSchema,
			Effects: []agent.ResourceEffectContract{
				{
					ObjectType:     "file",
					Effect:         "updated",
					ResultField:    "editedFiles",
					EffectIdentity: "path",
				},
				{
					ObjectType:     "workspace",
					Effect:         "modified",
					ResultField:    "editedFiles",
					EffectIdentity: "path",
				},
			},
		},
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
		OutputSchema:    filePreviewResultSchema,
		ResultContract: &agent.ToolResultContract{
			Schema: filePreviewResultSchema,
		},
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
		OutputSchema:    conversationHistoryResultSchema,
		ResultContract: &agent.ToolResultContract{
			Schema: conversationHistoryResultSchema,
		},
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
			result, errorValue := provider.handlerToolSet.InvokeInternal(toolContext, invocation)
			if errorValue != nil || result.Failed() {
				return result, errorValue
			}
			result.Effects = agent.ProjectResourceEffects(canonicalDefinition.ResultContract, result.Output.Data)
			return result, nil
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
	toolDefinition.InputIntentSchema = append(json.RawMessage{}, descriptorSpec.InputIntentSchema...)
	toolDefinition.OutputSchema = append(json.RawMessage{}, descriptorSpec.OutputSchema...)
	toolDefinition.ResultContract = copyKernelToolResultContract(descriptorSpec.ResultContract)
	return toolDefinition, nil
}

func copyKernelToolResultContract(contract *agent.ToolResultContract) *agent.ToolResultContract {
	if contract == nil {
		return nil
	}
	return &agent.ToolResultContract{
		Schema:            append(json.RawMessage{}, contract.Schema...),
		Effects:           append([]agent.ResourceEffectContract{}, contract.Effects...),
		EvidenceCondition: copyKernelEvidenceCondition(contract.EvidenceCondition),
	}
}

func copyKernelEvidenceCondition(condition *agent.EvidenceCondition) *agent.EvidenceCondition {
	if condition == nil {
		return nil
	}
	return &agent.EvidenceCondition{
		ResultField: condition.ResultField,
		Equals:      append(json.RawMessage{}, condition.Equals...),
	}
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
