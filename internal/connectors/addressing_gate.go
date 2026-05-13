package connectors

import (
	"context"
	"log/slog"
	"strings"

	"blueclaw/internal/agent"
)

func shouldIgnoreBeforeAuthorization(event PlatformInboundEvent) bool {
	return isMultiPersonConversation(event) && !event.Context.Addressing.BotMentioned && event.Context.Addressing.OtherPersonMentioned
}

func shouldIgnoreUninvitedAddressing(event PlatformInboundEvent) bool {
	return isMultiPersonConversation(event) && !event.Context.Addressing.BotMentioned
}

func (connectorRuntime *ConnectorRuntime) shouldLaunchForAddressing(ctx context.Context, platform string, event PlatformInboundEvent) (bool, string) {
	if !isMultiPersonConversation(event) {
		return true, ""
	}
	if event.Context.Addressing.BotMentioned {
		return true, ""
	}
	if event.Context.Addressing.OtherPersonMentioned {
		return false, "addressed_to_other_person"
	}
	addressingClass, errorValue := connectorRuntime.agentKernel.ClassifyAddressing(ctx, agent.AddressingClassificationRequest{
		Prompt:           event.Prompt,
		ConversationType: event.Context.ConversationType,
		SenderName:       event.Context.Sender.Name,
		SenderHandle:     event.Context.Sender.Handle,
		VisibleContext:   event.Context.ToAgentVisibleContext(),
	})
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".addressing.classifier_failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		return false, "addressing_classifier_failed"
	}
	if addressingClass == agent.AddressingClassAssistantRequested {
		return true, ""
	}
	return false, "addressing_" + string(addressingClass)
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
