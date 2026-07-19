package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/capability"
	"blueclaw/internal/e2e"
	"blueclaw/internal/llm"
	"blueclaw/internal/task"
)

func TestValidateStrictEmbeddingRetrievalRequiresReadyEmbeddingMode(t *testing.T) {
	validResult := e2e.VirtualSessionResult{TurnResults: []e2e.VirtualTurnResult{{Events: []task.TaskEvent{
		{Name: "agent.instructions_loaded", Body: `{"retrievalMode":"embedding","indexStatus":"ready"}`},
		{Name: "agent.instructions_loaded", Body: `{"retrievalMode":"direct","indexStatus":"bypassed"}`},
		{Name: "agent.instructions_loaded", Body: `{"retrievalMode":"structured_query","indexStatus":"empty_query"}`},
	}}}}
	if errorValue := validateStrictEmbeddingRetrieval(validResult); errorValue != nil {
		t.Fatalf("expected ready embedding retrieval, got %v", errorValue)
	}
	fallbackResult := e2e.VirtualSessionResult{TurnResults: []e2e.VirtualTurnResult{{Events: []task.TaskEvent{{
		Name: "agent.instructions_loaded",
		Body: `{"retrievalMode":"bm25_fallback","indexStatus":"query_failed"}`,
	}}}}}
	if errorValue := validateStrictEmbeddingRetrieval(fallbackResult); errorValue == nil {
		t.Fatal("expected BM25 fallback to fail strict live embedding verification")
	}
	directOnlyResult := e2e.VirtualSessionResult{TurnResults: []e2e.VirtualTurnResult{{Events: []task.TaskEvent{{
		Name: "agent.instructions_loaded",
		Body: `{"retrievalMode":"direct","indexStatus":"bypassed"}`,
	}}}}}
	if errorValue := validateStrictEmbeddingRetrieval(directOnlyResult); errorValue == nil {
		t.Fatal("expected direct-only retrieval to fail strict live embedding verification")
	}
}

func TestBuildVirtualTurnMetricsRecordsEfficiencyWithoutThresholds(t *testing.T) {
	turnResult := e2e.VirtualTurnResult{
		TaskRunID: "task-1",
		Events: []task.TaskEvent{
			{Name: "agent.action"},
			{Name: "agent.action"},
			{Name: "tool.task.add.requested"},
			{Name: "blueclaw.task.execution_duration", Body: `{"durationMs":4200}`},
		},
		LanguageModelCallEvents: []e2e.VirtualLanguageModelCallEvent{
			{LatencyMS: 1200},
			{LatencyMS: 800},
		},
		InformationalAssertions: []e2e.VirtualInformationalAssertion{{
			Name:      "expected event count tool.result",
			Satisfied: false,
			Detail:    "expected=1 actual=2",
		}},
	}
	metrics := buildVirtualTurnMetrics(3, turnResult)
	if metrics.TurnNumber != 3 || metrics.TaskRunID != "task-1" || metrics.AgentStepCount != 2 || metrics.ToolCallCount != 1 {
		t.Fatalf("unexpected step metrics: %+v", metrics)
	}
	if metrics.LanguageModelCallCount != 2 || metrics.LanguageModelLatencyMS != 2000 || metrics.TaskDurationMS != 4200 {
		t.Fatalf("unexpected duration metrics: %+v", metrics)
	}
	if len(metrics.InformationalAssertions) != 1 || metrics.InformationalAssertions[0].Satisfied {
		t.Fatalf("expected efficiency mismatch to remain informational: %+v", metrics.InformationalAssertions)
	}
}

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
		return `{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","estimatedMinutes":1,"requestedOutputFormats":null,"requiredEvidence":[],"initialToolNames":[],"responseLanguage":"ko","reason":"fake live router","userFacingReply":"","priorTaskReference":"none"}`
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

