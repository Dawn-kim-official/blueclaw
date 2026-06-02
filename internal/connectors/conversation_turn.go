package connectors

import (
	"context"

	"blueclaw/internal/agent"
	"blueclaw/internal/agentruntime"
	"blueclaw/internal/policy"
)

type ConversationTurn struct {
	Platform                  string
	Adapter                   PlatformAdapter
	Event                     PlatformInboundEvent
	ReplyTarget               ReplyTarget
	RequesterPersonID         string
	RequesterEmail            string
	PersonAccess              policy.PersonAccess
	IsApprovalContinuation    bool
	ActiveGoal                agent.ActiveGoal
	HasActiveGoal             bool
	PrecomputedTurnDecision   *agent.TurnDecision
	CheckpointSender          agent.AgentCheckpointSender
	AccessibleConversationIDs []string
}

func (connectorRuntime *ConnectorRuntime) buildTaskLaunchRequest(turn ConversationTurn) agentruntime.TaskLaunchRequest {
	event := turn.Event
	return agentruntime.TaskLaunchRequest{
		Source:                    agentruntime.TaskLaunchSourceConnector,
		SourceReference:           event.DedupeKey(),
		RequesterPersonID:         turn.RequesterPersonID,
		RequesterName:             event.Context.Sender.Name,
		RequesterCallingName:      event.Context.Sender.CallingName,
		RequesterHandle:           event.Context.Sender.Handle,
		RequesterEmail:            turn.RequesterEmail,
		RequesterPlatformUserID:   event.SenderID,
		IsApprovalContinuation:    turn.IsApprovalContinuation,
		ExistingTaskRunID:         existingGoalTaskRunIDFromTurn(turn),
		ProfileName:               "default",
		Platform:                  turn.Platform,
		ConversationID:            event.ConversationID,
		ConversationType:          event.Context.ConversationType,
		ConversationChannelID:     event.Context.ChannelID,
		ConversationChannelName:   event.Context.ChannelName,
		ReplyTargetID:             event.ReplyTargetID,
		Prompt:                    event.Prompt,
		ResponseLanguage:          responseLanguageForEvent(event),
		VisibleContext:            event.Context.ToAgentVisibleContext(),
		ActiveGoal:                activeGoalForLaunch(turn.ActiveGoal, turn.HasActiveGoal),
		PrecomputedTurnDecision:   turn.PrecomputedTurnDecision,
		HistoryProvider:           connectorHistoryProvider{adapter: turn.Adapter},
		PersonAccess:              turn.PersonAccess,
		MemoryNamespaces:          connectorRuntime.accessibleNamespaces(turn.RequesterPersonID, turn.PersonAccess, event),
		AccessibleConversationIDs: turn.AccessibleConversationIDs,
		CheckpointSender:          turn.CheckpointSender,
	}
}

func existingGoalTaskRunIDFromTurn(turn ConversationTurn) string {
	if turn.IsApprovalContinuation {
		return turn.ActiveGoal.TaskRunID
	}
	if turn.HasActiveGoal {
		return turn.ActiveGoal.TaskRunID
	}
	return ""
}

func (connectorRuntime *ConnectorRuntime) checkpointSenderForTurn(platform string, event PlatformInboundEvent, replyTarget ReplyTarget, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) agent.AgentCheckpointSender {
	return func(checkpointContext context.Context, checkpoint agent.AgentCheckpoint) error {
		return connectorRuntime.sendCheckpointReply(checkpointContext, platform, event, replyTarget, checkpoint, sendReply)
	}
}
