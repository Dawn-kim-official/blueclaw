package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/capability"
	"blueclaw/internal/identity"
	"blueclaw/internal/llm"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
	"blueclaw/internal/task"
)

func TestConnectorRuntimeProcessesInvitedMessageAndDeduplicates(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "안녕하세요"})
	event := testInboundEvent("message-1")

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected first event to process: %v", errorValue)
	}
	duplicateResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected duplicate event to process: %v", errorValue)
	}

	if result.TaskRunID == "" {
		t.Fatal("expected task run id")
	}
	if result.ReplyDispatchID != "dispatch-1" {
		t.Fatalf("expected first dispatch id, got %q", result.ReplyDispatchID)
	}
	if !duplicateResult.Duplicate {
		t.Fatal("expected duplicate result")
	}
	if len(adapter.sentReplies) != 1 {
		t.Fatalf("expected one reply, got %d", len(adapter.sentReplies))
	}
	if len(adapter.progressStarts) != 1 {
		t.Fatalf("expected one progress start, got %d", len(adapter.progressStarts))
	}
	if len(adapter.progressStops) != 1 {
		t.Fatalf("expected one progress stop, got %d", len(adapter.progressStops))
	}
}

func TestConnectorRuntimeRejectsUninvitedUserWithoutTask(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "ignored"})
	adapter.senderEmail = "outside@example.com"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testInboundEvent("message-1"))
	if errorValue != nil {
		t.Fatalf("expected uninvited user to receive rejection: %v", errorValue)
	}

	if result.TaskRunID != "" {
		t.Fatalf("expected no task run, got %q", result.TaskRunID)
	}
	if result.ReplyDispatchID != "dispatch-1" {
		t.Fatalf("expected rejection dispatch id, got %q", result.ReplyDispatchID)
	}
	if adapter.sentReplies[0].message != adapter.NotInvitedReply() {
		t.Fatalf("expected not invited reply, got %q", adapter.sentReplies[0].message)
	}
}

func TestConnectorRuntimeUsesFallbackReplyWhenLanguageModelFails(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{errorValue: errors.New("model unavailable")})

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testInboundEvent("message-1"))
	if errorValue != nil {
		t.Fatalf("expected fallback reply: %v", errorValue)
	}

	if result.TaskRunID == "" {
		t.Fatal("expected task run id")
	}
	if len(adapter.sentReplies) != 1 {
		t.Fatalf("expected one reply, got %d", len(adapter.sentReplies))
	}
	if adapter.sentReplies[0].message != "I am having trouble reaching the language model right now. I logged the failure so the model configuration can be fixed." {
		t.Fatalf("expected fallback reply, got %q", adapter.sentReplies[0].message)
	}
}

func TestConnectorRuntimeUsesOpaqueReplyTarget(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "reply"})
	event := testInboundEvent("message-1")
	event.ReplyTargetID = "opaque-reply-target"

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}

	if adapter.sentReplies[0].target.ReplyTargetID != "opaque-reply-target" {
		t.Fatalf("expected opaque reply target, got %q", adapter.sentReplies[0].target.ReplyTargetID)
	}
}

func TestConnectorRuntimeInjectsRequesterMemoryIntoLanguageModel(t *testing.T) {
	languageModel := &recordingLanguageModel{reply: "기억했습니다"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	memoryService := &memory.MemoryService{}
	memoryService.StoreMemoryFact(memory.MemoryFact{
		ScopeType:   memory.ScopeTypeUser,
		NamespaceID: "user:person-1",
		Content:     "사용자는 Graphiti 메모리 설계를 선택했다.",
	})
	connectorRuntime.UseMemoryService(memoryService)

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testInboundEvent("message-1"))
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}

	if len(languageModel.request.Messages) < 2 {
		t.Fatalf("expected memory context message, got %+v", languageModel.request.Messages)
	}
	if !strings.Contains(languageModel.request.Messages[1].Content, "Graphiti 메모리 설계") {
		t.Fatalf("expected requester memory in model context, got %q", languageModel.request.Messages[1].Content)
	}
}

