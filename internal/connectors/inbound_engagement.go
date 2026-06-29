package connectors

import (
	"context"
	"log/slog"
	"strings"

	"blueclaw/internal/agent"
)

const ambientDutyLaunchConfidenceThreshold = 0.7

type inboundEngagementDecision struct {
	ShouldLaunch bool
	IgnoreReason string
	AmbientDuty  agent.AmbientDutyContext
}

func shouldIgnoreUninvitedAddressing(event PlatformInboundEvent) bool {
	return isMultiPersonConversation(event) && !event.Context.Addressing.BotMentioned
}

func (connectorRuntime *ConnectorRuntime) resolveInboundEngagement(ctx context.Context, platform string, event PlatformInboundEvent) inboundEngagementDecision {
	if !isMultiPersonConversation(event) {
		return inboundEngagementDecision{ShouldLaunch: true}
	}
	if event.Context.Addressing.BotMentioned {
		return inboundEngagementDecision{ShouldLaunch: true}
	}
	addressingDecision, errorValue := connectorRuntime.agentKernel.ClassifyAddressing(ctx, agent.AddressingClassificationRequest{
		Prompt:           event.Prompt,
		ConversationType: event.Context.ConversationType,
		SenderName:       event.Context.Sender.Name,
		SenderHandle:     event.Context.Sender.Handle,
		VisibleContext:   event.Context.ToAgentVisibleContext(),
	})
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".addressing.classifier_failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		return inboundEngagementDecision{IgnoreReason: "addressing_classifier_failed dutyMatch=false"}
	}
	if addressingDecision.Target == agent.AddressingTargetBot {
		return inboundEngagementDecision{ShouldLaunch: true}
	}
	ambientDuty := ambientDutyContextFromAddressingDecision(addressingDecision)
	if ambientDuty.IsMatch {
		return inboundEngagementDecision{ShouldLaunch: true, AmbientDuty: ambientDuty}
	}
	return inboundEngagementDecision{IgnoreReason: "addressing_" + string(addressingDecision.Target) + " dutyMatch=false"}
}

func isMultiPersonConversation(event PlatformInboundEvent) bool {
	conversationType := strings.ToLower(strings.TrimSpace(event.Context.ConversationType))
	if conversationType == "" {
		return false
	}
	switch conversationType {
	case "d", "dm", "im", "direct":
		return false
	}
	return true
}

func ambientCaptureTurnDecision(dutyName string, responseLanguage string) *agent.TurnDecision {
	return &agent.TurnDecision{
		Route:            agent.TurnRouteStartTask,
		Classification:   agent.IntakeClassificationBoundedTask,
		TaskShape:        agent.TaskShapeMaintenanceTask,
		TaskComplexity:   agent.TaskComplexitySimple,
		EffortLevel:      agent.EffortLevelStandard,
		WorkKinds:        ambientCaptureWorkKinds(dutyName),
		InitialToolNames: ambientCaptureInitialToolNames(dutyName),
		ResponseLanguage: responseLanguage,
		Reason:           "ambient_duty_capture",
	}
}

func ambientCaptureWorkKinds(dutyName string) []string {
	if strings.TrimSpace(dutyName) == "calendar_upkeep" {
		return []string{agent.WorkKindCalendar}
	}
	return nil
}

func ambientCaptureInitialToolNames(dutyName string) []string {
	switch strings.TrimSpace(dutyName) {
	case "calendar_upkeep":
		return []string{"calendar.add", "calendar.update", "calendar.list"}
	case "team_flow_update":
		return []string{"task.add", "task.list", "task.update"}
	default:
		return nil
	}
}

func ambientDutyContextFromAddressingDecision(decision agent.AddressingDecision) agent.AmbientDutyContext {
	if !decision.DutyMatch || decision.DutyConfidence < ambientDutyLaunchConfidenceThreshold {
		return agent.AmbientDutyContext{}
	}
	return (agent.AmbientDutyContext{
		IsMatch:    true,
		Name:       decision.DutyName,
		Confidence: decision.DutyConfidence,
	}).Normalized()
}
