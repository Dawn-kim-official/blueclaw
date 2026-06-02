package connectors

import (
	"context"

	"blueclaw/internal/agent"
	"blueclaw/internal/task"
)

type taskReplyDecisionKind string

const (
	taskReplyDecisionConsume                 taskReplyDecisionKind = "consume"
	taskReplyDecisionSuppressCancelled       taskReplyDecisionKind = "suppress_cancelled"
	taskReplyDecisionSendUserNotice          taskReplyDecisionKind = "send_user_notice"
	taskReplyDecisionSuppressArtifactLocator taskReplyDecisionKind = "suppress_artifact_locator"
	taskReplyDecisionSuppressMissingAttach   taskReplyDecisionKind = "suppress_missing_attachment"
	taskReplyDecisionSendFinal               taskReplyDecisionKind = "send_final"
)

type taskReplyDecision struct {
	Kind   taskReplyDecisionKind
	Reason string
}

func decideTaskReply(turnResult agent.AgentTurnResult, isCancelledBeforeSend bool) taskReplyDecision {
	if turnResult.TurnRoute == agent.TurnRouteConsume {
		return taskReplyDecision{Kind: taskReplyDecisionConsume}
	}
	if turnResult.TaskRun.Status == task.TaskStatusCancelled || isCancelledBeforeSend {
		return taskReplyDecision{Kind: taskReplyDecisionSuppressCancelled, Reason: "task_cancelled"}
	}
	if turnResult.TaskRun.Status != task.TaskStatusCompleted {
		return taskReplyDecision{Kind: taskReplyDecisionSendUserNotice, Reason: "task_not_completed"}
	}
	if agent.FinishMessageContainsNonDeliverableArtifactLocator(turnResult.FinishMessage) {
		return taskReplyDecision{Kind: taskReplyDecisionSuppressArtifactLocator, Reason: "non_deliverable_artifact_locator"}
	}
	if agent.FinishMessageClaimsAttachmentDelivery(turnResult.FinishMessage) && len(turnResult.Attachments) == 0 {
		return taskReplyDecision{Kind: taskReplyDecisionSuppressMissingAttach, Reason: "missing_attachment_evidence"}
	}
	return taskReplyDecision{Kind: taskReplyDecisionSendFinal}
}

func (connectorRuntime *ConnectorRuntime) dispatchTaskReply(
	ctx context.Context,
	platform string,
	adapter PlatformAdapter,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	turnResult agent.AgentTurnResult,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (ConnectorRuntimeResult, error) {
	taskRunID := turnResult.TaskRun.TaskRunID
	decision := decideTaskReply(turnResult, connectorRuntime.taskRunWasCancelled(taskRunID))
	switch decision.Kind {
	case taskReplyDecisionConsume:
		reason := connectorRuntime.addConsumeReaction(ctx, platform, adapter, event, taskRunID, turnResult.ReactionEmojiName)
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, Reason: reason}, nil
	case taskReplyDecisionSuppressCancelled:
		connectorRuntime.agentKernel.AppendTaskEvent(taskRunID, "task.stop.outbox_suppressed", marshalConnectorEventBody(map[string]string{
			"messageID": event.MessageID,
			"reason":    "task was cancelled before final reply send",
		}))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, Reason: decision.Reason}, nil
	case taskReplyDecisionSendUserNotice:
		dispatchID, isSent := connectorRuntime.sendUserNoticeReply(ctx, platform, event, taskRunID, replyTarget, turnResult, sendReply)
		if isSent {
			return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, Reason: decision.Reason, ReplyDispatchID: dispatchID}, nil
		}
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, Reason: decision.Reason}, nil
	case taskReplyDecisionSuppressArtifactLocator:
		connectorRuntime.appendConnectorReplyEvent(taskRunID, "connector.reply.suppressed", connectorReplyEventBody(event, OutboundReply{TaskRunID: taskRunID, ReplyKind: connectorReplyKindSuccess}, "", "", decision.Reason))
		connectorRuntime.logger.Warn("connector."+platform+".outbound.blocked", "messageID", event.MessageID, "taskRunID", taskRunID, "reason", decision.Reason)
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, Reason: decision.Reason}, nil
	case taskReplyDecisionSuppressMissingAttach:
		connectorRuntime.appendConnectorReplyEvent(taskRunID, "connector.reply.suppressed", connectorReplyEventBody(event, OutboundReply{TaskRunID: taskRunID, ReplyKind: connectorReplyKindSuccess}, "", "", decision.Reason))
		connectorRuntime.logger.Warn("connector."+platform+".outbound.blocked", "messageID", event.MessageID, "taskRunID", taskRunID, "reason", decision.Reason)
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, Reason: decision.Reason}, nil
	default:
		return connectorRuntime.sendCompletedTaskReply(ctx, platform, event, taskRunID, replyTarget, turnResult, sendReply)
	}
}

func (connectorRuntime *ConnectorRuntime) sendCompletedTaskReply(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	taskRunID string,
	replyTarget ReplyTarget,
	turnResult agent.AgentTurnResult,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (ConnectorRuntimeResult, error) {
	dispatchID, errorValue := sendReply(ctx, replyTarget, OutboundReply{
		Message:         turnResult.FinishMessage,
		TaskRunID:       taskRunID,
		ReplyKind:       connectorReplyKindSuccess,
		Attachments:     turnResult.Attachments,
		RecoveryActions: recoveryActionsForEvent(turnResult.RecoveryActions, event),
	})
	if errorValue != nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, "connector.reply.failed", connectorReplyEventBody(event, OutboundReply{TaskRunID: taskRunID, ReplyKind: connectorReplyKindSuccess}, "", "", errorValue.Error()))
		connectorRuntime.logger.Error("connector."+platform+".outbound.failed", "messageID", event.MessageID, "taskRunID", taskRunID, "error", errorValue.Error())
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, Reason: "reply_failed"}, nil
	}
	if connectorRuntime.outboxRepository() == nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, "connector.reply.sent", connectorReplyEventBody(event, OutboundReply{TaskRunID: taskRunID, ReplyKind: connectorReplyKindSuccess}, "", dispatchID, ""))
	}
	connectorRuntime.logger.Info("connector."+platform+".outbound.sent", "messageID", event.MessageID, "taskRunID", taskRunID, "replyDispatchID", dispatchID)
	return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, ReplyDispatchID: dispatchID}, nil
}
