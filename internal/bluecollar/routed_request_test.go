package bluecollar

import (
	"context"
	"testing"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/intake"
)

const routedRequestRoutingTimeout = 30 * time.Second

func routedRequest(t *testing.T, responseContext context.Context, agentKernel *AgentKernel, request AgentRequest) AgentRequest {
	t.Helper()
	if request.PrecomputedTurnDecision != nil {
		return request
	}
	boundedRoutingContext, cancelRouting := context.WithTimeout(responseContext, routedRequestRoutingTimeout)
	defer cancelRouting()
	turnDecision, errorValue := intake.NewTurnRouter(agentKernel.turnRouterLanguageModel(), agentKernel.intakeOptions).Plan(boundedRoutingContext, request)
	if errorValue != nil {
		return request
	}
	request.PrecomputedTurnDecision = &turnDecision
	return request
}
