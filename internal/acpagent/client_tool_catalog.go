package acpagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Dawn-kim-official/blueclaw/toolcontract"
)

func toolSetFromClientCatalogs(ctx context.Context, mcpServers []acp.McpServer) (*toolcontract.ToolSet, error) {
	catalogTools := []mcpCatalogTool{}
	for _, mcpServer := range mcpServers {
		if mcpServer.Http == nil {
			return nil, errors.New("this agent connects to http mcp servers only")
		}
		serverTools, errorValue := connectCatalog(ctx, *mcpServer.Http)
		if errorValue != nil {
			return nil, errorValue
		}
		catalogTools = append(catalogTools, serverTools...)
	}
	toolNames := make([]string, 0, len(catalogTools))
	for _, catalogTool := range catalogTools {
		toolNames = append(toolNames, catalogTool.definition.Name)
	}
	toolSet := toolcontract.NewToolSet(toolNames)
	for _, catalogTool := range catalogTools {
		if errorValue := toolSet.RegisterTool(catalogTool.descriptor(), catalogTool.handler()); errorValue != nil {
			return nil, errorValue
		}
	}
	return toolSet, nil
}

type mcpCatalogTool struct {
	serverName string
	definition *mcp.Tool
	session    *mcp.ClientSession
}

func connectCatalog(ctx context.Context, httpServer acp.McpServerHttpInline) ([]mcpCatalogTool, error) {
	clientSession, errorValue := mcp.NewClient(&mcp.Implementation{Name: "bluecollar", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   httpServer.Url,
		HTTPClient: &http.Client{Transport: catalogHeaders{headers: httpServer.Headers}},
	}, nil)
	if errorValue != nil {
		return nil, fmt.Errorf("connect tool catalog %q: %w", httpServer.Name, errorValue)
	}
	toolList, errorValue := clientSession.ListTools(ctx, nil)
	if errorValue != nil {
		return nil, fmt.Errorf("list tool catalog %q: %w", httpServer.Name, errorValue)
	}
	catalogTools := make([]mcpCatalogTool, 0, len(toolList.Tools))
	for _, tool := range toolList.Tools {
		catalogTools = append(catalogTools, mcpCatalogTool{serverName: httpServer.Name, definition: tool, session: clientSession})
	}
	return catalogTools, nil
}

func (catalogTool mcpCatalogTool) descriptor() toolcontract.ToolDescriptor {
	inputSchema, errorValue := json.Marshal(catalogTool.definition.InputSchema)
	if errorValue != nil {
		inputSchema = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return toolcontract.ToolDescriptor{
		ID:              "mcp/" + catalogTool.serverName + "/" + catalogTool.definition.Name,
		ProviderID:      "mcp:" + catalogTool.serverName,
		Name:            catalogTool.definition.Name,
		Description:     catalogTool.definition.Description,
		Visibility:      toolcontract.ToolVisibilityModel,
		InputSchema:     inputSchema,
		SideEffectClass: sideEffectClassFromCatalog(catalogTool.definition),
		ResultContract:  &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object"}`)},
	}
}

func sideEffectClassFromCatalog(tool *mcp.Tool) string {
	if declaredClass, isDeclared := tool.Meta["blueclaw/sideEffectClass"].(string); isDeclared && strings.TrimSpace(declaredClass) != "" {
		return declaredClass
	}
	if tool.Annotations != nil && tool.Annotations.ReadOnlyHint {
		return toolcontract.ToolSideEffectRead
	}
	return toolcontract.ToolSideEffectStateChange
}

func (catalogTool mcpCatalogTool) handler() toolcontract.ToolHandler {
	return func(ctx context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		var arguments map[string]any
		if len(invocation.Input) > 0 {
			_ = json.Unmarshal(invocation.Input, &arguments)
		}
		callResult, errorValue := catalogTool.session.CallTool(ctx, &mcp.CallToolParams{Name: catalogTool.definition.Name, Arguments: arguments})
		if errorValue != nil {
			return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.Unavailable, "mcp_call", errorValue.Error()), nil
		}
		if callResult.IsError {
			return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "mcp_call", catalogResultText(callResult)), nil
		}
		return toolcontract.ToolSuccessData(catalogResultText(callResult), json.RawMessage(`{}`)), nil
	}
}

func catalogResultText(callResult *mcp.CallToolResult) string {
	segments := []string{}
	for _, content := range callResult.Content {
		if textContent, isText := content.(*mcp.TextContent); isText {
			segments = append(segments, textContent.Text)
		}
	}
	return strings.Join(segments, "\n")
}

type catalogHeaders struct {
	headers []acp.HttpHeader
}

func (catalogHeaders catalogHeaders) RoundTrip(request *http.Request) (*http.Response, error) {
	for _, header := range catalogHeaders.headers {
		request.Header.Set(header.Name, header.Value)
	}
	return http.DefaultTransport.RoundTrip(request)
}
