package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"blueclaw/internal/agent"
	"blueclaw/internal/agentruntime"
	"blueclaw/internal/task"
)

type interruptedTaskLaunchContext struct {
	SourceReference   string `json:"sourceReference"`
	ProfileName       string `json:"profileName"`
	RequesterPersonID string `json:"requesterPersonID"`
	ConversationID    string `json:"conversationID"`
	ReplyTargetID     string `json:"replyTargetID"`
	IsThread          bool   `json:"isThread"`
	ConversationType  string `json:"conversationType"`
	ChannelID         string `json:"channelID"`
	ChannelName       string `json:"channelName"`
	Platform          string `json:"platform"`
}

func (connectorRuntime *ConnectorRuntime) CanResumeInterruptedTaskRun(taskRun task.TaskRun) bool {
	taskEvents := connectorRuntime.agentKernel.ListTaskEvent(taskRun.TaskRunID)
	launchContext, isFound := interruptedTaskLaunchContextFromEvents(taskRun, taskEvents)
	if !isFound {
		return false
	}
	_, errorValue := connectorRuntime.findAdapter(launchContext.Platform)
	return errorValue == nil
}

func (connectorRuntime *ConnectorRuntime) ResumeInterruptedTaskRun(ctx context.Context, taskRun task.TaskRun) (ConnectorRuntimeResult, error) {
	taskEvents := connectorRuntime.agentKernel.ListTaskEvent(taskRun.TaskRunID)
	launchContext, isFound := interruptedTaskLaunchContextFromEvents(taskRun, taskEvents)
	if !isFound {
		return ConnectorRuntimeResult{}, errors.New("interrupted task launch context is missing")
	}
	adapter, errorValue := connectorRuntime.findAdapter(launchContext.Platform)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}
	event := interruptedTaskResumeEvent(taskRun, launchContext)
	replyTarget := ReplyTarget{
		ConversationID: event.ConversationID,
		ReplyTargetID:  event.ReplyTargetID,
		DedupeKey:      event.DedupeKey(),
	}
	sendReply := func(replyContext context.Context, target ReplyTarget, reply OutboundReply) (string, error) {
		return connectorRuntime.enqueueConnectorReply(withConnectorEvent(replyContext, event), target, reply)
	}
	launchResult, errorValue := connectorRuntime.currentTaskLauncher().Launch(ctx, connectorRuntime.interruptedTaskLaunchRequest(taskRun, taskEvents, launchContext, event, sendReply))
	if errorValue != nil {
		return connectorRuntime.completeInterruptedTaskResumeLaunchFailure(ctx, taskRun, launchContext, event, replyTarget, adapter, sendReply, errorValue)
	}
	return connectorRuntime.dispatchTaskReply(withConnectorEvent(ctx, event), adapter.Name(), adapter, event, replyTarget, launchResult.TurnResult, sendReply)
}

func (connectorRuntime *ConnectorRuntime) completeInterruptedTaskResumeLaunchFailure(ctx context.Context, taskRun task.TaskRun, launchContext interruptedTaskLaunchContext, event PlatformInboundEvent, replyTarget ReplyTarget, adapter PlatformAdapter, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error), errorValue error) (ConnectorRuntimeResult, error) {
	turnResult := connectorRuntime.agentKernel.CompleteLaunchFailure(ctx, agent.AgentTurnRequest{
		RequesterPersonID:      taskRun.RequesterPersonID,
		RequesterEmail:         connectorRuntime.identityService.ResolvePersonPrimaryEmail(taskRun.RequesterPersonID),
		IsApprovalContinuation: true,
		ExistingTaskRunID:      taskRun.TaskRunID,
		OriginReplyTargetID:    replyTarget.ReplyTargetID,
		OriginIsThread:         taskRun.OriginIsThread || launchContext.IsThread,
		ProfileName:            launchContext.ProfileName,
		Platform:               launchContext.Platform,
		ConversationID:         event.ConversationID,
		Prompt:                 taskRun.Prompt,
		ResponseLanguage:       event.Context.ResponseLanguage,
		VisibleContext:         event.Context.ToAgentVisibleContext(),
		ActiveGoal:             interruptedTaskActiveGoal(taskRun, connectorRuntime.agentKernel.ListTaskEvent(taskRun.TaskRunID)),
	}, "launch", "auto_resume", errorValue)
	return connectorRuntime.dispatchTaskReply(withConnectorEvent(ctx, event), adapter.Name(), adapter, event, replyTarget, turnResult, sendReply)
}

