package container

import (
	"context"
	"fmt"

	"github.com/blueclaw/blueclaw/internal/ipc"
	"github.com/blueclaw/blueclaw/internal/provider"
)

type AgentRunner struct {
	manager *Manager
}

func NewAgentRunner(manager *Manager) *AgentRunner {
	return &AgentRunner{manager: manager}
}

func (runner *AgentRunner) RunAgent(executionContext context.Context, request provider.Request, sessionID string) (provider.Response, error) {
	agentRequest := ipc.AgentRequest{
		SystemPrompt: request.SystemPrompt,
		Messages:     request.Messages,
		Model:        request.Model,
	}
	agentResponse, err := runner.manager.RunAgent(executionContext, sessionID, agentRequest)
	if err != nil {
		return provider.Response{}, fmt.Errorf("container agent: %w", err)
	}
	return provider.Response{
		Message:             agentResponse.Message,
		ToolCalls:           agentResponse.ToolCalls,
		IntermediateContent: agentResponse.IntermediateContent,
	}, nil
}
