package agentcontract

import "context"

type Harness interface {
	RunTurn(context.Context, AgentTurnRequest) (AgentTurnResult, error)
	RouteTurn(context.Context, AgentRequest) (TurnDecision, error)
}
