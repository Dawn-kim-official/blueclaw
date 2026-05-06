package adminapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"blueclaw/internal/llm"
)

type AttentionHandler struct {
	LanguageModel llm.LanguageModelProvider
}

type attentionRequest struct {
	JobID             string                 `json:"jobID"`
	ToolName          string                 `json:"toolName"`
	RequesterPersonID string                 `json:"requesterPersonID,omitempty"`
	RequesterEmail    string                 `json:"requesterEmail,omitempty"`
	ConversationID    string                 `json:"conversationID,omitempty"`
	Platform          string                 `json:"platform,omitempty"`
	PrivacyClass      string                 `json:"privacyClass,omitempty"`
	LocalDecision     attentionLocalDecision `json:"localDecision"`
	DeduplicationKey  string                 `json:"deduplicationKey"`
}

type attentionLocalDecision struct {
	ShouldEscalate   bool     `json:"shouldEscalate"`
	Importance       string   `json:"importance"`
	Confidence       float64  `json:"confidence"`
	ReasonCodes      []string `json:"reasonCodes"`
	SummaryForRemote string   `json:"summaryForRemote"`
	PrivacyClass     string   `json:"privacyClass"`
}

type attentionResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type attentionDecisionDocument struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Reason  string `json:"reason"`
}

func (attentionHandler AttentionHandler) HandleRunAttention(responseWriter http.ResponseWriter, request *http.Request) {
	if attentionHandler.LanguageModel == nil {
		http.Error(responseWriter, "attention language model is not configured", http.StatusServiceUnavailable)
		return
	}
	var attentionRequest attentionRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&attentionRequest); errorValue != nil {
		http.Error(responseWriter, "invalid attention request", http.StatusBadRequest)
		return
	}
	if errorValue := validateAttentionRequest(attentionRequest); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	structuredResponse, errorValue := attentionHandler.LanguageModel.GenerateStructuredResponse(request.Context(), llm.StructuredResponseRequest{
		Messages: []llm.Message{
			{Role: "system", Content: "Decide whether to send a short user-facing attention message. Return ATTENTION_SILENT unless the user is likely blocked or needs a timely nudge. Do not mention private local details beyond the supplied summary."},
			{Role: "user", Content: attentionPrompt(attentionRequest)},
		},
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_attention_decision",
			Document:           attentionDecisionSchema,
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadGateway)
		return
	}
	attentionResponse, errorValue := parseAttentionDecision(structuredResponse.Content)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(responseWriter, http.StatusOK, attentionResponse)
}

func validateAttentionRequest(request attentionRequest) error {
	if strings.TrimSpace(request.JobID) == "" {
		return errors.New("jobID is required")
	}
	if strings.TrimSpace(request.ToolName) == "" {
		return errors.New("toolName is required")
	}
	if !request.LocalDecision.ShouldEscalate {
		return errors.New("local decision must request escalation")
	}
	if strings.TrimSpace(request.LocalDecision.SummaryForRemote) == "" {
		return errors.New("summaryForRemote is required")
	}
	return nil
}

func attentionPrompt(request attentionRequest) string {
	document, errorValue := json.Marshal(request)
	if errorValue != nil {
		return "{}"
	}
	return string(document)
}

func parseAttentionDecision(content string) (attentionResponse, error) {
	var document attentionDecisionDocument
	if errorValue := json.Unmarshal([]byte(strings.TrimSpace(content)), &document); errorValue != nil {
		return attentionResponse{}, errorValue
	}
	status := strings.TrimSpace(document.Status)
	switch status {
	case "ATTENTION_SILENT":
		return attentionResponse{Status: status, Reason: strings.TrimSpace(document.Reason)}, nil
	case "ATTENTION_MESSAGE":
		message := sanitizeAttentionMessage(document.Message)
		if message == "" {
			return attentionResponse{Status: "ATTENTION_SILENT", Reason: "empty_message"}, nil
		}
		return attentionResponse{Status: status, Message: message, Reason: strings.TrimSpace(document.Reason)}, nil
	default:
		return attentionResponse{}, errors.New("attention status must be ATTENTION_SILENT or ATTENTION_MESSAGE")
	}
}

func sanitizeAttentionMessage(value string) string {
	trimmedValue := strings.TrimSpace(value)
	if len(trimmedValue) > 280 {
		return trimmedValue[:280]
	}
	return trimmedValue
}

const attentionDecisionSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["status", "message", "reason"],
  "properties": {
    "status": {"type": "string", "enum": ["ATTENTION_SILENT", "ATTENTION_MESSAGE"]},
    "message": {"type": "string", "maxLength": 280},
    "reason": {"type": "string", "maxLength": 160}
  }
}`
