package connectors

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/bluecollar"
	"github.com/Dawn-kim-official/blueclaw/internal/task"
)

type busyMessageResult struct {
	connectorResult ConnectorRuntimeResult
	isHandled       bool
	clearActiveGoal bool
}

func (connectorRuntime *ConnectorRuntime) handleBusyMessageIfNeeded(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	personID string,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	activeTaskRun, isFound := connectorRuntime.latestCurrentConversationActiveTask(personID, event.ConversationID)
	if !isFound {
		return connectorRuntime.handlePossibleFinishedTaskFollowUp(ctx, platform, event, replyTarget, personID, sendReply)
	}
	decision, errorValue := connectorRuntime.agentKernel.RouteTurn(ctx, bluecollar.AgentRequest{
		RequesterPersonID: personID,
		ConversationID:    event.ConversationID,
		Prompt:            event.Prompt,
		ResponseLanguage:  responseLanguageForEvent(event),
		VisibleContext:    event.Context.ToAgentVisibleContext(),
		ActiveTask:        connectorRuntime.activeTaskContext(activeTaskRun),
		TurnStartedAt:     time.Now(),
	})
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	connectorRuntime.agentKernel.AppendTaskEvent(activeTaskRun.TaskRunID, "task.busy_message.routed", marshalConnectorEventBody(map[string]string{
		"messageID":       event.MessageID,
		"busyRoute":       string(decision.BusyRoute),
		"reason":          strings.TrimSpace(decision.Reason),
		"latestUserInput": strings.TrimSpace(event.Prompt),
	}))
	switch decision.BusyRoute {
	case bluecollar.BusyRouteStatus:
		return connectorRuntime.handleBusyStatusMessage(ctx, platform, event, replyTarget, activeTaskRun, decision, sendReply)
	case bluecollar.BusyRouteSteer:
		return connectorRuntime.handleBusySteerMessage(ctx, platform, event, replyTarget, activeTaskRun, decision, sendReply)
	case bluecollar.BusyRouteReplace:
		connectorRuntime.replaceBusyTask(event, activeTaskRun, decision)
		return busyMessageResult{clearActiveGoal: true}, nil
	case bluecollar.BusyRouteCancel:
		return connectorRuntime.handleBusyCancelMessage(ctx, platform, event, replyTarget, activeTaskRun, decision, sendReply)
	case bluecollar.BusyRouteNewTask:
		connectorRuntime.supersedeBusyTask(event, platform, activeTaskRun, decision)
		return busyMessageResult{clearActiveGoal: true}, nil
	case bluecollar.BusyRouteUnrelated:
		return busyMessageResult{connectorResult: ConnectorRuntimeResult{Handled: true, Platform: platform, Ignored: true, Reason: "busy_unrelated"}, isHandled: true}, nil
	default:
		return busyMessageResult{}, errors.New("turn router returned an invalid busy route")
	}
}

func (connectorRuntime *ConnectorRuntime) handleBusyCancelMessage(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	activeTaskRun task.TaskRun,
	decision bluecollar.TurnDecision,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	_, _ = connectorRuntime.agentKernel.CancelTask(activeTaskRun.TaskRunID, activeTaskRun.RequesterPersonID, "task cancelled by newer user instruction")
	connectorRuntime.agentKernel.AppendTaskEvent(activeTaskRun.TaskRunID, "task.cancel.requested", marshalConnectorEventBody(map[string]string{
		"messageID":       event.MessageID,
		"reason":          strings.TrimSpace(decision.Reason),
		"latestUserInput": strings.TrimSpace(event.Prompt),
	}))
	reply, errorValue := connectorRuntime.generateBusyReply(ctx, event, activeTaskRun, "cancel", decision)
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	dispatchID, errorValue := sendReply(ctx, replyTarget, OutboundReply{Message: reply, TaskRunID: activeTaskRun.TaskRunID, ReplyKind: connectorReplyKindUserNotice})
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	return busyMessageResult{connectorResult: ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: activeTaskRun.TaskRunID, Reason: "busy_cancel", ReplyDispatchID: dispatchID}, isHandled: true, clearActiveGoal: true}, nil
}

func (connectorRuntime *ConnectorRuntime) handleBusyStatusMessage(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	activeTaskRun task.TaskRun,
	decision bluecollar.TurnDecision,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	connectorRuntime.agentKernel.AppendTaskEvent(activeTaskRun.TaskRunID, "task.status.requested", marshalConnectorEventBody(map[string]string{
		"messageID": event.MessageID,
		"reason":    strings.TrimSpace(decision.Reason),
	}))
	reply, errorValue := connectorRuntime.generateBusyReply(ctx, event, activeTaskRun, "status", decision)
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	dispatchID, errorValue := sendReply(ctx, replyTarget, OutboundReply{Message: reply, TaskRunID: activeTaskRun.TaskRunID, ReplyKind: connectorReplyKindCheckpoint})
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	return busyMessageResult{connectorResult: ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: activeTaskRun.TaskRunID, Reason: "busy_status", ReplyDispatchID: dispatchID}, isHandled: true}, nil
}

