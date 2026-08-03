package acpharness

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/internal/mcpserver"
	"github.com/Dawn-kim-official/blueclaw/taskstate"
)

const toolCatalogServerName = "blueclaw"

type ToolCatalogPublisher interface {
	PublishToolCatalog(requesterToolSet mcpserver.RequesterToolSet) (endpointURL string, bearerToken string, revoke func(), errorValue error)
}

type AgentProcess interface {
	Start(ctx context.Context) (input io.Writer, output io.Reader, wait func() error, errorValue error)
}

type Harness struct {
	agentProcess         AgentProcess
	toolCatalogPublisher ToolCatalogPublisher
	taskRunStore         taskstate.TaskRunStore
}

func New(agentProcess AgentProcess, toolCatalogPublisher ToolCatalogPublisher, taskRunStore taskstate.TaskRunStore) *Harness {
	return &Harness{agentProcess: agentProcess, toolCatalogPublisher: toolCatalogPublisher, taskRunStore: taskRunStore}
}

func (harness *Harness) RunTurn(ctx context.Context, request agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	if harness.agentProcess == nil || harness.toolCatalogPublisher == nil {
		return agentcontract.AgentTurnResult{}, errors.New("acp harness needs an agent process and a tool catalog publisher")
	}
	if strings.TrimSpace(request.RequesterPersonID) == "" {
		return agentcontract.AgentTurnResult{}, errors.New("acp harness refuses a turn with no requester, because tools execute as the requester")
	}
	endpointURL, bearerToken, revokeToolCatalog, errorValue := harness.toolCatalogPublisher.PublishToolCatalog(mcpserver.RequesterToolSet{
		RequesterPersonID: request.RequesterPersonID,
		ToolSet:           request.ToolSet,
	})
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	defer revokeToolCatalog()

	agentInput, agentOutput, waitForAgent, errorValue := harness.agentProcess.Start(ctx)
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	defer func() { _ = waitForAgent() }()

	turnObserver := &sessionObserver{}
	connection := acp.NewClientSideConnection(turnObserver, agentInput, agentOutput)
	if _, errorValue := connection.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	newSession, errorValue := connection.NewSession(ctx, acp.NewSessionRequest{
		Cwd: request.WorkspaceRootPath,
		McpServers: []acp.McpServer{{Http: &acp.McpServerHttpInline{
			Type:    "http",
			Name:    toolCatalogServerName,
			Url:     endpointURL,
			Headers: []acp.HttpHeader{{Name: "Authorization", Value: "Bearer " + bearerToken}},
		}}},
	})
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	promptResponse, errorValue := connection.Prompt(ctx, acp.PromptRequest{
		SessionId: newSession.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock(request.Prompt)},
	})
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	return harness.turnResult(request, turnObserver, promptResponse.StopReason), nil
}

func (harness *Harness) turnResult(request agentcontract.AgentTurnRequest, turnObserver *sessionObserver, stopReason acp.StopReason) agentcontract.AgentTurnResult {
	finishMessage := turnObserver.agentMessage()
	taskRun := taskstate.TaskRun{Status: taskStatusForStopReason(stopReason)}
	if harness.taskRunStore != nil && strings.TrimSpace(request.ExistingTaskRunID) != "" {
		if existingTaskRun, isFound := harness.taskRunStore.FindTaskRun(request.ExistingTaskRunID); isFound {
			taskRun = existingTaskRun
		}
	}
	return agentcontract.AgentTurnResult{
		TaskRun:       taskRun,
		FinishMessage: finishMessage,
		UserNotice:    finishMessage,
		ToolNames:     turnObserver.calledToolNames(),
	}
}

func taskStatusForStopReason(stopReason acp.StopReason) taskstate.TaskStatus {
	switch stopReason {
	case acp.StopReasonEndTurn:
		return taskstate.TaskStatusCompleted
	case acp.StopReasonCancelled:
		return taskstate.TaskStatusCancelled
	case acp.StopReasonRefusal:
		return taskstate.TaskStatusBlocked
	default:
		return taskstate.TaskStatusFailed
	}
}

type sessionObserver struct {
	mutex           sync.Mutex
	messageSegments []string
	toolNames       []string
}

func (observer *sessionObserver) agentMessage() string {
	observer.mutex.Lock()
	defer observer.mutex.Unlock()
	return strings.TrimSpace(strings.Join(observer.messageSegments, ""))
}

func (observer *sessionObserver) calledToolNames() []string {
	observer.mutex.Lock()
	defer observer.mutex.Unlock()
	return append([]string{}, observer.toolNames...)
}

func (observer *sessionObserver) SessionUpdate(_ context.Context, notification acp.SessionNotification) error {
	observer.mutex.Lock()
	defer observer.mutex.Unlock()
	if agentMessage := notification.Update.AgentMessageChunk; agentMessage != nil && agentMessage.Content.Text != nil {
		observer.messageSegments = append(observer.messageSegments, agentMessage.Content.Text.Text)
	}
	if toolCall := notification.Update.ToolCall; toolCall != nil {
		observer.toolNames = append(observer.toolNames, toolCall.Title)
	}
	return nil
}

var errFilesystemAndTerminalGoThroughTheToolCatalog = errors.New("this client does not serve fs or terminal over ACP; blueclaw's file and terminal tools are published on the MCP tool catalog, where they execute as the requester's POSIX user under the approval gate and the event ledger")

func (observer *sessionObserver) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, errFilesystemAndTerminalGoThroughTheToolCatalog
}

func (observer *sessionObserver) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, errFilesystemAndTerminalGoThroughTheToolCatalog
}

func (observer *sessionObserver) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, errFilesystemAndTerminalGoThroughTheToolCatalog
}

func (observer *sessionObserver) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, errFilesystemAndTerminalGoThroughTheToolCatalog
}

func (observer *sessionObserver) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, errFilesystemAndTerminalGoThroughTheToolCatalog
}

func (observer *sessionObserver) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, errFilesystemAndTerminalGoThroughTheToolCatalog
}

func (observer *sessionObserver) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, errFilesystemAndTerminalGoThroughTheToolCatalog
}

func (observer *sessionObserver) RequestPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, errors.New("approval is the sandbox's durable task gate, not an in-turn protocol request")
}
