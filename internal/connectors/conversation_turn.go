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
	PriorTask                 agent.PriorTaskContext
	PrecomputedTurnDecision   *agent.TurnDecision
	AmbientDuty               agent.AmbientDutyContext
	CheckpointSender          agent.AgentCheckpointSender
	AccessibleConversationIDs []string
	IsBlockedContinuation     bool
}

func (connectorRuntime *ConnectorRuntime) buildTaskLaunchRequest(turn ConversationTurn) agentruntime.TaskLaunchRequest {
	event := turn.Event
	checkpointSender := turn.CheckpointSender
	if turn.AmbientDuty.IsMatch {
		checkpointSender = nil
	}
	attachmentMaterialResolver := connectorAttachmentMaterialResolver{
		adapter:     turn.Adapter,
		personID:    turn.RequesterPersonID,
		event:       event,
		sentSources: connectorRuntime.sentAttachmentSources,
	}
	return agentruntime.TaskLaunchRequest{
		Source:                     agentruntime.TaskLaunchSourceConnector,
		SourceReference:            event.DedupeKey(),
		RequesterPersonID:          turn.RequesterPersonID,
		RequesterName:              connectorRuntime.requesterNameForEvent(turn.RequesterPersonID, event),
		RequesterCallingName:       event.Context.Sender.CallingName,
		RequesterHandle:            event.Context.Sender.Handle,
		RequesterEmail:             turn.RequesterEmail,
		RequesterPlatformUserID:    event.SenderID,
		IsApprovalContinuation:     turn.IsApprovalContinuation,
		IsRuntimeRestartResume:     turn.IsBlockedContinuation,
		ExistingTaskRunID:          existingGoalTaskRunIDFromTurn(turn),
		OriginReplyTargetID:        event.ReplyTargetID,
		OriginIsThread:             eventIsThreadReply(event),
		ProfileName:                "default",
		Platform:                   turn.Platform,
		ConversationID:             event.ConversationID,
		ConversationType:           event.Context.ConversationType,
		ConversationChannelID:      event.Context.ChannelID,
		ConversationChannelName:    event.Context.ChannelName,
		ReplyTargetID:              event.ReplyTargetID,
		Prompt:                     event.Prompt,
		InputParts:                 append([]agent.AgentPart{}, event.InputParts...),
		ResponseLanguage:           responseLanguageForEvent(event),
		VisibleContext:             event.Context.ToAgentVisibleContext(),
		ActiveGoal:                 activeGoalForLaunch(turn.ActiveGoal, turn.HasActiveGoal),
		PriorTask:                  turn.PriorTask,
		PrecomputedTurnDecision:    turn.PrecomputedTurnDecision,
		AmbientDuty:                turn.AmbientDuty,
		HistoryProvider:            connectorHistoryProvider{adapter: turn.Adapter},
		AttachmentMaterialResolver: attachmentMaterialResolver,
		PersonAccess:               turn.PersonAccess,
		MemoryNamespaces:           connectorRuntime.accessibleNamespaces(turn.RequesterPersonID, turn.PersonAccess, event),
		AccessibleConversationIDs:  turn.AccessibleConversationIDs,
		CheckpointSender:           checkpointSender,
	}
}

func eventIsThreadReply(event PlatformInboundEvent) bool {
	return event.ReplyTargetID != "" && event.MessageID != "" && event.ReplyTargetID != event.MessageID
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
