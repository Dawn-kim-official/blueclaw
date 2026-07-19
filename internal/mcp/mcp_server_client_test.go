package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestListToolsCollectsEveryPage(t *testing.T) {
	session := newPaginatedToolSession(t, 1, "alpha", "bravo", "charlie")

	tools, errorValue := (ServerClient{}).ListTools(context.Background(), session)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	toolNames := make([]string, 0, len(tools))
	for _, tool := range tools {
		toolNames = append(toolNames, tool.Name)
	}
	if !slices.Equal(toolNames, []string{"alpha", "bravo", "charlie"}) {
		t.Fatalf("expected every tool page, got %+v", toolNames)
	}
}

func TestListToolsPreservesContextCancellation(t *testing.T) {
	session := newPaginatedToolSession(t, 1, "alpha", "bravo")
	toolContext, cancel := context.WithCancel(context.Background())
	cancel()

	_, errorValue := (ServerClient{}).ListTools(toolContext, session)

	if !errors.Is(errorValue, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", errorValue)
	}
}

func TestParseToolResultPreservesErrorState(t *testing.T) {
	result, errorValue := ParseToolResult(`{"content":[],"isError":true}`)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.IsError {
		t.Fatal("expected MCP error state")
	}
}

func TestParseToolResultPreservesStructuredContent(t *testing.T) {
	result, errorValue := ParseToolResult(`{"content":[],"structuredContent":{"text":"blueclaw"},"isError":false}`)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(result.StructuredContent) != `{"text":"blueclaw"}` {
		t.Fatalf("expected structured content, got %s", result.StructuredContent)
	}
}

func TestParseToolResultRejectsInvalidOutput(t *testing.T) {
	for _, output := range []string{"invalid", `{"content":[]}`} {
		_, errorValue := ParseToolResult(output)
		if errorValue == nil || !strings.Contains(errorValue.Error(), "invalid normalized result") {
			t.Fatalf("expected invalid result error, got %v", errorValue)
		}
	}
}

func newPaginatedToolSession(t *testing.T, pageSize int, toolNames ...string) *serverSession {
	t.Helper()
	server := newPaginatedToolServer(pageSize, toolNames)
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	serverConnection, errorValue := server.Connect(context.Background(), serverTransport, nil)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() { _ = serverConnection.Close() })
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "1"}, nil)
	clientSession, errorValue := client.Connect(context.Background(), clientTransport, nil)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return &serverSession{session: clientSession}
}

func newPaginatedToolServer(pageSize int, toolNames []string) *sdkmcp.Server {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "test-server", Version: "1"}, &sdkmcp.ServerOptions{PageSize: pageSize})
	for _, toolName := range toolNames {
		server.AddTool(
			&sdkmcp.Tool{Name: toolName, InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`)},
			func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
				return &sdkmcp.CallToolResult{}, nil
			},
		)
	}
	return server
}