func TestConnectorRuntimeInjectsVisibleContextBeforeMemory(t *testing.T) {
	languageModel := &recordingLanguageModel{reply: "맥락 확인"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	event := testInboundEvent("message-1")
	event.Context = VisibleContext{
		Messages: []VisibleContextMessage{
			{Speaker: "admin", Text: "이전 메시지"},
		},
		HasMoreBefore: true,
		HistoryCursor: "cursor-1",
	}
	memoryService := &memory.MemoryService{}
	memoryService.StoreMemoryFact(memory.MemoryFact{
		ScopeType:   memory.ScopeTypeUser,
		NamespaceID: "user:person-1",
		Content:     "사용자는 간결한 설계를 선호한다.",
	})
	connectorRuntime.UseMemoryService(memoryService)

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}

	if len(languageModel.request.Messages) != 4 {
		t.Fatalf("expected system, visible context, memory, prompt messages, got %d", len(languageModel.request.Messages))
	}
	if !strings.Contains(languageModel.request.Messages[1].Content, "admin: 이전 메시지") {
		t.Fatalf("expected visible context first, got %q", languageModel.request.Messages[1].Content)
	}
	if !strings.Contains(languageModel.request.Messages[1].Content, "conversation.history") {
		t.Fatalf("expected history availability, got %q", languageModel.request.Messages[1].Content)
	}
	if !strings.Contains(languageModel.request.Messages[2].Content, "간결한 설계") {
		t.Fatalf("expected memory second, got %q", languageModel.request.Messages[2].Content)
	}
	if languageModel.request.Messages[3].Content != event.Prompt {
		t.Fatalf("expected prompt last, got %q", languageModel.request.Messages[3].Content)
	}
}

func TestConnectorRuntimeRunsAgentHistoryToolAndSendsOneFinalReply(t *testing.T) {
	languageModel := &connectorSequenceLanguageModel{contents: []string{
		`{"action":"fetch_history","toolInput":{"limit":20}}`,
		`{"action":"final_reply","finalReply":"이전 대화를 확인했습니다"}`,
	}}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	event := testInboundEvent("message-1")
	event.Context.HasMoreBefore = true
	event.Context.HistoryCursor = "cursor-1"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}

	if result.TaskRunID == "" {
		t.Fatal("expected task run id")
	}
	if len(adapter.historyCursors) != 1 || adapter.historyCursors[0] != "cursor-1" {
		t.Fatalf("expected history fetch with cursor, got %+v", adapter.historyCursors)
	}
	if len(adapter.sentReplies) != 1 {
		t.Fatalf("expected one final reply, got %d", len(adapter.sentReplies))
	}
	if adapter.sentReplies[0].message != "이전 대화를 확인했습니다" {
		t.Fatalf("expected final reply, got %q", adapter.sentReplies[0].message)
	}
}

func TestConnectorRuntimeReadsTypedCapabilityToolResponse(t *testing.T) {
	languageModel := &connectorSequenceLanguageModel{contents: []string{
		`{"action":"call_tool","toolName":"browser.observe","toolInput":{}}`,
		`{"action":"final_reply","finalReply":"브라우저를 확인했습니다"}`,
	}}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntime.UseAllowedToolNames([]string{"conversation.history", "memory.search", "browser.observe"})
	connectorRuntime.UseCapabilityTools(capability.Client{
		Endpoint: "http://capability.test",
		HTTPClient: testHTTPDoer(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != "/v1/tools/browser.observe/invoke" {
				t.Fatalf("unexpected capability path: %s", request.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"provider":"device","selectedBackend":"device_local","toolName":"browser.observe","status":"ok","result":{"url":"https://example.com","snapshotText":"Example"}}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}),
	}, []string{"browser.observe"})

	event := testInboundEvent("message-1")
	event.Prompt = "open browser and observe"
	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected capability tool event to process: %v", errorValue)
	}

	if len(languageModel.requests) < 2 || !structuredMessagesContain(languageModel.requests[1].Messages, "https://example.com") {
		t.Fatalf("expected typed capability result to be available as tool observation, got %+v", languageModel.requests)
	}
	if adapter.sentReplies[0].message != "브라우저를 확인했습니다" {
		t.Fatalf("expected final reply, got %q", adapter.sentReplies[0].message)
	}
}