func TestCreateLiveLanguageModelSupportsSDKDWithoutOpenRouterCredentials(t *testing.T) {
	t.Setenv("BLUECLAW_E2E_LLM_AUTH_KEY", "installation-key")
	t.Setenv("OPENROUTER_API_KEY", "")
	seed := int64(17)
	temperature := 0.1
	languageModel, errorValue := createLiveLanguageModel(virtualSessionArguments{
		LanguageModelProvider: "sdkd",
		LanguageModelEndpoint: "http://sdkd",
		LanguageModelName:     "deepseek/deepseek-v4-flash",
		ExecutionMode:         "remote",
		Seed:                  &seed,
		Temperature:           &temperature,
	})
	if errorValue != nil {
		t.Fatalf("expected sdkd live model: %v", errorValue)
	}
	sdkdClient, isSDKDClient := languageModel.(llm.SDKDClient)
	if !isSDKDClient {
		t.Fatalf("expected sdkd client, got %T", languageModel)
	}
	if sdkdClient.AuthKey != "installation-key" || sdkdClient.ModelName != "deepseek/deepseek-v4-flash" {
		t.Fatalf("unexpected sdkd live model configuration: %+v", sdkdClient)
	}
	if sdkdClient.GenerationOptions.Seed == nil || *sdkdClient.GenerationOptions.Seed != seed {
		t.Fatalf("expected sdkd generation seed, got %+v", sdkdClient.GenerationOptions)
	}
	expectedSchemaNames := []string{"blueclaw_agent_turn_action", "blueclaw_agent_turn_finalizer", "blueclaw_turn_router", "blueclaw_recovery_decision", "blueclaw_operation_contract"}
	if !slices.Equal(sdkdClient.StructuredSchemaNames, expectedSchemaNames) {
		t.Fatalf("expected authoritative SDKD schemas, got %#v", sdkdClient.StructuredSchemaNames)
	}
}

func TestParseVirtualSessionArgumentsLeavesEndpointOmitted(t *testing.T) {
	t.Setenv("BLUECLAW_E2E_LLM_ENDPOINT", "")
	arguments, errorValue := parseVirtualSessionArguments([]string{"--llm-provider", "sdkd"}, "task-lifecycle", t.TempDir())
	if errorValue != nil {
		t.Fatalf("expected arguments to parse: %v", errorValue)
	}
	if arguments.LanguageModelEndpoint != "" {
		t.Fatalf("expected omitted endpoint to remain empty, got %q", arguments.LanguageModelEndpoint)
	}
}

func TestCreateLiveLanguageModelUsesProviderConstructorDefaults(t *testing.T) {
	t.Setenv("BLUECLAW_E2E_LLM_AUTH_KEY", "installation-key")
	t.Setenv("OPENROUTER_API_KEY", "")

	capabilityModel, errorValue := createLiveLanguageModel(virtualSessionArguments{LanguageModelProvider: "capability"})
	if errorValue != nil {
		t.Fatalf("expected capability model: %v", errorValue)
	}
	capabilityClient, isCapabilityClient := capabilityModel.(llm.CapabilityLLMClient)
	if !isCapabilityClient || capabilityClient.CapabilityClient.Endpoint != capability.DefaultEndpoint {
		t.Fatalf("expected capability constructor default endpoint, got %#v", capabilityModel)
	}

	sdkdModel, errorValue := createLiveLanguageModel(virtualSessionArguments{LanguageModelProvider: "sdkd"})
	if errorValue != nil {
		t.Fatalf("expected sdkd model: %v", errorValue)
	}
	sdkdClient, isSDKDClient := sdkdModel.(llm.SDKDClient)
	if !isSDKDClient || sdkdClient.Endpoint != "http://blueclaw-sdkd" {
		t.Fatalf("expected sdkd constructor default endpoint, got %#v", sdkdModel)
	}
}

func TestCreateLiveLanguageModelPreservesUnixSocketTransportWithOmittedEndpoint(t *testing.T) {
	t.Setenv("BLUECLAW_E2E_LLM_AUTH_KEY", "installation-key")
	t.Setenv("OPENROUTER_API_KEY", "")
	arguments := virtualSessionArguments{LanguageModelSocket: "/tmp/blueclaw-llm.sock"}

	capabilityModel, errorValue := createLiveLanguageModel(virtualSessionArguments{
		LanguageModelProvider: "capability",
		LanguageModelSocket:   arguments.LanguageModelSocket,
	})
	if errorValue != nil {
		t.Fatalf("expected capability socket model: %v", errorValue)
	}
	capabilityClient := capabilityModel.(llm.CapabilityLLMClient).CapabilityClient
	if capabilityClient.Endpoint != "http://internkim-capability" {
		t.Fatalf("expected capability socket endpoint, got %q", capabilityClient.Endpoint)
	}
	if httpClient, isHTTPClient := capabilityClient.HTTPClient.(*http.Client); !isHTTPClient || httpClient.Transport == nil {
		t.Fatal("expected capability unix socket transport")
	} else if httpClient.Timeout != 0 {
		t.Fatalf("expected capability live client without timeout, got %s", httpClient.Timeout)
	}

	sdkdModel, errorValue := createLiveLanguageModel(virtualSessionArguments{
		LanguageModelProvider: "sdkd",
		LanguageModelSocket:   arguments.LanguageModelSocket,
	})
	if errorValue != nil {
		t.Fatalf("expected sdkd socket model: %v", errorValue)
	}
	sdkdClient := sdkdModel.(llm.SDKDClient)
	if sdkdClient.Endpoint != "http://blueclaw-sdkd" || sdkdClient.HTTPClient == nil {
		t.Fatalf("expected sdkd socket transport and default endpoint, got %#v", sdkdClient)
	}
	if httpClient, isHTTPClient := sdkdClient.HTTPClient.(*http.Client); !isHTTPClient || httpClient.Timeout != 0 {
		t.Fatalf("expected SDKD live client without timeout, got %#v", sdkdClient.HTTPClient)
	}
}

