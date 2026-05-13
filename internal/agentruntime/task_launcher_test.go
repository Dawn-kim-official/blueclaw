package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/capability"
	"blueclaw/internal/config"
	"blueclaw/internal/llm"
	"blueclaw/internal/mcp"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
	"blueclaw/internal/task"
)

func TestTaskLauncherCreatesAuditedAgentRun(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	agentKernel := agent.NewAgentKernel(task.NewTaskRunService(taskEventService), task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(staticRuntimeLanguageModel{content: runtimeFinalReply("done")})
	memoryService := &memory.MemoryService{}
	memoryService.StoreMemoryFact(memory.MemoryFact{
		ScopeType:   memory.ScopeTypeUser,
		NamespaceID: memory.UserNamespace("person-1").NamespaceID,
		Content:     "사용자는 발표자료 생성을 자주 요청한다.",
		Score:       0.8,
	})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryService(memoryService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"conversation.history", "memory.search"},
	}, nil)

	launchResult, errorValue := NewTaskLauncher(agentKernel, toolCatalogBuilder).Launch(context.Background(), TaskLaunchRequest{
		Source:                    TaskLaunchSourceConnector,
		SourceReference:           "mattermost:post-1",
		RequesterPersonID:         "person-1",
		ProfileName:               "default",
		ConversationID:            "channel-1",
		Prompt:                    "발표자료 만들어줘",
		HistoryProvider:           staticHistoryProvider{},
		PersonAccess:              policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
		MemoryNamespaces:          []memory.MemoryNamespace{memory.UserNamespace("person-1")},
		AccessibleConversationIDs: []string{"channel-1"},
	})
	if errorValue != nil {
		t.Fatalf("expected launch to succeed: %v", errorValue)
	}
	if launchResult.TurnResult.TaskRun.TaskRunID == "" {
		t.Fatal("expected task run id")
	}
	if len(launchResult.MemoryFacts) != 1 {
		t.Fatalf("expected memory search result, got %+v", launchResult.MemoryFacts)
	}
	if !containsString(launchResult.ToolNames, "conversation.history") || !containsString(launchResult.ToolNames, "memory.search") {
		t.Fatalf("expected launch tool catalog, got %+v", launchResult.ToolNames)
	}

	taskEvents := taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID)
	if !containsTaskEvent(taskEvents, "agent.instructions_loaded") {
		t.Fatalf("expected instructions_loaded event, got %+v", taskEvents)
	}
	taskLaunchEvent := findTaskEvent(taskEvents, "agent.task_launched")
	if taskLaunchEvent.Name == "" {
		t.Fatalf("expected task launch event, got %+v", taskEvents)
	}
	if !strings.Contains(taskLaunchEvent.Body, `"source":"connector"`) || !strings.Contains(taskLaunchEvent.Body, `"memoryFactCount":1`) {
		t.Fatalf("expected launch audit body, got %s", taskLaunchEvent.Body)
	}
}

func TestTaskLauncherAuditsMemorySearchFailureAndRunsWithoutMemory(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	agentKernel := agent.NewAgentKernel(task.NewTaskRunService(taskEventService), task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(staticRuntimeLanguageModel{content: runtimeFinalReply("done")})
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(failingGraphMemoryStore{errorValue: errors.New("graphiti unavailable")})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryService(memoryService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"memory.search"},
	}, nil)

	launchResult, errorValue := NewTaskLauncher(agentKernel, toolCatalogBuilder).Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		SourceReference:   "mattermost:post-1",
		RequesterPersonID: "person-1",
		ProfileName:       "default",
		ConversationID:    "channel-1",
		Prompt:            "내 이름 뭐야?",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
		MemoryNamespaces:  []memory.MemoryNamespace{memory.UserNamespace("person-1")},
	})
	if errorValue != nil {
		t.Fatalf("expected launch to continue without memory: %v", errorValue)
	}
	if len(launchResult.MemoryFacts) != 0 {
		t.Fatalf("expected no memory facts after search failure, got %+v", launchResult.MemoryFacts)
	}
	taskEvents := taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID)
	if !containsTaskEvent(taskEvents, "memory.search_failed") {
		t.Fatalf("expected memory search failure event, got %+v", taskEvents)
	}
}

