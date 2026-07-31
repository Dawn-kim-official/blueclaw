package adminapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/internal/connectors"
)

type ConnectorEventDiagnosticRepository interface {
	ListConnectorEventDiagnostics(context.Context, connectors.EventDiagnosticFilter) ([]connectors.EventDiagnostic, error)
}

type ConnectorEventDiagnosticHandler struct {
	Repository ConnectorEventDiagnosticRepository
}

func (handler ConnectorEventDiagnosticHandler) HandleList(responseWriter http.ResponseWriter, request *http.Request) {
	if handler.Repository == nil {
		http.Error(responseWriter, "connector event diagnostics are unavailable", http.StatusServiceUnavailable)
		return
	}
	diagnostics, errorValue := handler.Repository.ListConnectorEventDiagnostics(request.Context(), connectorEventDiagnosticFilter(request))
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, diagnostics)
}

func connectorEventDiagnosticFilter(request *http.Request) connectors.EventDiagnosticFilter {
	query := request.URL.Query()
	return connectors.EventDiagnosticFilter{
		Platform:       strings.TrimSpace(query.Get("platform")),
		ConversationID: strings.TrimSpace(query.Get("conversationID")),
		MessageID:      strings.TrimSpace(query.Get("messageID")),
		Limit:          connectorEventDiagnosticLimit(query.Get("limit")),
	}
}

func connectorEventDiagnosticLimit(value string) int {
	limit, errorValue := strconv.Atoi(strings.TrimSpace(value))
	if errorValue != nil {
		return 20
	}
	if limit <= 0 {
		return 20
	}
	if limit > 50 {
		return 50
	}
	return limit
}