func (connectorRuntime *ConnectorRuntime) handleBusySteerMessage(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	activeTaskRun task.TaskRun,
	decision bluecollar.TurnDecision,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	instruction := firstNonEmptyString(strings.TrimSpace(decision.BusyInstruction), strings.TrimSpace(event.Prompt))
	if !connectorRuntime.agentKernel.IsTaskRunActuallyRunning(activeTaskRun) {
		return connectorRuntime.resumePausedTaskForSteer(ctx, platform, event, replyTarget, activeTaskRun, instruction, decision, sendReply)
	}
	connectorRuntime.appendSteerRequestedEvent(activeTaskRun.TaskRunID, event, instruction, decision)
	reply, errorValue := connectorRuntime.generateBusyReply(ctx, event, activeTaskRun, "steer", decision)
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	dispatchID, errorValue := sendReply(ctx, replyTarget, OutboundReply{Message: reply, TaskRunID: activeTaskRun.TaskRunID, ReplyKind: connectorReplyKindCheckpoint})
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	return busyMessageResult{connectorResult: ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: activeTaskRun.TaskRunID, Reason: "busy_steer", ReplyDispatchID: dispatchID}, isHandled: true}, nil
}

func (connectorRuntime *ConnectorRuntime) appendSteerRequestedEvent(taskRunID string, event PlatformInboundEvent, instruction string, decision bluecollar.TurnDecision) {
	connectorRuntime.agentKernel.AppendTaskEvent(taskRunID, "task.steer.requested", marshalConnectorEventBody(map[string]string{
		"messageID":   event.MessageID,
		"instruction": instruction,
		"reason":      strings.TrimSpace(decision.Reason),
	}))
}

func (connectorRuntime *ConnectorRuntime) resumePausedTaskForSteer(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	activeTaskRun task.TaskRun,
	instruction string,
	decision bluecollar.TurnDecision,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	taskEvents := connectorRuntime.agentKernel.ListTaskEvent(activeTaskRun.TaskRunID)
	launchContext, isFound := interruptedTaskLaunchContextFromEvents(activeTaskRun, taskEvents)
	adapter, adapterError := connectorRuntime.findAdapter(firstNonEmptyString(platform, launchContext.Platform))
	if !isFound || adapterError != nil {
		return connectorRuntime.replySteerResumeUnavailable(ctx, platform, event, replyTarget, activeTaskRun, decision, sendReply)
	}
	connectorRuntime.appendSteerRequestedEvent(activeTaskRun.TaskRunID, event, instruction, decision)
	launchRequest := connectorRuntime.interruptedTaskLaunchRequest(activeTaskRun, taskEvents, launchContext, event, adapter, userSteerTaskProfile(platform, activeTaskRun.TaskRunID), sendReply)
	launchResult, errorValue := connectorRuntime.currentTaskLauncher().Launch(ctx, launchRequest)
	if errorValue != nil {
		failureTurnResult := connectorRuntime.agentKernel.CompleteLaunchFailure(ctx, bluecollar.AgentTurnRequest{
			RequesterPersonID: activeTaskRun.RequesterPersonID,
			ExistingTaskRunID: activeTaskRun.TaskRunID,
			Platform:          platform,
			ConversationID:    event.ConversationID,
			Prompt:            activeTaskRun.Prompt,
			ResponseLanguage:  event.Context.ResponseLanguage,
		}, "launch", "steer_resume", errorValue)
		connectorResult, dispatchError := connectorRuntime.dispatchTaskReply(withConnectorEvent(ctx, event), adapter.Name(), adapter, event, replyTarget, failureTurnResult, "", sendReply)
		if dispatchError != nil {
			return busyMessageResult{}, dispatchError
		}
		return busyMessageResult{connectorResult: connectorResult, isHandled: true}, nil
	}
	connectorResult, errorValue := connectorRuntime.dispatchTaskReply(withConnectorEvent(ctx, event), adapter.Name(), adapter, event, replyTarget, launchResult.TurnResult, "", sendReply)
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	return busyMessageResult{connectorResult: connectorResult, isHandled: true}, nil
}