func TestToolCatalogHidesHistoryWithoutProviderAndDeniedTools(t *testing.T) {
	mcpRegistry := mcp.NewMcpRegistry()
	mcpRegistry.LoadServerDefinition([]config.MCPServerConfiguration{
		{
			Name: "local-mcp",
			Tools: []config.MCPToolConfiguration{
				{Name: "allowed.tool", Description: "Allowed"},
				{Name: "blocked.tool", Description: "Blocked"},
			},
		},
	})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMCPRegistry(mcpRegistry)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"allowed.tool", "memory.search"},
	}, nil)

	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	toolNames := toolRegistry.ListToolNames()
	if containsString(toolNames, "conversation.history") {
		t.Fatalf("expected history tool to be hidden without provider, got %+v", toolNames)
	}
	if !containsString(toolNames, "allowed.tool") {
		t.Fatalf("expected allowed MCP tool, got %+v", toolNames)
	}
	if containsString(toolNames, "blocked.tool") {
		t.Fatalf("expected blocked MCP tool to be hidden, got %+v", toolNames)
	}

	toolResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{ToolName: "blocked.tool", Input: json.RawMessage(`{}`)})
	if errorValue != nil {
		t.Fatalf("expected denied tool as result: %v", errorValue)
	}
	if !toolResult.IsError || toolResult.Content != "tool is not allowed" {
		t.Fatalf("expected denied result, got %+v", toolResult)
	}
}

func TestToolCatalogProfileFiltersBuiltInTerminalTools(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"planner":   {"memory.search"},
		"developer": {"memory.search", "terminal.run", "terminal.session"},
	}, nil)

	plannerToolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "planner"})
	developerToolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "developer"})

	if containsString(plannerToolSet.ListToolNames(), "terminal.run") || containsString(plannerToolSet.ListToolNames(), "terminal.session") {
		t.Fatalf("expected planner terminal tools to be hidden, got %+v", plannerToolSet.ListToolNames())
	}
	if !containsString(developerToolSet.ListToolNames(), "terminal.run") || !containsString(developerToolSet.ListToolNames(), "terminal.session") {
		t.Fatalf("expected developer terminal tools, got %+v", developerToolSet.ListToolNames())
	}
}

