package agentruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"blueclaw/internal/agent"
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
