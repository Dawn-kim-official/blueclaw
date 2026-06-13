package agentruntime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/capability"
	"blueclaw/internal/config"
	"blueclaw/internal/llm"
	"blueclaw/internal/mcp"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
	"blueclaw/internal/security"
	"blueclaw/internal/security/actortest"
	"blueclaw/internal/task"
	"blueclaw/internal/workspacepath"
)

func TestTaskLauncherCreatesAuditedAgentRun(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	agentKernel := agent.NewAgentKernel(task.NewTaskRunService(taskEventService), task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(staticRuntimeLanguageModel{content: runtimeFinishMessage("done")})
	pinnedMemoryStore := memory.NewMarkdownStore(t.TempDir(), 1200)
	if _, errorValue := pinnedMemoryStore.MergePersonMemory(context.Background(), "person-1", "사용자는 발표자료 생성을 자주 요청한다."); errorValue != nil {
		t.Fatalf("expected pinned memory setup to succeed: %v", errorValue)
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UsePinnedMemoryStore(pinnedMemoryStore)
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
		t.Fatalf("expected pinned memory result, got %+v", launchResult.MemoryFacts)
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

func TestTaskLauncherAuditsPlatformMessageRegistryFingerprint(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	agentKernel := agent.NewAgentKernel(task.NewTaskRunService(taskEventService), task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(staticRuntimeLanguageModel{content: runtimeFinishMessage("done")})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{
		Endpoint:   "http://capability.local",
		HTTPClient: &recordingHTTPClient{responseBody: platformMessageLiveRegistryResponse(platformMessageDeleteCriteriaSchema())},
	}, testPlatformMessageCapabilityDescriptors())
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"platform.message.context", "platform.message.search", "platform.message.delete"},
	}, nil)

	launchResult, errorValue := NewTaskLauncher(agentKernel, toolCatalogBuilder).Launch(context.Background(), TaskLaunchRequest{
		Source:                    TaskLaunchSourceConnector,
		SourceReference:           "mattermost:post-1",
		RequesterPersonID:         "person-1",
		ProfileName:               "default",
		ConversationID:            "channel-1",
		Prompt:                    "너가 보낸 메시지 삭제해줘",
		PersonAccess:              policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
		AccessibleConversationIDs: []string{"channel-1"},
	})
	if errorValue != nil {
		t.Fatalf("expected matching registry launch to succeed: %v", errorValue)
	}

	taskLaunchEvent := findTaskEvent(taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID), "agent.task_launched")
	if taskLaunchEvent.Name == "" {
		t.Fatalf("expected launch event")
	}
	for _, expected := range []string{
		`"toolRegistryVersion":"platform-message-v1"`,
		`"capabilityDescriptorHash":"`,
		`"liveCapabilityHash":"`,
		`"platformMessageDescriptorHash":"`,
		`"livePlatformMessageDescriptorHash":"`,
		`"allowedToolHash":"`,
		`"hasPlatformMessageDelete":true`,
		`"liveHasPlatformMessageDelete":true`,
		`"hasOldMattermostPostDelete":false`,
		`"hasOldPlatformDMInspect":false`,
	} {
		if !strings.Contains(taskLaunchEvent.Body, expected) {
			t.Fatalf("expected launch event to contain %s, got %s", expected, taskLaunchEvent.Body)
		}
	}
}

