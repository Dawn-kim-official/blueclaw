package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	toolName := flag.String("tool-name", "", "the tool to call once the tool catalog is reachable")
	toolArgumentsJSON := flag.String("tool-arguments-json", "{}", "json object of arguments to pass to the tool call")
	flag.Parse()

	if strings.TrimSpace(*toolName) == "" {
		fmt.Fprintln(os.Stderr, "acptestagent: --tool-name is required")
		os.Exit(2)
	}
	var toolArguments map[string]any
	if errorValue := json.Unmarshal([]byte(*toolArgumentsJSON), &toolArguments); errorValue != nil {
		fmt.Fprintln(os.Stderr, "acptestagent: --tool-arguments-json must be a json object:", errorValue)
		os.Exit(2)
	}

	agent := &testAgent{toolName: *toolName, toolArguments: toolArguments}
	connection := acp.NewAgentSideConnection(agent, os.Stdout, os.Stdin)
	<-connection.Done()
}

type testAgent struct {
	toolName      string
	toolArguments map[string]any
}

func (agent *testAgent) Initialize(_ context.Context, request acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion:   request.ProtocolVersion,
		AgentInfo:         &acp.Implementation{Name: "acptestagent", Version: "0"},
		AgentCapabilities: acp.AgentCapabilities{McpCapabilities: acp.McpCapabilities{Http: true}},
	}, nil
}

func (agent *testAgent) NewSession(ctx context.Context, request acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	if len(request.McpServers) != 1 || request.McpServers[0].Http == nil {
		return acp.NewSessionResponse{}, fmt.Errorf("acptestagent connects to exactly one http tool catalog, got %d servers", len(request.McpServers))
	}
	toolCatalog := *request.McpServers[0].Http
	clientSession, errorValue := mcp.NewClient(&mcp.Implementation{Name: "acptestagent", Version: "0"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   toolCatalog.Url,
		HTTPClient: &http.Client{Transport: bearerHeader{headers: toolCatalog.Headers}},
	}, nil)
	if errorValue != nil {
		return acp.NewSessionResponse{}, fmt.Errorf("connect tool catalog %q: %w", toolCatalog.Name, errorValue)
	}
	defer clientSession.Close()
	if _, errorValue := clientSession.ListTools(ctx, nil); errorValue != nil {
		return acp.NewSessionResponse{}, fmt.Errorf("list tool catalog %q: %w", toolCatalog.Name, errorValue)
	}
	if _, errorValue := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: agent.toolName, Arguments: agent.toolArguments}); errorValue != nil {
		return acp.NewSessionResponse{}, fmt.Errorf("call catalog tool %q: %w", agent.toolName, errorValue)
	}
	return acp.NewSessionResponse{SessionId: "acptestagent-session"}, nil
}

func (agent *testAgent) Prompt(context.Context, acp.PromptRequest) (acp.PromptResponse, error) {
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (agent *testAgent) Cancel(context.Context, acp.CancelNotification) error { return nil }

func (agent *testAgent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

func (agent *testAgent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}

func (agent *testAgent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}

func (agent *testAgent) SetSessionConfigOption(context.Context, acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, nil
}

func (agent *testAgent) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, nil
}

func (agent *testAgent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, nil
}

func (agent *testAgent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, nil
}

type bearerHeader struct {
	headers []acp.HttpHeader
}

func (header bearerHeader) RoundTrip(request *http.Request) (*http.Response, error) {
	for _, headerField := range header.headers {
		request.Header.Set(headerField.Name, headerField.Value)
	}
	return http.DefaultTransport.RoundTrip(request)
}
