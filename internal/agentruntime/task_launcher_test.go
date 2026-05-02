package agentruntime

import (
	"context"
	"encoding/json"
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

	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{ProfileName: "default"})
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

	toolResult, errorValue := toolRegistry.InvokeTool(context.Background(), agent.ToolInvocation{ToolName: "blocked.tool", Input: json.RawMessage(`{}`)})
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

	plannerToolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{ProfileName: "planner"})
	developerToolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{ProfileName: "developer"})

	if containsString(plannerToolRegistry.ListToolNames(), "terminal.run") || containsString(plannerToolRegistry.ListToolNames(), "terminal.session") {
		t.Fatalf("expected planner terminal tools to be hidden, got %+v", plannerToolRegistry.ListToolNames())
	}
	if !containsString(developerToolRegistry.ListToolNames(), "terminal.run") || !containsString(developerToolRegistry.ListToolNames(), "terminal.session") {
		t.Fatalf("expected developer terminal tools, got %+v", developerToolRegistry.ListToolNames())
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
	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{ProfileName: "default"})

	toolResult, errorValue := toolRegistry.InvokeTool(agent.WithTaskRunID(context.Background(), taskRun.TaskRunID), agent.ToolInvocation{
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
	toolRegistry := toolCatalogBuilder.BuildToolRegistry(ToolCatalogRequest{ProfileName: "default"})

	toolResult, errorValue := toolRegistry.InvokeTool(agent.WithTaskRunID(context.Background(), taskRun.TaskRunID), agent.ToolInvocation{
		ToolName: "browser_handoff.openURL",
		Input:    json.RawMessage(`{"url":"https://example.com/login"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected browser handoff result: %v", errorValue)
	}
	if toolResult.Content != "opened" || toolResult.IsError {
		t.Fatalf("expected opened bridge response, got %+v", toolResult)
	}
	if httpClient.requestPath != "/v1/tools/browser.open/invoke" || !strings.Contains(httpClient.requestBody, "https://example.com/login") {
		t.Fatalf("expected browser bridge request, got path=%s body=%s", httpClient.requestPath, httpClient.requestBody)
	}
	if !containsTaskEvent(taskEventService.ListTaskEvent(taskRun.TaskRunID), "browser_handoff.opened") {
		t.Fatalf("expected browser handoff audit event")
	}
}

type recordingHTTPClient struct {
	requestPath string
	requestBody string
}

func (httpClient *recordingHTTPClient) Do(request *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(request.Body)
	httpClient.requestPath = request.URL.Path
	httpClient.requestBody = string(body)
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"content":"opened","status":"ok"}`)),
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
