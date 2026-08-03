package agentruntime

import (
	"encoding/json"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/toolcontract"
)

const originConversationReplyRefusal = "The reply to this conversation is delivered when the task completes, so sending it here would say it twice. Put what you want the requester to read in your finishing message. Use this tool only for a different conversation."

func replyBelongsToTheRuntime(operation string, request ToolCatalogRequest, rawInput json.RawMessage) bool {
	if strings.TrimSpace(operation) != "message_send" {
		return false
	}
	originConversationID := strings.TrimSpace(request.ConversationID)
	if originConversationID == "" {
		return false
	}
	decodedInput := map[string]any{}
	if json.Unmarshal(rawInput, &decodedInput) != nil {
		return false
	}
	for _, fieldName := range []string{"conversationID", "targetID", "threadID", "channelID"} {
		value, isText := decodedInput[fieldName].(string)
		if isText && strings.TrimSpace(value) == originConversationID {
			return true
		}
	}
	return false
}

func originConversationReplyFailure() toolcontract.ToolResult {
	return toolcontract.ToolFailureResult(toolcontract.FailurePolicyBlocked, toolcontract.FailureCodes.PolicyBlocked, "message_send", originConversationReplyRefusal)
}
