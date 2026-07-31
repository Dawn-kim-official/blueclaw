package adminapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Dawn-kim-official/blueclaw/internal/connectors"
)

func TestConnectorEventDiagnosticHandlerListsFilteredEvents(t *testing.T) {
	repository := &testConnectorEventDiagnosticRepository{
		diagnostics: []connectors.EventDiagnostic{{
			RawEventID:        "mattermost:conversation-1:post-1",
			Platform:          "mattermost",
			ConversationID:    "conversation-1",
			ExternalMessageID: "post-1",
			ConnectorStatus:   "succeeded",
			AttemptCount:      1,
			Result:            json.RawMessage(`{"handled":true,"reason":"ignored"}`),
			IngestedAt:        time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC),
		}},
	}
	handler := ConnectorEventDiagnosticHandler{Repository: repository}

	request := httptest.NewRequest(http.MethodGet, "/admin/api/connector/events?platform=mattermost&messageID=post-1&limit=100", nil)
	response := httptest.NewRecorder()
	handler.HandleList(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", response.Code, response.Body.String())
	}
	if repository.filter.Platform != "mattermost" || repository.filter.MessageID != "post-1" || repository.filter.Limit != 50 {
		t.Fatalf("unexpected filter: %+v", repository.filter)
	}
	var diagnostics []connectors.EventDiagnostic
	if errorValue := json.Unmarshal(response.Body.Bytes(), &diagnostics); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(diagnostics) != 1 || diagnostics[0].ConnectorStatus != "succeeded" {
		t.Fatalf("unexpected diagnostics: %+v", diagnostics)
	}
}

func TestConnectorEventDiagnosticHandlerRejectsMissingRepository(t *testing.T) {
	handler := ConnectorEventDiagnosticHandler{}

	request := httptest.NewRequest(http.MethodGet, "/admin/api/connector/events", nil)
	response := httptest.NewRecorder()
	handler.HandleList(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", response.Code)
	}
}

type testConnectorEventDiagnosticRepository struct {
	filter      connectors.EventDiagnosticFilter
	diagnostics []connectors.EventDiagnostic
}

func (repository *testConnectorEventDiagnosticRepository) ListConnectorEventDiagnostics(_ context.Context, filter connectors.EventDiagnosticFilter) ([]connectors.EventDiagnostic, error) {
	repository.filter = filter
	return repository.diagnostics, nil
}
