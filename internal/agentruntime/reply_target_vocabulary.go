package agentruntime

import (
	"encoding/json"
	"strings"
)

var originConversationTargetTypes = []string{"currentThread", "currentChannel"}

func narrowMessageSendTargets(inputSchema json.RawMessage, hasOriginConversation bool) json.RawMessage {
	if !hasOriginConversation || len(inputSchema) == 0 {
		return inputSchema
	}
	decodedSchema := map[string]any{}
	if json.Unmarshal(inputSchema, &decodedSchema) != nil {
		return inputSchema
	}
	properties, hasProperties := decodedSchema["properties"].(map[string]any)
	if !hasProperties {
		return inputSchema
	}
	targetType, hasTargetType := properties["targetType"].(map[string]any)
	if !hasTargetType {
		return inputSchema
	}
	offeredValues, hasEnum := targetType["enum"].([]any)
	if !hasEnum {
		return inputSchema
	}
	remainingValues := []any{}
	for _, offeredValue := range offeredValues {
		if valueText, isText := offeredValue.(string); isText && isOriginConversationTarget(valueText) {
			continue
		}
		remainingValues = append(remainingValues, offeredValue)
	}
	if len(remainingValues) == len(offeredValues) {
		return inputSchema
	}
	targetType["enum"] = remainingValues
	targetType["description"] = "Destination for the new message. The conversation this task came from is answered by the finishing message, so it is not a destination here."
	narrowedSchema, errorValue := json.Marshal(decodedSchema)
	if errorValue != nil {
		return inputSchema
	}
	return narrowedSchema
}

func withNarrowedReplyTargets(descriptors []CapabilityToolDescriptor, request ToolCatalogRequest) []CapabilityToolDescriptor {
	hasOriginConversation := strings.TrimSpace(request.ConversationID) != ""
	if !hasOriginConversation {
		return descriptors
	}
	narrowedDescriptors := make([]CapabilityToolDescriptor, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if descriptor.ModelName == "message_send" || descriptor.CanonicalName == "message_send" || descriptor.Name == "message_send" {
			descriptor.InputSchema = narrowMessageSendTargets(descriptor.InputSchema, true)
		}
		narrowedDescriptors = append(narrowedDescriptors, descriptor)
	}
	return narrowedDescriptors
}

func isOriginConversationTarget(targetType string) bool {
	for _, originTargetType := range originConversationTargetTypes {
		if strings.TrimSpace(targetType) == originTargetType {
			return true
		}
	}
	return false
}
