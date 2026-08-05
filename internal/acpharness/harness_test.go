package acpharness

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type publishedToolCatalog struct {
	handlerServer *httptest.Server
	resolver      *mcpserver.SessionTokenRequesterResolver
	revokeCount   int
}

func newPublishedToolCatalog(t *testing.T) *publishedToolCatalog {
	t.Helper()
	grantCount := 0
	resolver := mcpserver.NewSessionTokenRequesterResolver(func() string {
		grantCount++
		return "session-token-" + strconv.Itoa(grantCount)
	})
	handlerServer := httptest.NewServer(mcpserver.NewToolCatalogHandler(resolver, "test"))
	t.Cleanup(handlerServer.Close)
	return &publishedToolCatalog{handlerServer: handlerServer, resolver: resolver}
}

func (catalog *publishedToolCatalog) PublishToolCatalog(requesterToolSet mcpserver.RequesterToolSet) (string, string, func(), error) {
	sessionToken, errorValue := catalog.resolver.GrantSessionToken(requesterToolSet)
	if errorValue != nil {
		return "", "", func() {}, errorValue
	}
	return catalog.handlerServer.URL, sessionToken, func() {
		catalog.revokeCount++
		catalog.resolver.RevokeSessionToken(sessionToken)
	}, nil
}

type daemonExecutedTool struct {
	toolName          string
	requesterPersonID string
}

func requesterToolSet(t *testing.T, requesterPersonID string, executed *[]daemonExecutedTool) *toolcontract.ToolSet {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{"note_write"})
	toolSet.AllowTestReplacement()
	errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{
		ID:              "test:note_write",
		Name:            "note_write",
		Description:     "write a note",
		Visibility:      toolcontract.ToolVisibilityModel,
		InputSchema:     json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
		SideEffectClass: toolcontract.ToolSideEffectStateChange,
		ResultContract:  &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
	}, func(ctx context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		*executed = append(*executed, daemonExecutedTool{toolName: invocation.ToolName, requesterPersonID: requesterPersonID})
		return toolcontract.ToolSuccessData("note written", json.RawMessage(`{}`)), nil
	})
	if errorValue != nil {
		t.Fatalf("expected the tool to register: %v", errorValue)
	}
	return toolSet
}

type inProcessAgentProcess struct {
	agent *externalAgent
}

func (process *inProcessAgentProcess) Start(ctx context.Context) (io.Writer, io.Reader, func() error, error) {
	clientToAgentReader, clientToAgentWriter := io.Pipe()
	agentToClientReader, agentToClientWriter := io.Pipe()
	agentDone := make(chan struct{})
	go func() {
		defer close(agentDone)
		process.agent.serve(ctx, agentToClientWriter, clientToAgentReader)
	}()
	return clientToAgentWriter, agentToClientReader, func() error {
		_ = clientToAgentWriter.Close()
		<-agentDone
		return nil
	}, nil
}

type externalAgent struct {
	acceptsNoHTTPToolCatalog bool
	toolNameToCall           string
	toolArguments            map[string]any
	finalMessage             string
	observedCatalog          []string
	toolCallError            error
}

func (agent *externalAgent) serve(ctx context.Context, output io.Writer, input io.Reader) {
	connection := acp.NewAgentSideConnection(agent, output, input)
	<-connection.Done()
	_ = ctx
}

func (agent *externalAgent) Initialize(_ context.Context, request acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion:   request.ProtocolVersion,
		AgentCapabilities: acp.AgentCapabilities{McpCapabilities: acp.McpCapabilities{Http: !agent.acceptsNoHTTPToolCatalog}},
	}, nil
}