func TestApprovalRequestPausesActiveTaskRun(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "approve this")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"approval.request"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolResult, errorValue := toolRegistry.Invoke(agent.WithTaskRunID(context.Background(), taskRun.TaskRunID), agent.ToolInvocation{
		ToolName: "approval.request",
		Input:    json.RawMessage(`{"message":"Approve browser login?"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected approval tool to return a result: %v", errorValue)
	}
	if toolResult.IsError {
		t.Fatalf("expected approval request to succeed, got %+v", toolResult)
	}
	updatedTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if !isFound || updatedTaskRun.Status != task.TaskStatusWaitingApproval {
		t.Fatalf("expected waiting approval task run, got found=%v run=%+v", isFound, updatedTaskRun)
	}
	if !containsTaskEvent(taskEventService.ListTaskEvent(taskRun.TaskRunID), "approval.requested") {
		t.Fatalf("expected approval requested event")
	}
}

func TestApprovalRequestStoresUserFacingMessageSeparatelyFromReasonDetail(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "approve this")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"approval.request"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolResult, errorValue := toolRegistry.Invoke(agent.WithResponseLanguage(agent.WithTaskRunID(context.Background(), taskRun.TaskRunID), agent.ResponseLanguageKorean), agent.ToolInvocation{
		ToolName: "approval.request",
		Input:    json.RawMessage(`{"userFacingMessage":"동하 님에게 다음 DM을 보내도 될까요?\n\n테스트","reasonCode":"external_send","reasonDetail":"Direct messages are external sends and require approval before immediate delivery."}`),
	})

	if errorValue != nil {
		t.Fatalf("expected approval tool to return a result: %v", errorValue)
	}
	if toolResult.IsError {
		t.Fatalf("expected approval request to succeed, got %+v", toolResult)
	}
	approvalEvent := findTaskEvent(taskEventService.ListTaskEvent(taskRun.TaskRunID), "approval.requested")
	var approvalRequest map[string]string
	if errorValue := json.Unmarshal([]byte(approvalEvent.Body), &approvalRequest); errorValue != nil {
		t.Fatal(errorValue)
	}
	if approvalRequest["userFacingMessage"] != "동하 님에게 다음 DM을 보내도 될까요?\n\n테스트" {
		t.Fatalf("expected user-facing message in event, got %+v", approvalRequest)
	}
	if approvalRequest["reasonCode"] != "external_send" || approvalRequest["reasonDetail"] == "" {
		t.Fatalf("expected internal reason fields in event, got %+v", approvalRequest)
	}
}

func TestApprovalRequestAcceptsLegacyMessageWithoutExposingReasonAsMessage(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "approve this")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"approval.request"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolResult, errorValue := toolRegistry.Invoke(agent.WithTaskRunID(context.Background(), taskRun.TaskRunID), agent.ToolInvocation{
		ToolName: "approval.request",
		Input:    json.RawMessage(`{"message":"승인할까요?","reason":"legacy internal reason"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected legacy approval tool to return a result: %v", errorValue)
	}
	if toolResult.IsError {
		t.Fatalf("expected legacy approval request to succeed, got %+v", toolResult)
	}
	approvalEvent := findTaskEvent(taskEventService.ListTaskEvent(taskRun.TaskRunID), "approval.requested")
	if strings.Contains(approvalEvent.Body, `"reason":"legacy internal reason"`) {
		t.Fatalf("legacy reason must not be stored as user-facing reason, got %s", approvalEvent.Body)
	}
	if !strings.Contains(approvalEvent.Body, `"userFacingMessage":"승인할까요?"`) {
		t.Fatalf("expected legacy message to become userFacingMessage, got %s", approvalEvent.Body)
	}
}

func TestApprovalRequestRejectsMissingUserFacingMessageWithoutPausing(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "approve this")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"approval.request"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolResult, errorValue := toolRegistry.Invoke(agent.WithTaskRunID(context.Background(), taskRun.TaskRunID), agent.ToolInvocation{
		ToolName: "approval.request",
		Input:    json.RawMessage(`{"reasonCode":"external_send","reasonDetail":"internal only"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected approval tool to return a result: %v", errorValue)
	}
	if !toolResult.IsError || toolResult.ErrorCode != "approval_message_required" {
		t.Fatalf("expected missing message tool failure, got %+v", toolResult)
	}
	updatedTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if !isFound || updatedTaskRun.Status == task.TaskStatusWaitingApproval {
		t.Fatalf("expected task not to pause, got found=%v run=%+v", isFound, updatedTaskRun)
	}
	if containsTaskEvent(taskEventService.ListTaskEvent(taskRun.TaskRunID), "approval.requested") {
		t.Fatalf("unexpected approval event")
	}
}

func TestBrowserHandoffOpenURLUsesCapabilityBridge(t *testing.T) {
	httpClient := &recordingHTTPClient{}
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "open browser")

	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityTools(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, nil)
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"browser_handoff.openURL"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:             "default",
		RequesterPersonID:       "person-1",
		RequesterName:           "Dongha",
		RequesterEmail:          "dongha@example.com",
		RequesterPlatformUserID: "mattermost-user-1",
		ConversationID:          "conversation-1",
		Platform:                "mattermost",
	})

	toolResult, errorValue := toolRegistry.Invoke(agent.WithTaskRunID(context.Background(), taskRun.TaskRunID), agent.ToolInvocation{
		ToolName: "browser_handoff.openURL",
		Input:    json.RawMessage(`{"url":"https://example.com/login"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected browser handoff result: %v", errorValue)
	}
	if toolResult.Content != "opened" || toolResult.IsError {
		t.Fatalf("expected opened bridge response, got %+v", toolResult)
	}
	if httpClient.requestPath != "/v1/tools/browser.handoff/invoke" || !strings.Contains(httpClient.requestBody, "https://example.com/login") {
		t.Fatalf("expected browser bridge request, got path=%s body=%s", httpClient.requestPath, httpClient.requestBody)
	}
	var requestDocument struct {
		ExecutionMode        string `json:"executionMode"`
		RequiresUserPresence bool   `json:"requiresUserPresence"`
		Context              struct {
			RequesterPersonID       string `json:"requesterPersonID"`
			RequesterName           string `json:"requesterName"`
			RequesterEmail          string `json:"requesterEmail"`
			RequesterPlatformUserID string `json:"requesterPlatformUserID"`
			ConversationID          string `json:"conversationID"`
			Platform                string `json:"platform"`
		} `json:"context"`
	}
	if errorValue := json.Unmarshal([]byte(httpClient.requestBody), &requestDocument); errorValue != nil {
		t.Fatalf("expected browser bridge request json: %v", errorValue)
	}
	if requestDocument.ExecutionMode != "companion" || !requestDocument.RequiresUserPresence {
		t.Fatalf("expected browser bridge to require companion user presence, got %s presence=%v body=%s", requestDocument.ExecutionMode, requestDocument.RequiresUserPresence, httpClient.requestBody)
	}
	if requestDocument.Context.RequesterPersonID != "person-1" || requestDocument.Context.RequesterPlatformUserID != "mattermost-user-1" {
		t.Fatalf("expected requester identity in browser bridge request, got %+v", requestDocument.Context)
	}
	if !containsTaskEvent(taskEventService.ListTaskEvent(taskRun.TaskRunID), "browser_handoff.opened") {
		t.Fatalf("expected browser handoff audit event")
	}
}

func TestBrowserHandoffPausesTaskWhileWaitingForUser(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{"provider":"companion","toolName":"browser.handoff","status":"waiting_for_user","content":"브라우저에서 필요한 작업을 마친 뒤 완료 버튼을 눌러주세요.","result":{"state":"waiting_for_user","handoffID":"handoff-1","sessionID":"internkim","url":"https://example.com/login"}}`}
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "open browser")

	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityTools(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, nil)
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"browser_handoff.openURL"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:             "default",
		RequesterPersonID:       "person-1",
		RequesterName:           "Dongha",
		RequesterEmail:          "dongha@example.com",
		RequesterPlatformUserID: "mattermost-user-1",
		ConversationID:          "conversation-1",
		Platform:                "mattermost",
	})

	toolResult, errorValue := toolRegistry.Invoke(agent.WithTaskRunID(context.Background(), taskRun.TaskRunID), agent.ToolInvocation{
		ToolName: "browser_handoff.openURL",
		Input:    json.RawMessage(`{"url":"https://example.com/login"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected browser handoff result: %v", errorValue)
	}
	if toolResult.IsError {
		t.Fatalf("expected waiting handoff not to be an error: %+v", toolResult)
	}
	pausedTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if !isFound || pausedTaskRun.Status != task.TaskStatusWaitingUserInput {
		t.Fatalf("expected waiting user input task, got found=%v task=%+v", isFound, pausedTaskRun)
	}
	if httpClient.requestPath != "/v1/tools/browser.handoff/invoke" {
		t.Fatalf("expected browser handoff request, got path=%s", httpClient.requestPath)
	}
}

func TestInteractiveBrowserCapabilityUsesCompanion(t *testing.T) {
	httpClient := &recordingHTTPClient{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityTools(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []string{"browser.open"})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"browser.open"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:             "default",
		Prompt:                  "로그인해서 계정을 확인해줘",
		RequesterPersonID:       "person-1",
		RequesterPlatformUserID: "mattermost-user-1",
		ConversationID:          "conversation-1",
		Platform:                "mattermost",
	})

	toolResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "browser.open",
		Input:    json.RawMessage(`{"url":"https://example.com"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected browser capability result: %v", errorValue)
	}
	if toolResult.IsError {
		t.Fatalf("expected browser capability success, got %+v", toolResult)
	}
	var requestDocument struct {
		ExecutionMode        string `json:"executionMode"`
		RequiresUserPresence bool   `json:"requiresUserPresence"`
		PrivacyClass         string `json:"privacyClass"`
	}
	if errorValue := json.Unmarshal([]byte(httpClient.requestBody), &requestDocument); errorValue != nil {
		t.Fatalf("expected browser capability request json: %v", errorValue)
	}
	if requestDocument.ExecutionMode != "companion" || !requestDocument.RequiresUserPresence || requestDocument.PrivacyClass != "user_browser" {
		t.Fatalf("expected interactive browser capability to require companion, got %+v body=%s", requestDocument, httpClient.requestBody)
	}
}

func TestPublicBrowserCapabilityWithRequesterKeepsDeviceFallback(t *testing.T) {
	httpClient := &recordingHTTPClient{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityTools(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []string{"browser.open"})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"browser.open"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:             "default",
		Prompt:                  "https://example.com 열어줘",
		RequesterPersonID:       "person-1",
		RequesterPlatformUserID: "mattermost-user-1",
		ConversationID:          "conversation-1",
		Platform:                "mattermost",
	})

	toolResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "browser.open",
		Input:    json.RawMessage(`{"url":"https://example.com"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected browser capability result: %v", errorValue)
	}
	if toolResult.IsError {
		t.Fatalf("expected browser capability success, got %+v", toolResult)
	}
	var requestDocument map[string]any
	if errorValue := json.Unmarshal([]byte(httpClient.requestBody), &requestDocument); errorValue != nil {
		t.Fatalf("expected browser capability request json: %v", errorValue)
	}
	if _, isFound := requestDocument["executionMode"]; isFound {
		t.Fatalf("expected public requester browser capability to keep automatic fallback, got body=%s", httpClient.requestBody)
	}
}

func TestPrivateBrowserCapabilityUsesCompanion(t *testing.T) {
	httpClient := &recordingHTTPClient{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityTools(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []string{"browser.open"})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"browser.open"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "browser.open",
		Input:    json.RawMessage(`{"url":"http://127.0.0.1:3000"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected browser capability result: %v", errorValue)
	}
	if toolResult.IsError {
		t.Fatalf("expected browser capability success, got %+v", toolResult)
	}
	var requestDocument struct {
		ExecutionMode        string `json:"executionMode"`
		RequiresUserPresence bool   `json:"requiresUserPresence"`
		PrivacyClass         string `json:"privacyClass"`
	}
	if errorValue := json.Unmarshal([]byte(httpClient.requestBody), &requestDocument); errorValue != nil {
		t.Fatalf("expected browser capability request json: %v", errorValue)
	}
	if requestDocument.ExecutionMode != "companion" || !requestDocument.RequiresUserPresence || requestDocument.PrivacyClass != "user_browser" {
		t.Fatalf("expected private browser capability to require companion, got %+v body=%s", requestDocument, httpClient.requestBody)
	}
}

func TestBrowserFollowUpWithSensitiveVisibleContextUsesCompanion(t *testing.T) {
	httpClient := &recordingHTTPClient{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityTools(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []string{"browser.open"})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"browser.open"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		Prompt:      "다시 열어봐",
		VisibleContext: agent.VisibleContext{Messages: []agent.VisibleContextMessage{
			{Speaker: "사용자", Text: "구글 클라우드 콘솔에서 credential.json 받는 거 도와줘"},
			{Speaker: "김인턴", Text: "Companion 브라우저 연결이 필요합니다."},
		}},
	})

	toolResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "browser.open",
		Input:    json.RawMessage(`{"url":"https://console.cloud.google.com/"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected browser capability result: %v", errorValue)
	}
	if toolResult.IsError {
		t.Fatalf("expected browser capability success, got %+v", toolResult)
	}
	var requestDocument struct {
		ExecutionMode        string `json:"executionMode"`
		RequiresUserPresence bool   `json:"requiresUserPresence"`
		PrivacyClass         string `json:"privacyClass"`
	}
	if errorValue := json.Unmarshal([]byte(httpClient.requestBody), &requestDocument); errorValue != nil {
		t.Fatalf("expected browser capability request json: %v", errorValue)
	}
	if requestDocument.ExecutionMode != "companion" || !requestDocument.RequiresUserPresence || requestDocument.PrivacyClass != "user_browser" {
		t.Fatalf("expected browser follow-up to require companion, got %+v body=%s", requestDocument, httpClient.requestBody)
	}
}

func TestCapabilityDenialPreservesRecoveryAction(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{"provider":"companion","toolName":"browser.open","status":"denied","content":"Companion이 연결되어 있지 않아 브라우저를 열 수 없습니다.","isError":true,"result":{"status":"denied","code":"not_connected","toolName":"browser.open","userReason":"Companion이 연결되어 있지 않아 브라우저를 열 수 없습니다.","recovery":{"kind":"companion_connect","delivery":"dm_preferred","downloadURL":"https://example.com/companion.dmg","connectCommand":"/connect"}}}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityTools(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []string{"browser.open"})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"browser.open"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		Prompt:      "브라우저 열어줘",
	})

	toolResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "browser.open",
		Input:    json.RawMessage(`{"url":"https://example.com"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected browser capability result: %v", errorValue)
	}
	if !toolResult.IsError || len(toolResult.RecoveryActions) != 1 {
		t.Fatalf("expected recovery action on denied tool result, got %+v", toolResult)
	}
	recoveryAction := toolResult.RecoveryActions[0]
	if recoveryAction.Kind != "companion_connect" || recoveryAction.Delivery != "dm_preferred" || recoveryAction.ConnectCommand != "/connect" {
		t.Fatalf("unexpected recovery action: %+v", recoveryAction)
	}
}

func TestPublicBrowserCapabilityKeepsDeviceFallback(t *testing.T) {
	httpClient := &recordingHTTPClient{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityTools(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []string{"browser.open"})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"browser.open"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "browser.open",
		Input:    json.RawMessage(`{"url":"https://example.com"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected browser capability result: %v", errorValue)
	}
	if toolResult.IsError {
		t.Fatalf("expected browser capability success, got %+v", toolResult)
	}
	var requestDocument map[string]any
	if errorValue := json.Unmarshal([]byte(httpClient.requestBody), &requestDocument); errorValue != nil {
		t.Fatalf("expected browser capability request json: %v", errorValue)
	}
	if _, isFound := requestDocument["executionMode"]; isFound {
		t.Fatalf("expected public browser capability to keep automatic fallback, got body=%s", httpClient.requestBody)
	}
	if _, isFound := requestDocument["requiresUserPresence"]; isFound {
		t.Fatalf("expected public browser capability to avoid user presence, got body=%s", httpClient.requestBody)
	}
}

func TestBrowserHandoffOpenURLRecordsFailureWhenCompanionIsDisconnected(t *testing.T) {
	httpClient := &recordingHTTPClient{responseBody: `{"content":"Companion is not connected, so the browser was not opened. Ask the user to run /connect before retrying.","status":"denied","isError":true}`}
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "open browser")

	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityTools(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, nil)
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"browser_handoff.openURL"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default", RequesterPersonID: "person-1"})

	toolResult, errorValue := toolRegistry.Invoke(agent.WithTaskRunID(context.Background(), taskRun.TaskRunID), agent.ToolInvocation{
		ToolName: "browser_handoff.openURL",
		Input:    json.RawMessage(`{"url":"https://example.com/login"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected browser handoff denial result: %v", errorValue)
	}
	if !toolResult.IsError || !strings.Contains(toolResult.Content, "/connect") {
		t.Fatalf("expected /connect denial result, got %+v", toolResult)
	}
	taskEvents := taskEventService.ListTaskEvent(taskRun.TaskRunID)
	if !containsTaskEvent(taskEvents, "browser_handoff.failed") || containsTaskEvent(taskEvents, "browser_handoff.opened") {
		t.Fatalf("expected failed browser handoff audit event, got %+v", taskEvents)
	}
}

func TestCapabilityDescriptorAppearsInToolSetAndInvokesBridge(t *testing.T) {
	httpClient := &recordingHTTPClient{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:             "browser.open",
		InputSchema:      json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"],"additionalProperties":false}`),
		OutputSchema:     json.RawMessage(`{"type":"object","properties":{"status":{"type":"string"}}}`),
		PolicyResource:   "tool:browser.open",
		SideEffectClass:  "browser",
		RequiresApproval: true,
	}})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"browser.open"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	descriptions := toolRegistry.Descriptions()
	actionSchema := toolRegistry.ActionSchema(false, nil, false)
	if !strings.Contains(descriptions, `"url"`) || !strings.Contains(actionSchema, `"browser.open"`) {
		t.Fatalf("expected descriptor schema in prompt and action schema, got prompt=%s schema=%s", descriptions, actionSchema)
	}

	toolResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "browser.open",
		Input:    json.RawMessage(`{"url":"https://example.com"}`),
	})
	if errorValue != nil {
		t.Fatalf("expected capability descriptor invocation: %v", errorValue)
	}
	if toolResult.IsError || httpClient.requestPath != "/v1/tools/browser.open/invoke" {
		t.Fatalf("expected capability bridge invocation, got result=%+v path=%s", toolResult, httpClient.requestPath)
	}
}

func TestCapabilityToolExecutionUsesResourceAccess(t *testing.T) {
	resourceAccessRules := []policy.ResourceAccessPolicy{{
		Resource: "tool:company.broadcast.send",
		Actions:  []string{"execute"},
		Circles:  []string{"representative"},
	}}
	httpClient := &recordingHTTPClient{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityTools(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []string{"company.broadcast.send"})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"company.broadcast.send"},
	}, nil)

	staffToolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID:            "person-1",
			Circles:             []string{"staff"},
			ResourceAccessRules: resourceAccessRules,
		},
	})
	staffResult, errorValue := staffToolSet.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "company.broadcast.send",
		Input:    json.RawMessage(`{"message":"hello"}`),
	})
	if errorValue != nil {
		t.Fatalf("expected denied tool result: %v", errorValue)
	}
	if !staffResult.IsError || !strings.Contains(staffResult.Content, "cannot execute") {
		t.Fatalf("expected staff execution denial, got %+v", staffResult)
	}
	if httpClient.requestPath != "" {
		t.Fatalf("expected denied tool not to call capability bridge, got path=%s", httpClient.requestPath)
	}

	representativeToolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID:            "person-2",
			Circles:             []string{"staff", "representative"},
			ResourceAccessRules: resourceAccessRules,
		},
	})
	representativeResult, errorValue := representativeToolSet.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "company.broadcast.send",
		Input:    json.RawMessage(`{"message":"hello"}`),
	})
	if errorValue != nil {
		t.Fatalf("expected representative tool result: %v", errorValue)
	}
	if representativeResult.IsError {
		t.Fatalf("expected representative execution success, got %+v", representativeResult)
	}
	if httpClient.requestPath != "/v1/tools/company.broadcast.send/invoke" {
		t.Fatalf("expected capability bridge call, got path=%s body=%s", httpClient.requestPath, httpClient.requestBody)
	}
}