func TestTaskLauncherAuditsPlatformMessageSchemaSkewWithoutBlocking(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	agentKernel := agent.NewAgentKernel(task.NewTaskRunService(taskEventService), task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(staticRuntimeLanguageModel{content: runtimeFinishMessage("done")})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{
		Endpoint:   "http://capability.local",
		HTTPClient: &recordingHTTPClient{responseBody: platformMessageLiveRegistryResponse(platformMessageDeleteIDsOnlySchema())},
	}, testPlatformMessageCapabilityDescriptors())
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"platform.message.context", "platform.message.search", "platform.message.delete"},
	}, nil)

	launchResult, errorValue := NewTaskLauncher(agentKernel, toolCatalogBuilder).Launch(context.Background(), TaskLaunchRequest{
		Source:                    TaskLaunchSourceConnector,
		SourceReference:           "mattermost:post-1",
		RequesterPersonID:         "person-1",
		ProfileName:               "default",
		ConversationID:            "channel-1",
		Prompt:                    "너가 보낸 메시지 삭제해줘",
		PersonAccess:              policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
		AccessibleConversationIDs: []string{"channel-1"},
	})
	if errorValue != nil {
		t.Fatalf("expected schema skew to remain diagnostic-only, got %v", errorValue)
	}
	taskLaunchEvent := findTaskEvent(taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID), "agent.task_launched")
	if taskLaunchEvent.Name == "" {
		t.Fatal("expected task launch event")
	}
	if !strings.Contains(taskLaunchEvent.Body, `"platformMessageDescriptorHash":"`) ||
		!strings.Contains(taskLaunchEvent.Body, `"livePlatformMessageDescriptorHash":"`) {
		t.Fatalf("expected schema skew hashes in launch event, got %s", taskLaunchEvent.Body)
	}
}

func TestTaskLauncherRejectsStaleMessageToolRegistryBeforeModelCall(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	agentKernel := agent.NewAgentKernel(task.NewTaskRunService(taskEventService), task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(staticRuntimeLanguageModel{content: runtimeFinishMessage("should not run")})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{
		Endpoint: "http://capability.local",
		HTTPClient: &recordingHTTPClient{responseBody: `{"deviceCapabilities":[
			{"name":"platform.message.context"},
			{"name":"platform.message.search"},
			{"name":"platform.message.send"},
			{"name":"platform.message.update"},
			{"name":"platform.message.delete"}
		]}`},
	}, []CapabilityToolDescriptor{{Name: "mattermost.post.delete", PolicyResource: "tool:mattermost.post.delete"}})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"mattermost.post.delete"},
	}, nil)

	launchResult, errorValue := NewTaskLauncher(agentKernel, toolCatalogBuilder).Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		SourceReference:   "mattermost:post-1",
		RequesterPersonID: "person-1",
		ProfileName:       "default",
		ConversationID:    "channel-1",
		Prompt:            "너가 보낸 메시지 삭제해줘",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
	})
	if errorValue != nil {
		t.Fatalf("expected registry mismatch to return failed task result: %v", errorValue)
	}
	if launchResult.TurnResult.TaskRun.Status != task.TaskStatusFailed {
		t.Fatalf("expected failed task, got %+v", launchResult.TurnResult.TaskRun)
	}
	if !strings.Contains(launchResult.TurnResult.FailureNotice.SendableMessage(), "runtime_registry_mismatch") {
		t.Fatalf("expected raw registry mismatch notice, got %+v", launchResult.TurnResult.FailureNotice)
	}
	taskEvents := taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID)
	if !containsTaskEvent(taskEvents, "agent.launch_step.error") {
		t.Fatalf("expected launch step error event, got %+v", taskEvents)
	}
}

func TestTaskLauncherAddsStaffToRequesterAccess(t *testing.T) {
	personAccess := requesterPersonAccessForTaskLaunch(TaskLaunchRequest{
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			Circles: []string{"finance"},
		},
	})

	if personAccess.PersonID != "person-1" {
		t.Fatalf("expected requester person id to be copied, got %+v", personAccess)
	}
	if !containsString(personAccess.Circles, "staff") || !containsString(personAccess.Circles, "finance") {
		t.Fatalf("expected task requester access to include staff and explicit circles, got %+v", personAccess.Circles)
	}
}

