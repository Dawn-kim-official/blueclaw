package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	CatalogEndpointEnvironmentName = "BLUECLAW_TOOL_CATALOG_URL"
	CatalogTokenEnvironmentName    = "BLUECLAW_TOOL_CATALOG_TOKEN"

	StdioBridgeCommand = "mcp-tool-catalog"
)

type bearerToken struct {
	token string
}

func (header bearerToken) RoundTrip(request *http.Request) (*http.Response, error) {
	request.Header.Set("Authorization", "Bearer "+header.token)
	return http.DefaultTransport.RoundTrip(request)
}

func RunStdioCatalogBridge(ctx context.Context, endpointURL string, sessionToken string) error {
	if strings.TrimSpace(endpointURL) == "" || strings.TrimSpace(sessionToken) == "" {
		return errors.New("the catalog bridge needs the endpoint and the session token of a published catalog")
	}
	upstream, errorValue := mcp.NewClient(&mcp.Implementation{Name: toolCatalogServerName, Version: "bridge"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   endpointURL,
		HTTPClient: &http.Client{Transport: bearerToken{token: sessionToken}},
	}, nil)
	if errorValue != nil {
		return errorValue
	}
	defer upstream.Close()

	toolList, errorValue := upstream.ListTools(ctx, nil)
	if errorValue != nil {
		return errorValue
	}
	server := mcp.NewServer(&mcp.Implementation{Name: toolCatalogServerName, Version: "bridge"}, nil)
	for _, tool := range toolList.Tools {
		server.AddTool(tool, forwardToolCall(upstream, tool.Name))
	}
	return server.Run(ctx, &mcp.StdioTransport{})
}

func forwardToolCall(upstream *mcp.ClientSession, toolName string) mcp.ToolHandler {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return upstream.CallTool(ctx, &mcp.CallToolParams{
			Name:      toolName,
			Arguments: request.Params.Arguments,
			Meta:      request.Params.Meta,
		})
	}
}
