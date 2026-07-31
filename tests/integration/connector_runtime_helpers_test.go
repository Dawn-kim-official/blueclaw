package integration

import (
	"context"

	"github.com/Dawn-kim-official/blueclaw/internal/agent"
	"github.com/Dawn-kim-official/blueclaw/internal/connectors"
	"github.com/Dawn-kim-official/blueclaw/internal/identity"
	"github.com/Dawn-kim-official/blueclaw/internal/llm"
	"github.com/Dawn-kim-official/blueclaw/internal/task"
)

type integrationLanguageModel struct{}

func (languageModel integrationLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "ok", nil
}

func (languageModel integrationLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{Content: `{"reply":"ok"}`}, nil
}

func newIntegrationConnectorRuntime(identityService *identity.IdentityService) *connectors.ConnectorRuntime {
	taskEventService := task.NewTaskEventService()
	agentKernel := agent.NewAgentKernel(task.NewTaskRunService(taskEventService), task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(integrationLanguageModel{})

	connectorRuntime := connectors.NewConnectorRuntime(identityService, agentKernel, nil)
	return connectorRuntime
}
