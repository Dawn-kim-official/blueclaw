package httpserver

import (
	"net/http"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors/api"
)

type AgentReplyHandler struct {
	ReplyStore *api.ReplyStore
}

func (agentReplyHandler AgentReplyHandler) HandleListReplies(responseWriter http.ResponseWriter, request *http.Request) {
	if agentReplyHandler.ReplyStore == nil {
		http.Error(responseWriter, "agent reply store is not configured", http.StatusServiceUnavailable)
		return
	}
	conversationID := strings.TrimSpace(request.URL.Query().Get("conversationID"))
	if conversationID == "" {
		http.Error(responseWriter, "conversationID is required", http.StatusBadRequest)
		return
	}
	writeJSONResponse(responseWriter, http.StatusOK, map[string]any{
		"conversationID": conversationID,
		"replies":        agentReplyHandler.ReplyStore.List(conversationID),
	})
}