func (agent *externalAgent) NewSession(ctx context.Context, request acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	if len(request.McpServers) != 1 || request.McpServers[0].Http == nil {
		return acp.NewSessionResponse{}, io.ErrUnexpectedEOF
	}
	toolCatalog := request.McpServers[0].Http
	bearerToken := ""
	for _, header := range toolCatalog.Headers {
		if strings.EqualFold(header.Name, "Authorization") {
			bearerToken = strings.TrimPrefix(header.Value, "Bearer ")
		}
	}
	clientSession, errorValue := mcp.NewClient(&mcp.Implementation{Name: "external-agent", Version: "test"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   toolCatalog.Url,
		HTTPClient: &http.Client{Transport: bearerHeader{bearerToken: bearerToken}},
	}, nil)
	if errorValue != nil {
		agent.toolCallError = errorValue
		return acp.NewSessionResponse{SessionId: "session-1"}, nil
	}
	defer clientSession.Close()
	toolList, errorValue := clientSession.ListTools(ctx, nil)
	if errorValue != nil {
		agent.toolCallError = errorValue
		return acp.NewSessionResponse{SessionId: "session-1"}, nil
	}
	for _, tool := range toolList.Tools {
		agent.observedCatalog = append(agent.observedCatalog, tool.Name)
	}
	if _, errorValue := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: agent.toolNameToCall, Arguments: agent.toolArguments}); errorValue != nil {
		agent.toolCallError = errorValue
	}
	return acp.NewSessionResponse{SessionId: "session-1"}, nil
}

func (agent *externalAgent) Prompt(context.Context, acp.PromptRequest) (acp.PromptResponse, error) {
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (agent *externalAgent) Cancel(context.Context, acp.CancelNotification) error { return nil }
func (agent *externalAgent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}
func (agent *externalAgent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}
func (agent *externalAgent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}
func (agent *externalAgent) SetSessionConfigOption(context.Context, acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, nil
}
func (agent *externalAgent) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, nil
}
func (agent *externalAgent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, nil
}
func (agent *externalAgent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, nil
}

type bearerHeader struct {
	bearerToken string
}

func (header bearerHeader) RoundTrip(request *http.Request) (*http.Response, error) {
	request.Header.Set("Authorization", "Bearer "+header.bearerToken)
	return http.DefaultTransport.RoundTrip(request)
}

func TestHarnessRunsAnOutOfProcessAgentWhoseToolCallsExecuteInsideTheDaemon(t *testing.T) {
	executed := []daemonExecutedTool{}
	toolCatalog := newPublishedToolCatalog(t)
	agent := &externalAgent{toolNameToCall: "note_write", toolArguments: map[string]any{"text": "회의록"}}
	harness := New(&inProcessAgentProcess{agent: agent}, toolCatalog, nil)

	turnResult, errorValue := harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
	})
	if errorValue != nil {
		t.Fatalf("expected the external agent turn to run: %v", errorValue)
	}
	if agent.toolCallError != nil {
		t.Fatalf("expected the agent to reach the tool catalog: %v", agent.toolCallError)
	}
	if len(agent.observedCatalog) != 1 || agent.observedCatalog[0] != "note_write" {
		t.Fatalf("expected the agent to see the requester's catalog, got %+v", agent.observedCatalog)
	}
	if len(executed) != 1 || executed[0].toolName != "note_write" || executed[0].requesterPersonID != "person-1" {
		t.Fatalf("expected the daemon to execute the tool as the requester, got %+v", executed)
	}
	if turnResult.TaskRun.Status != "completed" {
		t.Fatalf("expected a completed turn, got %+v", turnResult.TaskRun)
	}
	if toolCatalog.revokeCount != 1 {
		t.Fatalf("expected the tool catalog session to be revoked after the turn, got %d", toolCatalog.revokeCount)
	}
}

func TestHarnessRefusesATurnWithNoRequester(t *testing.T) {
	executed := []daemonExecutedTool{}
	harness := New(&inProcessAgentProcess{agent: &externalAgent{}}, newPublishedToolCatalog(t), nil)

	if _, errorValue := harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		Prompt:  "회의록 정리해줘",
		ToolSet: requesterToolSet(t, "person-1", &executed),
	}); errorValue == nil {
		t.Fatal("expected a turn with no requester to be refused, because tools execute as the requester")
	}
}

func TestAnAgentThatCannotReachTheToolCatalogIsRefusedRatherThanRunWithoutIt(t *testing.T) {
	executed := []daemonExecutedTool{}
	harness := New(&inProcessAgentProcess{agent: &externalAgent{acceptsNoHTTPToolCatalog: true}}, newPublishedToolCatalog(t), nil)

	_, errorValue := harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
	})
	if errorValue == nil {
		t.Fatal("expected an agent that cannot accept the tool catalog to be refused, because a turn answered from no tools looks like a successful turn")
	}
}
