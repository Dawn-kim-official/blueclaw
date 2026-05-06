package agentruntime

import "encoding/json"

type taskLaunchEvent struct {
	Source            TaskLaunchSource `json:"source"`
	SourceReference   string           `json:"sourceReference"`
	ProfileName       string           `json:"profileName"`
	RequesterPersonID string           `json:"requesterPersonID"`
	ConversationID    string           `json:"conversationID"`
	ConversationType  string           `json:"conversationType,omitempty"`
	ChannelID         string           `json:"channelID,omitempty"`
	ChannelName       string           `json:"channelName,omitempty"`
	ToolNames         []string         `json:"toolNames"`
	MemoryFactCount   int              `json:"memoryFactCount"`
}

func marshalTaskLaunchEvent(request TaskLaunchRequest, profileName string, toolNames []string, memoryFactCount int) string {
	document, errorValue := json.Marshal(taskLaunchEvent{
		Source:            request.Source,
		SourceReference:   request.SourceReference,
		ProfileName:       profileName,
		RequesterPersonID: request.RequesterPersonID,
		ConversationID:    request.ConversationID,
		ConversationType:  request.ConversationType,
		ChannelID:         request.ConversationChannelID,
		ChannelName:       request.ConversationChannelName,
		ToolNames:         append([]string{}, toolNames...),
		MemoryFactCount:   memoryFactCount,
	})
	if errorValue != nil {
		return "{}"
	}
	return string(document)
}
