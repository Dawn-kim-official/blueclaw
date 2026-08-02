package agentcontract

import "context"

type Harness interface {
	RunTurn(context.Context, AgentTurnRequest) (AgentTurnResult, error)
	RouteTurn(context.Context, AgentRequest) (TurnDecision, error)
	RunAgentRequest(context.Context, AgentRequest) (AgentTurnResult, error)
	CompleteLaunchFailure(context.Context, AgentTurnRequest, string, string, error) AgentTurnResult
	RefreshSkillIndex(context.Context, InstructionBundle)
}
