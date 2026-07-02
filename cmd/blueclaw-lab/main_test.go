package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fakeOpenRouterServer struct {
	server               *httptest.Server
	mutex                sync.Mutex
	requestCount         int
	schemaNames          []string
	authorizationHeaders []string
	requestDocuments     []map[string]any
	statusCode           int
}

func newFakeOpenRouterServer(statusCode int) *fakeOpenRouterServer {
	fakeServer := &fakeOpenRouterServer{statusCode: statusCode}
	fakeServer.server = httptest.NewServer(http.HandlerFunc(fakeServer.handleRequest))
	return fakeServer
}

func (fakeServer *fakeOpenRouterServer) Close() {
	fakeServer.server.Close()
}

func (fakeServer *fakeOpenRouterServer) URL() string {
	return fakeServer.server.URL
}

func (fakeServer *fakeOpenRouterServer) RequestCount() int {
	fakeServer.mutex.Lock()
	defer fakeServer.mutex.Unlock()
	return fakeServer.requestCount
}

func (fakeServer *fakeOpenRouterServer) SchemaNames() []string {
	fakeServer.mutex.Lock()
	defer fakeServer.mutex.Unlock()
	return append([]string{}, fakeServer.schemaNames...)
}

func (fakeServer *fakeOpenRouterServer) AuthorizationHeaders() []string {
	fakeServer.mutex.Lock()
	defer fakeServer.mutex.Unlock()
	return append([]string{}, fakeServer.authorizationHeaders...)
}

func (fakeServer *fakeOpenRouterServer) RequestDocuments() []map[string]any {
	fakeServer.mutex.Lock()
	defer fakeServer.mutex.Unlock()
	return append([]map[string]any{}, fakeServer.requestDocuments...)
}