func TestConnectorRuntimeDetachesHTTPEventFromCanceledRequestContext(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "ok"})
	request, errorValue := http.NewRequest(http.MethodPost, "/connectors/test/events", strings.NewReader(`{}`))
	if errorValue != nil {
		t.Fatalf("expected request: %v", errorValue)
	}
	ctx, cancel := context.WithCancel(request.Context())
	cancel()
	request = request.WithContext(ctx)
	adapter.httpParseResult = HTTPParseResult{
		HasEvent: true,
		Event:    testInboundEvent("message-http"),
	}

	result, _, errorValue := connectorRuntime.HandleHTTPEvent(request.Context(), adapter.Name(), request)
	if errorValue != nil {
		t.Fatalf("expected detached http event to process: %v", errorValue)
	}
	if result.TaskRunID == "" {
		t.Fatalf("expected task run result, got %+v", result)
	}
}

func TestConnectorRuntimeStoresUserMemoryAcrossConversations(t *testing.T) {
	languageModel := &recordingLanguageModel{reply: "ok"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	graphStore := &fakeGraphMemoryStore{
		facts: []memory.MemoryFact{
			{ScopeType: memory.ScopeTypeUser, NamespaceID: "user:person-1", Content: "사용자의 이름은 민수다."},
		},
	}
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(graphStore)
	connectorRuntime.UseMemoryService(memoryService)
	connectorRuntime.UseMemoryScopeRouter(memory.NewMemoryScopeRouter(staticScopeLanguageModel{content: `{"storeWorkspace":false,"securityLevelRank":0,"requiredClasses":[]}`}, "default"))

	channelEvent := testInboundEvent("message-1")
	channelEvent.ConversationID = "channel-1"
	channelEvent.Prompt = "내 이름은 민수야"
	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, channelEvent)
	if errorValue != nil {
		t.Fatalf("expected channel memory event to process: %v", errorValue)
	}

	directEvent := testInboundEvent("message-2")
	directEvent.ConversationID = "dm-1"
	directEvent.Prompt = "내 이름 뭐야?"
	_, errorValue = connectorRuntime.HandleInboundEvent(context.Background(), adapter, directEvent)
	if errorValue != nil {
		t.Fatalf("expected direct memory recall event to process: %v", errorValue)
	}

	if len(graphStore.episodes) != 2 {
		t.Fatalf("expected Graphiti episode ingestion for both messages, got %d", len(graphStore.episodes))
	}
	if !containsEpisodeNamespace(graphStore.episodes[0], "user:person-1") {
		t.Fatalf("expected user namespace ingestion, got %+v", graphStore.episodes[0].Namespaces)
	}
	if !strings.Contains(languageModel.request.Messages[1].Content, "민수") {
		t.Fatalf("expected user memory from graph search in direct reply context, got %+v", languageModel.request.Messages)
	}
}

func TestConnectorRuntimeDoesNotShareUserMemoryWithOtherPerson(t *testing.T) {
	memoryService := &memory.MemoryService{}
	memoryService.StoreMemoryFact(memory.MemoryFact{
		ScopeType:   memory.ScopeTypeUser,
		NamespaceID: "user:person-1",
		Content:     "사용자의 이름은 민수다.",
	})

	records, errorValue := memoryService.SearchMemory(context.Background(), memory.MemorySearchRequest{
		ReaderPersonID:          "person-2",
		ReaderSecurityLevelRank: 100,
		ReaderGrantedClasses:    []string{"internal"},
		Namespaces:              []memory.MemoryNamespace{memory.UserNamespace("person-2")},
	})
	if errorValue != nil {
		t.Fatalf("expected memory search to succeed: %v", errorValue)
	}
	if len(records) != 0 {
		t.Fatalf("expected person-2 not to read person-1 user memory, got %d", len(records))
	}
}