func TestFlowTaskAddToolRequiresStaffCircle(t *testing.T) {
	resourceAccessRules := []policy.ResourceAccessPolicy{{
		Resource: "tool:flow.task.add",
		Actions:  []string{"execute"},
		Circles:  []string{"staff"},
	}}
	httpClient := &recordingHTTPClient{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityTools(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []string{"flow.task.add"})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"flow.task.add"},
	}, nil)

	guestToolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID:            "person-1",
			ResourceAccessRules: resourceAccessRules,
		},
	})
	guestResult, errorValue := guestToolSet.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "flow.task.add",
		Input:    json.RawMessage(`{"prompt":"10분 회의"}`),
	})
	if errorValue != nil {
		t.Fatalf("expected denied tool result: %v", errorValue)
	}
	if !guestResult.IsError {
		t.Fatalf("expected guest execution denial, got %+v", guestResult)
	}
	if httpClient.requestPath != "" {
		t.Fatalf("expected denied Flow tool not to call capability bridge, got path=%s", httpClient.requestPath)
	}

	staffToolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID:            "person-2",
			Circles:             []string{"staff"},
			ResourceAccessRules: resourceAccessRules,
		},
	})
	staffResult, errorValue := staffToolSet.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "flow.task.add",
		Input:    json.RawMessage(`{"prompt":"10분 회의"}`),
	})
	if errorValue != nil {
		t.Fatalf("expected staff tool result: %v", errorValue)
	}
	if staffResult.IsError {
		t.Fatalf("expected staff execution success, got %+v", staffResult)
	}
	if httpClient.requestPath != "/v1/tools/flow.task.add/invoke" {
		t.Fatalf("expected Flow capability bridge call, got path=%s body=%s", httpClient.requestPath, httpClient.requestBody)
	}
}

