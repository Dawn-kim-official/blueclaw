package connectors

import (
	"context"
	"log/slog"
	"strings"

	"blueclaw/internal/agent"
)

const ambientDutyLaunchConfidenceThreshold = 0.7

type addressingLaunchDecision struct {
	ShouldLaunch bool
	IgnoreReason string
	AmbientDuty  agent.AmbientDutyContext
}

func shouldIgnoreBeforeAuthorization(event PlatformInboundEvent) bool {
	return false
}

func shouldIgnoreUninvitedAddressing(event PlatformInboundEvent) bool {
	return isMultiPersonConversation(event) && !event.Context.Addressing.BotMentioned
}

func (connectorRuntime *ConnectorRuntime) shouldLaunchForAddressing(ctx context.Context, platform string, event PlatformInboundEvent) addressingLaunchDecision {
	if !isMultiPersonConversation(event) {
		return addressingLaunchDecision{ShouldLaunch: true}
	}
	if event.Context.Addressing.BotMentioned {
		return addressingLaunchDecision{ShouldLaunch: true}
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
		return addressingLaunchDecision{IgnoreReason: "addressing_classifier_failed dutyMatch=false"}
	}
	if addressingDecision.Target == agent.AddressingTargetBot {
		return addressingLaunchDecision{ShouldLaunch: true}
	}
	ambientDuty := ambientDutyContextFromAddressingDecision(addressingDecision)
	if ambientDuty.IsMatch {
		return addressingLaunchDecision{ShouldLaunch: true, AmbientDuty: ambientDuty}
	}
	return addressingLaunchDecision{IgnoreReason: "addressing_" + string(addressingDecision.Target) + " dutyMatch=false"}
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
