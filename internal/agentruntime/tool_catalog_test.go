package agentruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/capability"
	"blueclaw/internal/config"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
	"blueclaw/internal/security"
	"blueclaw/internal/security/actortest"
	"blueclaw/internal/task"
	"blueclaw/internal/workspacepath"
)

func TestMemoryRememberToolEnqueuesPersonMemory(t *testing.T) {
	queue := &recordingMemoryUpdateQueue{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryUpdateQueue(queue)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory.remember"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
		MemoryNamespaces:  []memory.MemoryNamespace{memory.UserNamespace("person-1")},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "memory.remember",
		Input:    agent.MarshalToolInput(map[string]string{"content": "Call the user master."}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected memory.remember success, got %s", result.ContentText())
	}
	if len(queue.jobs) != 1 {
		t.Fatalf("expected one queued memory job, got %+v", queue.jobs)
	}
	job := queue.jobs[0]
	if job.Namespace.NamespaceID != memory.UserNamespace("person-1").NamespaceID || job.Content != "Call the user master." {
		t.Fatalf("expected person memory job, got %+v", job)
	}
	if !strings.Contains(result.ContentText(), `"accepted":true`) {
		t.Fatalf("expected accepted result, got %s", result.ContentText())
	}
}

func TestMemoryRememberToolRejectsInaccessibleActiveCircle(t *testing.T) {
	queue := &recordingMemoryUpdateQueue{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryUpdateQueue(queue)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory.remember"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{Circles: []string{"staff"}},
		ActiveCircleID:    "admin",
		MemoryNamespaces:  []memory.MemoryNamespace{memory.CircleNamespace("default", "staff")},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "memory.remember",
		Input:    agent.MarshalToolInput(map[string]string{"content": "Shared circle fact."}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected inaccessible circle error, got %s", result.ContentText())
	}
	if len(queue.jobs) != 0 {
		t.Fatalf("expected no queued jobs, got %+v", queue.jobs)
	}
}

func TestMemoryRememberToolEnqueuesCircleMemoryForActiveCircle(t *testing.T) {
	queue := &recordingMemoryUpdateQueue{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryUpdateQueue(queue)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory.remember"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{Circles: []string{"staff", "hr-compensation"}},
		ActiveCircleID:    "hr-compensation",
		MemoryNamespaces:  []memory.MemoryNamespace{memory.CircleNamespace("default", "hr-compensation")},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "memory.remember",
		Input:    agent.MarshalToolInput(map[string]string{"content": "Compensation data belongs to HR."}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected memory.remember success, got %s", result.ContentText())
	}
	if len(queue.jobs) != 1 {
		t.Fatalf("expected one queued memory job, got %+v", queue.jobs)
	}
	if queue.jobs[0].Namespace.ScopeType != memory.ScopeTypeCircle || queue.jobs[0].Namespace.ScopeCircleID != "hr-compensation" {
		t.Fatalf("expected circle memory job, got %+v", queue.jobs[0])
	}
}

func TestMemoryRememberToolRejectsMultipleActiveCircleCandidates(t *testing.T) {
	queue := &recordingMemoryUpdateQueue{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryUpdateQueue(queue)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory.remember"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:             "default",
		RequesterPersonID:       "person-1",
		Prompt:                  "@admin @hr-compensation remember this",
		ConversationChannelName: "town-square",
		PersonAccess:            policy.PersonAccess{Circles: []string{"staff", "admin", "hr-compensation"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "memory.remember",
		Input:    agent.MarshalToolInput(map[string]string{"content": "Shared fact."}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected active circle conflict, got %s", result.ContentText())
	}
	if len(queue.jobs) != 0 {
		t.Fatalf("expected no queued jobs, got %+v", queue.jobs)
	}
}

func TestMemorySearchUsesPersonAndActiveCircleNamespaces(t *testing.T) {
	memoryService := &memory.MemoryService{}
	memoryService.StoreMemoryFact(memory.MemoryFact{
		ScopeType:   memory.ScopeTypeUser,
		NamespaceID: memory.UserNamespace("person-1").NamespaceID,
		Content:     "Call the user master.",
		SourceKind:  memory.MemorySourceKindFact,
	})
	memoryService.StoreMemoryFact(memory.MemoryFact{
		ScopeType:   memory.ScopeTypeCircle,
		NamespaceID: memory.CircleNamespace("default", "hr-compensation").NamespaceID,
		Content:     "Salary files stay in HR compensation.",
		SourceKind:  memory.MemorySourceKindFact,
	})
	memoryService.StoreMemoryFact(memory.MemoryFact{
		ScopeType:   memory.ScopeTypeCircle,
		NamespaceID: memory.CircleNamespace("default", "admin").NamespaceID,
		Content:     "Admin-only operational memory.",
		SourceKind:  memory.MemorySourceKindFact,
	})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryService(memoryService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory.search"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff", "hr-compensation", "admin"},
		},
		ActiveCircleID: "hr-compensation",
		MemoryNamespaces: []memory.MemoryNamespace{
			memory.UserNamespace("person-1"),
			memory.CircleNamespace("default", "hr-compensation"),
			memory.CircleNamespace("default", "admin"),
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "memory.search",
		Input:    agent.MarshalToolInput(map[string]string{"query": "memory"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected memory.search success, got %s", result.ContentText())
	}
	if !strings.Contains(result.ContentText(), "master") || !strings.Contains(result.ContentText(), "Salary files") {
		t.Fatalf("expected person and active circle memory, got %s", result.ContentText())
	}
	if strings.Contains(result.ContentText(), "Admin-only") {
		t.Fatalf("expected inactive circle memory to be excluded, got %s", result.ContentText())
	}
}

func TestMemorySearchReturnsRecoverableToolErrorWhenGraphitiFails(t *testing.T) {
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(failingGraphMemoryStore{errorValue: errors.New("http://127.0.0.1:7791 internal graphiti failure")})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryService(memoryService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory.search"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
		MemoryNamespaces:  []memory.MemoryNamespace{memory.UserNamespace("person-1")},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "memory.search",
		Input:    agent.MarshalToolInput(map[string]string{"query": "Graphiti release notes"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected recoverable memory.search tool error, got %+v", result)
	}
	if result.FailureCode() != agent.FailureCodes.Unavailable.String() || result.FailureStage() != "graphiti_search" {
		t.Fatalf("expected structured memory search failure, got %+v", result)
	}
	if strings.Contains(result.ContentText(), "web.search") {
		t.Fatalf("expected recovery guidance to stay out of raw tool output, got %q", result.ContentText())
	}
	if strings.Contains(result.ContentText(), "127.0.0.1") || strings.Contains(result.UserSafeFailureSummary(), "127.0.0.1") {
		t.Fatalf("expected internal Graphiti details to be hidden, got %+v", result)
	}
}

func TestResolveActiveCircleIDUsesChannelOrMention(t *testing.T) {
	channelCircleID, hasChannelConflict := ResolveActiveCircleID(ToolCatalogRequest{
		ConversationChannelName: "circle-hr-compensation",
		PersonAccess:            policy.PersonAccess{Circles: []string{"staff", "hr-compensation"}},
	})
	mentionedCircleID, hasMentionConflict := ResolveActiveCircleID(ToolCatalogRequest{
		Prompt:       "please remember this for @hr-compensation",
		PersonAccess: policy.PersonAccess{Circles: []string{"staff", "hr-compensation"}},
	})

	if channelCircleID != "hr-compensation" || hasChannelConflict {
		t.Fatalf("expected channel active circle, got %q conflict=%v", channelCircleID, hasChannelConflict)
	}
	if mentionedCircleID != "hr-compensation" || hasMentionConflict {
		t.Fatalf("expected mention active circle, got %q conflict=%v", mentionedCircleID, hasMentionConflict)
	}
}

func TestResolveActiveCircleIDIgnoresInaccessibleMention(t *testing.T) {
	circleID, hasConflict := ResolveActiveCircleID(ToolCatalogRequest{
		Prompt:       "please remember this for @hr-compensation",
		PersonAccess: policy.PersonAccess{Circles: []string{"staff"}},
	})

	if circleID != "" || hasConflict {
		t.Fatalf("expected inaccessible mention to be ignored, got %q conflict=%v", circleID, hasConflict)
	}
}

func TestFileAttachToolAttachesSinglePath(t *testing.T) {
	workspacePath := t.TempDir()
	requesterDeckPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "deck")
	writeTestFile(t, filepath.Join(requesterDeckPath, "deck.pptx"), "pptx")
	writeTestFile(t, filepath.Join(requesterDeckPath, "deck.pdf"), "%PDF")
	writeTestFile(t, filepath.Join(requesterDeckPath, "deck.html"), "<html></html>")
	writeTestFile(t, filepath.Join(requesterDeckPath, "deck-notes.txt"), "notes")

	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.attach",
		Input: agent.MarshalToolInput(map[string]any{
			"path": "tmp/deck/deck.pptx",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected successful attachment result, got %s", result.ContentText())
	}
	if len(result.Attachments) != 1 {
		t.Fatalf("expected one attachment, got %+v", result.Attachments)
	}
	if result.Attachments[0].Filename != "deck.pptx" {
		t.Fatalf("expected attachment filenames to match paths, got %+v", result.Attachments)
	}
}

func TestSkillSearchToolUsesSharedRetriever(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseSkillSearch(skillSearchTestRetriever{}, func() agent.InstructionBundle {
		return agent.InstructionBundle{Skills: []agent.SkillInstruction{{
			Name:         "mail",
			Description:  "Read, search, summarize, reply to, and send email messages.",
			AllowedTools: []string{"mail.message.search", "mail.message.read"},
		}}}
	})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "skill.search",
		Input: agent.MarshalToolInput(map[string]any{
			"queries": []map[string]string{{"description": "Search and read recent email messages."}},
			"limit":   5,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected skill.search success, got %s", result.ContentText())
	}
	var resultDocument agent.SkillSearchResult
	if errorValue := json.Unmarshal([]byte(result.ContentText()), &resultDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(resultDocument.Skills) != 1 || resultDocument.Skills[0].Name != "mail" {
		t.Fatalf("expected mail skill result, got %+v", resultDocument)
	}
	if !containsTestString(resultDocument.Skills[0].Tools, "mail.message.search") {
		t.Fatalf("expected mail tools in result, got %+v", resultDocument.Skills[0].Tools)
	}
}

func TestToolDescribeReturnsHiddenRegisteredToolSchema(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{{
		Name:        "site.app.create",
		Description: "Create a site.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"],"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "tool.describe",
		Input: agent.MarshalToolInput(map[string]any{
			"toolName": "site.app.create",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected tool.describe success, got %s", result.ContentText())
	}
	if !strings.Contains(result.ContentText(), "site.app.create") || !strings.Contains(result.ContentText(), "slug") {
		t.Fatalf("expected site tool schema in result, got %s", result.ContentText())
	}
}

func TestToolCatalogHidesPolicyDeniedCapabilityTools(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{{
		Name:           "site.app.create",
		Description:    "Create a site.",
		PolicyResource: "tool:site.app.create",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"],"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
			ResourceAccessRules: []policy.ResourceAccessPolicy{{
				Resource: "tool:site.app.create",
				Actions:  []string{"execute"},
				Circles:  []string{"admin"},
			}},
		},
	})

	if strings.Contains(toolRegistry.Descriptions(), "site.app.create") {
		t.Fatalf("expected denied site tool to be omitted from catalog, got %s", toolRegistry.Descriptions())
	}
	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "tool.describe",
		Input: agent.MarshalToolInput(map[string]any{
			"toolName": "site.app.create",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if strings.Contains(result.ContentText(), "site.app.create") {
		t.Fatalf("expected denied site tool to be omitted from describe result, got %s", result.ContentText())
	}
}

func TestSkillSearchToolExactNameIncludesCompletionMetadata(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseSkillSearch(skillSearchTestRetriever{}, func() agent.InstructionBundle {
		return agent.InstructionBundle{Skills: []agent.SkillInstruction{{
			Name:         "site-prototype",
			Description:  "Create sites.",
			AllowedTools: []string{"site.app.create", "site.app.publish"},
			Completion: agent.SkillCompletion{
				RequiredEvidenceTools: []string{"site.app.publish"},
			},
			Source: agent.InstructionSource{Path: "skills/site-prototype/SKILL.md"},
		}}}
	})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "skill.search",
		Input: agent.MarshalToolInput(map[string]any{
			"queries": []map[string]string{{"description": "site-prototype"}},
			"limit":   5,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.Contains(result.ContentText(), `"sourcePath":"skills/site-prototype/SKILL.md"`) || !strings.Contains(result.ContentText(), "site.app.publish") {
		t.Fatalf("expected exact skill metadata, got %s", result.ContentText())
	}
}

func TestScheduleCreateToolStoresCurrentReplyTarget(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "schedule.create",
		Input: agent.MarshalToolInput(map[string]any{
			"name":           "daily research",
			"prompt":         "매일 중요한 업계 뉴스를 조사해서 아침 7시에 알려줘.",
			"kind":           "cron",
			"cronExpression": "0 7 * * *",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule.create success, got %s", result.ContentText())
	}
	if len(repository.taskSchedules) != 1 {
		t.Fatalf("expected one schedule, got %+v", repository.taskSchedules)
	}
	taskSchedule := repository.taskSchedules[0]
	if taskSchedule.Platform != "mattermost" || taskSchedule.ConversationID != "channel-1" || taskSchedule.ReplyTargetID != "reply-target-1" {
		t.Fatalf("expected current reply target to be stored, got %+v", taskSchedule)
	}
	if taskSchedule.Prompt != "매일 중요한 업계 뉴스를 조사해서 아침 7시에 알려줘." {
		t.Fatalf("expected arbitrary scheduled task prompt, got %q", taskSchedule.Prompt)
	}
	if taskSchedule.ExecutionMode != task.TaskScheduleExecutionModeAgent {
		t.Fatalf("expected default agent execution mode, got %+v", taskSchedule)
	}
	if taskSchedule.TimeZone != "Asia/Seoul" || taskSchedule.NextRunAt == nil {
		t.Fatalf("expected default timezone and next run, got %+v", taskSchedule)
	}
}

func TestScheduleCreateSchemaRequiresExecutionMode(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	toolDefinition, isFound := findToolDefinition(toolRegistry.ListToolDefinitions(), "schedule.create")
	if !isFound {
		t.Fatal("expected schedule.create definition")
	}
	var schema struct {
		Required []string `json:"required"`
	}
	if errorValue := json.Unmarshal(toolDefinition.InputSchema, &schema); errorValue != nil {
		t.Fatal(errorValue)
	}
	if !containsString(schema.Required, "executionMode") {
		t.Fatalf("expected executionMode to be required, got %+v", schema.Required)
	}
}

func TestScheduleCreateToolStoresMessageExecutionMode(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "schedule.create",
		Input: agent.MarshalToolInput(map[string]any{
			"prompt":         "죄송합니다.",
			"executionMode":  "message",
			"kind":           "interval",
			"intervalSecond": 60,
			"maxRunCount":    10,
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule.create success, got %s", result.ContentText())
	}
	if len(repository.taskSchedules) != 1 {
		t.Fatalf("expected one schedule, got %+v", repository.taskSchedules)
	}
	if repository.taskSchedules[0].ExecutionMode != task.TaskScheduleExecutionModeMessage {
		t.Fatalf("expected message execution mode, got %+v", repository.taskSchedules[0])
	}
	if repository.taskSchedules[0].ExpiresAt != nil {
		t.Fatalf("expected schedule expiration to default to nil, got %+v", repository.taskSchedules[0])
	}
}

func TestScheduleCancelToolCancelsRequesterSchedules(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Minute)
	repository := &memoryTaskScheduleRepository{taskSchedules: []task.TaskSchedule{{
		TaskScheduleID:   "schedule-owned",
		CreatorPersonID:  "person-1",
		ConversationID:   "channel-1",
		Prompt:           "owned",
		Kind:             task.TaskScheduleKindInterval,
		IntervalSecond:   60,
		NextRunAt:        &nextRunAt,
		ExpiresAt:        timePointer(nextRunAt.Add(time.Hour)),
		AgentProfileName: "default",
	}, {
		TaskScheduleID:   "schedule-other",
		CreatorPersonID:  "person-2",
		ConversationID:   "channel-1",
		Prompt:           "other",
		Kind:             task.TaskScheduleKindInterval,
		IntervalSecond:   60,
		NextRunAt:        &nextRunAt,
		ExpiresAt:        timePointer(nextRunAt.Add(time.Hour)),
		AgentProfileName: "default",
	}}}
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	waitingTaskRun := taskRunService.CreateTaskRun("person-1", "channel-1", "승인 필요")
	if _, errorValue := taskRunService.PauseTaskRun(waitingTaskRun.TaskRunID, task.TaskStatusWaitingApproval, "approval"); errorValue != nil {
		t.Fatal(errorValue)
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.cancel"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "channel-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "schedule.cancel",
		Input: agent.MarshalToolInput(map[string]any{
			"scope": "mine",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule.cancel success, got %s", result.ContentText())
	}
	if !strings.Contains(result.ContentText(), `"cancelledScheduleCount":1`) || !strings.Contains(result.ContentText(), `"cancelledWaitCount":1`) {
		t.Fatalf("expected one schedule and one wait cancelled, got %s", result.ContentText())
	}
	ownedSchedule := repository.taskSchedules[1]
	if ownedSchedule.TaskScheduleID != "schedule-owned" || ownedSchedule.NextRunAt != nil || ownedSchedule.ExpiresAt == nil || ownedSchedule.ExpiresAt.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("expected owned schedule to expire, got %+v", ownedSchedule)
	}
	otherSchedule := repository.taskSchedules[0]
	if otherSchedule.TaskScheduleID != "schedule-other" || otherSchedule.NextRunAt == nil {
		t.Fatalf("expected other schedule to remain active, got %+v", otherSchedule)
	}
	cancelledTaskRun, isFound := taskRunService.FindTaskRun(waitingTaskRun.TaskRunID)
	if !isFound || cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected waiting task run to be cancelled, got found=%v task=%+v", isFound, cancelledTaskRun)
	}
}

func TestPlatformDMSendAvailabilityDependsOnTrustedContext(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{{
		Name:             "platform.dm.send",
		Description:      "Send a direct message",
		RequiresApproval: true,
	}})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"platform.dm.send"})

	immediateToolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	if !strings.Contains(immediateToolSet.Descriptions(), "ask approval before invoking") {
		t.Fatalf("expected immediate DM to ask approval, got %s", immediateToolSet.Descriptions())
	}
	scheduledToolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default", IsScheduledRun: true})
	if strings.Contains(scheduledToolSet.Descriptions(), "ask approval before invoking") {
		t.Fatalf("expected scheduled DM to be available, got %s", scheduledToolSet.Descriptions())
	}
	approvedToolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default", IsApprovalContinuation: true})
	if strings.Contains(approvedToolSet.Descriptions(), "ask approval before invoking") {
		t.Fatalf("expected approved continuation DM to be available, got %s", approvedToolSet.Descriptions())
	}
}

func TestCapabilityToolRequestIncludesTrustedExecutionContext(t *testing.T) {
	requestDocument := capabilityToolRequest("platform.dm.send", ToolCatalogRequest{
		TaskSource:              TaskLaunchSourceScheduled,
		IsScheduledRun:          true,
		IsApprovalContinuation:  true,
		RequesterPersonID:       "person-1",
		RequesterPlatformUserID: "mattermost-user-1",
		ConversationID:          "conversation-1",
		ConversationChannelID:   "channel-1",
		ReplyTargetID:           "reply-target-1",
		Platform:                "mattermost",
	}, json.RawMessage(`{"recipientHint":"샘플","message":"테스트"}`))
	contextDocument, isFound := requestDocument["context"].(map[string]any)
	if !isFound {
		t.Fatalf("expected context document, got %+v", requestDocument)
	}
	if contextDocument["taskSource"] != string(TaskLaunchSourceScheduled) || contextDocument["isScheduledRun"] != true || contextDocument["isApprovalContinuation"] != true {
		t.Fatalf("expected trusted execution context, got %+v", contextDocument)
	}
	if contextDocument["replyTargetID"] != "reply-target-1" {
		t.Fatalf("expected reply target in context, got %+v", contextDocument)
	}
}

func TestScheduleCreateToolInfersIntervalFromPrompt(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		Prompt:            "1분마다 \"1분 지났습니다\"라고 보내줘",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "schedule.create",
		Input: agent.MarshalToolInput(map[string]any{
			"prompt":      "1분 지났습니다",
			"kind":        "interval",
			"timeZone":    "Asia/Seoul",
			"maxRunCount": 10,
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule.create success, got %s", result.ContentText())
	}
	if len(repository.taskSchedules) != 1 {
		t.Fatalf("expected one schedule, got %+v", repository.taskSchedules)
	}
	if repository.taskSchedules[0].IntervalSecond != 60 {
		t.Fatalf("expected inferred one minute interval, got %+v", repository.taskSchedules[0])
	}
}

func TestScheduleCreateToolStoresMaxRunCount(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		Prompt:            `1분에 한 번씩 나한테 "죄송합니다" 10번 해봐`,
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "schedule.create",
		Input: agent.MarshalToolInput(map[string]any{
			"prompt":         "죄송합니다라고 말해줘.",
			"kind":           "interval",
			"intervalSecond": 60,
			"maxRunCount":    10,
			"timeZone":       "Asia/Seoul",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule.create success, got %s", result.ContentText())
	}
	if len(repository.taskSchedules) != 1 {
		t.Fatalf("expected one schedule, got %+v", repository.taskSchedules)
	}
	if repository.taskSchedules[0].MaxRunCount != 10 {
		t.Fatalf("expected max run count 10, got %+v", repository.taskSchedules[0])
	}
}

func TestScheduleCancelToolCancelsActiveScheduledTaskRuns(t *testing.T) {
	runAt := time.Now().UTC().Add(time.Hour)
	repository := &memoryTaskScheduleRepository{taskSchedules: []task.TaskSchedule{{
		TaskScheduleID:  "schedule-1",
		CreatorPersonID: "person-1",
		Prompt:          "테스트",
		Platform:        "mattermost",
		ConversationID:  "channel-1",
		ReplyTargetID:   "reply-target-1",
		TimeZone:        "Asia/Seoul",
		Kind:            task.TaskScheduleKindOnce,
		RunAt:           &runAt,
		NextRunAt:       &runAt,
		ExpiresAt:       timePointer(runAt.Add(time.Hour)),
	}}}
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "schedule:schedule-1", "테스트")
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.cancel"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "schedule.cancel",
		Input:    agent.MarshalToolInput(map[string]any{"scope": "mine"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule.cancel success, got %s", result.ContentText())
	}
	cancelledTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if !isFound || cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected scheduled task run to be cancelled, got found=%v run=%+v", isFound, cancelledTaskRun)
	}
}

func TestScheduleCreateToolRejectsMissingReplyTarget(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(&memoryTaskScheduleRepository{})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule.create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "schedule.create",
		Input: agent.MarshalToolInput(map[string]any{
			"prompt":         "오늘 일정을 보고 매일 아침 브리핑해줘.",
			"kind":           "cron",
			"cronExpression": "0 7 * * *",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || !strings.Contains(result.ContentText(), "reply target") {
		t.Fatalf("expected reply target error, got %+v", result)
	}
}

type memoryTaskScheduleRepository struct {
	taskSchedules []task.TaskSchedule
	failed        []string
}

type recordingMemoryUpdateQueue struct {
	jobs []memory.MemoryUpdateJob
}

func (queue *recordingMemoryUpdateQueue) Enqueue(job memory.MemoryUpdateJob) (memory.MemoryUpdateAccepted, error) {
	queue.jobs = append(queue.jobs, job)
	return memory.MemoryUpdateAccepted{Accepted: true, JobID: "job-1"}, nil
}

func (repository *memoryTaskScheduleRepository) UpsertTaskSchedule(taskSchedule task.TaskSchedule) error {
	repository.taskSchedules = append(repository.taskSchedules, taskSchedule)
	return nil
}

func (repository *memoryTaskScheduleRepository) ClaimDueTaskSchedules(int, time.Duration, time.Time, string) ([]task.TaskSchedule, error) {
	return append([]task.TaskSchedule{}, repository.taskSchedules...), nil
}

func (repository *memoryTaskScheduleRepository) MarkTaskScheduleSucceeded(taskSchedule task.TaskSchedule) error {
	repository.taskSchedules = []task.TaskSchedule{taskSchedule}
	return nil
}

func (repository *memoryTaskScheduleRepository) MarkTaskScheduleFailed(_ task.TaskSchedule, errorMessage string, _ time.Time) error {
	repository.failed = append(repository.failed, errorMessage)
	return nil
}

func (repository *memoryTaskScheduleRepository) CancelTaskSchedules(request task.TaskScheduleCancelRequest) (task.TaskScheduleCancelResult, error) {
	cancelledTaskSchedules := []task.TaskSchedule{}
	remainingTaskSchedules := []task.TaskSchedule{}
	for _, taskSchedule := range repository.taskSchedules {
		if memoryTaskScheduleMatchesCancelRequest(taskSchedule, request) {
			taskSchedule.ExpiresAt = timePointer(request.CancelledAt)
			taskSchedule.NextRunAt = nil
			cancelledTaskSchedules = append(cancelledTaskSchedules, taskSchedule)
			continue
		}
		remainingTaskSchedules = append(remainingTaskSchedules, taskSchedule)
	}
	repository.taskSchedules = append(remainingTaskSchedules, cancelledTaskSchedules...)
	return task.TaskScheduleCancelResult{TaskSchedules: cancelledTaskSchedules}, nil
}

func memoryTaskScheduleMatchesCancelRequest(taskSchedule task.TaskSchedule, request task.TaskScheduleCancelRequest) bool {
	if taskSchedule.CreatorPersonID != request.RequesterPersonID || taskSchedule.NextRunAt == nil {
		return false
	}
	switch request.Scope {
	case task.TaskScheduleCancelScopeCurrentConversation:
		return taskSchedule.ConversationID == request.ConversationID
	case task.TaskScheduleCancelScopeScheduleIDs:
		return containsString(request.TaskScheduleIDs, taskSchedule.TaskScheduleID)
	default:
		return true
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func TestFileToolsAcceptVirtualHomePathsWithoutLeakingHostPath(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	writeResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "home/projects/deck/presentation.md",
			"content": "# Deck",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.Failed() {
		t.Fatalf("expected file.write success, got %s", writeResult.ContentText())
	}
	if strings.Contains(writeResult.ContentText(), workspacePath) {
		t.Fatalf("expected file.write result not to expose host path, got %s", writeResult.ContentText())
	}
	if _, errorValue := os.Stat(filepath.Join(workspacePath, "private", "people", "person-1", "projects", "deck", "presentation.md")); errorValue != nil {
		t.Fatal(errorValue)
	}

	attachResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.attach",
		Input: agent.MarshalToolInput(map[string]string{
			"path": "home/projects/deck/presentation.md",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if attachResult.Failed() {
		t.Fatalf("expected file.attach success, got %s", attachResult.ContentText())
	}
	expectedDevicePath := "/workspace/private/people/person-1/projects/deck/presentation.md"
	if attachResult.Attachments[0].DevicePath != expectedDevicePath {
		t.Fatalf("expected agent workspace device path, got %+v", attachResult.Attachments[0])
	}
}

func TestFileWriteAcceptsPortablePathAndContent(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	writeResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]any{
			"path":    "home/projects/site/index.html",
			"content": "<html>ready</html>",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.Failed() {
		t.Fatalf("expected file.write success, got %s", writeResult.ContentText())
	}
	document, errorValue := os.ReadFile(filepath.Join(workspacePath, "private", "people", "person-1", "projects", "site", "index.html"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(document) != "<html>ready</html>" {
		t.Fatalf("expected content to be written, got %q", string(document))
	}
}

func TestFileToolsDenyCirclePathForNonMember(t *testing.T) {
	workspacePath := t.TempDir()
	financeDirectoryPath := filepath.Join(workspacePath, "circles", "finance")
	if errorValue := os.MkdirAll(financeDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, filepath.Join(financeDirectoryPath, "report.md"), "secret")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	writeResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "/workspace/circles/finance/report.md",
			"content": "changed",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !writeResult.Failed() || !strings.Contains(writeResult.ContentText(), "cannot write") {
		t.Fatalf("expected file.write denial, got %+v", writeResult)
	}

	attachResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.attach",
		Input: agent.MarshalToolInput(map[string]string{
			"path": "/workspace/circles/finance/report.md",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !attachResult.Failed() || !strings.Contains(attachResult.ContentText(), "cannot read") {
		t.Fatalf("expected file.attach read denial, got %+v", attachResult)
	}
}

func TestFileReadCapabilityDenyCirclePathForNonMember(t *testing.T) {
	workspacePath := t.TempDir()
	financeDirectoryPath := filepath.Join(workspacePath, "circles", "finance")
	if errorValue := os.MkdirAll(financeDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, filepath.Join(financeDirectoryPath, "report.pdf"), "secret")
	httpClient := &recordingHTTPClient{}
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "file.read",
		PolicyResource: "tool:file.read",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.read",
		Input: agent.MarshalToolInput(map[string]string{
			"path": "/workspace/circles/finance/report.pdf",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || !strings.Contains(result.ContentText(), "cannot read") {
		t.Fatalf("expected read denial, got %+v", result)
	}
	if httpClient.requestPath != "" {
		t.Fatalf("expected denied file.read not to call capability bridge, got path=%s", httpClient.requestPath)
	}
}

func TestFileReadCapabilityAllowCirclePathForMember(t *testing.T) {
	workspacePath := t.TempDir()
	financeDirectoryPath := filepath.Join(workspacePath, "circles", "finance")
	if errorValue := os.MkdirAll(financeDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, filepath.Join(financeDirectoryPath, "report.md"), "secret")
	httpClient := &recordingHTTPClient{responseBody: `{"content":"# Report","status":"ok","result":{"content":"# Report"}}`}
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "file.read",
		PolicyResource: "tool:file.read",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff", "finance"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.read",
		Input: agent.MarshalToolInput(map[string]string{
			"path": "/workspace/circles/finance/report.md",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() || !strings.Contains(result.ContentText(), `"content":"secret"`) {
		t.Fatalf("expected file.read success, got %+v", result)
	}
	if httpClient.requestPath != "" {
		t.Fatalf("expected built-in file.read not to call capability bridge, got path=%s body=%s", httpClient.requestPath, httpClient.requestBody)
	}
}

func TestFileToolsAllowCirclePathForMember(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff", "finance"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "/workspace/circles/finance/report.md",
			"content": "finance",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected finance member write success, got %+v", result)
	}
}

func TestFileWriteDefaultsToPrivateScopeForDirectMessage(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "notes.md",
			"content": "private",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected private write success, got %+v", result)
	}
	expectedPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "notes.md")
	if document, errorValue := os.ReadFile(expectedPath); errorValue != nil || string(document) != "private" {
		t.Fatalf("expected private file at %s, got %q and %v", expectedPath, string(document), errorValue)
	}
	if !strings.Contains(result.ContentText(), `"tmp/notes.md"`) {
		t.Fatalf("expected private agent path in result, got %s", result.ContentText())
	}
}

func TestFileWriteDefaultsToCircleScopeForCircleChannel(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:             "default",
		RequesterPersonID:       "person-1",
		ConversationID:          "channel:channel-1",
		ConversationType:        "P",
		ConversationChannelID:   "channel-1",
		ConversationChannelName: "circle-finance",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff", "finance"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "report.md",
			"content": "finance",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected circle write success, got %+v", result)
	}
	expectedPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "report.md")
	if document, errorValue := os.ReadFile(expectedPath); errorValue != nil || string(document) != "finance" {
		t.Fatalf("expected finance file at %s, got %q and %v", expectedPath, string(document), errorValue)
	}
}

func TestFileWriteDefaultsToStaffScopeForGeneralChannel(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:             "default",
		RequesterPersonID:       "person-1",
		ConversationID:          "thread:channel-1:post-1",
		ConversationType:        "O",
		ConversationChannelID:   "channel-1",
		ConversationChannelName: "town-square",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "status.md",
			"content": "staff",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected staff write success, got %+v", result)
	}
	expectedPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "status.md")
	if document, errorValue := os.ReadFile(expectedPath); errorValue != nil || string(document) != "staff" {
		t.Fatalf("expected staff file at %s, got %q and %v", expectedPath, string(document), errorValue)
	}
}

func TestFileAttachDefaultsToPrivateScopeForDirectMessage(t *testing.T) {
	workspacePath := t.TempDir()
	privateDirectoryPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp")
	if errorValue := os.MkdirAll(privateDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, filepath.Join(privateDirectoryPath, "notes.md"), "private")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.attach",
		Input: agent.MarshalToolInput(map[string]string{
			"path": "notes.md",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected private attach success, got %+v", result)
	}
	if result.Attachments[0].DevicePath != "/workspace/private/people/person-1/tmp/notes.md" {
		t.Fatalf("expected private device path, got %+v", result.Attachments[0])
	}
}

func TestFilePromoteCopiesDraftOutputToArtifacts(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	writeResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "tmp/deck/build/deck.pptx",
			"content": "pptx",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.Failed() {
		t.Fatalf("expected file.write success, got %s", writeResult.ContentText())
	}
	promoteResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.promote",
		Input: agent.MarshalToolInput(map[string]any{
			"path":                     "tmp/deck/build/deck.pptx",
			"destinationDirectoryPath": "artifacts/deck",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if promoteResult.Failed() {
		t.Fatalf("expected file.promote success, got %s", promoteResult.ContentText())
	}
	expectedPath := filepath.Join(workspacePath, "private", "people", "person-1", "artifacts", "deck", "deck.pptx")
	if document, errorValue := os.ReadFile(expectedPath); errorValue != nil || string(document) != "pptx" {
		t.Fatalf("expected promoted file at %s, got %q and %v", expectedPath, string(document), errorValue)
	}
	if !strings.Contains(promoteResult.ContentText(), `"artifacts/deck/deck.pptx"`) {
		t.Fatalf("expected promoted virtual path, got %s", promoteResult.ContentText())
	}
}

func TestWorkspacePathResolverRejectsDeniedPrefixes(t *testing.T) {
	workspacePath := t.TempDir()
	resolver := NewWorkspacePathResolver(workspacePath)
	scope := WorkspaceScopeForRequest(workspacePath, ToolCatalogRequest{RequesterPersonID: "person-1"}, "task-1")
	for _, path := range []string{"/tmp/a", "~/.cache/a", "/workspace/.blueclaw/tmp/a", "/workspace/private/people/person-1/tmp/a", "/workspace/private/people/person-2/tmp/a", "../escape"} {
		if _, errorValue := resolver.Resolve(path, scope); errorValue == nil {
			t.Fatalf("expected resolver to reject %q", path)
		}
	}
}

func TestWorkspacePathResolverMapsVirtualHomeToRequesterPrivateRoot(t *testing.T) {
	workspacePath := t.TempDir()
	resolver := NewWorkspacePathResolver(workspacePath)
	scope := WorkspaceScopeForRequest(workspacePath, ToolCatalogRequest{RequesterPersonID: "person-1"}, "task-1")
	resolvedPath, errorValue := resolver.Resolve("home/sites/site-1/DESIGN.md", scope)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if resolvedPath.VirtualPath != "home/sites/site-1/DESIGN.md" {
		t.Fatalf("unexpected virtual path: %+v", resolvedPath)
	}
	expectedConcretePath := filepath.Join(workspacePath, "private", "people", "person-1", "sites", "site-1", "DESIGN.md")
	if resolvedPath.ConcretePath != expectedConcretePath {
		t.Fatalf("unexpected concrete path: %+v", resolvedPath)
	}
}

func TestWorkspaceScopeEnvironmentStaysUnderRequesterPrivateRoot(t *testing.T) {
	workspacePath := t.TempDir()
	scope := WorkspaceScopeForRequest(workspacePath, ToolCatalogRequest{RequesterPersonID: "person-1"}, "task-1")
	environmentVariables := scope.EnvironmentVariables()
	requesterRootPath := filepath.Join(workspacePath, "private", "people", "person-1")
	taskRuntimeRootPath := filepath.Join(requesterRootPath, "tmp", "task-1", ".runtime")
	if environmentVariables["PATH"] != security.CanonicalRuntimePATH {
		t.Fatalf("expected canonical runtime PATH, got %+v", environmentVariables)
	}
	expectedEnvironmentPaths := map[string]string{
		"HOME":                  requesterRootPath,
		"TMPDIR":                filepath.Join(taskRuntimeRootPath, "tmp"),
		"TMP":                   filepath.Join(taskRuntimeRootPath, "tmp"),
		"TEMP":                  filepath.Join(taskRuntimeRootPath, "tmp"),
		"XDG_CACHE_HOME":        filepath.Join(taskRuntimeRootPath, "cache"),
		"XDG_CONFIG_HOME":       filepath.Join(taskRuntimeRootPath, "config"),
		"XDG_RUNTIME_DIR":       filepath.Join(taskRuntimeRootPath, "runtime"),
		"BUN_TMPDIR":            filepath.Join(taskRuntimeRootPath, "bun", "tmp"),
		"BUN_INSTALL":           filepath.Join(taskRuntimeRootPath, "bun", "install"),
		"BUN_INSTALL_CACHE_DIR": filepath.Join(taskRuntimeRootPath, "bun", "cache"),
		"npm_config_cache":      filepath.Join(taskRuntimeRootPath, "npm"),
	}
	for name, expectedPath := range expectedEnvironmentPaths {
		actualPath := environmentVariables[name]
		if actualPath != expectedPath {
			t.Fatalf("expected %s to be %s, got %s", name, expectedPath, actualPath)
		}
		if name != "HOME" && !strings.HasPrefix(actualPath, requesterRootPath+string(filepath.Separator)) {
			t.Fatalf("expected %s to stay under requester root, got %s", name, actualPath)
		}
		for _, deniedPrefix := range []string{"/tmp", "/opt", filepath.Join(workspacePath, "tmp"), filepath.Join(workspacePath, ".blueclaw")} {
			if actualPath == deniedPrefix || strings.HasPrefix(actualPath, deniedPrefix+string(filepath.Separator)) {
				t.Fatalf("expected %s to avoid denied prefix %s, got %s", name, deniedPrefix, actualPath)
			}
		}
	}
}

func TestTerminalRunTranslatesAgentWorkspacePaths(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"command":              "mkdir -p build && printf ok > build/result.txt",
			"workingDirectoryPath": "tmp/deck",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected terminal.run success, got %s", result.ContentText())
	}
	content, errorValue := os.ReadFile(filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "deck", "build", "result.txt"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(content) != "ok" {
		t.Fatalf("expected translated workspace command to write file, got %q", string(content))
	}
}

func TestTerminalRunAllowsServiceOwnedPathText(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"command": "printf '%s' /workspace/.blueclaw/tmp/deck",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected service-owned path text not to be policy-blocked, got %+v", result)
	}
}

func TestTerminalRunPathGuardrailFailureIsRecoverable(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"command": "/opt/blueclaw/builtin-skills-venv/bin/python --version",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected terminal.run path guardrail failure, got %+v", result)
	}
	if result.Failure.Code != agent.FailureCodes.InvalidInput.String() || result.Failure.Stage != "terminal_path_guardrail" {
		t.Fatalf("expected recoverable invalid input failure, got %+v", result.Failure)
	}
	if !result.Failure.Retryable || !result.Failure.SafeRetry {
		t.Fatalf("expected path guardrail failure to be retryable, got %+v", result.Failure)
	}
	for _, expectedText := range []string{
		"/opt/blueclaw/builtin-skills-venv/bin/python",
		"/workspace/skills/<skill>/scripts/skill_runtime.py",
	} {
		if !strings.Contains(result.ContentText(), expectedText) {
			t.Fatalf("expected result to contain %q, got %q", expectedText, result.ContentText())
		}
	}
}

func TestSitePublishInputIncludesEditableWorkspaceBundle(t *testing.T) {
	workspacePath := t.TempDir()
	sourceWorkspacePath := filepath.Join(workspacePath, "private", "people", "person-1", "sites", "site-1", "draft")
	writeTestFile(t, filepath.Join(sourceWorkspacePath, "app", "dist", "index.html"), "<html>ok</html>")
	writeTestFile(t, filepath.Join(sourceWorkspacePath, "app", "node_modules", "ignored.js"), "ignored")
	writeTestFile(t, filepath.Join(sourceWorkspacePath, "DESIGN.md"), "custom design")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)

	toolInput, errorValue := toolCatalogBuilder.enrichCapabilityToolInput("site.app.publish", ToolCatalogRequest{
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	}, agent.MarshalToolInput(map[string]any{"siteID": "site-1", "sourceWorkspacePath": "home/sites/site-1"}))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var inputDocument map[string]any
	if errorValue := json.Unmarshal(toolInput, &inputDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	if inputDocument["sourceWorkspacePath"] != "home/sites/site-1/draft" {
		t.Fatalf("unexpected source workspace path: %+v", inputDocument)
	}
	if inputDocument["sourceBundleFormat"] != "tar.gz" {
		t.Fatalf("unexpected source bundle format: %+v", inputDocument)
	}
	bundledPaths := siteSourceBundlePaths(t, inputDocument["sourceBundleBase64"].(string))
	if !containsTestString(bundledPaths, "app/dist/index.html") || !containsTestString(bundledPaths, "DESIGN.md") {
		t.Fatalf("expected source files in bundle, got %+v", bundledPaths)
	}
	if containsTestString(bundledPaths, "app/node_modules/ignored.js") {
		t.Fatalf("expected node_modules to be omitted from bundle: %+v", bundledPaths)
	}
}

func TestSiteReactScaffoldIncludesManagedBuildQualityContract(t *testing.T) {
	files := siteAppScaffoldFiles(siteCreateResult{Slug: "demo-site", Title: "Demo Site"})
	fileMap := map[string]string{}
	for _, file := range files {
		fileMap[file.Path] = file.Content
	}
	for _, path := range []string{
		"app/package.json",
		"app/index.html",
		"app/scripts/build.ts",
		"app/tsconfig.json",
		"app/vite.config.ts",
		"app/src/App.tsx",
		"app/src/main.tsx",
		"app/src/index.css",
		"app/src/prototype-data.ts",
	} {
		if strings.TrimSpace(fileMap[path]) == "" {
			t.Fatalf("expected React scaffold file %s", path)
		}
	}
	if _, exists := fileMap["app/src/content.html"]; exists {
		t.Fatalf("legacy HTML scaffold file should not be present")
	}
	for _, expectedText := range []string{`"react"`, `"vite"`, `"@vitejs/plugin-react"`, `"bun scripts/build.ts"`} {
		if !strings.Contains(fileMap["app/package.json"], expectedText) {
			t.Fatalf("site package manifest must contain %q", expectedText)
		}
	}
	if strings.Contains(fileMap["app/package.json"], "@google/design.md") {
		t.Fatalf("site package manifest must not depend on nested design.md CLI")
	}
	if strings.Contains(fileMap["app/package.json"], `": "^`) {
		t.Fatalf("site package manifest must pin exact dependency versions")
	}
	buildScript := fileMap["app/scripts/build.ts"]
	if strings.Contains(buildScript, "site quality gate failed") {
		t.Fatalf("build script must report quality issues without failing the build")
	}
	if !strings.Contains(buildScript, "suggestedFix") {
		t.Fatalf("build script must include actionable quality fixes")
	}
	for _, forbiddenText := range []string{`Bun.execPath`, `name: "bunx"`} {
		if strings.Contains(buildScript, forbiddenText) {
			t.Fatalf("build script must rely on canonical runtime PATH, not %q", forbiddenText)
		}
	}
	if !strings.Contains(buildScript, `PATH: canonicalRuntimePATH`) {
		t.Fatalf("build script must pass canonical PATH to child commands")
	}
	if strings.Contains(buildScript, `existsSync("node_modules")`) {
		t.Fatalf("build script must refresh dependencies instead of trusting stale node_modules")
	}
	if !strings.Contains(buildScript, `collectDesignQualityIssues`) || !strings.Contains(buildScript, `category: "designDocument"`) {
		t.Fatalf("build script must report DESIGN.md quality issues in-process")
	}
	if strings.Contains(buildScript, "@google/design.md") {
		t.Fatalf("build script must not spawn nested design.md CLI")
	}
	if strings.Contains(buildScript, "DESIGN.md lint failed") {
		t.Fatalf("build script must not fail solely because DESIGN.md quality issues were reported")
	}
	if strings.Contains(buildScript, "DESIGN.md is required") {
		t.Fatalf("build script must not fail solely because DESIGN.md is missing")
	}
	if !strings.Contains(buildScript, `await buildVite();`) {
		t.Fatalf("build script must call Vite in-process")
	}
	if strings.Contains(buildScript, `arguments: ["x", "vite", "build"]`) {
		t.Fatalf("build script must not spawn nested bun x vite")
	}
	viteIndex := strings.Index(buildScript, `await buildVite();`)
	qualityIndex := strings.LastIndex(buildScript, "writeBuildQuality(qualityIssues);")
	if viteIndex < 0 || qualityIndex < viteIndex {
		t.Fatalf("build script must write build-quality.json after vite build")
	}
}

func TestSuccessfulSiteBuildQualityNormalizesAfterBuild(t *testing.T) {
	workspacePath := t.TempDir()
	factory := actortest.NewDirectWorkspaceActorFactory()
	workspaceActor, errorValue := factory.Requester(context.Background(), security.WorkspaceActorRequest{
		WorkspaceRootPath: workspacePath,
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	qualityPath := workspacepath.Path{
		ConcretePath: filepath.Join(workspacePath, "sites", "site-1", ".internkim", "build-quality.json"),
		VirtualPath:  "/workspace/sites/site-1/.internkim/build-quality.json",
	}
	if toolFailure := writeSuccessfulSiteBuildQuality(context.Background(), workspaceActor, qualityPath); toolFailure != nil {
		t.Fatalf("unexpected quality write failure: %s", toolFailure.ContentText())
	}
	document, errorValue := os.ReadFile(qualityPath.ConcretePath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.Contains(string(document), `"postBuildNormalized": true`) || !strings.Contains(string(document), `"blockingIssueCount": 0`) {
		t.Fatalf("unexpected normalized build quality: %s", string(document))
	}
}

func TestSiteBuildQualityPayloadReportsIssuesAsSuccessData(t *testing.T) {
	workspacePath := t.TempDir()
	sourceWorkspacePath := filepath.Join(workspacePath, "sites", "site-1")
	writeTestFile(t, filepath.Join(sourceWorkspacePath, ".internkim", "build-quality.json"), `{
  "blockingIssueCount": 1,
  "issues": [
    {
      "severity": "blocking",
      "category": "templateSmell",
      "target": "src/App.tsx",
      "message": "Replace the scaffold starter.",
      "suggestedFix": "Use a domain-specific first screen."
    }
  ]
}`)
	factory := actortest.NewDirectWorkspaceActorFactory()
	workspaceActor, errorValue := factory.Requester(context.Background(), security.WorkspaceActorRequest{
		WorkspaceRootPath: workspacePath,
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	sourceWorkspace := workspacepath.Path{
		ConcretePath: sourceWorkspacePath,
		VirtualPath:  "/workspace/sites/site-1",
	}
	appWorkspace := workspacepath.Path{
		ConcretePath: filepath.Join(sourceWorkspacePath, "app"),
		VirtualPath:  "/workspace/sites/site-1/app",
	}

	payload := siteBuildQualityPayload(context.Background(), workspaceActor, sourceWorkspace, appWorkspace)
	if payload["qualityStatus"] != "delivery_blocked" || payload["qualityIssueCount"] != 1 || payload["blockingIssueCount"] != 1 {
		t.Fatalf("expected quality issue payload, got %+v", payload)
	}
	if payload["deliveryBlocked"] != true {
		t.Fatalf("expected starter leakage to block delivery, got %+v", payload)
	}
	targets, _ := payload["editableTargets"].([]string)
	if !containsTestString(targets, "/workspace/sites/site-1/app/src/App.tsx") {
		t.Fatalf("expected editable target, got %+v", payload)
	}
	if _, exists := payload["recommendedNextActions"]; !exists {
		t.Fatalf("expected recommended next actions, got %+v", payload)
	}
}

func TestSiteDeliveryBlockedBuildResultCreatesRecoveryFailure(t *testing.T) {
	result := siteDeliveryBlockedBuildResult(map[string]any{
		"deliveryBlocked": true,
		"deliveryBlockers": []string{
			"src/App.tsx: Replace the scaffold starter.",
		},
		"editableTargets": []string{"/workspace/sites/site-1/app/src/App.tsx"},
	})
	if result.Failure == nil {
		t.Fatal("expected delivery blocked build to create recoverable failure")
	}
	if result.Failure.Stage != "site_build_delivery" {
		t.Fatalf("expected site_build_delivery failure, got %+v", result.Failure)
	}
	if !containsTestString(result.Failure.RequiredPreconditions, "source_changed") {
		t.Fatalf("expected source_changed precondition, got %+v", result.Failure.RequiredPreconditions)
	}
	if len(result.Failure.RecoveryHints) == 0 || !containsTestString(result.Failure.RecoveryHints[0].ToolNames, "file.write") {
		t.Fatalf("expected file.write recovery hint, got %+v", result.Failure.RecoveryHints)
	}
	if len(result.Failure.AffectedResources) != 1 || result.Failure.AffectedResources[0].Path != "/workspace/sites/site-1/app/src/App.tsx" {
		t.Fatalf("expected affected source resource, got %+v", result.Failure.AffectedResources)
	}
}

func TestSiteBuildCommandFailureClassifiesSourceSyntaxErrors(t *testing.T) {
	result := siteBuildCommandFailureResult(agent.ToolFailureResult(agent.FailureExternalService, agent.FailureCodes.OperationFailed, "terminal_run", `/workspace/private/people/person/sites/site-1/draft/app/src/App.tsx:1:27: ERROR: Syntax error "n"`), workspacepath.Path{
		VirtualPath: "home/sites/site-1/draft/app",
	})
	if result.Failure == nil {
		t.Fatal("expected source syntax failure")
	}
	if result.Failure.Stage != "site_build_source" {
		t.Fatalf("expected site_build_source failure, got %+v", result.Failure)
	}
	if !containsTestString(result.Failure.RequiredPreconditions, "source_changed") {
		t.Fatalf("expected source_changed precondition, got %+v", result.Failure.RequiredPreconditions)
	}
	if len(result.Failure.RecoveryHints) == 0 || !containsTestString(result.Failure.RecoveryHints[0].ToolNames, "file.write") {
		t.Fatalf("expected file.write recovery hint, got %+v", result.Failure.RecoveryHints)
	}
	if len(result.Failure.AffectedResources) != 1 || result.Failure.AffectedResources[0].Path != "home/sites/site-1/draft/app/src/App.tsx" {
		t.Fatalf("expected App.tsx affected resource, got %+v", result.Failure.AffectedResources)
	}
}

func TestSiteCreateMaterializesEditableSourceWithRequesterActor(t *testing.T) {
	workspacePath := t.TempDir()
	httpClient := &recordingHTTPClient{responseBody: `{"status":"ok","result":{"siteID":"site-1","slug":"demo","title":"Demo","description":"Demo site description","idea":"Demo site idea","purpose":"portfolio","audience":"buyers","archetype":"portfolio","publishedURL":"https://demo.device.example.test","sourceWorkspacePath":"home/sites/site-1/draft","workspacePath":"home/sites/site-1","status":"draft","ownerIdentity":{"personID":"person-1","displayName":"Owner"}}}`}
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"site.app.create", "file.read"})
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "site.app.create",
		PolicyResource: "tool:site.app.create",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"],"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "site.app.create",
		Input:    agent.MarshalToolInput(map[string]string{"slug": "demo"}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected site.app.create success, got %s", result.ContentText())
	}
	sourceWorkspacePath := filepath.Join(workspacePath, "private", "people", "person-1", "sites", "site-1", "draft")
	for _, relativePath := range []string{".internkim/site.json", ".internkim/idea.md", "DESIGN.md", "app/package.json", "app/scripts/build.ts", "app/src/App.tsx", "app/src/main.tsx", "app/src/index.css", "app/src/prototype-data.ts"} {
		if _, errorValue := os.Stat(filepath.Join(sourceWorkspacePath, relativePath)); errorValue != nil {
			t.Fatalf("expected materialized source file %s: %v", relativePath, errorValue)
		}
	}
	if _, errorValue := os.Stat(filepath.Join(sourceWorkspacePath, "pocketbase", "pb_hooks", ".gitkeep")); !os.IsNotExist(errorValue) {
		t.Fatalf("site scaffold must not create PocketBase hooks by default: %v", errorValue)
	}
	packageDocument, errorValue := os.ReadFile(filepath.Join(sourceWorkspacePath, "app", "package.json"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.Contains(string(packageDocument), `"build": "bun scripts/build.ts"`) || strings.Contains(string(packageDocument), "latest") || !strings.Contains(string(packageDocument), `"react"`) {
		t.Fatalf("expected React scaffold package manifest, got %s", string(packageDocument))
	}
	metadataDocument, errorValue := os.ReadFile(filepath.Join(sourceWorkspacePath, ".internkim", "site.json"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.Contains(string(metadataDocument), `"idea": "Demo site idea"`) || !strings.Contains(string(metadataDocument), `"archetype": "portfolio"`) || !strings.Contains(string(metadataDocument), `"owner"`) {
		t.Fatalf("expected site metadata mirror, got %s", string(metadataDocument))
	}
	ideaDocument, errorValue := os.ReadFile(filepath.Join(sourceWorkspacePath, ".internkim", "idea.md"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.Contains(string(ideaDocument), "Demo site idea") {
		t.Fatalf("expected site idea mirror, got %s", string(ideaDocument))
	}
	if !strings.Contains(result.ContentText(), `"sourceWorkspacePath":"home/sites/site-1/draft"`) ||
		!strings.Contains(result.ContentText(), `"appWorkspacePath":"home/sites/site-1/draft/app"`) {
		t.Fatalf("expected virtual source workspace in result, got %s", result.ContentText())
	}
	readResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.read",
		Input: agent.MarshalToolInput(map[string]string{
			"path": "home/sites/site-1/draft/app/src/App.tsx",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if readResult.Failed() || !strings.Contains(readResult.ContentText(), "function App") {
		t.Fatalf("expected file.read to inspect materialized site source, got %s", readResult.ContentText())
	}
}

func TestSiteCreateAppWorkspaceSupportsBunLikeBuildRuntime(t *testing.T) {
	workspacePath := t.TempDir()
	httpClient := &recordingHTTPClient{responseBody: `{"status":"ok","result":{"siteID":"site-1","slug":"demo","title":"Demo","publishedURL":"https://demo.device.example.test","sourceWorkspacePath":"home/sites/site-1/draft","workspacePath":"home/sites/site-1","status":"draft"}}`}
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"site.app.create", "terminal.run"})
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "site.app.create",
		PolicyResource: "tool:site.app.create",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"],"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	createResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "site.app.create",
		Input:    agent.MarshalToolInput(map[string]string{"slug": "demo"}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if createResult.Failed() {
		t.Fatalf("expected site.app.create success, got %s", createResult.ContentText())
	}
	var createDocument map[string]any
	if errorValue := json.Unmarshal([]byte(createResult.ContentText()), &createDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	appWorkspacePath, isString := createDocument["appWorkspacePath"].(string)
	if !isString || strings.TrimSpace(appWorkspacePath) == "" {
		t.Fatalf("expected appWorkspacePath in site.app.create result, got %s", createResult.ContentText())
	}

	buildCommand := `
bun() {
  if [ "$1" != "run" ] || [ "$2" != "build" ]; then
    return 127
  fi
  for directory in "$HOME" "$TMPDIR" "$TMP" "$TEMP" "$XDG_CACHE_HOME" "$XDG_CONFIG_HOME" "$XDG_RUNTIME_DIR" "$BUN_TMPDIR" "$BUN_INSTALL" "$BUN_INSTALL_CACHE_DIR" "$npm_config_cache"; do
    if [ ! -d "$directory" ] || [ ! -w "$directory" ]; then
      echo 'error: AccessDenied accessing temporary directory. Please set $BUN_TMPDIR or $BUN_INSTALL' >&2
      return 74
    fi
  done
  case "$BUN_TMPDIR" in "$HOME"/*) ;; *) echo "BUN_TMPDIR escaped requester home: $BUN_TMPDIR" >&2; return 75 ;; esac
  case "$BUN_INSTALL" in "$HOME"/*) ;; *) echo "BUN_INSTALL escaped requester home: $BUN_INSTALL" >&2; return 75 ;; esac
  test -f package.json || return 76
  mkdir -p dist
  printf ok > dist/index.html
}
bun run build
`
	buildResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"workingDirectoryPath": appWorkspacePath,
			"environmentVariables": map[string]string{
				"BUN_TMPDIR":  "/tmp/not-blueclaw",
				"BUN_INSTALL": "/opt/not-blueclaw",
				"TMPDIR":      "/tmp/not-blueclaw",
			},
			"command": buildCommand,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if buildResult.Failed() {
		t.Fatalf("expected Bun-like build to succeed with requester runtime dirs, got %s", buildResult.ContentText())
	}
	distPath := filepath.Join(workspacePath, "private", "people", "person-1", "sites", "site-1", "draft", "app", "dist", "index.html")
	distDocument, errorValue := os.ReadFile(distPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(distDocument) != "ok" {
		t.Fatalf("expected build output from Bun-like command, got %q", string(distDocument))
	}
}

func TestSiteStatusAnnotatesWorkspaceHealth(t *testing.T) {
	workspacePath := t.TempDir()
	sourceWorkspacePath := filepath.Join(workspacePath, "private", "people", "person-1", "sites", "site-1", "draft")
	writeTestFile(t, filepath.Join(sourceWorkspacePath, "app", "src", "App.tsx"), "export default function App() { return null }\n")
	httpClient := &recordingHTTPClient{responseBody: `{"status":"ok","result":{"siteID":"site-1","slug":"demo","title":"Demo","sourceWorkspacePath":"home/sites/site-1/draft","appWorkspacePath":"home/sites/site-1/draft/app","status":"draft"}}`}
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"site.app.status"})
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "site.app.status",
		PolicyResource: "tool:site.app.status",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"siteID":{"type":"string"}},"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "site.app.status",
		Input:    agent.MarshalToolInput(map[string]string{"siteID": "site-1"}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.Contains(result.ContentText(), `"workspaceHealth":"stale_build"`) ||
		!strings.Contains(result.ContentText(), `"suggestedNextTool":"site.app.build"`) ||
		!strings.Contains(result.ContentText(), `"sourceWorkspacePath":"home/sites/site-1/draft"`) ||
		!strings.Contains(result.ContentText(), `"appWorkspacePath":"home/sites/site-1/draft/app"`) {
		t.Fatalf("expected workspace health annotation, got %s", result.ContentText())
	}
}

func TestSiteBuildRejectsSourceSubdirectoryCWD(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"site.app.build"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "site.app.build",
		Input: agent.MarshalToolInput(map[string]string{
			"appWorkspacePath": "home/sites/site-1/app/src",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || !strings.Contains(result.ContentText(), "not app/src") || !strings.Contains(result.ContentText(), "home/sites/site-1/app") {
		t.Fatalf("expected canonical cwd failure, got %s", result.ContentText())
	}
}

func TestSiteRepairRecreatesEditableWorkspace(t *testing.T) {
	workspacePath := t.TempDir()
	httpClient := &recordingHTTPClient{responseBody: `{"status":"ok","result":{"siteID":"site-1","slug":"demo","title":"Demo","description":"Demo site","idea":"Demo idea","purpose":"portfolio","archetype":"portfolio","sourceWorkspacePath":"home/sites/site-1/draft","appWorkspacePath":"home/sites/site-1/draft/app","status":"draft"}}`}
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"site.app.repair"})
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "site.app.status",
		PolicyResource: "tool:site.app.status",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"siteID":{"type":"string"}},"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "site.app.repair",
		Input:    agent.MarshalToolInput(map[string]string{"siteID": "site-1"}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected repair success, got %s", result.ContentText())
	}
	sourceWorkspacePath := filepath.Join(workspacePath, "private", "people", "person-1", "sites", "site-1", "draft")
	for _, relativePath := range []string{".internkim/site.json", ".internkim/idea.md", "DESIGN.md", "app/package.json"} {
		if _, errorValue := os.Stat(filepath.Join(sourceWorkspacePath, relativePath)); errorValue != nil {
			t.Fatalf("expected repaired file %s: %v", relativePath, errorValue)
		}
	}
	if !strings.Contains(result.ContentText(), `"publishedUnchanged":true`) {
		t.Fatalf("expected repair result to preserve published snapshot, got %s", result.ContentText())
	}
}

func TestSiteRepairResolvesCurrentConversationSiteWhenInputIsEmpty(t *testing.T) {
	workspacePath := t.TempDir()
	httpClient := &recordingHTTPClient{responseBody: `{"status":"ok","result":{"siteID":"site-1","slug":"demo","title":"Demo","description":"Demo site","idea":"Demo idea","purpose":"portfolio","archetype":"portfolio","sourceWorkspacePath":"home/sites/site-1/draft","appWorkspacePath":"home/sites/site-1/draft/app","status":"failed"}}`}
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"site.app.repair"})
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "site.app.status",
		PolicyResource: "tool:site.app.status",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"siteID":{"type":"string"}},"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "thread:channel:post",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "site.app.repair",
		Input:    agent.MarshalToolInput(map[string]string{}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected context repair success, got %s", result.ContentText())
	}
	if !strings.Contains(httpClient.requestBody, `"conversationID":"thread:channel:post"`) {
		t.Fatalf("expected site.app.status request to include conversation context, got %s", httpClient.requestBody)
	}
	if !strings.Contains(result.ContentText(), `"appWorkspacePath":"home/sites/site-1/draft/app"`) {
		t.Fatalf("expected repaired app workspace path from resolved site, got %s", result.ContentText())
	}
}

func TestSiteCreateAppWorkspaceBuildsOfflineWithBun(t *testing.T) {
	if !terminalTestCanResolveBun() {
		t.Skip("bun is not installed")
	}
	workspacePath := t.TempDir()
	httpClient := &recordingHTTPClient{responseBody: `{"status":"ok","result":{"siteID":"site-1","slug":"demo","title":"Demo","publishedURL":"https://demo.device.example.test","sourceWorkspacePath":"home/sites/site-1/draft","workspacePath":"home/sites/site-1","status":"draft"}}`}
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"site.app.create", "terminal.run"})
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "site.app.create",
		PolicyResource: "tool:site.app.create",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"],"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	createResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "site.app.create",
		Input:    agent.MarshalToolInput(map[string]string{"slug": "demo"}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if createResult.Failed() {
		t.Fatalf("expected site.app.create success, got %s", createResult.ContentText())
	}
	var createDocument map[string]any
	if errorValue := json.Unmarshal([]byte(createResult.ContentText()), &createDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	appWorkspacePath := createDocument["appWorkspacePath"].(string)

	buildResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"workingDirectoryPath": appWorkspacePath,
			"command":              "bun run build",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if buildResult.Failed() {
		t.Fatalf("expected offline Bun build to succeed, got %s", buildResult.ContentText())
	}
	distPath := filepath.Join(workspacePath, "private", "people", "person-1", "sites", "site-1", "draft", "app", "dist", "index.html")
	distDocument, errorValue := os.ReadFile(distPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	distContent := string(distDocument)
	for _, expectedText := range []string{"Dependency-free site scaffold", "InternKim site prototype loaded"} {
		if !strings.Contains(distContent, expectedText) {
			t.Fatalf("expected built site to contain %q, got %s", expectedText, distContent)
		}
	}
	for _, forbiddenText := range []string{"__SITE_STYLES__", "__SITE_BODY__", "__SITE_SCRIPT__"} {
		if strings.Contains(distContent, forbiddenText) {
			t.Fatalf("expected built site to replace placeholder %q, got %s", forbiddenText, distContent)
		}
	}
}

func terminalTestCanResolveBun() bool {
	for _, path := range strings.Split(security.CanonicalRuntimePATH, ":") {
		path = filepath.Join(path, "bun")
		if information, errorValue := os.Stat(path); errorValue == nil && !information.IsDir() {
			return true
		}
	}
	return false
}

func TestFileWriteProtectsManagedSitePackageManifest(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"file.write"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	managedResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "home/sites/site-1/app/package.json",
			"content": `{"project":"not a package manifest"}`,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !managedResult.Failed() || managedResult.Failure.Code != "managed_manifest_protected" {
		t.Fatalf("expected managed manifest protection, got %+v", managedResult)
	}

	tmpResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "tmp/demo/package.json",
			"content": `{"project":"freeform package file"}`,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if tmpResult.Failed() {
		t.Fatalf("expected tmp package manifest write to remain allowed, got %+v", tmpResult)
	}
}

func TestTerminalRunPreflightsNodePackageBuildManifest(t *testing.T) {
	workspacePath := t.TempDir()
	invalidPackagePath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "node-app", "package.json")
	writeTestFile(t, invalidPackagePath, `{"project":"not a package manifest"}`)
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"workingDirectoryPath": "tmp/node-app",
			"command":              "bun run build",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.Failure.Code != "package_manifest_invalid" || result.Failure.Stage != "package_manifest_preflight" {
		t.Fatalf("expected package manifest preflight failure, got %+v", result)
	}
	if !strings.Contains(result.ContentText(), "scripts") || !strings.Contains(result.ContentText(), "tmp/node-app/package.json") {
		t.Fatalf("expected manifest detail in result, got %s", result.ContentText())
	}
}

func TestSitePublishInputRejectsInaccessibleWorkspaceBundle(t *testing.T) {
	workspacePath := t.TempDir()
	sourceWorkspacePath := filepath.Join(workspacePath, "circles", "finance", "sites", "site-1")
	writeTestFile(t, filepath.Join(sourceWorkspacePath, "app", "dist", "index.html"), "<html>ok</html>")
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)

	_, errorValue := toolCatalogBuilder.enrichCapabilityToolInput("site.app.publish", ToolCatalogRequest{
		PersonAccess: policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	}, agent.MarshalToolInput(map[string]any{
		"siteID":              "site-1",
		"sourceWorkspacePath": "/workspace/circles/finance/sites/site-1",
	}))
	if errorValue == nil {
		t.Fatal("expected inaccessible workspace rejection")
	}
}

func TestTerminalRunDefaultsToPrivateScopeForDirectMessage(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"command": "pwd",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected terminal.run success, got %s", result.ContentText())
	}
	var commandResult security.CommandResult
	if errorValue := json.Unmarshal([]byte(result.ContentText()), &commandResult); errorValue != nil {
		t.Fatal(errorValue)
	}
	expectedSuffix := filepath.Join("private", "people", "person-1", "tmp")
	if !strings.HasSuffix(strings.TrimSpace(commandResult.Stdout), expectedSuffix) {
		t.Fatalf("expected terminal cwd under %s, got %q", expectedSuffix, commandResult.Stdout)
	}
}

func TestTerminalRunMaterializesRequesterRuntimeEnvironment(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"environmentVariables": map[string]string{"PATH": "/workspace/private/people/person-1/bin"},
			"command":              `test -d "$TMPDIR" && test -d "$BUN_TMPDIR" && test -d "$BUN_INSTALL" && printf '%s\n%s\n%s\n%s\n%s' "$HOME" "$PATH" "$TMPDIR" "$BUN_TMPDIR" "$BUN_INSTALL"`,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected terminal.run success, got %s", result.ContentText())
	}
	var commandResult security.CommandResult
	if errorValue := json.Unmarshal([]byte(result.ContentText()), &commandResult); errorValue != nil {
		t.Fatal(errorValue)
	}
	requesterRootPath := filepath.Join(workspacePath, "private", "people", "person-1")
	for _, expectedText := range []string{
		requesterRootPath,
		security.CanonicalRuntimePATH,
		filepath.Join(requesterRootPath, "tmp", ".runtime", "tmp"),
		filepath.Join(requesterRootPath, "tmp", ".runtime", "bun", "tmp"),
		filepath.Join(requesterRootPath, "tmp", ".runtime", "bun", "install"),
	} {
		if !strings.Contains(commandResult.Stdout, expectedText) {
			t.Fatalf("expected runtime environment path %s in stdout, got %q", expectedText, commandResult.Stdout)
		}
		if expectedText != requesterRootPath && expectedText != security.CanonicalRuntimePATH {
			if _, errorValue := os.Stat(expectedText); errorValue != nil {
				t.Fatalf("expected runtime environment directory %s: %v", expectedText, errorValue)
			}
		}
	}
}

func TestTerminalRunRelativeWorkingDirectoryUsesConversationDefault(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	writeResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "tmp/deck/input.txt",
			"content": "ok",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.Failed() {
		t.Fatalf("expected file.write success, got %s", writeResult.ContentText())
	}

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"command":              "pwd && cat input.txt && printf built > result.txt",
			"workingDirectoryPath": "tmp/deck",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected terminal.run success, got %s", result.ContentText())
	}
	var commandResult security.CommandResult
	if errorValue := json.Unmarshal([]byte(result.ContentText()), &commandResult); errorValue != nil {
		t.Fatal(errorValue)
	}
	expectedDirectoryPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "deck")
	if !strings.Contains(commandResult.Stdout, expectedDirectoryPath) || !strings.Contains(commandResult.Stdout, "ok") {
		t.Fatalf("expected terminal cwd and file content under private tmp, got %q", commandResult.Stdout)
	}
	resultDocument, errorValue := os.ReadFile(filepath.Join(expectedDirectoryPath, "result.txt"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(resultDocument) != "built" {
		t.Fatalf("expected terminal output under private tmp, got %q", string(resultDocument))
	}
	if _, errorValue := os.Stat(filepath.Join(workspacePath, "tmp", "deck")); !os.IsNotExist(errorValue) {
		t.Fatalf("terminal.run must not create workspace-root tmp for relative workingDirectoryPath")
	}
}

func TestFileWriteThroughWorkspaceActorTreatsContentAsData(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	content := "hello\n$(touch should-not-exist)\n"
	writeResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]any{
			"path":    "tmp/deck/input.txt",
			"content": content,
			"mode":    0600,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.Failed() {
		t.Fatalf("expected file.write success, got %s", writeResult.ContentText())
	}

	taskTmpPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp")
	document, errorValue := os.ReadFile(filepath.Join(taskTmpPath, "deck", "input.txt"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(document) != content {
		t.Fatalf("expected exact content, got %q", string(document))
	}
	if _, errorValue := os.Stat(filepath.Join(taskTmpPath, "should-not-exist")); !os.IsNotExist(errorValue) {
		t.Fatalf("file.write content must not be executed as shell, stat error %v", errorValue)
	}
	fileInformation, errorValue := os.Stat(filepath.Join(taskTmpPath, "deck", "input.txt"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if fileInformation.Mode().Perm() != 0600 {
		t.Fatalf("expected requester chmod mode 0600, got %v", fileInformation.Mode().Perm())
	}
}

func TestFileWriteRepairsGroupWriteBitsForTerminalFlow(t *testing.T) {
	workspacePath := t.TempDir()
	previousMask := syscall.Umask(0027)
	defer syscall.Umask(previousMask)

	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	writeResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "tmp/deck/input.txt",
			"content": "ok",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.Failed() {
		t.Fatalf("expected file.write success, got %s", writeResult.ContentText())
	}

	deckDirectoryPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "deck")
	directoryInformation, errorValue := os.Stat(deckDirectoryPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if directoryInformation.Mode().Perm()&0020 == 0 {
		t.Fatalf("expected group-writable deck directory, got mode %v", directoryInformation.Mode().Perm())
	}
	fileInformation, errorValue := os.Stat(filepath.Join(deckDirectoryPath, "input.txt"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if fileInformation.Mode().Perm()&0020 == 0 {
		t.Fatalf("expected group-writable file for requester terminal flow, got mode %v", fileInformation.Mode().Perm())
	}
}

func TestFileWriteAndTerminalRunShareRequesterWorkspaceActorView(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm:channel-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	writeResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "tmp/deck/input.txt",
			"content": "same workspace",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if writeResult.Failed() {
		t.Fatalf("expected file.write success, got %s", writeResult.ContentText())
	}

	runResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"workingDirectoryPath": "tmp/deck",
			"command":              "mkdir -p build && cat input.txt > build/output.txt",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if runResult.Failed() {
		t.Fatalf("expected terminal.run success, got %s", runResult.ContentText())
	}
	outputPath := filepath.Join(workspacePath, "private", "people", "person-1", "tmp", "deck", "build", "output.txt")
	document, errorValue := os.ReadFile(outputPath)
	if errorValue != nil || string(document) != "same workspace" {
		t.Fatalf("expected terminal to read file.write output, got %q and %v", string(document), errorValue)
	}
}

func TestFileWriteWithoutRequesterIdentityDoesNotFallbackToServiceUser(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "tmp/deck/input.txt",
			"content": "no service fallback",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.Failure.Stage != "actor_identity_missing" {
		t.Fatalf("expected actor identity failure, got %+v", result)
	}
	if _, errorValue := os.Stat(filepath.Join(workspacePath, "tmp", "deck", "input.txt")); !os.IsNotExist(errorValue) {
		t.Fatalf("file.write must not fall back to service-user workspace writes, stat error %v", errorValue)
	}
}

func TestTerminalRunDenyCircleWorkingDirectoryForNonMember(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newTerminalToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff"},
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"command":              "printf no",
			"workingDirectoryPath": "/workspace/circles/finance",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || !strings.Contains(result.ContentText(), "cannot use this workspace path") {
		t.Fatalf("expected terminal.run path denial, got %+v", result)
	}
}

func TestSkillAddCreatesUserManagedSkillAndRefreshes(t *testing.T) {
	workspacePath := t.TempDir()
	refreshCount := 0
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolCatalogBuilder.UseSkillChangeHandler(func(context.Context) {
		refreshCount++
	})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "skill.add",
		Input: agent.MarshalToolInput(map[string]string{
			"name":    "research-helper",
			"content": userSkillDocument("research-helper"),
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected skill.add success, got %s", result.ContentText())
	}
	if refreshCount != 1 {
		t.Fatalf("expected skill index refresh, got %d", refreshCount)
	}
	skillDocumentPath := filepath.Join(workspacePath, ".agents", "skills", "research-helper", "SKILL.md")
	document, errorValue := os.ReadFile(skillDocumentPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.Contains(string(document), "Research helper handles source lookups.") {
		t.Fatalf("expected skill document to be written, got %s", string(document))
	}
	if strings.Contains(result.ContentText(), workspacePath) || !strings.Contains(result.ContentText(), "/workspace/.agents/skills/research-helper/SKILL.md") {
		t.Fatalf("expected agent workspace path in result, got %s", result.ContentText())
	}
	resultDocument := decodeSkillAddResult(t, result.ContentText())
	if resultDocument.Name != "research-helper" || resultDocument.Status != "created" {
		t.Fatalf("expected structured skill.add result, got %+v", resultDocument)
	}
}

func TestSkillAddWritesAllowedResources(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	content := `---
name: report-helper
description: Help create reports from source material.
when_to_use: Use for report writing requests.
---
Use references/reporting.md and scripts/build_report.sh when needed.
`

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "skill.add",
		Input: agent.MarshalToolInput(map[string]any{
			"name":    "report-helper",
			"content": content,
			"resources": []map[string]any{
				{"path": "references/reporting.md", "content": "# Reporting"},
				{"path": "scripts/build_report.sh", "content": "echo ok", "mode": 0o700},
				{"path": "assets/template.txt", "content": "template"},
			},
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected skill.add success, got %s", result.ContentText())
	}
	for _, path := range []string{"references/reporting.md", "scripts/build_report.sh", "assets/template.txt"} {
		if _, errorValue := os.Stat(filepath.Join(workspacePath, ".agents", "skills", "report-helper", path)); errorValue != nil {
			t.Fatalf("expected resource %s: %v", path, errorValue)
		}
	}
	resultDocument := decodeSkillAddResult(t, result.ContentText())
	if len(resultDocument.ResourcePaths) != 3 {
		t.Fatalf("expected resource paths in result, got %+v", resultDocument)
	}
}

func TestSkillRemoveDeletesOnlyUserManagedSkill(t *testing.T) {
	workspacePath := t.TempDir()
	skillDirectoryPath := filepath.Join(workspacePath, ".agents", "skills", "research-helper")
	if errorValue := os.MkdirAll(skillDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, filepath.Join(skillDirectoryPath, "SKILL.md"), userSkillDocument("research-helper"))
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "skill.remove",
		Input: agent.MarshalToolInput(map[string]string{
			"name": "research-helper",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected skill.remove success, got %s", result.ContentText())
	}
	if _, errorValue := os.Stat(skillDirectoryPath); !os.IsNotExist(errorValue) {
		t.Fatalf("expected user-managed skill directory removed, got %v", errorValue)
	}
}

func TestSkillRemoveMissingSkillIsNonFatal(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "skill.remove",
		Input: agent.MarshalToolInput(map[string]string{
			"name": "missing-skill",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() || !strings.Contains(result.ContentText(), `"status":"missing"`) {
		t.Fatalf("expected non-fatal missing result, got %+v", result)
	}
}

func TestSkillManagementRejectsInvalidAndBuiltInNames(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	for _, input := range []map[string]string{
		{"name": "../escape", "content": userSkillDocument("escape")},
		{"name": "simple-slides", "content": userSkillDocument("simple-slides")},
	} {
		result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
			ToolName: "skill.add",
			Input:    agent.MarshalToolInput(input),
		})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if !result.Failed() {
			t.Fatalf("expected skill.add to reject %+v", input)
		}
	}

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "skill.remove",
		Input: agent.MarshalToolInput(map[string]string{
			"name": "agent-browser",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected skill.remove to reject built-in skill, got %+v", result)
	}
}

func TestSkillAddRejectsMalformedOrCustomFrontmatter(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	for _, content := range []string{
		"---\nname: broken\ndescription: Broken",
		"---\nname: custom\nsummary: no\n---\nBody.",
		"---\nname: custom\ntags: [one]\n---\nBody.",
		"---\nname: custom\ntriggerHints: [one]\n---\nBody.",
		"---\nname: custom\ncustomToolDependency: [terminal.run]\n---\nBody.",
		"---\nname: custom\nallowedProfiles: [default]\n---\nBody.",
	} {
		result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
			ToolName: "skill.add",
			Input: agent.MarshalToolInput(map[string]string{
				"name":    "broken",
				"content": content,
			}),
		})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if !result.Failed() {
			t.Fatalf("expected malformed skill document to be rejected: %s", content)
		}
	}
}

func TestSkillAddAcceptsStandardOptionalMetadata(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	content := `---
name: metadata-helper
description: Help with metadata-backed standard skill imports.
license: MIT
metadata:
  category: productivity
  locale: ko-KR
---
Use this skill when standard skill metadata should be preserved.
`

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "skill.add",
		Input: agent.MarshalToolInput(map[string]string{
			"name":    "metadata-helper",
			"content": content,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected standard optional metadata to be accepted, got %s", result.ContentText())
	}
}

func TestSkillAddRejectsInvalidResourcePaths(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	for _, resourcePath := range []string{
		"../escape.md",
		"/workspace/escape.md",
		"SKILL.md",
		".hidden/file.md",
		"notes/file.md",
	} {
		result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
			ToolName: "skill.add",
			Input: agent.MarshalToolInput(map[string]any{
				"name":    "resource-helper",
				"content": userSkillDocument("resource-helper"),
				"resources": []map[string]string{{
					"path":    resourcePath,
					"content": "no",
				}},
			}),
		})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if !result.Failed() {
			t.Fatalf("expected resource path %q to be rejected", resourcePath)
		}
	}
}

func TestSkillAddReturnsQualityWarnings(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	content := `---
name: tiny-helper
description: Tiny.
---
Use references/missing.md.
`

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "skill.add",
		Input: agent.MarshalToolInput(map[string]any{
			"name":    "tiny-helper",
			"content": content,
			"resources": []map[string]string{{
				"path":    "assets/unmentioned.txt",
				"content": "asset",
			}},
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected warning-only skill.add success, got %s", result.ContentText())
	}
	resultDocument := decodeSkillAddResult(t, result.ContentText())
	for _, expectedWarning := range []string{
		"when_to_use is recommended so retrieval has explicit trigger context",
		"description is short; include what the skill does and when to use it",
		"SKILL.md mentions references/ but no reference resources were supplied",
		"resource assets/unmentioned.txt is not mentioned from SKILL.md",
	} {
		if !containsTestString(resultDocument.Warnings, expectedWarning) {
			t.Fatalf("expected warning %q, got %+v", expectedWarning, resultDocument.Warnings)
		}
	}
}

func TestSkillAddReturnsLongBodyAndMissingScriptWarnings(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	content := `---
name: long-helper
description: Help with long deterministic workflows.
when_to_use: Use for long workflow requests.
---
Use scripts/missing.sh when needed.
` + strings.Repeat("step\n", longSkillBodyLineCount+1)

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "skill.add",
		Input: agent.MarshalToolInput(map[string]string{
			"name":    "long-helper",
			"content": content,
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected warning-only skill.add success, got %s", result.ContentText())
	}
	resultDocument := decodeSkillAddResult(t, result.ContentText())
	for _, expectedWarning := range []string{
		"skill body is long; move detailed material into references",
		"SKILL.md mentions scripts/ but no script resources were supplied",
	} {
		if !containsTestString(resultDocument.Warnings, expectedWarning) {
			t.Fatalf("expected warning %q, got %+v", expectedWarning, resultDocument.Warnings)
		}
	}
}

func TestFileWriteRejectsBuiltInSkillPaths(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := newFileToolTestCatalogBuilder(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	for _, path := range []string{
		"/workspace/skills/bash/SKILL.md",
		"/workspace/skills/.internkim-skills-manifest.json",
		"/workspace/.agents/skills/agent-browser/SKILL.md",
	} {
		result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
			ToolName: "file.write",
			Input: agent.MarshalToolInput(map[string]string{
				"path":    path,
				"content": "no",
			}),
		})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if !result.Failed() {
			t.Fatalf("expected file.write to reject immutable skill path %q", path)
		}
	}
}

func TestSkillManagementRejectsProductionServiceOwnedWorkspace(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath("/workspace")
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "skill.add",
		Input: agent.MarshalToolInput(map[string]string{
			"name":    "demo-skill",
			"content": "---\nname: demo-skill\ndescription: Demo skill for rejection testing.\n---\n# Demo\n",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.Failure.Stage != "actor_permission_denied" {
		t.Fatalf("expected actor_permission_denied for production skill.add, got %+v", result)
	}
}

func TestRequesterWorkspaceToolHandlersDoNotUseDirectFileMutation(t *testing.T) {
	document, errorValue := os.ReadFile("tool_catalog.go")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	source := string(document)
	for _, forbidden := range []string{
		"os.WriteFile(",
		"os.ReadFile(",
		"os.OpenFile(",
		"copyRegularFile(",
		"verifyRequesterTerminalCanReadFile(",
		"writeFileAsRequester(",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("requester workspace tool handlers must use WorkspaceActor, found %s", forbidden)
		}
	}
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if errorValue := os.MkdirAll(filepath.Dir(path), 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(path, []byte(content), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func newFileToolTestCatalogBuilder(workspacePath string) *ToolCatalogBuilder {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolCatalogBuilder.UseWorkspaceActorFactory(actortest.NewDirectWorkspaceActorFactory())
	return toolCatalogBuilder
}

func newTerminalToolTestCatalogBuilder(workspacePath string) *ToolCatalogBuilder {
	terminalService := security.NewTerminalSessionService(config.TerminalConfiguration{
		WorkspaceRootPath: workspacePath,
		Mode:              "firecrackerGuest",
		TimeoutSecond:     5,
		OutputMaxBytes:    4096,
		SessionMaxCount:   2,
	})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolCatalogBuilder.UseTerminalService(terminalService)
	toolCatalogBuilder.UseWorkspaceActorFactory(actortest.NewDirectWorkspaceActorFactory(terminalService))
	return toolCatalogBuilder
}

func findToolDefinition(toolDefinitions []agent.ToolDefinition, toolName string) (agent.ToolDefinition, bool) {
	for _, toolDefinition := range toolDefinitions {
		if toolDefinition.Name == toolName {
			return toolDefinition, true
		}
	}
	return agent.ToolDefinition{}, false
}

func siteSourceBundlePaths(t *testing.T, bundleBase64 string) []string {
	t.Helper()
	document, errorValue := base64.StdEncoding.DecodeString(bundleBase64)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	gzipReader, errorValue := gzip.NewReader(bytes.NewReader(document))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	paths := []string{}
	for {
		header, errorValue := tarReader.Next()
		if errorValue == io.EOF {
			return paths
		}
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		paths = append(paths, header.Name)
	}
}

func userSkillDocument(skillName string) string {
	return `---
name: ` + skillName + `
when_to_use: Use for research and source lookup requests.
allowed-tools:
  - memory.search
---
Research helper handles source lookups.
`
}

func decodeSkillAddResult(t *testing.T, content string) skillAddResult {
	t.Helper()
	var resultDocument skillAddResult
	if errorValue := json.Unmarshal([]byte(content), &resultDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	return resultDocument
}

func containsTestString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type skillSearchTestRetriever struct{}

func (skillSearchTestRetriever) Retrieve(context.Context, agent.AgentRequest, []agent.SkillInstruction, int) agent.SkillRetrievalResult {
	return agent.SkillRetrievalResult{}
}

func (skillSearchTestRetriever) Search(_ context.Context, _ agent.AgentRequest, _ []agent.SkillInstruction, querySet agent.SkillSearchQuerySet, _ int) agent.SkillRetrievalResult {
	if len(querySet.Queries) == 0 {
		return agent.SkillRetrievalResult{}
	}
	return agent.SkillRetrievalResult{
		RetrievalMode: "embedding",
		IndexStatus:   "ready",
		SelectedCandidates: []agent.SkillCandidate{{
			Name:   "mail",
			Score:  0.91,
			Reason: "embedding_similarity",
		}},
	}
}

func (skillSearchTestRetriever) Refresh(context.Context, []agent.SkillInstruction) {}
