package e2e

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/agenttest"
	"blueclaw/internal/capability"
	"blueclaw/internal/llm"
	"blueclaw/internal/task"
)

func TestPresentationScenarioDoesNotScriptToolCalls(t *testing.T) {
	scenario := PresentationLocalMultiturnSuccessScenario(t.TempDir())
	if len(scenario.Turns) != 1 {
		t.Fatalf("expected one slides turn, got %d", len(scenario.Turns))
	}
	if len(scenario.Turns[0].ActionResponses) != 0 {
		t.Fatal("slides scenario must not script model tool calls or artifact creation")
	}
}

func TestExpectedEventCountAllowsRepeatedReadResults(t *testing.T) {
	virtualTurn := VirtualTurn{
		ExpectedEventCounts: []VirtualEventCount{{
			Name:         "tool.capability.invoke.result",
			BodyFragment: "customer task",
			Count:        1,
		}},
	}
	turnResult := VirtualTurnResult{
		FinishMessage: "found",
		Events: []task.TaskEvent{
			{Name: "tool.capability.invoke.result", Body: `{"title":"customer task"}`},
			{Name: "tool.capability.invoke.result", Body: `{"title":"customer task"}`},
		},
	}
	if errorValue := assertTurnResult(t.TempDir(), virtualTurn, turnResult); errorValue != nil {
		t.Fatalf("expected repeated matching events to satisfy the result assertion: %v", errorValue)
	}
	assertions := informationalAssertionResults(virtualTurn, turnResult)
	if len(assertions) != 1 || assertions[0].Satisfied {
		t.Fatalf("expected the duplicate read to remain an informational efficiency mismatch: %+v", assertions)
	}
}

func TestLanguageModelCallAssertionRejectsError(t *testing.T) {
	errorValue := assertLanguageModelCallsSucceeded([]VirtualLanguageModelCallEvent{{
		Kind:       "structured",
		SchemaName: "blueclaw_turn_router",
		IsError:    true,
		Error:      "truncated",
	}})

	if errorValue == nil || !strings.Contains(errorValue.Error(), "blueclaw_turn_router") {
		t.Fatalf("expected strict assertion to reject the model error, got %v", errorValue)
	}
}

func TestVirtualObservedLanguageModelPreservesChatCapabilityAndMetadata(t *testing.T) {
	observed := newVirtualObservedLanguageModel(virtualChatTestProvider{})
	if _, isDirectChat := observed.(llm.ChatCompleter); isDirectChat {
		t.Fatal("expected virtual observer to expose ChatCompleter only through the optional accessor")
	}
	chatCompleter, isAvailable := llm.ResolveTextChatCompleter(observed)
	if !isAvailable {
		t.Fatal("expected virtual observer chat capability")
	}
	response, errorValue := chatCompleter.GenerateChatCompletion(context.Background(), llm.ChatCompletionRequest{
		Messages: []llm.ChatCompletionMessage{{Role: "user", Content: "reply"}},
	})
	if errorValue != nil || response.Message.Content != "virtual reply" {
		t.Fatalf("expected virtual chat response, got %+v %v", response, errorValue)
	}
	recorder, isRecorder := observed.(virtualLanguageModelCallRecorder)
	if !isRecorder {
		t.Fatal("expected virtual observer call recorder")
	}
	calls := recorder.CallsSince(0)
	if len(calls) != 1 || calls[0].Kind != "chat" || calls[0].Provider != "virtual-provider" || calls[0].Model != "virtual-model" || calls[0].SelectedBackend != "device" || calls[0].FinishReason != "stop" || !calls[0].UsedFallback {
		t.Fatalf("expected exact virtual chat metadata, got %+v", calls)
	}
}

func TestVirtualChatCallEventDerivesActionSchemaForForcedChatOnly(t *testing.T) {
	actionEvent := virtualChatCallEvent("chat", virtualActionChatRequest(), llm.ChatCompletionResponse{
		ProviderName:    "sdkd",
		ModelName:       "low-model",
		SelectedBackend: "device",
		FinishReason:    "tool_calls",
		UsedFallback:    true,
	}, time.Now(), nil)
	plainEvent := virtualChatCallEvent("chat", llm.ChatCompletionRequest{}, llm.ChatCompletionResponse{
		ProviderName:    "sdkd",
		ModelName:       "low-model",
		SelectedBackend: "device",
		FinishReason:    "stop",
	}, time.Now(), nil)
	if actionEvent.SchemaName != "blueclaw_agent_turn_action" || plainEvent.SchemaName != "" {
		t.Fatalf("expected only forced action chat to carry schema, got %+v %+v", actionEvent, plainEvent)
	}
}

func virtualActionChatRequest() llm.ChatCompletionRequest {
	return llm.ChatCompletionRequest{
		Tools: []llm.ChatCompletionTool{{
			Type:     "function",
			Function: llm.ChatCompletionFunction{Name: "blueclaw_agent_turn_action"},
		}},
		ToolChoice: json.RawMessage(`{"type":"function","function":{"name":"blueclaw_agent_turn_action"}}`),
	}
}

func TestVirtualObservedLanguageModelResolvesNestedChatAccessors(t *testing.T) {
	inner := newVirtualObservedLanguageModel(virtualChatTestProvider{})
	outer := newVirtualObservedLanguageModel(inner)
	if _, isDirectChat := outer.(llm.ChatCompleter); isDirectChat {
		t.Fatal("expected nested virtual observer to expose ChatCompleter only through the optional accessor")
	}
	chatCompleter, isAvailable := llm.ResolveTextChatCompleter(outer)
	if !isAvailable {
		t.Fatal("expected nested virtual observer chat capability")
	}
	response, errorValue := chatCompleter.GenerateChatCompletion(context.Background(), llm.ChatCompletionRequest{})
	if errorValue != nil || response.Message.Content != "virtual reply" {
		t.Fatalf("expected nested virtual chat response, got %+v %v", response, errorValue)
	}
	for name, provider := range map[string]llm.LanguageModelProvider{"outer": outer, "inner": inner} {
		recorder, isRecorder := provider.(virtualLanguageModelCallRecorder)
		if !isRecorder {
			t.Fatalf("expected %s virtual observer call recorder", name)
		}
		calls := recorder.CallsSince(0)
		if len(calls) != 1 || calls[0].Kind != "chat" || calls[0].Provider != "virtual-provider" || calls[0].Model != "virtual-model" || calls[0].SelectedBackend != "device" || calls[0].FinishReason != "stop" || !calls[0].UsedFallback {
			t.Fatalf("expected exact %s nested virtual chat metadata, got %+v", name, calls)
		}
	}
}

