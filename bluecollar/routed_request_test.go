package bluecollar

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Dawn-kim-official/blueclaw/model"
)

const routedRequestRoutingTimeout = 30 * time.Second

func routedRequest(t *testing.T, responseContext context.Context, agentKernel *AgentKernel, request AgentRequest) AgentRequest {
	t.Helper()
	if request.PrecomputedTurnDecision != nil {
		return request
	}
	boundedRoutingContext, cancelRouting := context.WithTimeout(responseContext, routedRequestRoutingTimeout)
	defer cancelRouting()
	routingRequest := model.StructuredResponseRequest{
		StructuredOutputSchema: model.StructuredOutputSchema{Name: turnRouterSchemaName},
	}
	response, errorValue := agentKernel.turnRouterLanguageModel().GenerateStructuredResponse(boundedRoutingContext, routingRequest)
	if errorValue != nil {
		return request
	}
	var turnDecision TurnDecision
	if errorValue := json.Unmarshal([]byte(response.Content), &turnDecision); errorValue != nil {
		return request
	}
	request.PrecomputedTurnDecision = &turnDecision
	return request
}
