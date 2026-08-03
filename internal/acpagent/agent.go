package acpagent

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/internal/bluecollar"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
)

type TurnStreamer interface {
	StreamTurn(context.Context, agentcontract.AgentTurnRequest) <-chan bluecollar.TurnEvent
}

type ToolSetRequest struct {
	WorkspaceRootPath string
	RequesterPersonID string
	Prompt            string
}

type Options struct {
	TurnStreamer       TurnStreamer
	BuildToolSet       func(ToolSetRequest) *toolcontract.ToolSet
	BuildInstruction   func(workspaceRootPath string) string
	RequesterPersonID  string
	AgentName          string
	AgentVersion       string
	ResponseLanguage   string
	TaskLevel          agentcontract.TaskLevel
	NewSessionIdentity func() string
}

type Agent struct {
	options    Options
	connection *acp.AgentSideConnection
	mutex      sync.Mutex
	sessions   map[acp.SessionId]*session
}

var _ acp.Agent = (*Agent)(nil)

func New(options Options) *Agent {
	return &Agent{options: normalizeOptions(options), sessions: map[acp.SessionId]*session{}}
}

func (agent *Agent) UseConnection(connection *acp.AgentSideConnection) {
	agent.connection = connection
}

func Serve(ctx context.Context, options Options, readFromClient io.Reader, writeToClient io.Writer) error {
	agent := New(options)
	connection := acp.NewAgentSideConnection(agent, writeToClient, readFromClient)
	agent.UseConnection(connection)
	select {
	case <-connection.Done():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (agent *Agent) Initialize(_ context.Context, request acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: negotiatedProtocolVersion(request.ProtocolVersion),
		AgentInfo:       &acp.Implementation{Name: agent.options.AgentName, Version: agent.options.AgentVersion},
		AuthMethods:     []acp.AuthMethod{},
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession:        false,
			PromptCapabilities: acp.PromptCapabilities{Image: false, Audio: false, EmbeddedContext: false},
		},
	}, nil
}

func negotiatedProtocolVersion(requestedVersion acp.ProtocolVersion) acp.ProtocolVersion {
	if requestedVersion > 0 && requestedVersion < acp.ProtocolVersionNumber {
		return requestedVersion
	}
	return acp.ProtocolVersionNumber
}

func (agent *Agent) NewSession(ctx context.Context, request acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	workspaceRootPath := strings.TrimSpace(request.Cwd)
	if workspaceRootPath == "" {
		return acp.NewSessionResponse{}, acp.NewInvalidParams(map[string]any{"reason": "cwd is required and must be an absolute path"})
	}
	clientToolSet, errorValue := clientSuppliedToolSet(ctx, request.McpServers)
	if errorValue != nil {
		return acp.NewSessionResponse{}, acp.NewInvalidParams(map[string]any{"reason": errorValue.Error()})
	}
	sessionID := acp.SessionId(agent.options.NewSessionIdentity())
	agent.mutex.Lock()
	agent.sessions[sessionID] = &session{sessionID: sessionID, workspaceRootPath: workspaceRootPath, clientToolSet: clientToolSet}
	agent.mutex.Unlock()
	return acp.NewSessionResponse{SessionId: sessionID}, nil
}

func (agent *Agent) Prompt(ctx context.Context, request acp.PromptRequest) (acp.PromptResponse, error) {
	promptSession, isKnown := agent.findSession(request.SessionId)
	if !isKnown {
		return acp.PromptResponse{}, acp.NewInvalidParams(map[string]any{
			"sessionId": string(request.SessionId),
			"reason":    "unknown session; call session/new first",
		})
	}
	promptText, errorValue := promptTextFromContentBlocks(request.Prompt)
	if errorValue != nil {
		return acp.PromptResponse{}, errorValue
	}
	return agent.runTurn(ctx, request.SessionId, promptSession, promptText)
}

