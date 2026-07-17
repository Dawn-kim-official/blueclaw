package connectors

import (
	"encoding/json"
	"strings"

	"blueclaw/internal/agent"
	"blueclaw/internal/agentruntime"
	"blueclaw/internal/capability"
)

var connectorTestCapabilityInputSchema = json.RawMessage(`{"type":"object","additionalProperties":{"type":["string","number","boolean","array","object","null"]}}`)

func (connectorRuntime *ConnectorRuntime) UseTestCapabilityTools(capabilityClient capability.Client, toolNames []string) {
	descriptors := make([]agentruntime.CapabilityToolDescriptor, 0, len(toolNames))
	for _, toolName := range toolNames {
		descriptors = append(descriptors, connectorTestCapabilityToolDescriptor(toolName))
	}
	connectorRuntime.UseCapabilityToolDescriptors(capabilityClient, descriptors)
}

func connectorTestCapabilityToolDescriptor(toolName string) agentruntime.CapabilityToolDescriptor {
	sideEffectClass := connectorTestCapabilitySideEffect(toolName)
	descriptor := agentruntime.CapabilityToolDescriptor{
		Name:            toolName,
		CanonicalName:   toolName,
		Namespace:       connectorTestCapabilityNamespace(toolName),
		ModelName:       toolName,
		ModelVisibility: agent.ToolVisibilityModel,
		Description:     "Test capability " + toolName,
		PrivacyClass:    "test",
		InputSchema:     connectorTestCapabilityInputSchema,
		OutputSchema:    json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		PolicyResource:  "tool:" + toolName,
		SideEffectClass: sideEffectClass,
		Availability:    agentruntime.CapabilityAvailability{State: "ok"},
		Idempotency:     agentruntime.CapabilityIdempotency{Scope: "operation"},
	}
	if toolName == "browser.snapshot" {
		descriptor.PrivacyClass = "user_browser"
	}
	if sideEffectClass != agent.ToolSideEffectRead {
		descriptor.CompletionEvidence = &agentruntime.CapabilityCompletionEvidence{Mode: "success", Action: toolName, TargetKind: descriptor.Namespace}
	}
	if sideEffectClass == agent.ToolSideEffectDestructive || sideEffectClass == agent.ToolSideEffectExternalSend {
		descriptor.RequiresApproval = true
	}
	return descriptor
}

func connectorTestCapabilitySideEffect(toolName string) string {
	switch toolName {
	case "browser.snapshot":
		return agent.ToolSideEffectRead
	case "calendar.delete":
		return agent.ToolSideEffectDestructive
	case "message.send":
		return agent.ToolSideEffectExternalSend
	default:
		return agent.ToolSideEffectWorkspaceWrite
	}
}

func connectorTestCapabilityNamespace(toolName string) string {
	if separator := strings.IndexByte(toolName, '.'); separator > 0 {
		return toolName[:separator]
	}
	return toolName
}