func TestVirtualObservedLanguageModelDoesNotInventChatCapability(t *testing.T) {
	observed := newVirtualObservedLanguageModel(virtualPlainTestProvider{})
	if _, isAvailable := llm.ResolveTextChatCompleter(observed); isAvailable {
		t.Fatal("expected virtual observer without ChatCompleter to remain unavailable")
	}
	if _, isAvailable := llm.ResolveRecoveryChatCompleter(observed); isAvailable {
		t.Fatal("expected virtual observer without RecoveryChatCompleter to remain unavailable")
	}
	if _, isAvailable := llm.ResolveLocalRecoveryChatCompleter(observed); isAvailable {
		t.Fatal("expected virtual observer without LocalRecoveryChatCompleter to remain unavailable")
	}
}

func TestVirtualObservedLanguageModelPreservesNestedRecoveryChatCapabilities(t *testing.T) {
	inner := newVirtualObservedLanguageModel(virtualChatTestProvider{})
	outer := newVirtualObservedLanguageModel(inner)

	recoveryProvider, hasRecoveryChat := llm.ResolveRecoveryChatCompleter(outer)
	if !hasRecoveryChat {
		t.Fatal("expected nested virtual recovery chat capability")
	}
	localRecoveryProvider, hasLocalRecoveryChat := llm.ResolveLocalRecoveryChatCompleter(outer)
	if !hasLocalRecoveryChat {
		t.Fatal("expected nested virtual local recovery chat capability")
	}
	request := llm.ChatCompletionRequest{Messages: []llm.ChatCompletionMessage{{Role: "user", Content: "failure"}}}
	response, errorValue := recoveryProvider.GenerateRecoveryChatCompletion(context.Background(), request)
	if errorValue != nil || response.Message.Content != "virtual recovery reply" {
		t.Fatalf("expected nested virtual recovery response, got %+v %v", response, errorValue)
	}
	response, errorValue = localRecoveryProvider.GenerateLocalRecoveryChatCompletion(context.Background(), request)
	if errorValue != nil || response.Message.Content != "virtual local recovery reply" {
		t.Fatalf("expected nested virtual local recovery response, got %+v %v", response, errorValue)
	}
	assertVirtualRecoveryChatCall(t, inner, "inner")
	assertVirtualRecoveryChatCall(t, outer, "outer")
}

func assertVirtualRecoveryChatCall(t *testing.T, provider llm.LanguageModelProvider, label string) {
	t.Helper()
	recorder, isRecorder := provider.(virtualLanguageModelCallRecorder)
	if !isRecorder {
		t.Fatalf("expected %s virtual observer call recorder", label)
	}
	calls := recorder.CallsSince(0)
	if len(calls) != 2 {
		t.Fatalf("expected two %s recovery calls, got %+v", label, calls)
	}
	for index, call := range calls {
		expectedKind := []string{"recovery_chat", "local_recovery_chat"}[index]
		if call.Kind != expectedKind {
			t.Fatalf("expected %s recovery kind %q, got %+v", label, expectedKind, call)
		}
		if call.Provider != "virtual-recovery-provider" || call.Model != "virtual-recovery-model" || call.SelectedBackend != "remote" || call.FinishReason != "stop" || call.UsedFallback {
			t.Fatalf("expected %s recovery routing metadata, got %+v", label, call)
		}
	}
}

func TestVirtualObservedLanguageModelRecordsChatErrors(t *testing.T) {
	observed := newVirtualObservedLanguageModel(virtualChatErrorTestProvider{})
	chatCompleter, isAvailable := llm.ResolveTextChatCompleter(observed)
	if !isAvailable {
		t.Fatal("expected virtual observer chat capability")
	}
	_, errorValue := chatCompleter.GenerateChatCompletion(context.Background(), llm.ChatCompletionRequest{})
	if errorValue == nil {
		t.Fatal("expected virtual chat error")
	}
	recorder, isRecorder := observed.(virtualLanguageModelCallRecorder)
	if !isRecorder {
		t.Fatal("expected virtual observer call recorder")
	}
	calls := recorder.CallsSince(0)
	if len(calls) != 1 || !calls[0].IsError || calls[0].Error != "virtual chat failed" || calls[0].Provider != "virtual-provider" || calls[0].Model != "virtual-model" || calls[0].SelectedBackend != "remote" || calls[0].FinishReason != "error" || !calls[0].UsedFallback {
		t.Fatalf("expected exact virtual chat error metadata, got %+v", calls)
	}
}

type virtualPlainTestProvider struct{}

func (virtualPlainTestProvider) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (virtualPlainTestProvider) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, nil
}

type virtualChatTestProvider struct{}

func (virtualChatTestProvider) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (virtualChatTestProvider) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, nil
}

func (virtualChatTestProvider) GenerateChatCompletion(context.Context, llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	return llm.ChatCompletionResponse{
		FinishReason:    "stop",
		ProviderName:    "virtual-provider",
		ModelName:       "virtual-model",
		SelectedBackend: "device",
		UsedFallback:    true,
		Message:         llm.ChatCompletionMessage{Role: "assistant", Content: "virtual reply"},
	}, nil
}

func (virtualChatTestProvider) GenerateRecoveryChatCompletion(context.Context, llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	return llm.ChatCompletionResponse{
		FinishReason:    "stop",
		ProviderName:    "virtual-recovery-provider",
		ModelName:       "virtual-recovery-model",
		SelectedBackend: "remote",
		Message:         llm.ChatCompletionMessage{Role: "assistant", Content: "virtual recovery reply"},
	}, nil
}

func (virtualChatTestProvider) GenerateLocalRecoveryChatCompletion(context.Context, llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	return llm.ChatCompletionResponse{
		FinishReason:    "stop",
		ProviderName:    "virtual-recovery-provider",
		ModelName:       "virtual-recovery-model",
		SelectedBackend: "remote",
		Message:         llm.ChatCompletionMessage{Role: "assistant", Content: "virtual local recovery reply"},
	}, nil
}

type virtualChatErrorTestProvider struct{}

func (virtualChatErrorTestProvider) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (virtualChatErrorTestProvider) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{}, nil
}

func (virtualChatErrorTestProvider) GenerateChatCompletion(context.Context, llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	return llm.ChatCompletionResponse{
		FinishReason:    "error",
		ProviderName:    "virtual-provider",
		ModelName:       "virtual-model",
		SelectedBackend: "remote",
		UsedFallback:    true,
	}, errors.New("virtual chat failed")
}