func (agent *Agent) runTurn(ctx context.Context, sessionID acp.SessionId, promptSession *session, promptText string) (acp.PromptResponse, error) {
	promptSession.mutex.Lock()
	defer promptSession.mutex.Unlock()

	toolSet := promptSession.clientToolSet
	if toolSet == nil {
		toolSet = agent.options.BuildToolSet(ToolSetRequest{
			WorkspaceRootPath: promptSession.workspaceRootPath,
			RequesterPersonID: agent.options.RequesterPersonID,
			Prompt:            promptText,
		})
	}
	turnResult, errorValue := agent.streamTurnEvents(ctx, sessionID, promptSession, toolSet, promptText)
	if errorValue != nil {
		return acp.PromptResponse{}, errorValue
	}
	promptSession.taskRunID = continuationTaskRunID(turnResult)
	agent.publishClosingMessage(ctx, sessionID, turnResult)
	return acp.PromptResponse{StopReason: stopReasonForTurn(ctx, turnResult)}, nil
}

func (agent *Agent) streamTurnEvents(ctx context.Context, sessionID acp.SessionId, promptSession *session, toolSet *toolcontract.ToolSet, promptText string) (agentcontract.AgentTurnResult, error) {
	turnEvents := agent.options.TurnStreamer.StreamTurn(ctx, agent.turnRequest(promptSession, toolSet, promptText))
	finalEvent := bluecollar.TurnEvent{}
	for turnEvent := range turnEvents {
		if turnEvent.Kind == bluecollar.TurnEventFinal {
			finalEvent = turnEvent
			continue
		}
		agent.publishSessionUpdate(ctx, sessionID, sessionUpdateForTurnEvent(turnEvent, toolSet, promptSession.nextToolCallIdentity()))
	}
	if finalEvent.Error != nil {
		return agentcontract.AgentTurnResult{}, finalEvent.Error
	}
	return finalEvent.Result, nil
}

func (agent *Agent) turnRequest(promptSession *session, toolSet *toolcontract.ToolSet, promptText string) agentcontract.AgentTurnRequest {
	return agentcontract.AgentTurnRequest{
		ConversationID:       string(promptSession.sessionID),
		ExistingTaskRunID:    promptSession.taskRunID,
		Prompt:               promptText,
		RequesterPersonID:    agent.options.RequesterPersonID,
		ResponseLanguage:     agent.options.ResponseLanguage,
		ToolSet:              toolSet,
		PinnedToolNames:      toolSet.ListToolNames(),
		WorkspaceRootPath:    promptSession.workspaceRootPath,
		WorkspaceDefaultPath: promptSession.workspaceRootPath,
		InstructionPrompt:    agent.options.BuildInstruction(promptSession.workspaceRootPath),
		TaskLevel:            agent.options.TaskLevel,
		CheckpointSender:     acceptCheckpointForStreaming,
	}
}

func acceptCheckpointForStreaming(context.Context, agentcontract.AgentCheckpoint) error {
	return nil
}

func (agent *Agent) publishClosingMessage(ctx context.Context, sessionID acp.SessionId, turnResult agentcontract.AgentTurnResult) {
	closingMessage := strings.TrimSpace(turnResult.FinishMessage)
	if closingMessage == "" {
		closingMessage = strings.TrimSpace(turnResult.UserNotice)
	}
	if closingMessage == "" {
		return
	}
	agent.publishSessionUpdate(ctx, sessionID, acp.UpdateAgentMessageText(closingMessage))
}

func (agent *Agent) publishSessionUpdate(ctx context.Context, sessionID acp.SessionId, update acp.SessionUpdate) {
	if agent.connection == nil {
		return
	}
	_ = agent.connection.SessionUpdate(ctx, acp.SessionNotification{SessionId: sessionID, Update: update})
}

func (agent *Agent) findSession(sessionID acp.SessionId) (*session, bool) {
	agent.mutex.Lock()
	defer agent.mutex.Unlock()
	promptSession, isKnown := agent.sessions[sessionID]
	return promptSession, isKnown
}

func (agent *Agent) Cancel(context.Context, acp.CancelNotification) error {
	return nil
}

