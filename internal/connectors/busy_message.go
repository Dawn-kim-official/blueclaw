package connectors

import (
	"context"
	"strings"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/task"
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
		return busyMessageResult{}, nil
	}
	decision := connectorRuntime.agentKernel.RouteTurn(ctx, agent.AgentRequest{
		RequesterPersonID: personID,
		ConversationID:    event.ConversationID,
		Prompt:            event.Prompt,
		ResponseLanguage:  responseLanguageForEvent(event),
		VisibleContext:    event.Context.ToAgentVisibleContext(),
		ActiveTask:        connectorRuntime.activeTaskContext(activeTaskRun),
		TurnStartedAt:     time.Now(),
	})
	connectorRuntime.agentKernel.AppendTaskEvent(activeTaskRun.TaskRunID, "task.busy_message.routed", marshalConnectorEventBody(map[string]string{
		"messageID":       event.MessageID,
		"busyRoute":       string(decision.BusyRoute),
		"reason":          strings.TrimSpace(decision.Reason),
		"latestUserInput": strings.TrimSpace(event.Prompt),
	}))
	switch decision.BusyRoute {
	case agent.BusyRouteStatus:
		return connectorRuntime.handleBusyStatusMessage(ctx, platform, event, replyTarget, activeTaskRun, decision, sendReply)
	case agent.BusyRouteSteer:
		return connectorRuntime.handleBusySteerMessage(ctx, platform, event, replyTarget, activeTaskRun, decision, sendReply)
	case agent.BusyRouteReplace:
		connectorRuntime.replaceBusyTask(event, activeTaskRun, decision)
		return busyMessageResult{clearActiveGoal: true}, nil
	case agent.BusyRouteNewTask:
		return busyMessageResult{clearActiveGoal: true}, nil
	case agent.BusyRouteUnrelated:
		return busyMessageResult{connectorResult: ConnectorRuntimeResult{Handled: true, Platform: platform, Ignored: true, Reason: "busy_unrelated"}, isHandled: true}, nil
	default:
		return busyMessageResult{clearActiveGoal: true}, nil
	}
}

func (connectorRuntime *ConnectorRuntime) handleBusyStatusMessage(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	activeTaskRun task.TaskRun,
	decision agent.TurnDecision,
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
	decision agent.TurnDecision,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	instruction := firstNonEmptyString(strings.TrimSpace(decision.BusyInstruction), strings.TrimSpace(event.Prompt))
	connectorRuntime.agentKernel.AppendTaskEvent(activeTaskRun.TaskRunID, "task.steer.requested", marshalConnectorEventBody(map[string]string{
		"messageID":   event.MessageID,
		"instruction": instruction,
		"reason":      strings.TrimSpace(decision.Reason),
	}))
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

func (connectorRuntime *ConnectorRuntime) replaceBusyTask(event PlatformInboundEvent, activeTaskRun task.TaskRun, decision agent.TurnDecision) {
	_, _ = connectorRuntime.agentKernel.CancelTask(activeTaskRun.TaskRunID, activeTaskRun.RequesterPersonID, "task replaced by newer user instruction")
	connectorRuntime.agentKernel.AppendTaskEvent(activeTaskRun.TaskRunID, "task.replaced", marshalConnectorEventBody(map[string]string{
		"messageID":       event.MessageID,
		"reason":          strings.TrimSpace(decision.Reason),
		"latestUserInput": strings.TrimSpace(event.Prompt),
	}))
}

func (connectorRuntime *ConnectorRuntime) generateBusyReply(ctx context.Context, event PlatformInboundEvent, activeTaskRun task.TaskRun, route string, decision agent.TurnDecision) (string, error) {
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
		"Do not claim the task is complete. Do not expose internal event names or task IDs.",
	}, "\n")
	return connectorRuntime.agentKernel.GenerateReplyWithContext(ctx, prompt, event.Context.ToAgentVisibleContext(), nil)
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

func (connectorRuntime *ConnectorRuntime) activeTaskContext(taskRun task.TaskRun) agent.ActiveTaskContext {
	return agent.ActiveTaskContext{
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