func TestTaskLauncherProvisionsRequesterWorkspaceBeforeToolSet(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	agentKernel := agent.NewAgentKernel(task.NewTaskRunService(taskEventService), task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(staticRuntimeLanguageModel{content: runtimeFinishMessage("done")})
	workspacePath := t.TempDir()
	requesterHomePath := filepath.Join(workspacePath, "private", "people", "person-1")
	provisioner := &recordingRequesterWorkspaceProvisioner{
		provision: func(personAccess policy.PersonAccess, workspaceRootPath string) error {
			if personAccess.PersonID != "person-1" {
				t.Fatalf("expected requester person access, got %+v", personAccess)
			}
			if workspaceRootPath != workspacePath {
				t.Fatalf("expected workspace root %s, got %s", workspacePath, workspaceRootPath)
			}
			if _, errorValue := os.Stat(requesterHomePath); !os.IsNotExist(errorValue) {
				t.Fatalf("expected requester home to be absent before provisioning, got %v", errorValue)
			}
			return os.MkdirAll(requesterHomePath, 02770)
		},
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"memory.search"},
	}, nil)
	taskLauncher := NewTaskLauncher(agentKernel, toolCatalogBuilder)
	taskLauncher.UseRequesterWorkspaceProvisioner(provisioner)

	launchResult, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		SourceReference:   "mattermost:post-1",
		RequesterPersonID: "person-1",
		ProfileName:       "default",
		ConversationID:    "channel-1",
		Prompt:            "prepare workspace",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
	})
	if errorValue != nil {
		t.Fatalf("expected launch to succeed: %v", errorValue)
	}
	if provisioner.callCount != 1 {
		t.Fatalf("expected one requester provisioning call, got %d", provisioner.callCount)
	}
	workspaceActor, errorValue := actortest.NewDirectWorkspaceActorFactory().Requester(context.Background(), security.WorkspaceActorRequest{
		WorkspaceRootPath: workspacePath,
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	requesterSitePath := workspacepath.Directory{
		ConcretePath: filepath.Join(requesterHomePath, "sites", "site-1"),
		VirtualPath:  "home/sites/site-1",
	}
	if errorValue := workspaceActor.MkdirAll(context.Background(), requesterSitePath, 02770); errorValue != nil {
		t.Fatalf("expected requester actor mkdir to succeed after launch provisioning: %v", errorValue)
	}
	taskEvents := taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID)
	launchStepBodies := launchStepResultBodies(taskEvents)
	if len(launchStepBodies) < 2 {
		t.Fatalf("expected launch step results, got %+v", taskEvents)
	}
	if !strings.Contains(launchStepBodies[0], `"stepName":"provision_requester_workspace"`) {
		t.Fatalf("expected requester provisioning before toolset build, got %+v", launchStepBodies)
	}
	if !strings.Contains(launchStepBodies[1], `"stepName":"build_tool_set"`) {
		t.Fatalf("expected toolset build after requester provisioning, got %+v", launchStepBodies)
	}
}