func (connectorRuntime *ConnectorRuntime) interruptedTaskLaunchRequest(taskRun task.TaskRun, taskEvents []task.TaskEvent, launchContext interruptedTaskLaunchContext, event PlatformInboundEvent, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) agentruntime.TaskLaunchRequest {
	personAccess := connectorRuntime.identityService.ResolvePersonAccess(taskRun.RequesterPersonID)
	conversationID := firstNonEmptyString(taskRun.OriginConversationID, launchContext.ConversationID)
	return agentruntime.TaskLaunchRequest{
		Source:                    agentruntime.TaskLaunchSourceConnector,
		SourceReference:           "auto_resume:" + taskRun.TaskRunID,
		RequesterPersonID:         taskRun.RequesterPersonID,
		RequesterEmail:            connectorRuntime.identityService.ResolvePersonPrimaryEmail(taskRun.RequesterPersonID),
		IsApprovalContinuation:    true,
		IsRuntimeRestartResume:    true,
		ExistingTaskRunID:         taskRun.TaskRunID,
		OriginReplyTargetID:       firstNonEmptyString(taskRun.OriginReplyTargetID, launchContext.ReplyTargetID),
		OriginIsThread:            taskRun.OriginIsThread || launchContext.IsThread,
		ProfileName:               launchContext.ProfileName,
		Platform:                  launchContext.Platform,
		ConversationID:            conversationID,
		ConversationType:          launchContext.ConversationType,
		ConversationChannelID:     launchContext.ChannelID,
		ConversationChannelName:   launchContext.ChannelName,
		ReplyTargetID:             firstNonEmptyString(taskRun.OriginReplyTargetID, launchContext.ReplyTargetID),
		Prompt:                    taskRun.Prompt,
		ResponseLanguage:          event.Context.ResponseLanguage,
		VisibleContext:            event.Context.ToAgentVisibleContext(),
		ActiveGoal:                interruptedTaskActiveGoal(taskRun, taskEvents),
		PrecomputedTurnDecision:   interruptedTaskTurnDecision(event.Context.ResponseLanguage),
		PersonAccess:              personAccess,
		MemoryNamespaces:          connectorRuntime.accessibleNamespaces(taskRun.RequesterPersonID, personAccess, event),
		AccessibleConversationIDs: []string{conversationID},
		CheckpointSender:          connectorRuntime.checkpointSenderForTurn(launchContext.Platform, event, ReplyTarget{ConversationID: conversationID, ReplyTargetID: event.ReplyTargetID, DedupeKey: event.DedupeKey()}, sendReply),
	}
}

func interruptedTaskLaunchContextFromEvents(taskRun task.TaskRun, taskEvents []task.TaskEvent) (interruptedTaskLaunchContext, bool) {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name != "agent.task_launched" {
			continue
		}
		var launchContext interruptedTaskLaunchContext
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &launchContext); errorValue != nil {
			continue
		}
		launchContext.ConversationID = firstNonEmptyString(taskRun.OriginConversationID, launchContext.ConversationID)
		launchContext.ReplyTargetID = firstNonEmptyString(taskRun.OriginReplyTargetID, launchContext.ReplyTargetID)
		launchContext.Platform = firstNonEmptyString(launchContext.Platform, platformFromSourceReference(launchContext.SourceReference))
		if strings.TrimSpace(launchContext.Platform) == "" || strings.TrimSpace(launchContext.ConversationID) == "" || strings.TrimSpace(launchContext.ReplyTargetID) == "" {
			continue
		}
		return launchContext, true
	}
	return interruptedTaskLaunchContext{}, false
}

func interruptedTaskResumeEvent(taskRun task.TaskRun, launchContext interruptedTaskLaunchContext) PlatformInboundEvent {
	conversationID := firstNonEmptyString(taskRun.OriginConversationID, launchContext.ConversationID)
	replyTargetID := firstNonEmptyString(taskRun.OriginReplyTargetID, launchContext.ReplyTargetID)
	return PlatformInboundEvent{
		Platform:       launchContext.Platform,
		Source:         "auto_resume",
		ConversationID: conversationID,
		MessageID:      "auto_resume:" + taskRun.TaskRunID,
		SenderID:       taskRun.RequesterPersonID,
		ReplyTargetID:  replyTargetID,
		Prompt:         taskRun.Prompt,
		Context: VisibleContext{
			ResponseLanguage: "",
			ConversationType: launchContext.ConversationType,
			ChannelID:        launchContext.ChannelID,
			ChannelName:      launchContext.ChannelName,
		},
	}
}

func interruptedTaskActiveGoal(taskRun task.TaskRun, taskEvents []task.TaskEvent) agent.ActiveGoal {
	activeGoal := latestActiveGoal(taskEvents)
	activeGoal.GoalID = firstNonEmptyString(activeGoal.GoalID, taskRun.TaskRunID)
	activeGoal.TaskRunID = firstNonEmptyString(activeGoal.TaskRunID, taskRun.TaskRunID)
	activeGoal.OriginalInstruction = firstNonEmptyString(activeGoal.OriginalInstruction, taskRun.Prompt)
	activeGoal.CurrentObjective = firstNonEmptyString(activeGoal.CurrentObjective, taskRun.Prompt)
	activeGoal.KnownContext = append(activeGoal.KnownContext, interruptedTaskRuntimeContext())
	activeGoal.Status = agent.ActiveGoalStatusActive
	return activeGoal
}

func interruptedTaskTurnDecision(responseLanguage string) *agent.TurnDecision {
	return &agent.TurnDecision{
		Route:            agent.TurnRouteContinueTask,
		Classification:   agent.IntakeClassificationBoundedTask,
		TaskShape:        agent.TaskShapeMaintenanceTask,
		TaskComplexity:   agent.TaskComplexityNormal,
		EffortLevel:      agent.EffortLevelStandard,
		ResponseLanguage: responseLanguage,
		Reason:           "runtime_restart_auto_resume",
	}
}

func interruptedTaskRuntimeContext() string {
	return "Runtime restarted mid-task before the prior attempt completed. Assess prior progress from the task event ledger and restored observations, then continue or redo only the work that is still missing."
}

func platformFromSourceReference(sourceReference string) string {
	platform, _, isFound := strings.Cut(strings.TrimSpace(sourceReference), ":")
	if !isFound {
		return ""
	}
	return strings.TrimSpace(platform)
}