func (agent *Agent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, unsupportedFeature(acp.AgentMethodAuthenticate,
		"this agent advertises no authentication methods; the host it runs inside owns identity")
}

func (agent *Agent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, unsupportedFeature(acp.AgentMethodLogout,
		"this agent advertises no authentication methods, so there is no session to log out of")
}

func (agent *Agent) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, unsupportedFeature(acp.AgentMethodSessionClose,
		"the close session capability is not advertised; task runs are retired by the host task store, not by the client")
}

func (agent *Agent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, unsupportedFeature(acp.AgentMethodSessionList,
		"the list sessions capability is not advertised; the host owns which task runs a person may see")
}

func (agent *Agent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, unsupportedFeature(acp.AgentMethodSessionResume,
		"the resume session capability is not advertised; a session lives only as long as this connection")
}

func (agent *Agent) SetSessionConfigOption(context.Context, acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, unsupportedFeature(acp.AgentMethodSessionSetConfigOption,
		"this agent publishes no session configuration options")
}

func (agent *Agent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, unsupportedFeature(acp.AgentMethodSessionSetMode,
		"this agent publishes no session modes; the approval boundary is the host runtime gate, not a client mode")
}

func unsupportedFeature(methodName string, reason string) *acp.RequestError {
	requestError := acp.NewMethodNotFound(methodName)
	requestError.Data = map[string]any{"method": methodName, "reason": reason}
	return requestError
}

func normalizeOptions(options Options) Options {
	if options.BuildInstruction == nil {
		options.BuildInstruction = func(string) string { return "" }
	}
	if options.NewSessionIdentity == nil {
		options.NewSessionIdentity = newRandomSessionIdentity
	}
	if strings.TrimSpace(string(options.TaskLevel)) == "" {
		options.TaskLevel = agentcontract.TaskLevelLow
	}
	if strings.TrimSpace(options.AgentName) == "" {
		options.AgentName = "bluecollar"
	}
	if strings.TrimSpace(options.AgentVersion) == "" {
		options.AgentVersion = "0"
	}
	return options
}

func promptTextFromContentBlocks(contentBlocks []acp.ContentBlock) (string, error) {
	lines := make([]string, 0, len(contentBlocks))
	for _, contentBlock := range contentBlocks {
		line, errorValue := promptLineForContentBlock(contentBlock)
		if errorValue != nil {
			return "", errorValue
		}
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	promptText := strings.TrimSpace(strings.Join(lines, "\n"))
	if promptText == "" {
		return "", acp.NewInvalidParams(map[string]any{"reason": "the prompt carries no text or resource link content"})
	}
	return promptText, nil
}

func promptLineForContentBlock(contentBlock acp.ContentBlock) (string, error) {
	if contentBlock.Text != nil {
		return contentBlock.Text.Text, nil
	}
	if contentBlock.ResourceLink != nil {
		return fmt.Sprintf("%s (%s)", contentBlock.ResourceLink.Name, contentBlock.ResourceLink.Uri), nil
	}
	if contentBlock.Image != nil {
		return "", unadvertisedPromptContent("image")
	}
	if contentBlock.Audio != nil {
		return "", unadvertisedPromptContent("audio")
	}
	if contentBlock.Resource != nil {
		return "", unadvertisedPromptContent("embeddedContext")
	}
	return "", acp.NewInvalidParams(map[string]any{"reason": "the prompt carries a content block this agent does not recognise"})
}

func unadvertisedPromptContent(capabilityName string) *acp.RequestError {
	return acp.NewInvalidParams(map[string]any{
		"capability": capabilityName,
		"reason":     "this agent reports " + capabilityName + " as unsupported in its prompt capabilities",
	})
}

func clientSuppliedToolSet(ctx context.Context, mcpServers []acp.McpServer) (*toolcontract.ToolSet, error) {
	if len(mcpServers) == 0 {
		return nil, nil
	}
	return toolSetFromClientCatalogs(ctx, mcpServers)
}