func TestPresentationLocalMultiturnSuccessLive(t *testing.T) {
	if !truthyEnvironmentValue(os.Getenv("BLUECLAW_E2E_LIVE")) {
		t.Skip("set BLUECLAW_E2E_LIVE=1 to explicitly run costed live slides virtual session")
	}
	endpoint := strings.TrimSpace(os.Getenv("BLUECLAW_E2E_LLM_ENDPOINT"))
	socketPath := strings.TrimSpace(os.Getenv("BLUECLAW_E2E_LLM_UNIX_SOCKET"))
	if endpoint == "" && socketPath == "" {
		t.Skip("set BLUECLAW_E2E_LLM_ENDPOINT or BLUECLAW_E2E_LLM_UNIX_SOCKET to run live slides virtual session")
	}
	scenario := PresentationLocalMultiturnSuccessScenario(t.TempDir())
	if skillDirectoryPath := rootPresentationSkillPath(); skillDirectoryPath != "" {
		scenario.Skills = nil
		scenario.SkillDirectoryPaths = []string{skillDirectoryPath}
	}
	scenario.LanguageModel = llm.CapabilityLLMClient{
		CapabilityClient: capability.NewClient(capability.Configuration{
			Endpoint:       endpoint,
			UnixSocketPath: socketPath,
		}),
		ModelName:     os.Getenv("BLUECLAW_E2E_LLM_MODEL"),
		ExecutionMode: firstNonEmptyTestString(os.Getenv("BLUECLAW_E2E_LLM_EXECUTION_MODE"), "auto"),
	}

	result, errorValue := RunVirtualSession(context.Background(), scenario)
	if errorValue != nil {
		t.Fatalf("expected slides scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn result, got %d", len(result.TurnResults))
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "tool.terminal.run.result", "exitCode") {
		t.Fatal("expected terminal build to succeed")
	}
}

func rootPresentationSkillPath() string {
	candidatePath := filepath.Clean("../../../../assets/blueclaw-workspace/skills/presentation")
	if _, errorValue := os.Stat(candidatePath); errorValue == nil {
		return candidatePath
	}
	return ""
}

func TestMemoryGuidedFollowup(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), MemoryGuidedFollowupScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected memory scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turn results, got %d", len(result.TurnResults))
	}
	secondTurn := result.TurnResults[1]
	if !eventsContain(secondTurn.Events, "agent.task_launched", `"memoryFactCount":`) {
		t.Fatal("expected task launch memory fact count")
	}
	if strings.Contains(secondTurn.FinishMessage, "아까") {
		t.Fatalf("expected concrete recalled preference, got %q", secondTurn.FinishMessage)
	}
}

func TestPlainQuestionAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), PlainQuestionAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected plain question acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn result, got %d", len(result.TurnResults))
	}
	turnResult := result.TurnResults[0]
	if strings.TrimSpace(turnResult.FinishMessage) == "" {
		t.Fatal("expected non-empty final reply")
	}
	if toolEventCount(turnResult.Events) != 0 {
		t.Fatalf("expected no tool events, got events: %s", summarizeEvents(turnResult.Events))
	}
	if failureEventCount(turnResult.Events) != 0 {
		t.Fatalf("expected no failure events, got events: %s", summarizeEvents(turnResult.Events))
	}
}

func TestFailedAssertionReturnsObservedTurnResult(t *testing.T) {
	scenario := PlainQuestionAcceptanceScenario(t.TempDir())
	scenario.Turns[0].ExpectedTaskStatus = task.TaskStatusFailed

	result, errorValue := RunVirtualSession(context.Background(), scenario)

	if errorValue == nil {
		t.Fatal("expected task status assertion to fail")
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected failing turn result to remain observable, got %d", len(result.TurnResults))
	}
	if result.TurnResults[0].TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected observed completed status, got %q", result.TurnResults[0].TaskStatus)
	}
}

func TestVirtualTaskCapabilityPreservesLifecycleState(t *testing.T) {
	service := virtualCapabilityService{}
	addResponse := service.response("task.add", []byte(`{"input":{"prompt":"비용 테스트 회귀 확인"},"context":{}}`))
	updateResponse := service.response("task.update", []byte(`{"input":{"query":"비용 테스트 회귀 확인","title":"비용 테스트 회귀 확인 완료 준비"},"context":{}}`))
	listResponse := service.response("task.list", []byte(`{"input":{},"context":{}}`))
	approvalResponse := service.response("task.delete", []byte(`{"input":{"query":"비용 테스트 회귀 확인 완료 준비"},"context":{}}`))
	deleteResponse := service.response("task.delete", []byte(`{"input":{"query":"비용 테스트 회귀 확인 완료 준비"},"context":{"isApprovalContinuation":true}}`))
	emptyListResponse := service.response("task.list", []byte(`{"input":{},"context":{}}`))

	if !strings.Contains(addResponse, `"taskID":"task-1"`) {
		t.Fatalf("expected created task identity, got %s", addResponse)
	}
	if !strings.Contains(updateResponse, `"content":"비용 테스트 회귀 확인 완료 준비"`) {
		t.Fatalf("expected updated title, got %s", updateResponse)
	}
	if !strings.Contains(listResponse, `"content":"비용 테스트 회귀 확인 완료 준비"`) {
		t.Fatalf("expected list to return updated task, got %s", listResponse)
	}
	if !strings.Contains(approvalResponse, `"errorCode":"approval_required"`) {
		t.Fatalf("expected delete approval gate, got %s", approvalResponse)
	}
	if !strings.Contains(deleteResponse, `"status":"deleted"`) {
		t.Fatalf("expected approved delete, got %s", deleteResponse)
	}
	if strings.Contains(emptyListResponse, `"taskID":"task-1"`) {
		t.Fatalf("expected deleted task to be absent, got %s", emptyListResponse)
	}
}

