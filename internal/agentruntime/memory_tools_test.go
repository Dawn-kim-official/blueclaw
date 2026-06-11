package agentruntime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"blueclaw/internal/agent"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
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