func TestTaskLauncherAuditsPinnedMemoryFailureAndRunsWithoutMemory(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	agentKernel := agent.NewAgentKernel(task.NewTaskRunService(taskEventService), task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(staticRuntimeLanguageModel{content: runtimeFinishMessage("done")})
	rootPath := t.TempDir()
	if errorValue := os.WriteFile(filepath.Join(rootPath, "people"), []byte("not a directory"), 0600); errorValue != nil {
		t.Fatalf("expected pinned memory failure setup to succeed: %v", errorValue)
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UsePinnedMemoryStore(memory.NewMarkdownStore(rootPath, 1200))
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
		t.Fatalf("expected no memory facts after pinned memory failure, got %+v", launchResult.MemoryFacts)
	}
	taskEvents := taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID)
	if !containsTaskEvent(taskEvents, "memory.pinned_load_failed") {
		t.Fatalf("expected pinned memory failure event, got %+v", taskEvents)
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
	if !toolResult.Failed() || toolResult.ContentText() != "tool is not allowed" {
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
		"default": {"ask.confirm"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolResult, errorValue := toolRegistry.Invoke(agent.WithTaskRunID(context.Background(), taskRun.TaskRunID), agent.ToolInvocation{
		ToolName: "ask.confirm",
		Input:    json.RawMessage(`{"userFacingMessage":"Approve browser login?","reasonCode":"credential_access"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected approval tool to return a result: %v", errorValue)
	}
	if toolResult.Failed() {
		t.Fatalf("expected approval request to succeed, got %+v", toolResult)
	}
	updatedTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if !isFound || updatedTaskRun.Status != task.TaskStatusWaitingApproval {
		t.Fatalf("expected waiting approval task run, got found=%v run=%+v", isFound, updatedTaskRun)
	}
	if !containsTaskEvent(taskEventService.ListTaskEvent(taskRun.TaskRunID), "confirmation.requested") {
		t.Fatalf("expected approval requested event")
	}
}

func TestApprovalRequestReusesPriorApprovalForSameTaskAndConfirmation(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "approve this")
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "confirmation.requested", marshalToolResult(map[string]string{
		"userFacingMessage": "Approve browser login?",
		"reasonCode":        "credential_access",
	}))
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "agent.intake", marshalToolResult(map[string]string{
		"approval": "approve",
		"reason":   "interactive_confirm",
	}))
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"ask.confirm"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolResult, errorValue := toolRegistry.Invoke(agent.WithTaskRunID(context.Background(), taskRun.TaskRunID), agent.ToolInvocation{
		ToolName: "ask.confirm",
		Input:    json.RawMessage(`{"userFacingMessage":"Approve   browser\nlogin?","reasonCode":"credential_access"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected approval tool to return a result: %v", errorValue)
	}
	if toolResult.Failed() || !strings.Contains(toolResult.ContentText(), `"approvalReuse":"already_approved"`) {
		t.Fatalf("expected already-approved result, got %+v", toolResult)
	}
	if countTaskEvents(taskEventService.ListTaskEvent(taskRun.TaskRunID), "confirmation.requested") != 1 {
		t.Fatalf("expected no duplicate confirmation request")
	}
	updatedTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if !isFound || updatedTaskRun.Status == task.TaskStatusWaitingApproval {
		t.Fatalf("expected task not to pause again, got found=%v run=%+v", isFound, updatedTaskRun)
	}
}

func TestApprovalRequestEmitsAskForDifferentConfirmationContent(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "approve this")
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "confirmation.requested", marshalToolResult(map[string]string{
		"userFacingMessage": "Approve browser login?",
		"reasonCode":        "credential_access",
	}))
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "confirmation.reply_classified", marshalToolResult(map[string]string{
		"approval": "approve",
	}))
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"ask.confirm"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolResult, errorValue := toolRegistry.Invoke(agent.WithTaskRunID(context.Background(), taskRun.TaskRunID), agent.ToolInvocation{
		ToolName: "ask.confirm",
		Input:    json.RawMessage(`{"userFacingMessage":"Approve sending the DM?","reasonCode":"credential_access"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected approval tool to return a result: %v", errorValue)
	}
	if toolResult.Failed() || strings.Contains(toolResult.ContentText(), `"approvalReuse":"already_approved"`) {
		t.Fatalf("expected normal approval request result, got %+v", toolResult)
	}
	if countTaskEvents(taskEventService.ListTaskEvent(taskRun.TaskRunID), "confirmation.requested") != 2 {
		t.Fatalf("expected new confirmation request")
	}
}

func TestApprovalRequestEmitsAskAfterPriorRejection(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "approve this")
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "confirmation.requested", marshalToolResult(map[string]string{
		"userFacingMessage": "Approve browser login?",
		"reasonCode":        "credential_access",
	}))
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "confirmation.reply_classified", marshalToolResult(map[string]string{
		"approval": "approve",
	}))
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "confirmation.requested", marshalToolResult(map[string]string{
		"userFacingMessage": "Approve browser login?",
		"reasonCode":        "credential_access",
	}))
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "confirmation.reply_classified", marshalToolResult(map[string]string{
		"approval": "reject",
	}))
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "confirmation.rejected", marshalToolResult(map[string]string{
		"reason": "interactive_cancel",
	}))
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"ask.confirm"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolResult, errorValue := toolRegistry.Invoke(agent.WithTaskRunID(context.Background(), taskRun.TaskRunID), agent.ToolInvocation{
		ToolName: "ask.confirm",
		Input:    json.RawMessage(`{"userFacingMessage":"Approve browser login?","reasonCode":"credential_access"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected approval tool to return a result: %v", errorValue)
	}
	if toolResult.Failed() || strings.Contains(toolResult.ContentText(), `"approvalReuse":"already_approved"`) {
		t.Fatalf("expected normal approval request after rejection, got %+v", toolResult)
	}
	if countTaskEvents(taskEventService.ListTaskEvent(taskRun.TaskRunID), "confirmation.requested") != 3 {
		t.Fatalf("expected confirmation request after rejection")
	}
}

func TestApprovalRequestAllowsSensitiveReasonCodes(t *testing.T) {
	for _, reasonCode := range allowedApprovalReasonCodes {
		taskEventService := task.NewTaskEventService()
		taskRunService := task.NewTaskRunService(taskEventService)
		taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "approve this")
		toolCatalogBuilder := NewToolCatalogBuilder()
		toolCatalogBuilder.UseTaskRunService(taskRunService)
		toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
			"default": {"ask.confirm"},
		}, nil)
		toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

		toolResult, errorValue := toolRegistry.Invoke(agent.WithTaskRunID(context.Background(), taskRun.TaskRunID), agent.ToolInvocation{
			ToolName: "ask.confirm",
			Input:    json.RawMessage(`{"userFacingMessage":"Approve sensitive action?","reasonCode":"` + reasonCode + `"}`),
		})

		if errorValue != nil {
			t.Fatalf("expected approval tool to return a result for %s: %v", reasonCode, errorValue)
		}
		if toolResult.Failed() {
			t.Fatalf("expected approval request to succeed for %s, got %+v", reasonCode, toolResult)
		}
	}
}

func TestApprovalRequestStoresUserFacingMessageSeparatelyFromReasonDetail(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "approve this")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"ask.confirm"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolResult, errorValue := toolRegistry.Invoke(agent.WithResponseLanguage(agent.WithTaskRunID(context.Background(), taskRun.TaskRunID), agent.ResponseLanguageKorean), agent.ToolInvocation{
		ToolName: "ask.confirm",
		Input:    json.RawMessage(`{"userFacingMessage":"샘플 님에게 다음 DM을 보내도 될까요?\n\n테스트","reasonCode":"external_send","reasonDetail":"Direct messages are external sends and require approval before immediate delivery."}`),
	})

	if errorValue != nil {
		t.Fatalf("expected approval tool to return a result: %v", errorValue)
	}
	if toolResult.Failed() {
		t.Fatalf("expected approval request to succeed, got %+v", toolResult)
	}
	approvalEvent := findTaskEvent(taskEventService.ListTaskEvent(taskRun.TaskRunID), "confirmation.requested")
	var approvalRequest map[string]string
	if errorValue := json.Unmarshal([]byte(approvalEvent.Body), &approvalRequest); errorValue != nil {
		t.Fatal(errorValue)
	}
	if approvalRequest["userFacingMessage"] != "샘플 님에게 다음 DM을 보내도 될까요?\n\n테스트" {
		t.Fatalf("expected user-facing message in event, got %+v", approvalRequest)
	}
	if approvalRequest["reasonCode"] != "external_send" || approvalRequest["reasonDetail"] == "" {
		t.Fatalf("expected internal reason fields in event, got %+v", approvalRequest)
	}
}

func TestApprovalRequestRejectsMissingUserFacingMessageWithoutPausing(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "approve this")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"ask.confirm"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolResult, errorValue := toolRegistry.Invoke(agent.WithTaskRunID(context.Background(), taskRun.TaskRunID), agent.ToolInvocation{
		ToolName: "ask.confirm",
		Input:    json.RawMessage(`{"reasonCode":"external_send","reasonDetail":"internal only"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected approval tool to return a result: %v", errorValue)
	}
	if !toolResult.Failed() || toolResult.FailureCode() != agent.FailureCodes.InvalidInput.String() {
		t.Fatalf("expected missing message tool failure, got %+v", toolResult)
	}
	updatedTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if !isFound || updatedTaskRun.Status == task.TaskStatusWaitingApproval {
		t.Fatalf("expected task not to pause, got found=%v run=%+v", isFound, updatedTaskRun)
	}
	if containsTaskEvent(taskEventService.ListTaskEvent(taskRun.TaskRunID), "confirmation.requested") {
		t.Fatalf("unexpected approval event")
	}
}

func TestApprovalRequestRejectsMissingReasonCodeWithoutPausing(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "approve this")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"ask.confirm"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolResult, errorValue := toolRegistry.Invoke(agent.WithTaskRunID(context.Background(), taskRun.TaskRunID), agent.ToolInvocation{
		ToolName: "ask.confirm",
		Input:    json.RawMessage(`{"userFacingMessage":"Proceed?"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected approval tool to return a result: %v", errorValue)
	}
	if !toolResult.Failed() || !strings.Contains(toolResult.ContentText(), "ask.confirm is reserved for sensitive action consent only") {
		t.Fatalf("expected missing reason code guidance, got %+v", toolResult)
	}
	updatedTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if !isFound || updatedTaskRun.Status == task.TaskStatusWaitingApproval {
		t.Fatalf("expected task not to pause, got found=%v run=%+v", isFound, updatedTaskRun)
	}
	if containsTaskEvent(taskEventService.ListTaskEvent(taskRun.TaskRunID), "confirmation.requested") {
		t.Fatalf("unexpected approval event")
	}
}

func TestApprovalRequestRejectsUnrecognizedReasonCodeWithoutPausing(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "approve this")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"ask.confirm"},
	}, nil)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolResult, errorValue := toolRegistry.Invoke(agent.WithTaskRunID(context.Background(), taskRun.TaskRunID), agent.ToolInvocation{
		ToolName: "ask.confirm",
		Input:    json.RawMessage(`{"userFacingMessage":"Understood?","reasonCode":"user_acknowledgement"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected approval tool to return a result: %v", errorValue)
	}
	if !toolResult.Failed() || !strings.Contains(toolResult.ContentText(), "Allowed reasonCode values: "+strings.Join(allowedApprovalReasonCodes, ", ")) {
		t.Fatalf("expected unrecognized reason code guidance with allowed list, got %+v", toolResult)
	}
	updatedTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if !isFound || updatedTaskRun.Status == task.TaskStatusWaitingApproval {
		t.Fatalf("expected task not to pause, got found=%v run=%+v", isFound, updatedTaskRun)
	}
	if containsTaskEvent(taskEventService.ListTaskEvent(taskRun.TaskRunID), "confirmation.requested") {
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
	if toolResult.ContentText() != "opened" || toolResult.Failed() {
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
	if toolResult.Failed() {
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

	toolResult, errorValue := toolRegistry.Invoke(agent.WithWorkKinds(context.Background(), []string{agent.WorkKindUserBrowser}), agent.ToolInvocation{
		ToolName: "browser.open",
		Input:    json.RawMessage(`{"url":"https://example.com"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected browser capability result: %v", errorValue)
	}
	if toolResult.Failed() {
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
	if toolResult.Failed() {
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
	if toolResult.Failed() {
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

	toolResult, errorValue := toolRegistry.Invoke(agent.WithWorkKinds(context.Background(), []string{agent.WorkKindUserBrowser}), agent.ToolInvocation{
		ToolName: "browser.open",
		Input:    json.RawMessage(`{"url":"https://console.cloud.google.com/"}`),
	})

	if errorValue != nil {
		t.Fatalf("expected browser capability result: %v", errorValue)
	}
	if toolResult.Failed() {
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
	if !toolResult.Failed() || len(toolResult.RecoveryActions) != 1 {
		t.Fatalf("expected recovery action on denied tool result, got %+v", toolResult)
	}
	recoveryAction := toolResult.RecoveryActions[0]
	if recoveryAction.Kind != "companion_connect" || recoveryAction.Delivery != "dm_preferred" || recoveryAction.ConnectCommand != "/connect" {
		t.Fatalf("unexpected recovery action: %+v", recoveryAction)
	}
	if len(toolResult.Failure.RecoveryHints) != 1 || toolResult.Failure.RecoveryHints[0].Action != "companion_connect" {
		t.Fatalf("expected recovery hint normalized onto failure, got %+v", toolResult.Failure)
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
	if toolResult.Failed() {
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
	if !toolResult.Failed() || !strings.Contains(toolResult.ContentText(), "/connect") {
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
	if toolResult.Failed() || httpClient.requestPath != "/v1/tools/browser.open/invoke" {
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
	if !staffResult.Failed() || !strings.Contains(staffResult.ContentText(), "tool is not allowed") {
		t.Fatalf("expected staff execution denial, got %+v", staffResult)
	}
	if strings.Contains(staffToolSet.Descriptions(), "company.broadcast.send") {
		t.Fatalf("expected denied tool to be omitted from catalog, got %s", staffToolSet.Descriptions())
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
	if representativeResult.Failed() {
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
	if !guestResult.Failed() {
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
	if staffResult.Failed() {
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
	body := []byte{}
	if request.Body != nil {
		body, _ = io.ReadAll(request.Body)
	}
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

func testPlatformMessageCapabilityDescriptors() []CapabilityToolDescriptor {
	return []CapabilityToolDescriptor{
		{Name: "platform.message.context", PolicyResource: "tool:platform.message.context", InputSchema: platformMessageEmptySchema()},
		{Name: "platform.message.search", PolicyResource: "tool:platform.message.search", InputSchema: platformMessageEmptySchema()},
		{Name: "platform.message.send", PolicyResource: "tool:platform.message.send", InputSchema: platformMessageEmptySchema()},
		{Name: "platform.message.update", PolicyResource: "tool:platform.message.update", InputSchema: platformMessageEmptySchema()},
		{Name: "platform.message.delete", PolicyResource: "tool:platform.message.delete", InputSchema: platformMessageDeleteCriteriaSchema()},
	}
}

func platformMessageLiveRegistryResponse(deleteSchema json.RawMessage) string {
	response := map[string]any{
		"deviceCapabilities": []map[string]any{
			{"name": "platform.message.context", "inputSchema": platformMessageEmptySchema()},
			{"name": "platform.message.search", "inputSchema": platformMessageEmptySchema()},
			{"name": "platform.message.send", "inputSchema": platformMessageEmptySchema()},
			{"name": "platform.message.update", "inputSchema": platformMessageEmptySchema()},
			{"name": "platform.message.delete", "inputSchema": deleteSchema},
		},
	}
	document, errorValue := json.Marshal(response)
	if errorValue != nil {
		panic(errorValue)
	}
	return string(document)
}

func platformMessageEmptySchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

func platformMessageDeleteCriteriaSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"messageIDs":{"type":"array","items":{"type":"string"}},"scope":{"type":"string"},"deliveryTarget":{"type":"object","properties":{"type":{"type":"string"},"personHint":{"type":"string"},"channelID":{"type":"string"},"channelName":{"type":"string"}}},"authoredBy":{"type":"string"},"query":{"type":"string"},"limit":{"type":"integer"}}}`)
}

func platformMessageDeleteIDsOnlySchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"messageIDs":{"type":"array","items":{"type":"string"}}},"required":["messageIDs"]}`)
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

type recordingRequesterWorkspaceProvisioner struct {
	callCount  int
	provision  func(policy.PersonAccess, string) error
	errorValue error
}

func (provisioner *recordingRequesterWorkspaceProvisioner) ProvisionRequesterWorkspace(ctx context.Context, personAccess policy.PersonAccess, workspaceRootPath string) error {
	_ = ctx
	provisioner.callCount++
	if provisioner.errorValue != nil {
		return provisioner.errorValue
	}
	if provisioner.provision == nil {
		return nil
	}
	return provisioner.provision(personAccess, workspaceRootPath)
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

func runtimeFinishMessage(reply string) string {
	return `{"action":"finish","goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[],"finishMessage":"` + reply + `"}`
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

func countTaskEvents(taskEvents []task.TaskEvent, name string) int {
	count := 0
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == name {
			count++
		}
	}
	return count
}

func launchStepResultBodies(taskEvents []task.TaskEvent) []string {
	bodies := []string{}
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == "agent.launch_step.result" {
			bodies = append(bodies, taskEvent.Body)
		}
	}
	return bodies
}

func findTaskEvent(taskEvents []task.TaskEvent, name string) task.TaskEvent {
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == name {
			return taskEvent
		}
	}
	return task.TaskEvent{}
}
