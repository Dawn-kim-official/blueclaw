package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blueclaw/blueclaw/internal/agent"
	"github.com/blueclaw/blueclaw/internal/configuration"
	"github.com/blueclaw/blueclaw/internal/outbox"
	"github.com/blueclaw/blueclaw/internal/provider"
	"github.com/blueclaw/blueclaw/internal/scheduler"
	"github.com/blueclaw/blueclaw/internal/tool"
)

type mockLLMProvider struct{}

func (mock *mockLLMProvider) Name() string { return "mock" }
func (mock *mockLLMProvider) SendMessage(_ context.Context, _ provider.Request) (provider.Response, error) {
	return provider.Response{Message: provider.Message{Role: "assistant", Content: "{}"}}, nil
}

type mockRunner struct {
	response provider.Response
}

func (mock *mockRunner) RunAgent(_ context.Context, _ provider.Request, _ string) (provider.Response, error) {
	return mock.response, nil
}

func setupTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	temporaryDirectory := t.TempDir()
	runner := &mockRunner{
		response: provider.Response{
			Message: provider.Message{Role: "assistant", Content: "test response"},
		},
	}
	registry := tool.NewRegistry()
	config := configuration.DefaultConfiguration()
	messageOutbox := outbox.NewOutbox(temporaryDirectory)
	schedulerService, _ := scheduler.NewService(temporaryDirectory, runner, messageOutbox)
	server := NewServer(runner, &mockLLMProvider{}, registry, temporaryDirectory, config, messageOutbox, schedulerService, nil)
	return server, server.Handler()
}

func parseDoneEvent(t *testing.T, body []byte) chatStreamDone {
	t.Helper()
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if event.Type == "done" {
			var done chatStreamDone
			if err := json.Unmarshal(scanner.Bytes(), &done); err != nil {
				t.Fatalf("failed to parse done event: %v", err)
			}
			return done
		}
	}
	t.Fatalf("no done event found in stream: %s", string(body))
	return chatStreamDone{}
}

func TestChatEndpointCreatesSession(t *testing.T) {
	_, handler := setupTestServer(t)
	body := `{"message":"hello"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/chat", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	response := parseDoneEvent(t, recorder.Body.Bytes())
	if response.SessionID == "" {
		t.Error("expected non-empty session ID")
	}
	if response.Response != "test response" {
		t.Errorf("expected %q, got %q", "test response", response.Response)
	}
}

func TestChatEndpointResumesSession(t *testing.T) {
	_, handler := setupTestServer(t)
	firstBody := `{"message":"hello"}`
	firstRequest := httptest.NewRequest(http.MethodPost, "/v1/chat", bytes.NewBufferString(firstBody))
	firstRequest.Header.Set("Content-Type", "application/json")
	firstRecorder := httptest.NewRecorder()
	handler.ServeHTTP(firstRecorder, firstRequest)
	firstResponse := parseDoneEvent(t, firstRecorder.Body.Bytes())
	secondBody, _ := json.Marshal(chatRequest{Message: "follow up", SessionID: firstResponse.SessionID})
	secondRequest := httptest.NewRequest(http.MethodPost, "/v1/chat", bytes.NewBuffer(secondBody))
	secondRequest.Header.Set("Content-Type", "application/json")
	secondRecorder := httptest.NewRecorder()
	handler.ServeHTTP(secondRecorder, secondRequest)
	secondResponse := parseDoneEvent(t, secondRecorder.Body.Bytes())
	if secondResponse.SessionID != firstResponse.SessionID {
		t.Errorf("expected same session ID %q, got %q", firstResponse.SessionID, secondResponse.SessionID)
	}
}

func TestChatEndpointRejectsEmptyMessage(t *testing.T) {
	_, handler := setupTestServer(t)
	body := `{"message":""}`
	request := httptest.NewRequest(http.MethodPost, "/v1/chat", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", recorder.Code)
	}
}

func TestChatEndpointRejectsInvalidJSON(t *testing.T) {
	_, handler := setupTestServer(t)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat", bytes.NewBufferString("{invalid"))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", recorder.Code)
	}
}

func TestDeleteSessionEndpoint(t *testing.T) {
	server, handler := setupTestServer(t)
	session := agent.NewSession("delete-me")
	server.sessionsMutex.Lock()
	server.sessions["delete-me"] = session
	server.sessionsMutex.Unlock()
	request := httptest.NewRequest(http.MethodDelete, "/v1/sessions/delete-me", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", recorder.Code)
	}
	server.sessionsMutex.RLock()
	_, exists := server.sessions["delete-me"]
	server.sessionsMutex.RUnlock()
	if exists {
		t.Error("expected session to be removed")
	}
}

func TestDeleteSessionNotFound(t *testing.T) {
	_, handler := setupTestServer(t)
	request := httptest.NewRequest(http.MethodDelete, "/v1/sessions/nonexistent", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", recorder.Code)
	}
}

func TestHealthEndpoint(t *testing.T) {
	_, handler := setupTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", recorder.Code)
	}
	var response healthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse health response: %v", err)
	}
	if response.Status != "healthy" {
		t.Errorf("expected status %q, got %q", "healthy", response.Status)
	}
}
