package acpagent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"sync"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/Dawn-kim-official/blueclaw/agenttest"
	"github.com/Dawn-kim-official/blueclaw/internal/bluecollar"
	"github.com/Dawn-kim-official/blueclaw/taskstate"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
)

const scriptedToolName = "alpha"

func TestAgentServesOneTurnOverTheProtocol(t *testing.T) {
	client, clientConnection := startTestConnection(t, newTestOptions())

	initializeResponse, errorValue := clientConnection.Initialize(context.Background(), acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientInfo:      &acp.Implementation{Name: "test-client", Version: "0"},
	})
	if errorValue != nil {
		t.Fatalf("initialize failed: %v", errorValue)
	}
	if initializeResponse.ProtocolVersion != acp.ProtocolVersionNumber {
		t.Fatalf("expected protocol version %d, got %d", acp.ProtocolVersionNumber, initializeResponse.ProtocolVersion)
	}
	if initializeResponse.AgentCapabilities.LoadSession {
		t.Fatal("expected loadSession to stay unadvertised while task state owns history")
	}

	newSessionResponse, errorValue := clientConnection.NewSession(context.Background(), acp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []acp.McpServer{},
	})
	if errorValue != nil {
		t.Fatalf("session/new failed: %v", errorValue)
	}
	if newSessionResponse.SessionId == "" {
		t.Fatal("expected a session id")
	}

	promptResponse, errorValue := clientConnection.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newSessionResponse.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("do it")},
	})
	if errorValue != nil {
		t.Fatalf("session/prompt failed: %v", errorValue)
	}
	if promptResponse.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("expected end_turn, got %q", promptResponse.StopReason)
	}

	updates := client.recordedUpdates()
	if messageTexts := agentMessageTexts(updates); len(messageTexts) != 2 || messageTexts[0] != "first reply" || messageTexts[1] != "last reply" {
		t.Fatalf("expected the streamed reply then the closing reply, got %v", messageTexts)
	}
	toolCalls := toolCallUpdates(updates)
	if len(toolCalls) != 1 {
		t.Fatalf("expected one tool call update, got %d", len(toolCalls))
	}
	if toolCalls[0].Title != scriptedToolName {
		t.Fatalf("expected tool call titled %q, got %q", scriptedToolName, toolCalls[0].Title)
	}
	if toolCalls[0].Kind != acp.ToolKindRead {
		t.Fatalf("expected the read tool kind carried by the descriptor, got %q", toolCalls[0].Kind)
	}
	if client.hostBoundaryCallCount() != 0 {
		t.Fatalf("expected no filesystem, terminal, or permission calls to cross to the client, got %d", client.hostBoundaryCallCount())
	}
}

func TestAgentRefusesUnadvertisedSessionMethodsWithAReason(t *testing.T) {
	_, clientConnection := startTestConnection(t, newTestOptions())

	_, errorValue := clientConnection.ListSessions(context.Background(), acp.ListSessionsRequest{})
	requestError := &acp.RequestError{}
	if !errors.As(errorValue, &requestError) {
		t.Fatalf("expected a protocol error, got %v", errorValue)
	}
	if requestError.Code != acp.NewMethodNotFound(acp.AgentMethodSessionList).Code {
		t.Fatalf("expected method not found, got %d", requestError.Code)
	}
	if reasonFromErrorData(requestError.Data) == "" {
		t.Fatalf("expected the refusal to carry a reason, got %v", requestError.Data)
	}
}

func TestAgentRefusesPromptContentItDoesNotAdvertise(t *testing.T) {
	_, clientConnection := startTestConnection(t, newTestOptions())

	newSessionResponse, errorValue := clientConnection.NewSession(context.Background(), acp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []acp.McpServer{},
	})
	if errorValue != nil {
		t.Fatalf("session/new failed: %v", errorValue)
	}
	_, errorValue = clientConnection.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newSessionResponse.SessionId,
		Prompt:    []acp.ContentBlock{acp.ImageBlock("", "image/png")},
	})
	requestError := &acp.RequestError{}
	if !errors.As(errorValue, &requestError) {
		t.Fatalf("expected a protocol error, got %v", errorValue)
	}
	if reasonFromErrorData(requestError.Data) == "" {
		t.Fatalf("expected the refusal to carry a reason, got %v", requestError.Data)
	}
}

func TestAgentRefusesClientSuppliedMcpServersWithAReason(t *testing.T) {
	_, clientConnection := startTestConnection(t, newTestOptions())

	_, errorValue := clientConnection.NewSession(context.Background(), acp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []acp.McpServer{{Stdio: &acp.McpServerStdio{Name: "other", Command: "/bin/true"}}},
	})
	requestError := &acp.RequestError{}
	if !errors.As(errorValue, &requestError) {
		t.Fatalf("expected a protocol error, got %v", errorValue)
	}
	if reasonFromErrorData(requestError.Data) == "" {
		t.Fatalf("expected the refusal to carry a reason, got %v", requestError.Data)
	}
}