func TestConnectorRuntimeRejectsMissingHistoryCursorWhenMoreContextExists(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{reply: "ignored"})
	event := testInboundEvent("message-1")
	event.Context.HasMoreBefore = true

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected malformed event to be ignored: %v", errorValue)
	}
	if !result.Ignored || result.Reason != "missing_history_cursor" {
		t.Fatalf("expected missing history cursor rejection, got %+v", result)
	}
}

func TestPlatformInboundEventOnlyUsesTextAndSenderCompatibilityAliases(t *testing.T) {
	var event PlatformInboundEvent
	errorValue := json.Unmarshal([]byte(`{
		"conversationID":"conversation-1",
		"messageID":"message-1",
		"senderUserID":"sender-1",
		"text":"hello",
		"rootMessageID":"root-1",
		"replyParentID":"parent-1"
	}`), &event)
	if errorValue != nil {
		t.Fatalf("expected compatibility event to decode: %v", errorValue)
	}

	if event.SenderID != "sender-1" {
		t.Fatalf("expected sender compatibility alias, got %q", event.SenderID)
	}
	if event.Prompt != "hello" {
		t.Fatalf("expected text compatibility alias, got %q", event.Prompt)
	}
	if event.ReplyTargetID != "" {
		t.Fatalf("expected no reply target inference, got %q", event.ReplyTargetID)
	}
}

type testAdapter struct {
	senderEmail     string
	httpParseResult HTTPParseResult
	sentReplies     []testReply
	progressStarts  []ReplyTarget
	progressStops   []ReplyTarget
	historyCursors  []string
}

type testReply struct {
	target  ReplyTarget
	message string
}

func (adapter *testAdapter) Name() string {
	return "test"
}

func (adapter *testAdapter) ParseHTTPEvent(context.Context, *http.Request) (HTTPParseResult, error) {
	return adapter.httpParseResult, nil
}

func (adapter *testAdapter) ParseRealtimeEvent(context.Context, []byte, string) (PlatformInboundEvent, bool, error) {
	return PlatformInboundEvent{}, false, nil
}

func (adapter *testAdapter) ResolveIdentity(context.Context, string) (identity.PlatformAccountIdentity, error) {
	return identity.PlatformAccountIdentity{
		Platform:       adapter.Name(),
		ExternalUserID: "sender-user",
		Email:          adapter.senderEmail,
		DisplayName:    "Sender",
	}, nil
}

func (adapter *testAdapter) StartProgress(_ context.Context, target ReplyTarget) error {
	adapter.progressStarts = append(adapter.progressStarts, target)
	return nil
}

func (adapter *testAdapter) StopProgress(_ context.Context, target ReplyTarget) error {
	adapter.progressStops = append(adapter.progressStops, target)
	return nil
}

func (adapter *testAdapter) SendReply(_ context.Context, target ReplyTarget, message string) (string, error) {
	adapter.sentReplies = append(adapter.sentReplies, testReply{target: target, message: message})
	return "dispatch-" + strconv.Itoa(len(adapter.sentReplies)), nil
}

func (adapter *testAdapter) FetchHistory(_ context.Context, historyCursor string, _ int) (VisibleContext, error) {
	adapter.historyCursors = append(adapter.historyCursors, historyCursor)
	return VisibleContext{
		Messages: []VisibleContextMessage{{Speaker: "admin", Text: "older message"}},
	}, nil
}

func (adapter *testAdapter) NotInvitedReply() string {
	return "not invited"
}

type testLanguageModel struct {
	reply      string
	errorValue error
}

func (languageModel testLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return languageModel.reply, languageModel.errorValue
}

func (languageModel testLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if languageModel.errorValue != nil {
		return llm.StructuredResponse{}, languageModel.errorValue
	}
	return llm.StructuredResponse{Content: `{"reply":"` + languageModel.reply + `"}`}, nil
}

