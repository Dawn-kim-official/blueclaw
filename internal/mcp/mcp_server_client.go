package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type ServerClient struct{}

type ToolResult struct {
	Content           []json.RawMessage `json:"content"`
	StructuredContent json.RawMessage   `json:"structuredContent,omitempty"`
	IsError           bool              `json:"isError"`
}

type serverSession struct {
	session *sdkmcp.ClientSession
}

func (serverClient ServerClient) Connect(ctx context.Context, serverDefinition ServerDefinition) (*serverSession, error) {
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "blueclaw", Version: "1"}, nil)
	transport, errorValue := clientTransport(serverDefinition)
	if errorValue != nil {
		return nil, errorValue
	}
	session, errorValue := client.Connect(ctx, transport, nil)
	if errorValue != nil {
		return nil, errorValue
	}
	return &serverSession{session: session}, nil
}

func (serverClient ServerClient) ListTools(ctx context.Context, session *serverSession) ([]*sdkmcp.Tool, error) {
	tools := []*sdkmcp.Tool{}
	for tool, errorValue := range session.session.Tools(ctx, nil) {
		if errorValue != nil {
			return nil, errorValue
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

func (serverClient ServerClient) InvokeTool(ctx context.Context, session *serverSession, invocation Invocation) (string, error) {
	arguments, errorValue := parseToolArguments(invocation.Input)
	if errorValue != nil {
		return "", errorValue
	}
	result, errorValue := session.session.CallTool(ctx, &sdkmcp.CallToolParams{Name: invocation.ToolName, Arguments: arguments})
	if errorValue != nil {
		return "", errorValue
	}
	return normalizeToolResult(result)
}

func clientTransport(serverDefinition ServerDefinition) (sdkmcp.Transport, error) {
	switch serverDefinition.Transport {
	case TransportStdio:
		if strings.TrimSpace(serverDefinition.Command) == "" {
			return nil, errors.New("mcp stdio command is required")
		}
		return &sdkmcp.CommandTransport{Command: exec.Command(serverDefinition.Command, serverDefinition.Arguments...)}, nil
	case TransportStreamableHTTP:
		if strings.TrimSpace(serverDefinition.Endpoint) == "" {
			return nil, errors.New("mcp streamable http endpoint is required")
		}
		return &sdkmcp.StreamableClientTransport{Endpoint: serverDefinition.Endpoint, MaxRetries: -1, DisableStandaloneSSE: true}, nil
	default:
		return nil, errors.New("mcp transport is unsupported")
	}
}

func parseToolArguments(input string) (map[string]any, error) {
	if strings.TrimSpace(input) == "" {
		return map[string]any{}, nil
	}
	var arguments map[string]any
	if errorValue := json.Unmarshal([]byte(input), &arguments); errorValue != nil {
		return nil, errors.New("mcp tool input must be a JSON object")
	}
	if arguments == nil {
		return nil, errors.New("mcp tool input must be a JSON object")
	}
	return arguments, nil
}

func normalizeToolResult(result *sdkmcp.CallToolResult) (string, error) {
	if result == nil {
		return "", errors.New("mcp tool returned no result")
	}
	content := make([]json.RawMessage, 0, len(result.Content))
	for _, item := range result.Content {
		encoded, errorValue := item.MarshalJSON()
		if errorValue != nil {
			return "", errors.New("mcp tool returned invalid content")
		}
		content = append(content, encoded)
	}
	normalized := ToolResult{Content: content, IsError: result.IsError}
	if result.StructuredContent != nil {
		encoded, errorValue := json.Marshal(result.StructuredContent)
		if errorValue != nil {
			return "", errors.New("mcp tool returned invalid structured content")
		}
		normalized.StructuredContent = encoded
	}
	encoded, errorValue := json.Marshal(normalized)
	if errorValue != nil {
		return "", errors.New("mcp tool result could not be normalized")
	}
	return string(encoded), nil
}

func ParseToolResult(output string) (ToolResult, error) {
	var document struct {
		Content           []json.RawMessage `json:"content"`
		StructuredContent json.RawMessage   `json:"structuredContent,omitempty"`
		IsError           *bool             `json:"isError"`
	}
	if errorValue := json.Unmarshal([]byte(output), &document); errorValue != nil || document.IsError == nil {
		return ToolResult{}, errors.New("mcp tool returned invalid normalized result")
	}
	return ToolResult{Content: document.Content, StructuredContent: document.StructuredContent, IsError: *document.IsError}, nil
}