func (connectorRuntime *ConnectorRuntime) replySteerResumeUnavailable(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	activeTaskRun task.TaskRun,
	decision bluecollar.TurnDecision,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	connectorRuntime.agentKernel.AppendTaskEvent(activeTaskRun.TaskRunID, "task.steer.resume_unavailable", marshalConnectorEventBody(map[string]string{
		"messageID": event.MessageID,
		"reason":    strings.TrimSpace(decision.Reason),
	}))
	reply, errorValue := connectorRuntime.generateBusyReply(ctx, event, activeTaskRun, "steer", decision)
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	dispatchID, errorValue := sendReply(ctx, replyTarget, OutboundReply{Message: reply, TaskRunID: activeTaskRun.TaskRunID, ReplyKind: connectorReplyKindUserNotice})
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	return busyMessageResult{connectorResult: ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: activeTaskRun.TaskRunID, Reason: "busy_steer_resume_unavailable", ReplyDispatchID: dispatchID}, isHandled: true}, nil
}

func (connectorRuntime *ConnectorRuntime) replaceBusyTask(event PlatformInboundEvent, activeTaskRun task.TaskRun, decision bluecollar.TurnDecision) {
	_, _ = connectorRuntime.agentKernel.CancelTask(activeTaskRun.TaskRunID, activeTaskRun.RequesterPersonID, "task replaced by newer user instruction")
	connectorRuntime.agentKernel.AppendTaskEvent(activeTaskRun.TaskRunID, "task.replaced", marshalConnectorEventBody(map[string]string{
		"messageID":       event.MessageID,
		"reason":          strings.TrimSpace(decision.Reason),
		"latestUserInput": strings.TrimSpace(event.Prompt),
	}))
}

func (connectorRuntime *ConnectorRuntime) supersedeBusyTask(event PlatformInboundEvent, platform string, activeTaskRun task.TaskRun, decision bluecollar.TurnDecision) {
	_, _ = connectorRuntime.agentKernel.CancelTask(activeTaskRun.TaskRunID, activeTaskRun.RequesterPersonID, "superseded_by_new_message")
	connectorRuntime.resolveOpenTaskWaitsForTaskRun(activeTaskRun.RequesterPersonID, platform, activeTaskRun.OriginConversationID, activeTaskRun.TaskRunID)
	connectorRuntime.agentKernel.AppendTaskEvent(activeTaskRun.TaskRunID, "task.superseded_by_message", marshalConnectorEventBody(map[string]string{
		"messageID":       event.MessageID,
		"reason":          strings.TrimSpace(decision.Reason),
		"latestUserInput": strings.TrimSpace(event.Prompt),
	}))
}

func (connectorRuntime *ConnectorRuntime) generateBusyReply(ctx context.Context, event PlatformInboundEvent, activeTaskRun task.TaskRun, route string, decision bluecollar.TurnDecision) (string, error) {
	prompt := strings.Join([]string{
		"Write a short user-facing reply for an in-progress task.",
		"Response language: " + responseLanguageForEvent(event),
		"Route: " + route,
		"Original task: " + strings.TrimSpace(activeTaskRun.Prompt),
		"Task status: " + string(activeTaskRun.Status),
		"Current progress: " + connectorRuntime.activeTaskEventSummary(activeTaskRun.TaskRunID),
		"Latest user message: " + strings.TrimSpace(event.Prompt),
		"Routing reason: " + strings.TrimSpace(decision.Reason),
		"Steering instruction: " + strings.TrimSpace(decision.BusyInstruction),
		"Do not expose internal event names or task IDs.",
		"Status and steer replies must not claim the task is complete. Cancel replies may say the active task has been stopped.",
	}, "\n")
	return connectorRuntime.agentKernel.GenerateReplyWithContext(ctx, prompt, event.Context.ToAgentVisibleContext(), nil)
}

// recentlyFinishedTaskFollowUpWindow bounds how long after a task leaves active status a
// later message is still eligible to be treated as a follow-up to it, rather than a
// self-contained new request.
const recentlyFinishedTaskFollowUpWindow = 15 * time.Second