func (fakeServer *fakeOpenRouterServer) handleRequest(responseWriter http.ResponseWriter, request *http.Request) {
	requestDocument := openRouterRequestDocument(request)
	schemaName := schemaNameFromOpenRouterDocument(requestDocument)
	fakeServer.mutex.Lock()
	fakeServer.requestCount++
	fakeServer.schemaNames = append(fakeServer.schemaNames, schemaName)
	fakeServer.authorizationHeaders = append(fakeServer.authorizationHeaders, request.Header.Get("Authorization"))
	fakeServer.requestDocuments = append(fakeServer.requestDocuments, requestDocument)
	fakeServer.mutex.Unlock()
	if fakeServer.statusCode >= http.StatusBadRequest {
		responseWriter.WriteHeader(fakeServer.statusCode)
		_, _ = responseWriter.Write([]byte(`{"error":{"message":"fake server failure"}}`))
		return
	}
	responseWriter.Header().Set("Content-Type", "application/json")
	encodedContent, _ := json.Marshal(openRouterContentForSchema(schemaName))
	_, _ = responseWriter.Write([]byte(`{"choices":[{"message":{"content":` + string(encodedContent) + `}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
}

func openRouterRequestDocument(request *http.Request) map[string]any {
	var requestDocument map[string]any
	_ = json.NewDecoder(request.Body).Decode(&requestDocument)
	if requestDocument == nil {
		return map[string]any{}
	}
	return requestDocument
}

func schemaNameFromOpenRouterDocument(requestDocument map[string]any) string {
	responseFormat, isFound := requestDocument["response_format"].(map[string]any)
	if !isFound {
		return ""
	}
	jsonSchema, isFound := responseFormat["json_schema"].(map[string]any)
	if !isFound {
		return ""
	}
	name, _ := jsonSchema["name"].(string)
	return strings.TrimSpace(name)
}

func openRouterContentForSchema(schemaName string) string {
	switch schemaName {
	case "blueclaw_skill_search_queries":
		return `{"queries":[]}`
	case "blueclaw_turn_router":
		return `{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","taskComplexity":"normal","effortLevel":"standard","requestedOutputFormats":null,"siteRequestEvidence":"","responseLanguage":"ko","reason":"fake live router","userFacingReply":""}`
	case "blueclaw_agent_turn_action":
		return `{"action":"finish","message":"fake live reply from OpenRouter","completionSummary":"fake live reply from OpenRouter","replyParts":[{"type":"text","text":"fake live reply from OpenRouter"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":[],"qualityReview":[],"executionStateUpdate":{}}`
	default:
		return "fake recovery reply"
	}
}

func TestRunVirtualSessionLiveLanguageModelUsesOpenRouterKeyFileAndFakeServer(t *testing.T) {
	fakeServer := newFakeOpenRouterServer(http.StatusOK)
	defer fakeServer.Close()
	arguments := parseLiveVirtualSessionTestArguments(t, fakeServer.URL())

	output, errorValue := captureStandardOutput(func() error {
		return runVirtualSession(context.Background(), arguments)
	})

	if errorValue != nil {
		t.Fatalf("expected live virtual session to pass: %v\n%s", errorValue, output)
	}
	if fakeServer.RequestCount() == 0 {
		t.Fatal("expected fake OpenRouter server to receive at least one request")
	}
	if !containsString(fakeServer.SchemaNames(), "blueclaw_agent_turn_action") {
		t.Fatalf("expected full path to call action model, got schemas %+v", fakeServer.SchemaNames())
	}
	if !allStringsEqual(fakeServer.AuthorizationHeaders(), "Bearer sk-file-test") {
		t.Fatalf("expected key file authorization header, got %+v", fakeServer.AuthorizationHeaders())
	}
	if !allOpenRouterRequestsUseGenerationOptions(fakeServer.RequestDocuments(), 123, 0.25) {
		t.Fatalf("expected seed and temperature to be forwarded, got %+v", fakeServer.RequestDocuments())
	}
	if !strings.Contains(output, "fake live reply from OpenRouter") {
		t.Fatalf("expected fake model reply in output, got %s", output)
	}
	if strings.Contains(output, "요청 처리 중 오류") {
		t.Fatalf("expected live reply instead of fallback, got %s", output)
	}
}

func TestRunVirtualSessionLiveLanguageModelPrintsFailureSummary(t *testing.T) {
	fakeServer := newFakeOpenRouterServer(http.StatusInternalServerError)
	defer fakeServer.Close()
	arguments := parseLiveVirtualSessionTestArguments(t, fakeServer.URL())

	output, errorValue := captureStandardOutput(func() error {
		return runVirtualSession(context.Background(), arguments)
	})

	if errorValue != nil {
		t.Fatalf("expected failure scenario to complete with user notice: %v\n%s", errorValue, output)
	}
	if fakeServer.RequestCount() == 0 {
		t.Fatal("expected fake OpenRouter server to receive at least one request")
	}
	if !strings.Contains(output, "llm.call error:") {
		t.Fatalf("expected llm.call failure summary, got %s", output)
	}
	if !strings.Contains(output, "code=500") {
		t.Fatalf("expected HTTP 500 detail in failure summary, got %s", output)
	}
}

func parseLiveVirtualSessionTestArguments(t *testing.T, baseURL string) virtualSessionArguments {
	t.Helper()
	homeDirectoryPath := t.TempDir()
	keyDirectoryPath := filepath.Join(homeDirectoryPath, ".internkim")
	if errorValue := os.MkdirAll(keyDirectoryPath, 0700); errorValue != nil {
		t.Fatalf("failed to create key directory: %v", errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(keyDirectoryPath, "openrouter_api_key"), []byte("sk-file-test\n"), 0600); errorValue != nil {
		t.Fatalf("failed to write key file: %v", errorValue)
	}
	t.Setenv("HOME", homeDirectoryPath)
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("OPENROUTER_BASE_URL", baseURL)
	originalDelay := delayLiveVirtualSession
	delayLiveVirtualSession = func() {}
	t.Cleanup(func() {
		delayLiveVirtualSession = originalDelay
	})
	arguments, errorValue := parseVirtualSessionArguments([]string{
		"--scenario", "plain_question_acceptance",
		"--artifact-dir", t.TempDir(),
		"--live-llm",
		"--llm-model", "test-model",
		"--seed", "123",
		"--temperature", "0.25",
	}, "presentation", t.TempDir())
	if errorValue != nil {
		t.Fatalf("expected parse to succeed: %v", errorValue)
	}
	if !arguments.LiveLanguageModel {
		t.Fatal("expected --live-llm to enable live language model")
	}
	return arguments
}

func allOpenRouterRequestsUseGenerationOptions(requestDocuments []map[string]any, seed int64, temperature float64) bool {
	if len(requestDocuments) == 0 {
		return false
	}
	for _, requestDocument := range requestDocuments {
		if requestDocument["seed"] != float64(seed) || requestDocument["temperature"] != temperature {
			return false
		}
	}
	return true
}

func captureStandardOutput(action func() error) (string, error) {
	originalStandardOutput := os.Stdout
	readPipe, writePipe, errorValue := os.Pipe()
	if errorValue != nil {
		return "", errorValue
	}
	os.Stdout = writePipe
	actionError := action()
	_ = writePipe.Close()
	os.Stdout = originalStandardOutput
	output, readError := io.ReadAll(readPipe)
	if actionError != nil {
		return string(output), actionError
	}
	return string(output), readError
}

func containsString(values []string, expectedValue string) bool {
	for _, value := range values {
		if value == expectedValue {
			return true
		}
	}
	return false
}

func allStringsEqual(values []string, expectedValue string) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if value != expectedValue {
			return false
		}
	}
	return true
}
