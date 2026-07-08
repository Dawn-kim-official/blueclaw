package adminapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeConversationResetRepository struct {
	channelID   string
	deletedRows int64
}

func (repository *fakeConversationResetRepository) ResetMattermostDirectConversation(_ context.Context, channelID string) (int64, error) {
	repository.channelID = channelID
	return repository.deletedRows, nil
}

func TestConversationResetHandlerDeletesByChannelID(t *testing.T) {
	repository := &fakeConversationResetRepository{deletedRows: 7}
	handler := ConversationResetHandler{Repository: repository}

	request := httptest.NewRequest(http.MethodPost, "/admin/api/conversation/reset", strings.NewReader(`{"channelID":"dm-channel-1"}`))
	response := httptest.NewRecorder()
	handler.HandleReset(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	if repository.channelID != "dm-channel-1" {
		t.Fatalf("channelID = %q", repository.channelID)
	}
	if !strings.Contains(response.Body.String(), `"deletedRows":7`) {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestConversationResetHandlerRejectsMissingChannelID(t *testing.T) {
	handler := ConversationResetHandler{Repository: &fakeConversationResetRepository{}}

	request := httptest.NewRequest(http.MethodPost, "/admin/api/conversation/reset", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	handler.HandleReset(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestConversationResetHandlerUnavailableWithoutRepository(t *testing.T) {
	handler := ConversationResetHandler{}

	request := httptest.NewRequest(http.MethodPost, "/admin/api/conversation/reset", strings.NewReader(`{"channelID":"dm-channel-1"}`))
	response := httptest.NewRecorder()
	handler.HandleReset(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
}