// handlePossibleFinishedTaskFollowUp covers the narrow race where the active task finished
// between when a message was flagged as worth fast-tracking and when it is actually
// classified: rather than silently falling through into new-task creation (turning a
// correction into a duplicate task) or silently dropping the message, it tells the user the
// prior task already finished and lets them decide whether to start something new.
func (connectorRuntime *ConnectorRuntime) handlePossibleFinishedTaskFollowUp(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	personID string,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	finishedTaskRun, isFound := connectorRuntime.latestRecentlyFinishedConversationTask(personID, event.ConversationID)
	if !isFound {
		return busyMessageResult{}, nil
	}
	if event.RawReceivedAt.IsZero() || !event.RawReceivedAt.Before(finishedTaskRun.UpdatedAt) {
		return busyMessageResult{}, nil
	}
	isRelated, errorValue := connectorRuntime.agentKernel.ClassifyActiveTaskFollowUp(ctx, bluecollar.ActiveTaskFollowUpClassificationRequest{
		ActiveTaskPrompt: finishedTaskRun.Prompt,
		ActiveTaskStatus: string(finishedTaskRun.Status),
		LatestMessage:    event.Prompt,
	})
	if errorValue != nil || !isRelated {
		return busyMessageResult{}, nil
	}
	connectorRuntime.agentKernel.AppendTaskEvent(finishedTaskRun.TaskRunID, "task.busy_message.after_finish", marshalConnectorEventBody(map[string]string{
		"messageID":       event.MessageID,
		"latestUserInput": strings.TrimSpace(event.Prompt),
	}))
	reply, errorValue := connectorRuntime.generateFinishedTaskFollowUpReply(ctx, event, finishedTaskRun)
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	dispatchID, errorValue := sendReply(ctx, replyTarget, OutboundReply{Message: reply, TaskRunID: finishedTaskRun.TaskRunID, ReplyKind: connectorReplyKindUserNotice})
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	return busyMessageResult{connectorResult: ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: finishedTaskRun.TaskRunID, Reason: "busy_finished_followup", ReplyDispatchID: dispatchID}, isHandled: true}, nil
}

func (connectorRuntime *ConnectorRuntime) generateFinishedTaskFollowUpReply(ctx context.Context, event PlatformInboundEvent, finishedTaskRun task.TaskRun) (string, error) {
	prompt := strings.Join([]string{
		"Write a short user-facing reply. The task the user seems to be reacting to already finished before this message arrived.",
		"Response language: " + responseLanguageForEvent(event),
		"Finished task: " + strings.TrimSpace(finishedTaskRun.Prompt),
		"Final status: " + string(finishedTaskRun.Status),
		"Latest user message: " + strings.TrimSpace(event.Prompt),
		"Tell the user the task already finished before this message could apply to it, and ask whether they want it undone/redone or want to start something new. Do not silently start new work.",
	}, "\n")
	return connectorRuntime.agentKernel.GenerateReplyWithContext(ctx, prompt, event.Context.ToAgentVisibleContext(), nil)
}

func (connectorRuntime *ConnectorRuntime) latestRecentlyFinishedConversationTask(personID string, conversationID string) (task.TaskRun, bool) {
	var latestTaskRun task.TaskRun
	isFound := false
	cutoff := time.Now().Add(-recentlyFinishedTaskFollowUpWindow)
	for _, taskRun := range connectorRuntime.agentKernel.ListTaskRunByPersonID(personID) {
		if taskRun.OriginConversationID != conversationID {
			continue
		}
		if isTaskControlActiveStatus(taskRun.Status) {
			continue
		}
		if taskRun.UpdatedAt.Before(cutoff) {
			continue
		}
		if !isFound || taskRun.UpdatedAt.After(latestTaskRun.UpdatedAt) {
			latestTaskRun = taskRun
			isFound = true
		}
	}
	return latestTaskRun, isFound
}

func (connectorRuntime *ConnectorRuntime) latestCurrentConversationActiveTask(personID string, conversationID string) (task.TaskRun, bool) {
	var latestTaskRun task.TaskRun
	isFound := false
	for _, taskRun := range connectorRuntime.activeTaskRunsForPerson(personID) {
		if taskRun.OriginConversationID != conversationID {
			continue
		}
		if !isFound || taskRun.UpdatedAt.After(latestTaskRun.UpdatedAt) {
			latestTaskRun = taskRun
			isFound = true
		}
	}
	return latestTaskRun, isFound
}

func (connectorRuntime *ConnectorRuntime) activeTaskContext(taskRun task.TaskRun) bluecollar.ActiveTaskContext {
	return bluecollar.ActiveTaskContext{
		TaskRunID: taskRun.TaskRunID,
		Prompt:    taskRun.Prompt,
		Status:    string(taskRun.Status),
		Summary:   connectorRuntime.activeTaskEventSummary(taskRun.TaskRunID),
	}
}

func (connectorRuntime *ConnectorRuntime) activeTaskEventSummary(taskRunID string) string {
	events := connectorRuntime.agentKernel.ListTaskEvent(taskRunID)
	summaries := []string{}
	for index := len(events) - 1; index >= 0 && len(summaries) < 6; index-- {
		event := events[index]
		body := strings.TrimSpace(event.Body)
		if len(body) > 240 {
			body = body[:240]
		}
		summaries = append(summaries, strings.TrimSpace(event.Name)+" "+body)
	}
	if len(summaries) == 0 {
		return "No progress events have been recorded yet."
	}
	return strings.Join(summaries, "\n")
}