func TestCreateLiveLanguageModelPreservesExplicitEndpointOverrides(t *testing.T) {
	t.Setenv("BLUECLAW_E2E_LLM_AUTH_KEY", "installation-key")
	t.Setenv("OPENROUTER_API_KEY", "")
	for _, provider := range []string{"capability", "sdkd"} {
		languageModel, errorValue := createLiveLanguageModel(virtualSessionArguments{
			LanguageModelProvider: provider,
			LanguageModelEndpoint: "https://explicit-llm.example",
			LanguageModelSocket:   "/tmp/blueclaw-llm.sock",
		})
		if errorValue != nil {
			t.Fatalf("expected %s model: %v", provider, errorValue)
		}
		var endpoint string
		switch client := languageModel.(type) {
		case llm.CapabilityLLMClient:
			endpoint = client.CapabilityClient.Endpoint
		case llm.SDKDClient:
			endpoint = client.Endpoint
		default:
			t.Fatalf("unexpected %s model type %T", provider, languageModel)
		}
		if endpoint != "https://explicit-llm.example" {
			t.Fatalf("expected explicit endpoint for %s, got %q", provider, endpoint)
		}
	}
}

func TestSaveVirtualSessionEvidenceRecordsRoutingMetadataWithoutSecrets(t *testing.T) {
	artifactDirectoryPath := t.TempDir()
	result := e2e.VirtualSessionResult{
		ScenarioName:          "task-lifecycle",
		ArtifactDirectoryPath: artifactDirectoryPath,
		TurnResults: []e2e.VirtualTurnResult{{
			TaskStatus:    "blocked",
			FailureReason: "operation contract was invalid",
			LanguageModelCallEvents: []e2e.VirtualLanguageModelCallEvent{{
				Kind:            "recovery_chat",
				Provider:        "llama.cpp",
				Model:           "gemma",
				SelectedBackend: "device",
				UsedFallback:    false,
				FinishReason:    "stop",
			}},
		}},
	}
	arguments := virtualSessionArguments{
		LanguageModelProvider:    "sdkd",
		LanguageModelAuthKeyPath: "/tmp/secret-auth-key",
		ExecutionMode:            "auto",
	}
	if errorValue := saveVirtualSessionEvidence(arguments, result, nil); errorValue != nil {
		t.Fatalf("expected evidence to save: %v", errorValue)
	}
	document, errorValue := os.ReadFile(filepath.Join(artifactDirectoryPath, "llm-routing-evidence.json"))
	if errorValue != nil {
		t.Fatalf("expected evidence file: %v", errorValue)
	}
	content := string(document)
	for _, expectedText := range []string{"task-lifecycle", "failed", "blocked", "operation contract was invalid", "sdkd", "recovery_chat", "llama.cpp", "gemma", "device", "blueclaw_agent_turn_action", "blueclaw_agent_turn_finalizer", "blueclaw_turn_router", "blueclaw_recovery_decision", "blueclaw_operation_contract"} {
		if !strings.Contains(content, expectedText) {
			t.Fatalf("evidence missing %q: %s", expectedText, content)
		}
	}
	if strings.Contains(content, "secret-auth-key") {
		t.Fatalf("evidence leaked authentication path: %s", content)
	}
	resultDocument, errorValue := os.ReadFile(filepath.Join(artifactDirectoryPath, "result.json"))
	if errorValue != nil {
		t.Fatalf("expected full result evidence file: %v", errorValue)
	}
	for _, expectedText := range []string{"task-lifecycle", "blocked", "operation contract was invalid"} {
		if !strings.Contains(string(resultDocument), expectedText) {
			t.Fatalf("result evidence missing %q: %s", expectedText, resultDocument)
		}
	}
	if strings.Contains(string(resultDocument), "secret-auth-key") {
		t.Fatalf("result evidence leaked authentication path: %s", resultDocument)
	}
}