type recordingLanguageModel struct {
	reply   string
	request llm.StructuredResponseRequest
}

func (languageModel *recordingLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return languageModel.reply, nil
}

func (languageModel *recordingLanguageModel) GenerateStructuredResponse(_ context.Context, structuredResponseRequest llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	languageModel.request = structuredResponseRequest
	return llm.StructuredResponse{Content: `{"reply":"` + languageModel.reply + `"}`}, nil
}

type connectorSequenceLanguageModel struct {
	contents []string
	requests []llm.StructuredResponseRequest
}

func (languageModel *connectorSequenceLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *connectorSequenceLanguageModel) GenerateStructuredResponse(_ context.Context, structuredResponseRequest llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	languageModel.requests = append(languageModel.requests, structuredResponseRequest)
	index := len(languageModel.requests) - 1
	if index >= len(languageModel.contents) {
		index = len(languageModel.contents) - 1
	}
	return llm.StructuredResponse{Content: languageModel.contents[index]}, nil
}

type staticScopeLanguageModel struct {
	content string
}

func (languageModel staticScopeLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel staticScopeLanguageModel) GenerateStructuredResponse(_ context.Context, structuredResponseRequest llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if structuredResponseRequest.StructuredOutputSchema.Name == "blueclaw_memory_scope_route" {
		return llm.StructuredResponse{Content: languageModel.content}, nil
	}
	return llm.StructuredResponse{Content: `{"reply":"ok"}`}, nil
}

type fakeGraphMemoryStore struct {
	episodes []memory.MemoryEpisode
	facts    []memory.MemoryFact
}

func (store *fakeGraphMemoryStore) AddEpisode(_ context.Context, episode memory.MemoryEpisode) error {
	store.episodes = append(store.episodes, episode)
	return nil
}

func (store *fakeGraphMemoryStore) SearchFacts(_ context.Context, request memory.MemorySearchRequest) ([]memory.MemoryFact, error) {
	facts := []memory.MemoryFact{}
	for _, fact := range store.facts {
		for _, namespace := range request.Namespaces {
			if fact.NamespaceID == namespace.NamespaceID {
				facts = append(facts, fact)
			}
		}
	}
	return facts, nil
}

func containsEpisodeNamespace(episode memory.MemoryEpisode, namespaceID string) bool {
	for _, namespace := range episode.Namespaces {
		if namespace.NamespaceID == namespaceID {
			return true
		}
	}
	return false
}

type testHTTPDoer func(*http.Request) (*http.Response, error)

func (doer testHTTPDoer) Do(request *http.Request) (*http.Response, error) {
	return doer(request)
}

func structuredMessagesContain(messages []llm.Message, fragment string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, fragment) {
			return true
		}
	}
	return false
}

func newTestConnectorRuntime(t *testing.T, languageModel llm.LanguageModelProvider) (*ConnectorRuntime, *testAdapter) {
	t.Helper()

	identityService := identity.NewIdentityService(policy.PolicyProjection{
		PersonIDByEmail: map[string]string{"invited@example.com": "person-1"},
		PersonAccessByPersonID: map[string]policy.PersonAccess{
			"person-1": {PersonID: "person-1", SecurityLevelRank: 100, GrantedClasses: []string{"internal", "finance"}},
		},
	})
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := agent.NewAgentKernel(taskRunService, task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(languageModel)

	connectorRuntime := NewConnectorRuntime(identityService, agentKernel, nil)
	adapter := &testAdapter{senderEmail: "invited@example.com"}
	connectorRuntime.RegisterAdapter(adapter)
	return connectorRuntime, adapter
}

func testInboundEvent(messageID string) PlatformInboundEvent {
	return PlatformInboundEvent{
		Platform:       "test",
		Source:         "test",
		ConversationID: "direct-1",
		MessageID:      messageID,
		SenderID:       "sender-user",
		ReplyTargetID:  "reply-target-1",
		Prompt:         "hello",
	}
}