func TestDOCXAttachmentValidationRejectsTextAndAcceptsCanonicalPackage(t *testing.T) {
	invalidPath := filepath.Join(t.TempDir(), "document.docx")
	if errorValue := os.WriteFile(invalidPath, []byte("plain text renamed as docx"), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := validateDOCXAttachment(invalidPath, agent.FileAttachment{DevicePath: invalidPath}); errorValue == nil {
		t.Fatal("expected renamed text file to fail docx validation")
	}

	placeholderPath := filepath.Join(t.TempDir(), "placeholder.docx")
	writeDOCX(t, placeholderPath, map[string]string{
		"[Content_Types].xml":          "<xml/>",
		"word/document.xml":            "<xml/>",
		"word/_rels/document.xml.rels": "<xml/>",
	})
	if errorValue := validateDOCXAttachment(placeholderPath, agent.FileAttachment{DevicePath: placeholderPath}); errorValue == nil {
		t.Fatal("expected placeholder XML to fail docx validation")
	}

	validPath := filepath.Join(t.TempDir(), "document.docx")
	writeCanonicalDOCX(t, validPath)
	if errorValue := validateDOCXAttachment(validPath, agent.FileAttachment{DevicePath: validPath}); errorValue != nil {
		t.Fatalf("expected canonical docx package to pass validation: %v", errorValue)
	}
}

func TestVirtualSitePublishRequiresValidSourceContent(t *testing.T) {
	workspacePath := t.TempDir()
	service := virtualCapabilityService{
		workspacePath: workspacePath,
		site: &virtualCapabilityRecord{
			ID:                  "site-1",
			Values:              map[string]any{"slug": "demo"},
			SourceWorkspacePath: "/workspace/circles/staff/sites/demo/draft",
		},
	}
	requestBody := []byte(`{"input":{"siteID":"site-1"}}`)

	response := service.response("site.publish", requestBody)
	if !strings.Contains(response, `"status":"error"`) || service.sitePublished {
		t.Fatalf("expected publish without source to fail closed, got %s", response)
	}

	sourcePath := filepath.Join(workspacePath, "circles", "staff", "sites", "demo", "draft", "app", "public")
	if errorValue := os.MkdirAll(sourcePath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	contentPath := filepath.Join(sourcePath, "site-content.json")
	if errorValue := os.WriteFile(contentPath, []byte("not json"), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	response = service.response("site.publish", requestBody)
	if !strings.Contains(response, `"status":"error"`) || service.sitePublished {
		t.Fatalf("expected invalid source content to fail closed, got %s", response)
	}

	if errorValue := os.WriteFile(contentPath, []byte(`{"siteName":"Virtual Site"}`), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	response = service.response("site.publish", requestBody)
	if !strings.Contains(response, `"status":"ok"`) || !strings.Contains(response, `"sourceSHA256"`) {
		t.Fatalf("expected valid source content to publish with metadata, got %s", response)
	}
	if strings.Contains(response, workspacePath) || !strings.Contains(response, "/workspace/circles/staff/sites/demo/draft/app/public/site-content.json") {
		t.Fatalf("expected virtual source metadata without host path, got %s", response)
	}

	statusResponse := service.response("site.status", requestBody)
	if !strings.Contains(statusResponse, `"workspacePath":"/workspace/circles/staff/sites/demo"`) {
		t.Fatalf("expected site status workspace root, got %s", statusResponse)
	}
}

func writeCanonicalDOCX(t *testing.T, path string) {
	writeDOCX(t, path, map[string]string{
		"[Content_Types].xml":          `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"word/document.xml":            `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Test</w:t></w:r></w:p></w:body></w:document>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="document.xml"/></Relationships>`,
	})
}

func writeDOCX(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	file, errorValue := os.Create(path)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	archive := zip.NewWriter(file)
	for name, content := range entries {
		entry, entryError := archive.Create(name)
		if entryError != nil {
			_ = file.Close()
			t.Fatal(entryError)
		}
		if _, entryError = entry.Write([]byte(content)); entryError != nil {
			_ = file.Close()
			t.Fatal(entryError)
		}
	}
	if errorValue := archive.Close(); errorValue != nil {
		_ = file.Close()
		t.Fatal(errorValue)
	}
	if errorValue := file.Close(); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func TestVirtualCapabilityCatalogUsesOperationSchemas(t *testing.T) {
	catalog := virtualCapabilityCatalogResponse(map[string]bool{"task.add": true, "calendar.update": true})

	for _, expectedText := range []string{`"prompt"`, `"eventID"`, `"query"`, `"startISO"`, `"endISO"`} {
		if !strings.Contains(catalog, expectedText) {
			t.Fatalf("expected catalog schema field %s, got %s", expectedText, catalog)
		}
	}
}

func TestLanguageModelCassetteRoundTripVirtualSession(t *testing.T) {
	recordingLanguageModel := NewRecordingLanguageModel(agenttest.NewActionScriptedLanguageModel(
		actionFinishMessage("cassette reply sequence"),
	))
	recordedScenario := cassetteRoundTripScenario(t.TempDir(), recordingLanguageModel)
	recordedResult, errorValue := RunVirtualSession(context.Background(), recordedScenario)
	if errorValue != nil {
		t.Fatalf("expected recording session to pass: %v", errorValue)
	}

	cassettePath := filepath.Join(t.TempDir(), "model-cassette.json")
	if errorValue := SaveLanguageModelCassette(cassettePath, recordingLanguageModel.Cassette()); errorValue != nil {
		t.Fatalf("expected cassette save to pass: %v", errorValue)
	}
	cassette, errorValue := LoadLanguageModelCassette(cassettePath)
	if errorValue != nil {
		t.Fatalf("expected cassette load to pass: %v", errorValue)
	}

	replayingLanguageModel := NewReplayingLanguageModel(cassette)
	replayedScenario := cassetteRoundTripScenario(t.TempDir(), replayingLanguageModel)
	replayedResult, errorValue := RunVirtualSession(context.Background(), replayedScenario)
	if errorValue != nil {
		t.Fatalf("expected replay session to pass: %v", errorValue)
	}

	recordedReply := recordedResult.TurnResults[0].FinishMessage
	replayedReply := replayedResult.TurnResults[0].FinishMessage
	if recordedReply != replayedReply {
		t.Fatalf("expected replayed reply %q to match recorded reply %q", replayedReply, recordedReply)
	}
	if !reflect.DeepEqual(cassette.Entries, replayingLanguageModel.ReturnedEntries()) {
		t.Fatalf("expected replayed model response sequence to match cassette")
	}
}

func cassetteRoundTripScenario(artifactDirectoryPath string, languageModel llm.LanguageModelProvider) VirtualSessionScenario {
	return VirtualSessionScenario{
		Name:                  "cassette_round_trip",
		ArtifactDirectoryPath: artifactDirectoryPath,
		LanguageModel:         languageModel,
		Turns: []VirtualTurn{{
			Prompt:                 "도구 없이 짧게 답해줘.",
			ExpectedReplyFragments: []string{"cassette reply sequence"},
		}},
	}
}

func TestWebSearchAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), WebSearchAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected web search acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn result, got %d", len(result.TurnResults))
	}
	turnResult := result.TurnResults[0]
	if countEvents(turnResult.Events, "tool.web.search.requested") != 1 {
		t.Fatalf("expected one web.search request, got events: %s", summarizeEvents(turnResult.Events))
	}
	if !strings.Contains(turnResult.FinishMessage, "BlueclawSearchStubToken") {
		t.Fatalf("expected final reply to contain search stub token, got %q", turnResult.FinishMessage)
	}
}

func TestToolPermissionScenarioReturnsPlannedFallback(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), ToolPermissionHidesSkillScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected permission scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if !strings.Contains(turnResult.FinishMessage, "필요한 도구") {
		t.Fatalf("expected planned fallback reply, got %q", turnResult.FinishMessage)
	}
}

func TestFileWriteLegacyModeAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), FileWriteLegacyModeAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected legacy mode scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if turnResult.TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected completed turn, got %s", turnResult.TaskStatus)
	}
	if countEvents(turnResult.Events, "tool.file.write.requested") != 1 {
		t.Fatalf("expected one file.write request, got events: %s", summarizeEvents(turnResult.Events))
	}
	if countEvents(turnResult.Events, "tool.terminal.run.requested") != 1 {
		t.Fatalf("expected one terminal.run request, got events: %s", summarizeEvents(turnResult.Events))
	}
	if eventsContain(turnResult.Events, "tool.terminal.run.result", "permission denied") {
		t.Fatalf("terminal.run must not hit permission denied; events: %s", summarizeEvents(turnResult.Events))
	}
}

func TestAmbientTaskCaptureAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AmbientTaskCaptureAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected ambient task capture scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "agent.ambient_duty_launch", `"dutyName":"team_flow_update"`) {
		t.Fatalf("expected ambient duty launch for an other-person-mentioned task assignment; events: %s", summarizeEvents(turnResult.Events))
	}
	if eventsContain(turnResult.Events, "tool.terminal.run.requested", "") {
		t.Fatalf("ambient capture must not reach terminal.run; events: %s", summarizeEvents(turnResult.Events))
	}
	reviseResult := result.TurnResults[1]
	if !requestedToolCallPresent(reviseResult.Events, "task.update") {
		t.Fatalf("expected a same-thread follow-up to update the existing task; events: %s", summarizeEvents(reviseResult.Events))
	}
	if countRequestedToolCalls(reviseResult.Events, "task.add") > 0 {
		t.Fatalf("same-thread revision must update, not add a duplicate task; events: %s", summarizeEvents(reviseResult.Events))
	}
	if turnResult.DidReply || reviseResult.DidReply {
		t.Fatalf("ambient task capture must stay silent, got first=%q second=%q", turnResult.FinishMessage, reviseResult.FinishMessage)
	}
}

func TestVirtualSessionAcceptsReactionOnlyTurn(t *testing.T) {
	scenario := VirtualSessionScenario{
		Name:                  "reaction-only",
		ArtifactDirectoryPath: t.TempDir(),
		AddressingResponse:    `{"target":"anyone","shouldRespond":false,"reactionEmoji":"eyes","dutyMatch":false,"dutyName":"","dutyConfidence":0}`,
		Turns: []VirtualTurn{{
			Prompt:           "참고로 공유합니다.",
			ExpectedResponse: VirtualResponseReact,
			ConversationType: "channel",
			ActionResponses:  []string{actionFinishMessage("unused")},
		}},
	}

	result, errorValue := RunVirtualSession(context.Background(), scenario)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(result.TurnResults[0].Reactions) != 1 || result.TurnResults[0].Reactions[0].EmojiName != "eyes" {
		t.Fatalf("expected eyes reaction, got %+v", result.TurnResults[0].Reactions)
	}
}

func TestGWSDisabled(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), GWSDisabledScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected gws disabled scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "tool.google.drive.import_pptx.requested", "google.drive.import_pptx") {
		t.Fatal("expected attempted google tool request to be audited")
	}
	if !eventsContain(turnResult.Events, "tool.google.drive.import_pptx.result", "tool is not allowed") {
		t.Fatal("expected google tool to be denied by catalog allowlist")
	}
}

func TestScheduleCreateAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), ScheduleCreateAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected schedule acceptance scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "tool.capability.invoke.requested", "schedule.create") ||
		!eventsContain(turnResult.Events, "tool.capability.invoke.result", "intervalSecond") {
		t.Fatalf("expected capability schedule create; events: %s", summarizeEvents(turnResult.Events))
	}
	if !strings.Contains(turnResult.ModelContext, "schedule.create") {
		t.Fatal("expected model context to document schedule.create capability")
	}
}

func TestScheduleLifecycleAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), ScheduleLifecycleAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected schedule lifecycle acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 3 {
		t.Fatalf("expected three turn results, got %d", len(result.TurnResults))
	}
	firstTurnResult := result.TurnResults[0]
	secondTurnResult := result.TurnResults[1]
	thirdTurnResult := result.TurnResults[2]
	if !eventsContain(firstTurnResult.Events, "tool.capability.invoke.requested", "schedule.create") ||
		!eventsContain(firstTurnResult.Events, "tool.capability.invoke.result", "intervalSecond") {
		t.Fatalf("expected initial interval schedule through the capability kernel; events: %s", summarizeEvents(firstTurnResult.Events))
	}
	if !eventsContain(secondTurnResult.Events, "tool.capability.invoke.requested", "schedule.update") ||
		!eventsContain(secondTurnResult.Events, "tool.capability.invoke.result", "intervalSecond") {
		t.Fatalf("expected modification through the capability kernel; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !eventsContain(thirdTurnResult.Events, "tool.capability.invoke.requested", "schedule.cancel") {
		t.Fatalf("expected deletion through the capability kernel; events: %s", summarizeEvents(thirdTurnResult.Events))
	}
	if activeScheduleCount(result.TaskSchedules) != 0 {
		t.Fatalf("expected zero active schedules, got %+v", result.TaskSchedules)
	}
}

func TestCalendarEventLifecycleAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), CalendarEventLifecycleAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected calendar event lifecycle acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 3 {
		t.Fatalf("expected three turn results, got %d", len(result.TurnResults))
	}
	firstTurnResult := result.TurnResults[0]
	secondTurnResult := result.TurnResults[1]
	thirdTurnResult := result.TurnResults[2]
	if countEventsWithFragment(firstTurnResult.Events, "tool.capability.invoke.requested", "calendar.add") != 1 {
		t.Fatalf("expected one calendar add request; events: %s", summarizeEvents(firstTurnResult.Events))
	}
	if countEventsWithFragment(secondTurnResult.Events, "tool.capability.invoke.requested", "calendar.update") != 1 {
		t.Fatalf("expected one calendar update request; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !eventsContain(secondTurnResult.Events, "tool.capability.invoke.requested", "2026-06-13T14:00:00+09:00") {
		t.Fatalf("expected updated time in calendar update input; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if countEventsWithFragment(thirdTurnResult.Events, "tool.capability.invoke.requested", "calendar.delete") != 1 {
		t.Fatalf("expected one calendar delete request; events: %s", summarizeEvents(thirdTurnResult.Events))
	}
}

func TestVirtualCalendarUpdateUsesUnchangedTitleAsTarget(t *testing.T) {
	service := virtualCapabilityService{}
	addResponse := service.calendarResponse("calendar.add", []byte(`{"input":{"title":"비용 테스트 일정","startISO":"2026-07-16T10:00:00+09:00","endISO":"2026-07-16T11:00:00+09:00"}}`))
	if !strings.Contains(addResponse, `"eventID":"event-1"`) {
		t.Fatalf("expected created event, got %s", addResponse)
	}
	updateResponse := service.calendarResponse("calendar.update", []byte(`{"input":{"title":"비용 테스트 일정","startISO":"2026-07-16T14:00:00+09:00","endISO":"2026-07-16T15:00:00+09:00"}}`))
	if !strings.Contains(updateResponse, `"status":"ok"`) || !strings.Contains(updateResponse, `T14:00:00+09:00`) {
		t.Fatalf("expected title-targeted update, got %s", updateResponse)
	}
	renameResponse := service.calendarResponse("calendar.update", []byte(`{"input":{"title":"새 일정 이름","startISO":"2026-07-16T16:00:00+09:00","endISO":"2026-07-16T17:00:00+09:00"}}`))
	if !strings.Contains(renameResponse, `"status":"error"`) || !strings.Contains(renameResponse, `not found`) {
		t.Fatalf("expected rename without an old target to fail, got %s", renameResponse)
	}
}

func TestVirtualCapabilityCatalogUsesRuntimeRegistryContract(t *testing.T) {
	var catalog struct {
		DeviceCapabilities []struct {
			Name        string          `json:"name"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"deviceCapabilities"`
	}
	document := virtualCapabilityCatalogResponse(map[string]bool{"calendar.delete": true})
	if errorValue := json.Unmarshal([]byte(document), &catalog); errorValue != nil {
		t.Fatalf("expected valid capability catalog, got %v: %s", errorValue, document)
	}
	if len(catalog.DeviceCapabilities) != 1 || catalog.DeviceCapabilities[0].Name != "calendar.delete" {
		t.Fatalf("expected runtime device capability descriptor, got %+v", catalog.DeviceCapabilities)
	}
	var schema struct {
		AdditionalProperties *bool `json:"additionalProperties"`
	}
	if errorValue := json.Unmarshal(catalog.DeviceCapabilities[0].InputSchema, &schema); errorValue != nil {
		t.Fatalf("expected calendar delete schema, got %v", errorValue)
	}
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Fatalf("expected closed calendar delete schema, got %s", catalog.DeviceCapabilities[0].InputSchema)
	}
}

func TestAmbientDutyCalendarAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AmbientDutyCalendarAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected ambient duty calendar acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn result, got %d", len(result.TurnResults))
	}
	turnResult := result.TurnResults[0]
	if countRequestedToolCalls(turnResult.Events, "calendar.add") != 1 {
		t.Fatalf("expected one calendar add request; events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "agent.ambient_duty_launch", `"dutyName":"calendar_upkeep"`) {
		t.Fatalf("expected ambient duty launch event; events: %s", summarizeEvents(turnResult.Events))
	}
	if turnResult.DidReply {
		t.Fatalf("expected ambient calendar duty to stay silent, got %q", turnResult.FinishMessage)
	}
}

func TestSkillLifecycleAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), SkillLifecycleAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected skill lifecycle acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turn results, got %d", len(result.TurnResults))
	}
	firstTurnResult := result.TurnResults[0]
	secondTurnResult := result.TurnResults[1]
	if countEvents(firstTurnResult.Events, "tool.skill.add.requested") != 1 {
		t.Fatalf("expected one skill.add request; events: %s", summarizeEvents(firstTurnResult.Events))
	}
	if countEvents(secondTurnResult.Events, "tool.skill.remove.requested") != 1 {
		t.Fatalf("expected one skill.remove request; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	skillDirectoryPath := filepath.Join(result.ArtifactDirectoryPath, "workspace", ".agents", "skills", "memo-helper")
	if _, errorValue := os.Stat(skillDirectoryPath); !os.IsNotExist(errorValue) {
		t.Fatalf("expected memo-helper skill directory to be removed, got %v", errorValue)
	}
}

func TestCapabilityQuestionAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), CapabilityQuestionAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected capability question acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn result, got %d", len(result.TurnResults))
	}
	turnResult := result.TurnResults[0]
	requestedBodies := eventBodies(turnResult.Events, "tool.skill.search.requested")
	if len(requestedBodies) != 1 {
		t.Fatalf("expected one skill.search request, got events: %s", summarizeEvents(turnResult.Events))
	}
	if strings.Contains(requestedBodies[0], "queries") || strings.Contains(requestedBodies[0], "limit") {
		t.Fatalf("expected empty skill.search input, got %s", requestedBodies[0])
	}
	if !eventsContain(turnResult.Events, "tool.skill.search.result", "presentation") {
		t.Fatalf("expected skill.search result to include presentation; events: %s", summarizeEvents(turnResult.Events))
	}
	if !strings.Contains(turnResult.FinishMessage, "presentation") {
		t.Fatalf("expected final reply to mention presentation, got %q", turnResult.FinishMessage)
	}
}

func TestTaskHistoryQuestionAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), TaskHistoryQuestionAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected task history question acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turn results, got %d", len(result.TurnResults))
	}
	secondTurnResult := result.TurnResults[1]
	if countEvents(secondTurnResult.Events, "tool.task.history.requested") != 1 {
		t.Fatalf("expected one task.history request; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !eventsContain(secondTurnResult.Events, "tool.task.history.result", "계약서 확인 요약 작업") {
		t.Fatalf("expected task.history result to include prior task prompt; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !strings.Contains(secondTurnResult.FinishMessage, "계약서 확인 요약") {
		t.Fatalf("expected final reply to mention prior task, got %q", secondTurnResult.FinishMessage)
	}
}

func TestMemoryExplicitToolAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), MemoryExplicitToolAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected memory explicit tool acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turn results, got %d", len(result.TurnResults))
	}
	firstTurnResult := result.TurnResults[0]
	secondTurnResult := result.TurnResults[1]
	if firstTurnResult.TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected first turn success, got %s", firstTurnResult.TaskStatus)
	}
	if secondTurnResult.TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected second turn success, got %s", secondTurnResult.TaskStatus)
	}
	if countEvents(firstTurnResult.Events, "tool.memory.remember.requested")+countEvents(secondTurnResult.Events, "tool.memory.remember.requested") != 1 {
		t.Fatalf("expected exactly one memory.remember request; first events: %s second events: %s", summarizeEvents(firstTurnResult.Events), summarizeEvents(secondTurnResult.Events))
	}
	if !eventsContain(firstTurnResult.Events, "tool.memory.remember.requested", "Korean") {
		t.Fatalf("expected memory.remember input to include Korean; events: %s", summarizeEvents(firstTurnResult.Events))
	}
	if countEvents(firstTurnResult.Events, "tool.memory.search.requested")+countEvents(secondTurnResult.Events, "tool.memory.search.requested") != 1 {
		t.Fatalf("expected exactly one memory.search request; first events: %s second events: %s", summarizeEvents(firstTurnResult.Events), summarizeEvents(secondTurnResult.Events))
	}
	if !strings.Contains(secondTurnResult.FinishMessage, "Korean") {
		t.Fatalf("expected final reply to mention Korean, got %q", secondTurnResult.FinishMessage)
	}
}

func TestFailureExplanationAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), FailureExplanationAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected failure explanation acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turn results, got %d", len(result.TurnResults))
	}
	firstTurnResult := result.TurnResults[0]
	secondTurnResult := result.TurnResults[1]
	if firstTurnResult.TaskStatus != task.TaskStatusFailed {
		t.Fatalf("expected first turn failure, got %s", firstTurnResult.TaskStatus)
	}
	if !strings.Contains(firstTurnResult.FailureReason, "permission denied") {
		t.Fatalf("expected failure reason to mention permission denied, got %q", firstTurnResult.FailureReason)
	}
	if secondTurnResult.TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected second turn success, got %s", secondTurnResult.TaskStatus)
	}
	if countEvents(secondTurnResult.Events, "tool.task.history.requested") == 0 {
		t.Fatalf("expected task.history request in second turn; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !eventsContain(secondTurnResult.Events, "tool.task.history.result", "failureReason") || !eventsContain(secondTurnResult.Events, "tool.task.history.result", "permission denied") {
		t.Fatalf("expected task.history result to include failure reason; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !strings.Contains(secondTurnResult.FinishMessage, "permission denied") {
		t.Fatalf("expected final reply to mention permission denied, got %q", secondTurnResult.FinishMessage)
	}
}

func TestOneTimeScheduleAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), OneTimeScheduleAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected one-time schedule acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn result, got %d", len(result.TurnResults))
	}
	turnResult := result.TurnResults[0]
	if countRequestedToolCalls(turnResult.Events, "schedule.create") != 1 {
		t.Fatalf("expected one-time schedule creation event; events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "tool.capability.invoke.result", "schedule.create") {
		t.Fatalf("expected one-time schedule capability result; events: %s", summarizeEvents(turnResult.Events))
	}
}

func TestSitePrototypeAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), SitePrototypeAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected site prototype acceptance scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "agent.instructions_loaded", "site-prototype") {
		t.Fatal("expected site-prototype skill to be selected")
	}
	if !eventsContain(turnResult.Events, "tool.capability.invoke.result", "publishedURL") {
		t.Fatalf("expected site publish result to include a public URL; events: %s", summarizeEvents(turnResult.Events))
	}
	if !strings.Contains(turnResult.ModelContext, "site.create") || !strings.Contains(turnResult.ModelContext, "site.publish") {
		t.Fatal("expected model context to document site app capabilities")
	}
}

func TestSiteEditRedeployAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), SiteEditRedeployAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected site edit redeploy acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turn results, got %d", len(result.TurnResults))
	}
	secondTurnResult := result.TurnResults[1]
	if secondTurnResult.TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected second turn success, got %s", secondTurnResult.TaskStatus)
	}
	if countEvents(secondTurnResult.Events, "tool.terminal.run.requested") != 0 {
		t.Fatalf("expected no terminal.run for a content-only edit in turn two; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if countEventsWithFragment(secondTurnResult.Events, "tool.file.write.requested", "site-content.json") == 0 {
		t.Fatalf("expected a content-only site-content.json edit in turn two; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if countEventsWithFragment(secondTurnResult.Events, "tool.capability.invoke.requested", "site.publish") == 0 {
		t.Fatalf("expected site.publish capability invocation in turn two; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !strings.Contains(secondTurnResult.FinishMessage, "https://") {
		t.Fatalf("expected final assistant message to contain a URL, got %q", secondTurnResult.FinishMessage)
	}
}

func TestSiteCustomStructureAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), SiteCustomStructureAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected site custom structure acceptance scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if turnResult.TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected completed turn, got %s", turnResult.TaskStatus)
	}
	if !eventsContain(turnResult.Events, "agent.site_publish_prerequisite_rejected", "") {
		t.Fatalf("expected the first site.publish attempt to be rejected by the prerequisite gate; events: %s", summarizeEvents(turnResult.Events))
	}
	if countEvents(turnResult.Events, "tool.terminal.run.requested") != 1 {
		t.Fatalf("expected exactly one terminal.run build after the app/src change; events: %s", summarizeEvents(turnResult.Events))
	}
	if countEvents(turnResult.Events, "tool.site.publish.requested") != 1 {
		t.Fatalf("expected site.publish to succeed only after the rebuild; events: %s", summarizeEvents(turnResult.Events))
	}
	if !strings.Contains(turnResult.FinishMessage, "https://") {
		t.Fatalf("expected final assistant message to contain a URL, got %q", turnResult.FinishMessage)
	}
}

func TestSiteLifecycleAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), SiteLifecycleAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected site lifecycle acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 4 {
		t.Fatalf("expected four turn results, got %d", len(result.TurnResults))
	}
	deleteRequestTurnResult := result.TurnResults[2]
	if deleteRequestTurnResult.TaskStatus != task.TaskStatusWaitingApproval {
		t.Fatalf("expected delete turn to wait for approval, got %s", deleteRequestTurnResult.TaskStatus)
	}
	if !eventsContain(deleteRequestTurnResult.Events, "approval.pending_call", "site.delete") {
		t.Fatalf("expected pending site.delete approval; events: %s", summarizeEvents(deleteRequestTurnResult.Events))
	}
	deleteCompletionTurnResult := result.TurnResults[3]
	if deleteCompletionTurnResult.TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected delete completion, got %s", deleteCompletionTurnResult.TaskStatus)
	}
	if !eventsContain(deleteCompletionTurnResult.Events, "tool.capability.invoke.result", "deleted") {
		t.Fatalf("expected site.delete result; events: %s", summarizeEvents(deleteCompletionTurnResult.Events))
	}
}

func TestSiteSuggestedRepairRecovery(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), SiteSuggestedRepairRecoveryScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected suggested repair recovery scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if turnResult.TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected completed turn, got %s", turnResult.TaskStatus)
	}
	if !eventsContain(turnResult.Events, "agent.completion_required", "") {
		t.Fatalf("expected completion gate to reject early finish; events: %s", summarizeEvents(turnResult.Events))
	}
	if countEventsWithFragment(turnResult.Events, "tool.capability.invoke.requested", "site.repair") != 1 {
		t.Fatalf("expected one site.repair call; events: %s", summarizeEvents(turnResult.Events))
	}
	if countEventsWithFragment(turnResult.Events, "tool.capability.invoke.requested", "site.publish") != 1 {
		t.Fatalf("expected one site.publish call; events: %s", summarizeEvents(turnResult.Events))
	}
	if !strings.Contains(turnResult.FinishMessage, "https://") {
		t.Fatalf("expected final assistant message to contain a URL, got %q", turnResult.FinishMessage)
	}
}

func TestAskChoiceReplyAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AskChoiceReplyAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected ask choice reply acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turns, got %+v", result)
	}
}

func TestDirectMessageSendConfirmAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), DirectMessageSendConfirmAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected direct message send confirm acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turns, got %+v", result)
	}
	firstTurnResult := result.TurnResults[0]
	secondTurnResult := result.TurnResults[1]
	if !eventsContain(firstTurnResult.Events, "confirmation.requested", "external_send") {
		t.Fatalf("expected confirmation request before send; events: %s", summarizeEvents(firstTurnResult.Events))
	}
	if countRequestedToolCalls(firstTurnResult.Events, "message.send") != 0 {
		t.Fatalf("expected confirmation before any send attempt; events: %s", summarizeEvents(firstTurnResult.Events))
	}
	if countRequestedToolCalls(secondTurnResult.Events, "message.send") != 1 {
		t.Fatalf("expected exactly one approved send request; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !eventsContain(secondTurnResult.Events, "tool.capability.invoke.result", "virtual-platform-message-001") {
		t.Fatalf("expected send result message id observation; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !strings.Contains(secondTurnResult.FinishMessage, "보냈습니다") {
		t.Fatalf("expected successful delivery reply, got %q", secondTurnResult.FinishMessage)
	}
}

func TestChannelPostAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), ChannelPostAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected channel post acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected confirmation and execution turns, got %+v", result)
	}
	if countRequestedToolCalls(result.TurnResults[0].Events, "message.send") != 0 {
		t.Fatalf("expected confirmation before channel send; events: %s", summarizeEvents(result.TurnResults[0].Events))
	}
	turnResult := result.TurnResults[1]
	if countRequestedToolCalls(turnResult.Events, "message.send") != 1 {
		t.Fatalf("expected one send request, got events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "tool.capability.invoke.requested", `"targetType":"channel"`) {
		t.Fatalf("expected channel delivery target; events: %s", summarizeEvents(turnResult.Events))
	}
	if eventsContain(turnResult.Events, "tool.capability.invoke.requested", `"targetType":"directMessage"`) {
		t.Fatalf("expected no direct message target; events: %s", summarizeEvents(turnResult.Events))
	}
}

func TestPlatformMessageEditAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), PlatformMessageEditAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected platform message edit acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn, got %+v", result)
	}
	turnResult := result.TurnResults[0]
	if countRequestedToolCalls(turnResult.Events, "message.update") != 1 {
		t.Fatalf("expected one message update request; events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "tool.capability.invoke.requested", `"messageID":"virtual-platform-message-001"`) {
		t.Fatalf("expected message ID in update input; events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "tool.capability.invoke.requested", `"text":"오늘 오후 6시에 전체 공지 회의가 있습니다."`) {
		t.Fatalf("expected new text in update input; events: %s", summarizeEvents(turnResult.Events))
	}
}

func TestAttachmentMaterialRead(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AttachmentMaterialReadScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected attachment material read scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "tool.image.read.requested", `"materialID":"mattermost:file-1"`) {
		t.Fatalf("expected image.read to use materialID; events: %s", summarizeEvents(turnResult.Events))
	}
	if eventsContain(turnResult.Events, "tool.terminal.run.requested", "terminal.run") {
		t.Fatalf("expected attachment read not to search the workspace; events: %s", summarizeEvents(turnResult.Events))
	}
	if turnResult.UserModelImagePartCount == 0 {
		t.Fatalf("expected image.read result to reach the model as a user image part; context: %s", turnResult.ModelContext)
	}
	if len(turnResult.Attachments) != 0 {
		t.Fatalf("expected image.read result not to be reattached, got %+v", turnResult.Attachments)
	}
}

func TestAttachmentHTMLPreviewRecovery(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AttachmentHTMLPreviewRecoveryScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected attachment html preview recovery scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if eventsContain(turnResult.Events, "tool.terminal.run.requested", "terminal.run") {
		t.Fatalf("expected html attachment preview not to search the workspace; events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "tool.file.preview.result", "Virtual HTML Title") {
		t.Fatalf("expected html preview content in tool result; events: %s", summarizeEvents(turnResult.Events))
	}
}

func TestAttachmentHTMLPreviousPreviewRecovery(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AttachmentHTMLPreviousPreviewRecoveryScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected previous attachment html preview recovery scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if eventsContain(turnResult.Events, "tool.terminal.run.requested", "terminal.run") {
		t.Fatalf("expected previous html attachment preview not to search the workspace; events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "tool.file.preview.result", "Virtual HTML Title") {
		t.Fatalf("expected previous html preview content in tool result; events: %s", summarizeEvents(turnResult.Events))
	}
}

func TestAttachmentCurrentImageInput(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AttachmentCurrentImageInputScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected current image input scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if turnResult.ModelImagePartCount == 0 {
		t.Fatalf("expected current image attachment to reach model input; context: %s", turnResult.ModelContext)
	}
	if turnResult.UserModelImagePartCount == 0 {
		t.Fatalf("expected current image attachment to reach the model as a user image part; context: %s", turnResult.ModelContext)
	}
	if eventsContain(turnResult.Events, "tool.image.read.requested", "image.read") {
		t.Fatalf("expected current image input not to require image.read; events: %s", summarizeEvents(turnResult.Events))
	}
	if eventsContain(turnResult.Events, "tool.terminal.run.requested", "terminal.run") {
		t.Fatalf("expected current image input not to search the workspace; events: %s", summarizeEvents(turnResult.Events))
	}
	if len(turnResult.Attachments) != 0 {
		t.Fatalf("expected current image input not to be reattached, got %+v", turnResult.Attachments)
	}
}

func firstNonEmptyTestString(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}

func truthyEnvironmentValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func toolEventCount(events []task.TaskEvent) int {
	count := 0
	for _, event := range events {
		if strings.HasPrefix(event.Name, "tool.") {
			count++
		}
	}
	return count
}

func failureEventCount(events []task.TaskEvent) int {
	count := 0
	for _, event := range events {
		normalizedName := strings.ToLower(event.Name)
		if strings.Contains(normalizedName, "fail") || strings.Contains(normalizedName, "error") {
			count++
		}
	}
	return count
}

func activeScheduleCount(taskSchedules []task.TaskSchedule) int {
	count := 0
	for _, taskSchedule := range taskSchedules {
		if taskSchedule.NextRunAt != nil {
			count++
		}
	}
	return count
}

func eventBodies(events []task.TaskEvent, name string) []string {
	bodies := []string{}
	for _, event := range events {
		if event.Name == name {
			bodies = append(bodies, event.Body)
		}
	}
	return bodies
}