type recordingHTTPClient struct {
	requestPath  string
	requestBody  string
	responseBody string
}

func (httpClient *recordingHTTPClient) Do(request *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(request.Body)
	httpClient.requestPath = request.URL.Path
	httpClient.requestBody = string(body)
	responseBody := httpClient.responseBody
	if responseBody == "" {
		responseBody = `{"content":"opened","status":"ok"}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(responseBody)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

type staticRuntimeLanguageModel struct {
	content string
}

func (languageModel staticRuntimeLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel staticRuntimeLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{Content: languageModel.content}, nil
}

type staticHistoryProvider struct{}

func (historyProvider staticHistoryProvider) FetchHistory(context.Context, string, int) (agent.VisibleContext, error) {
	return agent.VisibleContext{}, nil
}

type failingGraphMemoryStore struct {
	errorValue error
}

func (store failingGraphMemoryStore) AddEpisode(context.Context, memory.MemoryEpisode) (memory.MemoryIngestionResult, error) {
	return memory.MemoryIngestionResult{}, nil
}

func (store failingGraphMemoryStore) SearchFacts(context.Context, memory.MemorySearchRequest) ([]memory.MemoryFact, error) {
	return nil, store.errorValue
}

func runtimeFinalReply(reply string) string {
	return `{"action":"final_reply","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[],"finalReply":"` + reply + `"}`
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func containsTaskEvent(taskEvents []task.TaskEvent, name string) bool {
	return findTaskEvent(taskEvents, name).Name != ""
}

func findTaskEvent(taskEvents []task.TaskEvent, name string) task.TaskEvent {
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == name {
			return taskEvent
		}
	}
	return task.TaskEvent{}
}
