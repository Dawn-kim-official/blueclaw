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
		server.AddTool(tool, invokeThroughToolSet(requesterToolSet.ToolSet, toolDescriptor.Name))
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
	return &mcp.Tool{
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
	}, true
}

func invokeThroughToolSet(toolSet *toolcontract.ToolSet, toolName string) mcp.ToolHandler {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		toolResult, errorValue := toolSet.Invoke(ctx, toolcontract.ToolInvocation{
			ToolName: toolName,
			Input:    request.Params.Arguments,
		})
		if errorValue != nil {
			return nil, errorValue
		}
		return callToolResult(toolResult), nil
	}
}

func callToolResult(toolResult toolcontract.ToolResult) *mcp.CallToolResult {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: resultText(toolResult)}},
		IsError: toolResult.Failed(),
	}
	if len(toolResult.Output.Data) > 0 {
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
