package adminapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type ConversationResetRepository interface {
	ResetMattermostDirectConversation(ctx context.Context, channelID string) (int64, error)
}

type ConversationResetHandler struct {
	Repository ConversationResetRepository
}

type conversationResetRequest struct {
	ChannelID string `json:"channelID"`
}

func (handler ConversationResetHandler) HandleReset(responseWriter http.ResponseWriter, request *http.Request) {
	if handler.Repository == nil {
		http.Error(responseWriter, "conversation reset is unavailable", http.StatusServiceUnavailable)
		return
	}
	var payload conversationResetRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&payload); errorValue != nil {
		http.Error(responseWriter, "invalid request body", http.StatusBadRequest)
		return
	}
	channelID := strings.TrimSpace(payload.ChannelID)
	if channelID == "" {
		http.Error(responseWriter, "channelID is required", http.StatusBadRequest)
		return
	}
	deletedRows, errorValue := handler.Repository.ResetMattermostDirectConversation(request.Context(), channelID)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]int64{"deletedRows": deletedRows})
}
