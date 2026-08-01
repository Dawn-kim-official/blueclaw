package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/internal/bluecollar"
)

const kernelToolProviderID = "kernel"

type kernelToolDescriptorSpec struct {
	Name              string
	Namespace         string
	PrivacyClass      string
	Visibility        string
	PolicyResource    string
	SideEffectClass   string
	RequiresApproval  bool
	CompletionMode    string
	Idempotency       string
	InputIntentSchema json.RawMessage
	OutputSchema      json.RawMessage
	ResultContract    *bluecollar.ToolResultContract
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
		Name:              bluecollar.TerminalRunToolName,
		Namespace:         "terminal",
		PrivacyClass:      "workspace",
		Visibility:        bluecollar.ToolVisibilityModel,
		PolicyResource:    "tool:terminal.run",
		SideEffectClass:   bluecollar.ToolSideEffectWorkspaceWrite,
		CompletionMode:    bluecollar.ToolCompletionObservation,
		Idempotency:       bluecollar.ToolIdempotencyNone,
		InputIntentSchema: terminalRunInputIntentSchema,
		OutputSchema:      terminalRunResultSchema,
		ResultContract: &bluecollar.ToolResultContract{
			Schema: terminalRunResultSchema,
			EvidenceCondition: &bluecollar.EvidenceCondition{
				ResultField: "completed",
				Equals:      json.RawMessage(`true`),
			},
		},
	},
	{
		Name:              bluecollar.FileDeliverToolName,
		Namespace:         "file",
		PrivacyClass:      "workspace",
		Visibility:        bluecollar.ToolVisibilityModel,
		PolicyResource:    "tool:file.deliver",
		SideEffectClass:   bluecollar.ToolSideEffectExternalWrite,
		CompletionMode:    bluecollar.ToolCompletionObservation,
		Idempotency:       bluecollar.ToolIdempotencyNone,
		InputIntentSchema: fileDeliverInputIntentSchema,
		OutputSchema:      fileDeliverResultSchema,
		ResultContract: &bluecollar.ToolResultContract{
			Schema: fileDeliverResultSchema,
			Effects: []bluecollar.ResourceEffectContract{{
				ObjectType:     "file",
				Effect:         "attached",
				ResultField:    "deliveredPaths",
				EffectIdentity: "path",
			}},
		},
	},
	{
		Name:            bluecollar.SkillSearchToolName,
		Namespace:       "skill",
		PrivacyClass:    "workspace",
		Visibility:      bluecollar.ToolVisibilityModel,
		PolicyResource:  "tool:skill.search",
		SideEffectClass: bluecollar.ToolSideEffectRead,
		CompletionMode:  bluecollar.ToolCompletionNone,
		Idempotency:     bluecollar.ToolIdempotencyNone,
		OutputSchema:    skillSearchResultSchema,
		ResultContract: &bluecollar.ToolResultContract{
			Schema: skillSearchResultSchema,
		},
	},
	{
		Name:            bluecollar.FileReadToolName,
		Namespace:       "file",
		PrivacyClass:    "workspace",
		Visibility:      bluecollar.ToolVisibilityModel,
		PolicyResource:  "tool:file.read",
		SideEffectClass: bluecollar.ToolSideEffectRead,
		CompletionMode:  bluecollar.ToolCompletionNone,
		Idempotency:     bluecollar.ToolIdempotencyNone,
		OutputSchema:    fileReadResultSchema,
		ResultContract: &bluecollar.ToolResultContract{
			Schema: fileReadResultSchema,
		},
	},
	{
		Name:              bluecollar.FileWriteToolName,
		Namespace:         "file",
		PrivacyClass:      "workspace",
		Visibility:        bluecollar.ToolVisibilityModel,
		PolicyResource:    "tool:file.write",
		SideEffectClass:   bluecollar.ToolSideEffectWorkspaceWrite,
		CompletionMode:    bluecollar.ToolCompletionObservation,
		Idempotency:       bluecollar.ToolIdempotencyNone,
		InputIntentSchema: fileWriteInputIntentSchema,
		OutputSchema:      fileWriteResultSchema,
		ResultContract: &bluecollar.ToolResultContract{
			Schema: fileWriteResultSchema,
			Effects: []bluecollar.ResourceEffectContract{
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
		Name:              bluecollar.FileDeleteToolName,
		Namespace:         "file",
		PrivacyClass:      "workspace",
		Visibility:        bluecollar.ToolVisibilityModel,
		PolicyResource:    "tool:file.delete",
		SideEffectClass:   bluecollar.ToolSideEffectDestructive,
		RequiresApproval:  true,
		CompletionMode:    bluecollar.ToolCompletionObservation,
		Idempotency:       bluecollar.ToolIdempotencyNone,
		InputIntentSchema: fileDeleteInputIntentSchema,
		OutputSchema:      fileDeleteResultSchema,
		ResultContract: &bluecollar.ToolResultContract{
			Schema: fileDeleteResultSchema,
			Effects: []bluecollar.ResourceEffectContract{{
				ObjectType:     "file",
				Effect:         "deleted",
				ResultField:    "path",
				EffectIdentity: "path",
			}},
		},
	},
	{
		Name:              bluecollar.FileEditToolName,
		Namespace:         "file",
		PrivacyClass:      "workspace",
		Visibility:        bluecollar.ToolVisibilityModel,
		PolicyResource:    "tool:file.edit",
		SideEffectClass:   bluecollar.ToolSideEffectWorkspaceWrite,
		CompletionMode:    bluecollar.ToolCompletionObservation,
		Idempotency:       bluecollar.ToolIdempotencyNone,
		InputIntentSchema: fileEditInputIntentSchema,
		OutputSchema:      fileEditResultSchema,
		ResultContract: &bluecollar.ToolResultContract{
			Schema: fileEditResultSchema,
			Effects: []bluecollar.ResourceEffectContract{
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
		Name:            bluecollar.FilePreviewToolName,
		Namespace:       "file",
		PrivacyClass:    "workspace",
		Visibility:      bluecollar.ToolVisibilityModel,
		PolicyResource:  "tool:file.preview",
		SideEffectClass: bluecollar.ToolSideEffectRead,
		CompletionMode:  bluecollar.ToolCompletionNone,
		Idempotency:     bluecollar.ToolIdempotencyNone,
		OutputSchema:    filePreviewResultSchema,
		ResultContract: &bluecollar.ToolResultContract{
			Schema: filePreviewResultSchema,
		},
	},
	{
		Name:            bluecollar.PlanUpdateToolName,
		Namespace:       "plan",
		PrivacyClass:    "workspace",
		Visibility:      bluecollar.ToolVisibilityModel,
		PolicyResource:  "tool:plan.update",
		SideEffectClass: bluecollar.ToolSideEffectNone,
		CompletionMode:  bluecollar.ToolCompletionNone,
		Idempotency:     bluecollar.ToolIdempotencyNone,
		OutputSchema:    planUpdateResultSchema,
		ResultContract: &bluecollar.ToolResultContract{
			Schema: planUpdateResultSchema,
		},
	},
	{
		Name:            bluecollar.RequestToolsToolName,
		Namespace:       "tools",
		PrivacyClass:    "workspace",
		Visibility:      bluecollar.ToolVisibilityModel,
		PolicyResource:  "tool:request_tools",
		SideEffectClass: bluecollar.ToolSideEffectNone,
		CompletionMode:  bluecollar.ToolCompletionNone,
		Idempotency:     bluecollar.ToolIdempotencyNone,
		OutputSchema:    requestToolsResultSchema,
		ResultContract: &bluecollar.ToolResultContract{
			Schema: requestToolsResultSchema,
		},
	},
	{
		Name:            bluecollar.ConversationHistoryToolName,
		Namespace:       "conversation",
		PrivacyClass:    "conversation",
		Visibility:      bluecollar.ToolVisibilityModel,
		PolicyResource:  "tool:conversation.history",
		SideEffectClass: bluecollar.ToolSideEffectRead,
		CompletionMode:  bluecollar.ToolCompletionNone,
		Idempotency:     bluecollar.ToolIdempotencyNone,
		OutputSchema:    conversationHistoryResultSchema,
		ResultContract: &bluecollar.ToolResultContract{
			Schema: conversationHistoryResultSchema,
		},
	},
}

type kernelToolProvider struct {
	handlerToolSet *bluecollar.ToolSet
}

func (provider kernelToolProvider) ProviderID() string {
	return kernelToolProviderID
}

func (provider kernelToolProvider) ListTools(context.Context) ([]bluecollar.BoundTool, error) {
	registeredToolNames := provider.handlerToolSet.ListRegisteredToolNames()
	for _, toolName := range registeredToolNames {
		if _, isFound := kernelToolDescriptorSpecForName(toolName); !isFound {
			return nil, fmt.Errorf("kernel provider registered unexpected tool %s", toolName)
		}
	}
	boundTools := make([]bluecollar.BoundTool, 0, len(registeredToolNames))
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

func (provider kernelToolProvider) boundTool(toolDefinition bluecollar.ToolDefinition) (bluecollar.BoundTool, error) {
	canonicalDefinition, errorValue := canonicalKernelToolDescriptor(toolDefinition)
	if errorValue != nil {
		return bluecollar.BoundTool{}, errorValue
	}
	return bluecollar.BoundTool{
		Definition: canonicalDefinition,
		Availability: bluecollar.ToolAvailability{
			Status: bluecollar.ToolAvailabilityAvailable,
		},
		Handler: func(toolContext context.Context, invocation bluecollar.ToolInvocation) (bluecollar.ToolResult, error) {
			invocation.ToolName = canonicalDefinition.Name
			result, errorValue := provider.handlerToolSet.InvokeInternal(toolContext, invocation)
			if errorValue != nil || result.Failed() {
				return result, errorValue
			}
			result.Effects = bluecollar.ProjectResourceEffects(canonicalDefinition.ResultContract, result.Output.Data)
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

func canonicalKernelToolDescriptor(toolDefinition bluecollar.ToolDefinition) (bluecollar.ToolDefinition, error) {
	descriptorSpec, isFound := kernelToolDescriptorSpecForName(toolDefinition.Name)
	if !isFound {
		return bluecollar.ToolDefinition{}, errors.New("kernel descriptor is not registered: " + strings.TrimSpace(toolDefinition.Name))
	}
	if descriptorSpec.Namespace == "" || descriptorSpec.PrivacyClass == "" || descriptorSpec.Visibility == "" || descriptorSpec.PolicyResource == "" || descriptorSpec.SideEffectClass == "" || descriptorSpec.CompletionMode == "" || descriptorSpec.Idempotency == "" || len(descriptorSpec.OutputSchema) == 0 {
		return bluecollar.ToolDefinition{}, errors.New("kernel descriptor is incomplete: " + descriptorSpec.Name)
	}
	if strings.TrimSpace(toolDefinition.Description) == "" || len(toolDefinition.InputSchema) == 0 {
		return bluecollar.ToolDefinition{}, errors.New("kernel handler definition is incomplete: " + descriptorSpec.Name)
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
	toolDefinition.Completion = bluecollar.ToolCompletion{Mode: descriptorSpec.CompletionMode}
	toolDefinition.Idempotency = descriptorSpec.Idempotency
	toolDefinition.InputIntentSchema = append(json.RawMessage{}, descriptorSpec.InputIntentSchema...)
	toolDefinition.OutputSchema = append(json.RawMessage{}, descriptorSpec.OutputSchema...)
	toolDefinition.ResultContract = copyKernelToolResultContract(descriptorSpec.ResultContract)
	return toolDefinition, nil
}

func copyKernelToolResultContract(contract *bluecollar.ToolResultContract) *bluecollar.ToolResultContract {
	if contract == nil {
		return nil
	}
	return &bluecollar.ToolResultContract{
		Schema:            append(json.RawMessage{}, contract.Schema...),
		Effects:           append([]bluecollar.ResourceEffectContract{}, contract.Effects...),
		EvidenceCondition: copyKernelEvidenceCondition(contract.EvidenceCondition),
	}
}

func copyKernelEvidenceCondition(condition *bluecollar.EvidenceCondition) *bluecollar.EvidenceCondition {
	if condition == nil {
		return nil
	}
	return &bluecollar.EvidenceCondition{
		ResultField: condition.ResultField,
		Equals:      append(json.RawMessage{}, condition.Equals...),
	}
}

func newKernelToolProvider(toolCatalogBuilder *ToolCatalogBuilder, handlerContext toolHandlerContext, availableToolSet *bluecollar.ToolSet) kernelToolProvider {
	handlerToolSet := bluecollar.NewToolSet(nil)
	toolCatalogBuilder.registerHistoryTool(handlerToolSet, handlerContext.request)
	toolCatalogBuilder.registerTerminalTools(handlerToolSet, handlerContext)
	toolCatalogBuilder.registerFileTools(handlerToolSet, handlerContext)
	toolCatalogBuilder.registerSkillSearchTool(handlerToolSet, handlerContext, availableToolSet)
	toolCatalogBuilder.registerPlanUpdateTool(handlerToolSet)
	toolCatalogBuilder.registerRequestToolsTool(handlerToolSet)
	return kernelToolProvider{handlerToolSet: handlerToolSet}
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerKernelTools(toolSet *bluecollar.ToolSet, handlerContext toolHandlerContext) {
	provider := newKernelToolProvider(toolCatalogBuilder, handlerContext, toolSet)
	if errorValue := toolSet.RegisterProvider(context.Background(), provider); errorValue != nil {
		panic(fmt.Errorf("register trusted kernel tool provider: %w", errorValue))
	}
}