func TestSaveVirtualSessionEvidencePreservesFailureResult(t *testing.T) {
	artifactDirectoryPath := t.TempDir()
	result := e2e.VirtualSessionResult{
		ScenarioName:          "file-write",
		ArtifactDirectoryPath: artifactDirectoryPath,
		TurnResults: []e2e.VirtualTurnResult{{
			TaskRunID:  "task-1",
			TaskStatus: task.TaskStatusCompleted,
			Events: []task.TaskEvent{{
				Name: "agent.operation_contract",
				Body: `{"operations":[{"toolName":"file.deliver"}]}`,
			}},
		}},
	}
	runError := errors.New("strict assertion failed")

	if errorValue := saveVirtualSessionEvidence(virtualSessionArguments{}, result, runError); errorValue != nil {
		t.Fatalf("expected failure evidence to save: %v", errorValue)
	}
	document, errorValue := os.ReadFile(filepath.Join(artifactDirectoryPath, "result.json"))
	if errorValue != nil {
		t.Fatalf("expected result evidence: %v", errorValue)
	}
	var evidence virtualSessionResultEvidence
	if errorValue := json.Unmarshal(document, &evidence); errorValue != nil {
		t.Fatalf("expected result evidence JSON: %v", errorValue)
	}
	if evidence.Status != "failed" || evidence.RunError != runError.Error() {
		t.Fatalf("expected failed run evidence, got %+v", evidence)
	}
	if len(evidence.Result.TurnResults) != 1 || len(evidence.Result.TurnResults[0].Events) != 1 {
		t.Fatalf("expected task event evidence, got %+v", evidence.Result)
	}
}

func TestVirtualSessionEvidenceStatusUsesFinalTaskOutcome(t *testing.T) {
	testCases := []struct {
		name           string
		taskStatus     task.TaskStatus
		runError       error
		expectedStatus string
	}{
		{name: "completed", taskStatus: task.TaskStatusCompleted, expectedStatus: "succeeded"},
		{name: "waiting", taskStatus: task.TaskStatusWaitingUserInput, expectedStatus: "incomplete"},
		{name: "blocked", taskStatus: task.TaskStatusBlocked, expectedStatus: "failed"},
		{name: "run error", taskStatus: task.TaskStatusCompleted, runError: errors.New("harness failed"), expectedStatus: "failed"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			result := e2e.VirtualSessionResult{TurnResults: []e2e.VirtualTurnResult{{TaskStatus: testCase.taskStatus}}}
			if status := virtualSessionEvidenceStatus(result, testCase.runError); status != testCase.expectedStatus {
				t.Fatalf("expected %s, got %s", testCase.expectedStatus, status)
			}
		})
	}
}