func newTestOptions() Options {
	languageModel := agenttest.NewActionScriptedLanguageModel(
		`{"action":"continue","toolName":"`+scriptedToolName+`","toolInput":{},"message":"first reply"}`,
		finishActionDocument("last reply"),
	)
	turnRunner := bluecollar.NewAgentTurnRunner(
		taskstate.NewTaskRunService(taskstate.NewTaskEventService()),
		taskstate.NewTaskStepService(),
		taskstate.NewTaskArtifactService(),
		languageModel,
		bluecollar.TurnOptions{},
	)
	sessionCount := 0
	return Options{
		TurnStreamer:      turnRunner,
		BuildToolSet:      func(ToolSetRequest) *toolcontract.ToolSet { return newScriptedToolSet() },
		RequesterPersonID: "person-1",
		NewSessionIdentity: func() string {
			sessionCount++
			return "session-" + strconv.Itoa(sessionCount)
		},
	}
}

func startTestConnection(t *testing.T, options Options) (*recordingClient, *acp.ClientSideConnection) {
	t.Helper()
	agentReader, clientWriter := io.Pipe()
	clientReader, agentWriter := io.Pipe()
	agent := New(options)
	agent.UseConnection(acp.NewAgentSideConnection(agent, agentWriter, agentReader))
	client := &recordingClient{}
	clientConnection := acp.NewClientSideConnection(client, clientWriter, clientReader)
	t.Cleanup(func() {
		_ = clientWriter.Close()
		_ = agentWriter.Close()
	})
	return client, clientConnection
}

func newScriptedToolSet() *toolcontract.ToolSet {
	toolSet := toolcontract.NewToolSet([]string{scriptedToolName})
	_ = toolSet.RegisterTool(toolcontract.ToolDefinition{
		ID:                "test:" + scriptedToolName,
		Name:              scriptedToolName,
		Visibility:        toolcontract.ToolVisibilityModel,
		SideEffectClass:   toolcontract.ToolSideEffectRead,
		InputSchema:       json.RawMessage(`{"type":"object","properties":{}}`),
		InputIntentSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		OutputSchema:      json.RawMessage(`{"type":"object","properties":{}}`),
		ResultContract: &toolcontract.ToolResultContract{
			Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		},
	}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolSuccessData("alpha result", json.RawMessage(`{}`)), nil
	})
	return toolSet
}

func finishActionDocument(reply string) string {
	document, errorValue := json.Marshal(map[string]any{
		"action":             "finish",
		"message":            reply,
		"completionSummary":  reply,
		"replyParts":         []map[string]string{{"type": "text", "text": reply}},
		"goalStatus":         "satisfied",
		"goalSatisfied":      true,
		"completionEvidence": []string{},
		"qualityReview":      []string{},
	})
	if errorValue != nil {
		return ""
	}
	return string(document)
}

func agentMessageTexts(updates []acp.SessionUpdate) []string {
	messageTexts := []string{}
	for _, update := range updates {
		if update.AgentMessageChunk == nil || update.AgentMessageChunk.Content.Text == nil {
			continue
		}
		messageTexts = append(messageTexts, update.AgentMessageChunk.Content.Text.Text)
	}
	return messageTexts
}

func toolCallUpdates(updates []acp.SessionUpdate) []*acp.SessionUpdateToolCall {
	toolCalls := []*acp.SessionUpdateToolCall{}
	for _, update := range updates {
		if update.ToolCall != nil {
			toolCalls = append(toolCalls, update.ToolCall)
		}
	}
	return toolCalls
}

func reasonFromErrorData(data any) string {
	document, errorValue := json.Marshal(data)
	if errorValue != nil {
		return ""
	}
	var decoded struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(document, &decoded) != nil {
		return ""
	}
	return decoded.Reason
}

type recordingClient struct {
	mutex             sync.Mutex
	updates           []acp.SessionUpdate
	hostBoundaryCalls int
}

var _ acp.Client = (*recordingClient)(nil)

func (client *recordingClient) SessionUpdate(_ context.Context, notification acp.SessionNotification) error {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.updates = append(client.updates, notification.Update)
	return nil
}

func (client *recordingClient) recordedUpdates() []acp.SessionUpdate {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return append([]acp.SessionUpdate{}, client.updates...)
}

func (client *recordingClient) hostBoundaryCallCount() int {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return client.hostBoundaryCalls
}

func (client *recordingClient) recordHostBoundaryCall() error {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.hostBoundaryCalls++
	return errors.New("the test client implements no host-side capability")
}

func (client *recordingClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, client.recordHostBoundaryCall()
}

func (client *recordingClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, client.recordHostBoundaryCall()
}

func (client *recordingClient) RequestPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, client.recordHostBoundaryCall()
}

func (client *recordingClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, client.recordHostBoundaryCall()
}

func (client *recordingClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, client.recordHostBoundaryCall()
}

func (client *recordingClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, client.recordHostBoundaryCall()
}

func (client *recordingClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, client.recordHostBoundaryCall()
}

func (client *recordingClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, client.recordHostBoundaryCall()
}
