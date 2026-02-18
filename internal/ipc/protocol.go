package ipc

import "github.com/blueclaw/blueclaw/internal/provider"

type AgentRequest struct {
	SystemPrompt string             `json:"systemPrompt"`
	Messages     []provider.Message `json:"messages"`
	Model        string             `json:"model,omitempty"`
}

type AgentResponse struct {
	Message             provider.Message    `json:"message"`
	ToolCalls           []provider.ToolCall `json:"toolCalls,omitempty"`
	IntermediateContent []string            `json:"intermediateContent,omitempty"`
	Error               string              `json:"error,omitempty"`
}

type ScheduleCreate struct {
	CronExpression string `json:"cronExpression"`
	Prompt         string `json:"prompt"`
}

type EmbeddingCreate struct {
	Text string `json:"text"`
}

type HeartbeatIntervalCreate struct {
	Duration string `json:"duration"`
}

// StdioOutbound is sent from container agent → daemon via stdout.
type StdioOutbound struct {
	Type                    string                   `json:"type"`
	LLMRequest              *provider.Request        `json:"llmRequest,omitempty"`
	ScheduleRequest         *ScheduleCreate          `json:"scheduleRequest,omitempty"`
	EmbeddingRequest        *EmbeddingCreate         `json:"embeddingRequest,omitempty"`
	HeartbeatIntervalRequest *HeartbeatIntervalCreate `json:"heartbeatIntervalRequest,omitempty"`
	DoneResponse            *provider.Response       `json:"doneResponse,omitempty"`
	ErrorMessage            string                   `json:"errorMessage,omitempty"`
	Notification            string                   `json:"notification,omitempty"`
}

// StdioInbound is sent from daemon → container agent via stdin.
type StdioInbound struct {
	Type            string             `json:"type"`
	LLMResponse     *provider.Response `json:"llmResponse,omitempty"`
	ScheduleID      string             `json:"scheduleId,omitempty"`
	ScheduleNext    string             `json:"scheduleNext,omitempty"`
	EmbeddingVector []float32          `json:"embeddingVector,omitempty"`
	ErrorMessage    string             `json:"errorMessage,omitempty"`
}