func TestSaveVirtualSessionEvidenceUsesOrderedVirtualCallRecorderWithoutDuplicates(t *testing.T) {
	artifactDirectoryPath := t.TempDir()
	result := e2e.VirtualSessionResult{
		ScenarioName:          "task-lifecycle",
		ArtifactDirectoryPath: artifactDirectoryPath,
		TurnResults: []e2e.VirtualTurnResult{{
			LanguageModelCallEvents: []e2e.VirtualLanguageModelCallEvent{
				{
					Kind:            "chat",
					SchemaName:      "blueclaw_agent_turn_action",
					Provider:        "sdkd",
					Model:           "low-model",
					SelectedBackend: "device",
					FinishReason:    "tool_calls",
				},
				{
					Kind:            "chat",
					Provider:        "sdkd",
					Model:           "low-model",
					SelectedBackend: "device",
					FinishReason:    "stop",
				},
			},
			Events: []task.TaskEvent{{
				Name: "llm.call",
				Body: `{"kind":"chat","schemaName":"duplicate-should-not-appear"}`,
			}},
		}},
	}
	if errorValue := saveVirtualSessionEvidence(virtualSessionArguments{LanguageModelProvider: "sdkd"}, result, nil); errorValue != nil {
		t.Fatalf("expected evidence to save: %v", errorValue)
	}
	document, errorValue := os.ReadFile(filepath.Join(artifactDirectoryPath, "llm-routing-evidence.json"))
	if errorValue != nil {
		t.Fatalf("expected evidence file: %v", errorValue)
	}
	var evidence virtualSessionEvidence
	if errorValue := json.Unmarshal(document, &evidence); errorValue != nil {
		t.Fatalf("expected evidence JSON: %v", errorValue)
	}
	if len(evidence.Calls) != 2 {
		t.Fatalf("expected two ordered virtual calls without duplicates, got %+v", evidence.Calls)
	}
	if evidence.Calls[0].SchemaName != "blueclaw_agent_turn_action" || evidence.Calls[0].FinishReason != "tool_calls" {
		t.Fatalf("expected action call first with metadata, got %+v", evidence.Calls)
	}
	if evidence.Calls[1].SchemaName != "" || evidence.Calls[1].FinishReason != "stop" {
		t.Fatalf("expected plain chat second without schema, got %+v", evidence.Calls)
	}
	if strings.Contains(string(document), "duplicate-should-not-appear") {
		t.Fatalf("persisted task event was incorrectly duplicated: %s", document)
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

func TestParseVirtualSessionArgumentsAcceptsStrictScenarioFile(t *testing.T) {
	arguments, errorValue := parseVirtualSessionArguments([]string{
		"--scenario-file", "/repo/tests/expensive/task.json",
		"--strict-assertions",
		"--validate-only",
		"--maximum-model-tier", "xlow",
	}, "presentation", t.TempDir())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if arguments.ScenarioFilePath != "/repo/tests/expensive/task.json" || !arguments.StrictAssertions || !arguments.ValidateOnly || arguments.MaximumModelTier != "xlow" {
		t.Fatalf("unexpected scenario file arguments: %+v", arguments)
	}
}

func TestConfigureVirtualScenarioCodingUsesCeilingOnlyWhenConfigured(t *testing.T) {
	providerFactory := func(modelName string) llm.LanguageModelProvider {
		return llm.OpenRouterClient{ModelName: modelName}
	}

	uncappedScenario := e2e.VirtualSessionScenario{}
	configureVirtualScenarioModelTiers(&uncappedScenario, "", providerFactory)
	uncappedModelNames := virtualProviderModelNames(uncappedScenario.CodingLanguageModel)
	if !slices.Contains(uncappedModelNames, "z-ai/glm-5.2") {
		t.Fatalf("expected uncapped coding provider to use coding model, got %v", uncappedModelNames)
	}

	cappedScenario := e2e.VirtualSessionScenario{}
	configureVirtualScenarioModelTiers(&cappedScenario, "high", providerFactory)
	cappedModelNames := virtualProviderModelNames(cappedScenario.CodingLanguageModel)
	if !slices.Contains(cappedModelNames, "google/gemini-3-flash-preview") {
		t.Fatalf("expected coding provider to use high ceiling, got %v", cappedModelNames)
	}
	for _, forbiddenModelName := range []string{"z-ai/glm-5.2", "openai/gpt-5.6-luna", "google/gemini-3.5-flash"} {
		if slices.Contains(cappedModelNames, forbiddenModelName) {
			t.Fatalf("expected coding provider to stay at or below high, got %v", cappedModelNames)
		}
	}
}

type virtualTierTestProvider struct {
	modelName string
}

func (provider virtualTierTestProvider) GenerateResponse(context.Context, string) (string, error) {
	return "reply", nil
}

func (provider virtualTierTestProvider) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	content := openRouterContentForSchema(request.StructuredOutputSchema.Name)
	if request.StructuredOutputSchema.Name == "blueclaw_turn_router" {
		content = `{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"xhigh","estimatedMinutes":60,"requestedOutputFormats":null,"requiredEvidence":[],"initialToolNames":[],"responseLanguage":"ko","reason":"xhigh integration test","userFacingReply":"","priorTaskReference":"none"}`
	}
	return llm.StructuredResponse{ModelName: provider.modelName, Content: content}, nil
}

func TestConfigureVirtualScenarioCappedProviderReportsCeilingTier(t *testing.T) {
	providerFactory := func(modelName string) llm.LanguageModelProvider {
		return virtualTierTestProvider{modelName: modelName}
	}
	scenario := e2e.VirtualSessionScenario{}
	configureVirtualScenarioModelTiers(&scenario, "low", providerFactory)
	response, errorValue := scenario.HighLanguageModel.GenerateStructuredResponse(context.Background(), llm.StructuredResponseRequest{})
	if errorValue != nil {
		t.Fatalf("expected capped high provider to succeed: %v", errorValue)
	}
	if response.ModelTier != "low" {
		t.Fatalf("expected capped high provider to report low tier, got %+v", response)
	}
}

func TestVirtualModelCeilingDoesNotReduceTaskWorkDuration(t *testing.T) {
	providerFactory := func(modelName string) llm.LanguageModelProvider {
		return virtualTierTestProvider{modelName: modelName}
	}
	scenario := e2e.VirtualSessionScenario{
		Name:                     "xhigh_task_with_low_model_ceiling",
		ArtifactDirectoryPath:    t.TempDir(),
		DisableScriptedModel:     true,
		UseLooseAssertions:       true,
		FailOnLanguageModelError: true,
		Turns: []e2e.VirtualTurn{{
			Prompt:             "팀 운영 개선안을 검토하고 핵심 결론을 알려줘",
			ExpectedTaskStatus: task.TaskStatusCompleted,
		}},
	}
	configureVirtualScenarioModelTiers(&scenario, "low", providerFactory)

	result, errorValue := e2e.RunVirtualSession(context.Background(), scenario)
	if errorValue != nil {
		t.Fatalf("expected capped xhigh virtual session to succeed: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one virtual turn, got %+v", result.TurnResults)
	}
	intakeTaskLevel := virtualTurnIntakeTaskLevel(t, result.TurnResults[0])
	actionModelTier := virtualTurnActionModelTier(t, result.TurnResults[0])
	workDuration := agent.TaskLevelProfileForLevel(intakeTaskLevel).Duration

	if actionModelTier != "low" {
		t.Fatalf("expected authoritative action call to use the low ceiling, got %q", actionModelTier)
	}
	if intakeTaskLevel != agent.TaskLevelXHigh {
		t.Fatalf("expected authoritative intake task level xhigh, got %q", intakeTaskLevel)
	}
	if workDuration != time.Hour {
		t.Fatalf("expected the selected xhigh task to retain a one-hour work duration, got %s", workDuration)
	}
}

func virtualTurnIntakeTaskLevel(t *testing.T, turnResult e2e.VirtualTurnResult) agent.TaskLevel {
	t.Helper()
	for _, event := range turnResult.Events {
		if event.Name != "agent.intake" {
			continue
		}
		var intakeDecision struct {
			TaskLevel agent.TaskLevel `json:"level"`
		}
		if errorValue := json.Unmarshal([]byte(event.Body), &intakeDecision); errorValue != nil {
			t.Fatalf("expected valid agent.intake event: %v", errorValue)
		}
		return intakeDecision.TaskLevel
	}
	t.Fatalf("expected agent.intake event, got %+v", turnResult.Events)
	return ""
}

func virtualTurnActionModelTier(t *testing.T, turnResult e2e.VirtualTurnResult) string {
	t.Helper()
	for _, event := range turnResult.Events {
		if event.Name != "llm.call" {
			continue
		}
		var languageModelCall struct {
			SchemaName string `json:"schemaName"`
			ModelTier  string `json:"modelTier"`
		}
		if errorValue := json.Unmarshal([]byte(event.Body), &languageModelCall); errorValue != nil {
			t.Fatalf("expected valid llm.call event: %v", errorValue)
		}
		if languageModelCall.SchemaName == "blueclaw_agent_turn_action" {
			return languageModelCall.ModelTier
		}
	}
	t.Fatalf("expected authoritative action llm.call event, got %+v", turnResult.Events)
	return ""
}

func virtualProviderModelNames(provider llm.LanguageModelProvider) []string {
	modelNames := map[string]bool{}
	var collect func(llm.LanguageModelProvider)
	collect = func(currentProvider llm.LanguageModelProvider) {
		if tieredProvider, isTieredProvider := currentProvider.(interface {
			UnderlyingProvider() llm.LanguageModelProvider
		}); isTieredProvider {
			collect(tieredProvider.UnderlyingProvider())
			return
		}
		switch typedProvider := currentProvider.(type) {
		case llm.OpenRouterClient:
			modelNames[typedProvider.ModelName] = true
		case llm.FallbackLanguageModelProvider:
			collect(typedProvider.PrimaryProvider)
			collect(typedProvider.FallbackProvider)
		case llm.VisionFallbackProvider:
			collect(typedProvider.TextOnlyModel)
			collect(typedProvider.VisionModel)
		}
	}
	collect(provider)
	result := make([]string, 0, len(modelNames))
	for modelName := range modelNames {
		result = append(result, modelName)
	}
	sort.Strings(result)
	return result
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
