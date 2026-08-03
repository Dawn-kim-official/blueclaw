package connectors

import (
	"encoding/json"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/taskstate"
)

const messageSendToolName = "message_send"

type toolRequestedEventBody struct {
	ToolName string          `json:"toolName"`
	Input    json.RawMessage `json:"input"`
}

func agentAlreadyRepliedToConversation(taskEvents []taskstate.TaskEvent, conversationID string) bool {
	trimmedConversationID := strings.TrimSpace(conversationID)
	if trimmedConversationID == "" {
		return false
	}
	for _, taskEvent := range taskEvents {
		if taskEvent.Name != "tool."+messageSendToolName+".requested" {
			continue
		}
		requested := toolRequestedEventBody{}
		if json.Unmarshal([]byte(taskEvent.Body), &requested) != nil {
			continue
		}
		if messageSendTargetsConversation(requested.Input, trimmedConversationID) && messageSendSucceeded(taskEvents) {
			return true
		}
	}
	return false
}

func messageSendTargetsConversation(toolInput json.RawMessage, conversationID string) bool {
	decodedInput := map[string]any{}
	if json.Unmarshal(toolInput, &decodedInput) != nil {
		return false
	}
	for _, fieldName := range []string{"conversationID", "targetID", "channelID", "threadID"} {
		if value, isText := decodedInput[fieldName].(string); isText && strings.TrimSpace(value) == conversationID {
			return true
		}
	}
	return false
}

func messageSendSucceeded(taskEvents []taskstate.TaskEvent) bool {
	for _, taskEvent := range taskEvents {
		if taskEvent.Name != "tool."+messageSendToolName+".result" {
			continue
		}
		if !strings.Contains(taskEvent.Body, "\"failure\"") {
			return true
		}
	}
	return false
}
