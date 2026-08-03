package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Dawn-kim-official/blueclaw/toolcontract"
)

const toolCatalogServerName = "blueclaw-tool-catalog"

type RequesterToolSet struct {
	RequesterPersonID string
	ToolSet           *toolcontract.ToolSet
	ApprovalGate      ApprovalGate
	HarnessSession    HarnessSession
}

func NewToolCatalogServer(requesterToolSet RequesterToolSet, version string) (*mcp.Server, error) {
	if strings.TrimSpace(requesterToolSet.RequesterPersonID) == "" {
		return nil, errors.New("tool catalog server refuses to serve a tool set with no requester")
	}
	if requesterToolSet.ToolSet == nil {
		return nil, errors.New("tool catalog server requires a tool set")
	}
	server := mcp.NewServer(&mcp.Implementation{Name: toolCatalogServerName, Version: version}, nil)
	for _, toolDescriptor := range requesterToolSet.ToolSet.ListDescribedToolDefinitions() {
		tool, isServable := servableTool(toolDescriptor)
		if !isServable {
			continue
		}
		server.AddTool(tool, invokeThroughToolSet(requesterToolSet, toolDescriptor, tool.OutputSchema != nil))
	}
	return server, nil
}

func servableTool(toolDescriptor toolcontract.ToolDescriptor) (*mcp.Tool, bool) {
	inputSchema := toolDescriptor.InputSchema
	if len(inputSchema) == 0 {
		return nil, false
	}
	var decodedSchema map[string]any
	if json.Unmarshal(inputSchema, &decodedSchema) != nil {
		return nil, false
	}
	tool := &mcp.Tool{
		Name:        toolDescriptor.Name,
		Description: toolDescriptor.Description,
		InputSchema: decodedSchema,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:   toolDescriptor.SideEffectClass == toolcontract.ToolSideEffectRead,
			IdempotentHint: strings.TrimSpace(toolDescriptor.Idempotency) == "idempotent",
		},
		Meta: mcp.Meta{
			"blueclaw/sideEffectClass":         toolDescriptor.SideEffectClass,
			"blueclaw/approvalScope":           toolDescriptor.ApprovalScope,
			"blueclaw/requiresApproval":        toolDescriptor.RequiresApproval,
			"blueclaw/requiresRequesterDevice": toolDescriptor.RequiresRequesterDevice,
		},
	}
	var decodedOutputSchema map[string]any
	if len(toolDescriptor.OutputSchema) > 0 && json.Unmarshal(toolDescriptor.OutputSchema, &decodedOutputSchema) == nil {
		tool.OutputSchema = decodedOutputSchema
	}
	return tool, true
}

func invokeThroughToolSet(requesterToolSet RequesterToolSet, toolDescriptor toolcontract.ToolDescriptor, hasOutputSchema bool) mcp.ToolHandler {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if toolDescriptor.RequiresApproval {
			gateResult, isSettled := awaitApprovalBeforeInvoking(ctx, requesterToolSet, toolDescriptor, request.Params.Arguments)
			if !isSettled {
				return callToolResult(gateResult, hasOutputSchema), nil
			}
		}
		toolResult, errorValue := requesterToolSet.ToolSet.Invoke(ctx, toolcontract.ToolInvocation{
			ToolName: toolDescriptor.Name,
			Input:    request.Params.Arguments,
		})
		if errorValue != nil {
			return nil, errorValue
		}
		return callToolResult(toolResult, hasOutputSchema), nil
	}
}

func awaitApprovalBeforeInvoking(ctx context.Context, requesterToolSet RequesterToolSet, toolDescriptor toolcontract.ToolDescriptor, toolInput json.RawMessage) (toolcontract.ToolResult, bool) {
	if requesterToolSet.ApprovalGate == nil {
		return heldCallResult(errApprovalGateMissing.Error()), false
	}
	outcome, errorValue := requesterToolSet.ApprovalGate.AwaitApproval(ctx, approvalRequestForTool(requesterToolSet, toolDescriptor, toolInput))
	if errorValue != nil {
		return heldCallResult(errorValue.Error()), false
	}
	switch outcome.Decision {
	case ApprovalDecisionApproved:
		return toolcontract.ToolResult{}, true
	case ApprovalDecisionRejected:
		return rejectedCallResult(outcome.Notice), false
	default:
		return heldCallResult(outcome.Notice), false
	}
}

func callToolResult(toolResult toolcontract.ToolResult, hasOutputSchema bool) *mcp.CallToolResult {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: resultText(toolResult)}},
		IsError: toolResult.Failed(),
	}
	if hasOutputSchema && len(toolResult.Output.Data) > 0 {
		var structuredContent any
		if json.Unmarshal(toolResult.Output.Data, &structuredContent) == nil {
			result.StructuredContent = structuredContent
		}
	}
	return result
}

func resultText(toolResult toolcontract.ToolResult) string {
	if toolResult.Failure != nil {
		return fmt.Sprintf("%s: %s", toolResult.Failure.Code, toolResult.Failure.UserSafeSummary)
	}
	return toolResult.Output.Content
}
