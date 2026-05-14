package connectors

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"blueclaw/internal/agent"
	"blueclaw/internal/task"
)

type taskControlSelection struct {
	intent             agent.TaskControlIntent
	reason             string
	cancelledTaskRuns  []task.TaskRun
	hasMultipleTargets bool
	hasNoTarget        bool
}

func (connectorRuntime *ConnectorRuntime) handleTaskControlIfRequested(
	ctx context.Context,
	platform string,
	adapter PlatformAdapter,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	personID string,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (ConnectorRuntimeResult, bool) {
	decision, hasDecision := taskControlIntent(event)
	if !hasDecision || decision.Intent == agent.TaskControlIntentNone {
		return ConnectorRuntimeResult{}, false
	}

	selection := connectorRuntime.applyTaskControlIntent(decision, personID, event.ConversationID)
	for _, taskRun := range selection.cancelledTaskRuns {
		connectorRuntime.agentKernel.AppendTaskEvent(taskRun.TaskRunID, "task.stop.requested", marshalConnectorEventBody(map[string]string{
			"messageID": event.MessageID,
			"intent":    string(selection.intent),
			"reason":    selection.reason,
		}))
		connectorRuntime.agentKernel.AppendTaskEvent(taskRun.TaskRunID, "task.stop.classified", marshalConnectorEventBody(map[string]any{
			"intent":             string(selection.intent),
			"reason":             selection.reason,
			"classifiedByLLM":    false,
			"originConversation": event.ConversationID,
		}))
		connectorRuntime.agentKernel.AppendTaskEvent(taskRun.TaskRunID, "task.stop.cancelled", marshalConnectorEventBody(map[string]string{
			"messageID": event.MessageID,
		}))
	}

	reply := taskControlReply(selection, responseLanguageForEvent(event))
	dispatchID, errorValue := sendReply(ctx, replyTarget, OutboundReply{Message: reply})
	if errorValue != nil {
		connectorRuntime.logger.Error("connector."+platform+".task_control.reply_failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, Reason: "task_control_reply_failed"}, true
	}
	connectorRuntime.logger.Info("connector."+adapter.Name()+".task_control.handled", slog.String("messageID", event.MessageID), slog.String("intent", string(selection.intent)), slog.Int("cancelled", len(selection.cancelledTaskRuns)))
	return ConnectorRuntimeResult{Handled: true, Platform: platform, Reason: "task_control", ReplyDispatchID: dispatchID}, true
}

func taskControlIntent(event PlatformInboundEvent) (agent.TaskControlIntentDecision, bool) {
	if intent := exactTaskControlIntent(event.Prompt); intent != agent.TaskControlIntentNone {
		return agent.TaskControlIntentDecision{Intent: intent, Reason: "explicit control command"}, true
	}
	return agent.TaskControlIntentDecision{}, false
}

func (connectorRuntime *ConnectorRuntime) applyTaskControlIntent(decision agent.TaskControlIntentDecision, personID string, conversationID string) taskControlSelection {
	selection := taskControlSelection{
		intent: decision.Intent,
		reason: firstNonEmptyString(decision.Reason, "user requested task stop"),
	}
	switch decision.Intent {
	case agent.TaskControlIntentStopAll:
		selection.cancelledTaskRuns = connectorRuntime.agentKernel.CancelActiveTasks(task.TaskRunCancelRequest{
			RequesterPersonID: personID,
			Reason:            selection.reason,
		})
	case agent.TaskControlIntentStop:
		selection.cancelledTaskRuns = connectorRuntime.cancelCurrentConversationTasks(personID, conversationID, selection.reason)
		if len(selection.cancelledTaskRuns) == 0 {
			selection.cancelledTaskRuns, selection.hasMultipleTargets = connectorRuntime.cancelSingleActiveTask(personID, selection.reason)
		}
	}
	selection.hasNoTarget = len(selection.cancelledTaskRuns) == 0 && !selection.hasMultipleTargets
	return selection
}

func (connectorRuntime *ConnectorRuntime) cancelCurrentConversationTasks(personID string, conversationID string, reason string) []task.TaskRun {
	return connectorRuntime.agentKernel.CancelActiveTasks(task.TaskRunCancelRequest{
		RequesterPersonID:     personID,
		OriginConversationIDs: []string{conversationID},
		Reason:                reason,
	})
}

func (connectorRuntime *ConnectorRuntime) cancelSingleActiveTask(personID string, reason string) ([]task.TaskRun, bool) {
	activeTaskRuns := connectorRuntime.activeTaskRunsForPerson(personID)
	if len(activeTaskRuns) == 0 {
		return nil, false
	}
	if len(activeTaskRuns) > 1 {
		return nil, true
	}
	cancelledTaskRuns := connectorRuntime.agentKernel.CancelActiveTasks(task.TaskRunCancelRequest{
		TaskRunIDs:        []string{activeTaskRuns[0].TaskRunID},
		RequesterPersonID: personID,
		Reason:            reason,
	})
	return cancelledTaskRuns, false
}

func (connectorRuntime *ConnectorRuntime) activeTaskRunsForPerson(personID string) []task.TaskRun {
	taskRuns := []task.TaskRun{}
	for _, taskRun := range connectorRuntime.agentKernel.ListTaskRunByPersonID(personID) {
		if isTaskControlActiveStatus(taskRun.Status) {
			taskRuns = append(taskRuns, taskRun)
		}
	}
	return taskRuns
}

func (connectorRuntime *ConnectorRuntime) hasActiveTaskForPerson(personID string) bool {
	return len(connectorRuntime.activeTaskRunsForPerson(personID)) > 0
}

func (connectorRuntime *ConnectorRuntime) taskRunWasCancelled(taskRunID string) bool {
	taskRun, isFound := connectorRuntime.agentKernel.FindTaskRun(taskRunID)
	return isFound && taskRun.Status == task.TaskStatusCancelled
}

func shouldProcessBeforeConversationLock(event PlatformInboundEvent) bool {
	return exactTaskControlIntent(event.Prompt) != agent.TaskControlIntentNone
}

func exactTaskControlIntent(prompt string) agent.TaskControlIntent {
	normalizedPrompt := strings.ToLower(strings.TrimSpace(prompt))
	switch normalizedPrompt {
	case "/stop", "/중단":
		return agent.TaskControlIntentStop
	case "/stop-all", "/stop_all", "/중단-전부":
		return agent.TaskControlIntentStopAll
	default:
		return agent.TaskControlIntentNone
	}
}

func isTaskControlActiveStatus(status task.TaskStatus) bool {
	switch status {
	case task.TaskStatusPlanned, task.TaskStatusRunning, task.TaskStatusWaitingApproval, task.TaskStatusWaitingUserInput, task.TaskStatusBlocked:
		return true
	default:
		return false
	}
}

func taskControlReply(selection taskControlSelection, responseLanguage string) string {
	if selection.hasMultipleTargets {
		if responseLanguage == "en" {
			return "I found multiple running tasks. Use `/stop-all` to stop all of your current tasks."
		}
		return "진행 중인 작업이 여러 개입니다. 모두 멈추려면 `/stop-all`을 사용해 주세요."
	}
	if selection.hasNoTarget {
		if responseLanguage == "en" {
			return "There is no active task to stop right now."
		}
		return "현재 중단할 작업이 없습니다."
	}
	if responseLanguage == "en" {
		return "Stopped " + taskControlCountText(len(selection.cancelledTaskRuns)) + ". Scheduled future runs were not cancelled."
	}
	return "진행 중인 작업 " + taskControlCountText(len(selection.cancelledTaskRuns)) + "을 중단했습니다. 예약된 반복 실행은 유지됩니다."
}

func taskControlCountText(count int) string {
	return fmt.Sprint(count)
}
