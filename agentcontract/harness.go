package agentcontract

import "context"

type Harness interface {
	RunTurn(context.Context, AgentTurnRequest) (AgentTurnResult, error)
	RouteTurn(context.Context, AgentRequest) (TurnDecision, error)
	RunAgentRequest(context.Context, AgentRequest) (AgentTurnResult, error)
	CompleteLaunchFailure(context.Context, AgentTurnRequest, string, string, error) AgentTurnResult
	GenerateReply(context.Context, string) (string, error)
	GenerateReplyWithContext(context.Context, string, VisibleContext, []MemoryFact) (string, error)
	RefreshSkillIndex(context.Context, InstructionBundle)
}
