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
	"testing"
	"time"

	"blueclaw/internal/agent"
	"blueclaw/internal/capability"
	"blueclaw/internal/config"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
	"blueclaw/internal/security"
	"blueclaw/internal/task"
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
		Input:    agent.MarshalToolInput("Call the user master."),
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
		Input:    agent.MarshalToolInput("Shared circle fact."),
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
		Input:    agent.MarshalToolInput("Compensation data belongs to HR."),
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
		Input:    agent.MarshalToolInput("Shared fact."),
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
		Input:    agent.MarshalToolInput("memory"),
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
		Input:    agent.MarshalToolInput("Graphiti release notes"),
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

func TestFileAttachToolAttachesMultiplePaths(t *testing.T) {
	workspacePath := t.TempDir()
	writeTestFile(t, filepath.Join(workspacePath, "deck.pptx"), "pptx")
	writeTestFile(t, filepath.Join(workspacePath, "deck.pdf"), "%PDF")
	writeTestFile(t, filepath.Join(workspacePath, "deck.html"), "<html></html>")
	writeTestFile(t, filepath.Join(workspacePath, "deck-notes.txt"), "notes")

	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.attach",
		Input: agent.MarshalToolInput(map[string]any{
			"paths": []string{"deck.pptx", "deck.pdf", "deck.html", "deck-notes.txt"},
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected successful attachment result, got %s", result.ContentText())
	}
	if len(result.Attachments) != 4 {
		t.Fatalf("expected four attachments, got %+v", result.Attachments)
	}
	if result.Attachments[0].Filename != "deck.pptx" || result.Attachments[3].Filename != "deck-notes.txt" {
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
	}, json.RawMessage(`{"recipientHint":"동하","message":"테스트"}`))
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

func TestFileToolsAcceptAgentWorkspacePathsWithoutLeakingHostPath(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	writeResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.write",
		Input: agent.MarshalToolInput(map[string]string{
			"path":    "/workspace/deck/presentation.md",
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
	if _, errorValue := os.Stat(filepath.Join(workspacePath, "deck", "presentation.md")); errorValue != nil {
		t.Fatal(errorValue)
	}

	attachResult, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "file.attach",
		Input: agent.MarshalToolInput(map[string]string{
			"path": "/workspace/deck/presentation.md",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if attachResult.Failed() {
		t.Fatalf("expected file.attach success, got %s", attachResult.ContentText())
	}
	if attachResult.Attachments[0].DevicePath != "/workspace/deck/presentation.md" {
		t.Fatalf("expected agent workspace device path, got %+v", attachResult.Attachments[0])
	}
}

func TestFileToolsDenyCirclePathForNonMember(t *testing.T) {
	workspacePath := t.TempDir()
	financeDirectoryPath := filepath.Join(workspacePath, "circles", "finance")
	if errorValue := os.MkdirAll(financeDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, filepath.Join(financeDirectoryPath, "report.md"), "secret")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
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
	if errorValue == nil {
		t.Fatalf("expected file.attach access error, got %+v", attachResult)
	}
	if !strings.Contains(errorValue.Error(), "cannot read") {
		t.Fatalf("expected file.attach read denial, got %v", errorValue)
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
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "file.read",
		PolicyResource: "tool:file.read",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
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
	writeTestFile(t, filepath.Join(financeDirectoryPath, "report.pdf"), "secret")
	httpClient := &recordingHTTPClient{responseBody: `{"content":"# Report","status":"ok","result":{"content":"# Report"}}`}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{Endpoint: "http://capability.local", HTTPClient: httpClient}, []CapabilityToolDescriptor{{
		Name:           "file.read",
		PolicyResource: "tool:file.read",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"staff", "finance"},
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
	if result.Failed() || result.ContentText() != "# Report" {
		t.Fatalf("expected file.read success, got %+v", result)
	}
	if httpClient.requestPath != "/v1/tools/file.read/invoke" {
		t.Fatalf("expected capability bridge call, got path=%s body=%s", httpClient.requestPath, httpClient.requestBody)
	}
}

func TestFileToolsAllowCirclePathForMember(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
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
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
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
	expectedPath := filepath.Join(workspacePath, "private", "people", "person-1", "notes.md")
	if document, errorValue := os.ReadFile(expectedPath); errorValue != nil || string(document) != "private" {
		t.Fatalf("expected private file at %s, got %q and %v", expectedPath, string(document), errorValue)
	}
	if !strings.Contains(result.ContentText(), `"/workspace/private/people/person-1/notes.md"`) {
		t.Fatalf("expected private agent path in result, got %s", result.ContentText())
	}
}

func TestFileWriteDefaultsToCircleScopeForCircleChannel(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
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
	expectedPath := filepath.Join(workspacePath, "circles", "finance", "report.md")
	if document, errorValue := os.ReadFile(expectedPath); errorValue != nil || string(document) != "finance" {
		t.Fatalf("expected finance file at %s, got %q and %v", expectedPath, string(document), errorValue)
	}
}

func TestFileWriteDefaultsToStaffScopeForGeneralChannel(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
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
	expectedPath := filepath.Join(workspacePath, "circles", "staff", "status.md")
	if document, errorValue := os.ReadFile(expectedPath); errorValue != nil || string(document) != "staff" {
		t.Fatalf("expected staff file at %s, got %q and %v", expectedPath, string(document), errorValue)
	}
}

func TestFileAttachDefaultsToPrivateScopeForDirectMessage(t *testing.T) {
	workspacePath := t.TempDir()
	privateDirectoryPath := filepath.Join(workspacePath, "private", "people", "person-1")
	if errorValue := os.MkdirAll(privateDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeTestFile(t, filepath.Join(privateDirectoryPath, "notes.md"), "private")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
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
	if result.Attachments[0].DevicePath != "/workspace/private/people/person-1/notes.md" {
		t.Fatalf("expected private device path, got %+v", result.Attachments[0])
	}
}

func TestTerminalRunTranslatesAgentWorkspacePaths(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolCatalogBuilder.UseTerminalService(security.NewTerminalSessionService(config.TerminalConfiguration{
		WorkspaceRootPath: workspacePath,
		Mode:              "firecrackerGuest",
		TimeoutSecond:     5,
		OutputMaxBytes:    4096,
		SessionMaxCount:   2,
	}))
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), agent.ToolInvocation{
		ToolName: "terminal.run",
		Input: agent.MarshalToolInput(map[string]any{
			"command":              "mkdir -p /workspace/deck && printf ok > /workspace/deck/result.txt",
			"workingDirectoryPath": "/workspace",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected terminal.run success, got %s", result.ContentText())
	}
	content, errorValue := os.ReadFile(filepath.Join(workspacePath, "deck", "result.txt"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(content) != "ok" {
		t.Fatalf("expected translated workspace command to write file, got %q", string(content))
	}
}

func TestTerminalRunAllowsServiceOwnedPathText(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolCatalogBuilder.UseTerminalService(security.NewTerminalSessionService(config.TerminalConfiguration{
		WorkspaceRootPath: workspacePath,
		Mode:              "firecrackerGuest",
		TimeoutSecond:     5,
		OutputMaxBytes:    4096,
		SessionMaxCount:   2,
	}))
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

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
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolCatalogBuilder.UseTerminalService(security.NewTerminalSessionService(config.TerminalConfiguration{
		WorkspaceRootPath: workspacePath,
		Mode:              "firecrackerGuest",
		TimeoutSecond:     5,
		OutputMaxBytes:    4096,
		SessionMaxCount:   2,
	}))
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

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
	sourceWorkspacePath := filepath.Join(workspacePath, "circles", "staff", "sites", "site-1")
	writeTestFile(t, filepath.Join(sourceWorkspacePath, "app", "dist", "index.html"), "<html>ok</html>")
	writeTestFile(t, filepath.Join(sourceWorkspacePath, "app", "node_modules", "ignored.js"), "ignored")
	writeTestFile(t, filepath.Join(sourceWorkspacePath, "DESIGN.md"), "custom design")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)

	toolInput, errorValue := toolCatalogBuilder.enrichCapabilityToolInput("site.app.publish", ToolCatalogRequest{
		PersonAccess: policy.PersonAccess{PersonID: "person-1", Circles: []string{"staff"}},
	}, agent.MarshalToolInput(map[string]any{"siteID": "site-1"}))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var inputDocument map[string]any
	if errorValue := json.Unmarshal(toolInput, &inputDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	if inputDocument["sourceWorkspacePath"] != "/workspace/circles/staff/sites/site-1" {
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

func TestSitePublishInputRejectsInaccessibleWorkspaceBundle(t *testing.T) {
	workspacePath := t.TempDir()
	sourceWorkspacePath := filepath.Join(workspacePath, "circles", "finance", "sites", "site-1")
	writeTestFile(t, filepath.Join(sourceWorkspacePath, "app", "dist", "index.html"), "<html>ok</html>")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)

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
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolCatalogBuilder.UseTerminalService(security.NewTerminalSessionService(config.TerminalConfiguration{
		WorkspaceRootPath: workspacePath,
		Mode:              "firecrackerGuest",
		TimeoutSecond:     5,
		OutputMaxBytes:    4096,
		SessionMaxCount:   2,
	}))
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
	expectedSuffix := filepath.Join("private", "people", "person-1")
	if !strings.HasSuffix(strings.TrimSpace(commandResult.Stdout), expectedSuffix) {
		t.Fatalf("expected terminal cwd under %s, got %q", expectedSuffix, commandResult.Stdout)
	}
}

func TestTerminalRunDenyCircleWorkingDirectoryForNonMember(t *testing.T) {
	workspacePath := t.TempDir()
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolCatalogBuilder.UseTerminalService(security.NewTerminalSessionService(config.TerminalConfiguration{
		WorkspaceRootPath: workspacePath,
		Mode:              "firecrackerGuest",
		TimeoutSecond:     5,
		OutputMaxBytes:    4096,
		SessionMaxCount:   2,
	}))
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
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
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
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
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
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
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
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
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
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
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
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
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
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
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
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
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
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
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
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
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
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
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

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if errorValue := os.MkdirAll(filepath.Dir(path), 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(path, []byte(content), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
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
