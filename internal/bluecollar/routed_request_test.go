package bluecollar

import (
	"context"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/intake"
)

func routedRequest(t *testing.T, responseContext context.Context, agentKernel *AgentKernel, request AgentRequest) AgentRequest {
	t.Helper()
	if request.PrecomputedTurnDecision != nil {
		return request
	}
	turnDecision, errorValue := intake.NewTurnRouter(agentKernel.turnRouterLanguageModel(), agentKernel.intakeOptions).Plan(responseContext, request)
	if errorValue != nil {
		return request
	}
	request.PrecomputedTurnDecision = &turnDecision
	return request
}
