package agentruntime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/agent"
	"github.com/Dawn-kim-official/blueclaw/internal/memory"
	"github.com/Dawn-kim-official/blueclaw/internal/policy"
	"github.com/Dawn-kim-official/blueclaw/internal/task"
)

type staticGraphMemoryStore struct {
	facts []memory.MemoryFact
}

func (store staticGraphMemoryStore) AddEpisode(context.Context, memory.MemoryEpisode) (memory.MemoryIngestionResult, error) {
	return memory.MemoryIngestionResult{}, nil
}

func (store staticGraphMemoryStore) SearchFacts(context.Context, memory.MemorySearchRequest) ([]memory.MemoryFact, error) {
	return store.facts, nil
}

func TestTaskLauncherInjectsGraphMemoryAtLaunch(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	agentKernel := agent.NewAgentKernel(task.NewTaskRunService(taskEventService), task.NewTaskStepService())
	useRuntimeTestLanguageModel(agentKernel, runtimeFinishMessage("done"))
	pinnedMemoryStore := memory.NewMarkdownStore(t.TempDir(), 1200)
	if _, errorValue := pinnedMemoryStore.MergePersonMemory(context.Background(), "person-1", "The user prefers terse release notes."); errorValue != nil {
		t.Fatal(errorValue)
	}
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(staticGraphMemoryStore{facts: []memory.MemoryFact{{
		ScopeType:   memory.ScopeTypeUser,
		NamespaceID: memory.UserNamespace("person-1").NamespaceID,
		Content:     "The user leads the quarterly launch project.",
		SourceKind:  memory.MemorySourceKindFact,
		Score:       0.9,
	}}})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UsePinnedMemoryStore(pinnedMemoryStore)
	toolCatalogBuilder.UseMemoryService(memoryService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"memory.search"},
	}, nil)

	launchResult, errorValue := NewTaskLauncher(agentKernel, toolCatalogBuilder).Launch(context.Background(), TaskLaunchRequest{
		Source:                    TaskLaunchSourceConnector,
		SourceReference:           "mattermost:post-1",
		RequesterPersonID:         "person-1",
		ProfileName:               "default",
		ConversationID:            "channel-1",
		Prompt:                    "How is the quarterly launch going?",
		PersonAccess:              policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
		MemoryNamespaces:          []memory.MemoryNamespace{memory.UserNamespace("person-1")},
		AccessibleConversationIDs: []string{"channel-1"},
	})
	if errorValue != nil {
		t.Fatalf("expected launch to succeed: %v", errorValue)
	}
	if len(launchResult.MemoryFacts) != 2 {
		t.Fatalf("expected pinned and graph memory facts, got %+v", launchResult.MemoryFacts)
	}
	if !containsMemoryFactContent(launchResult.MemoryFacts, "quarterly launch project") {
		t.Fatalf("expected graph fact to be injected, got %+v", launchResult.MemoryFacts)
	}
	if !containsMemoryFactContent(launchResult.MemoryFacts, "terse release notes") {
		t.Fatalf("expected pinned fact to be kept, got %+v", launchResult.MemoryFacts)
	}
}

func TestTaskLauncherKeepsPinnedMemoryWhenGraphSearchFails(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	agentKernel := agent.NewAgentKernel(task.NewTaskRunService(taskEventService), task.NewTaskStepService())
	useRuntimeTestLanguageModel(agentKernel, runtimeFinishMessage("done"))
	pinnedMemoryStore := memory.NewMarkdownStore(t.TempDir(), 1200)
	if _, errorValue := pinnedMemoryStore.MergePersonMemory(context.Background(), "person-1", "The user prefers terse release notes."); errorValue != nil {
		t.Fatal(errorValue)
	}
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(failingGraphMemoryStore{errorValue: errors.New("graphiti unavailable")})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UsePinnedMemoryStore(pinnedMemoryStore)
	toolCatalogBuilder.UseMemoryService(memoryService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"memory.search"},
	}, nil)

	launchResult, errorValue := NewTaskLauncher(agentKernel, toolCatalogBuilder).Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		SourceReference:   "mattermost:post-2",
		RequesterPersonID: "person-1",
		ProfileName:       "default",
		ConversationID:    "channel-1",
		Prompt:            "Summarize the launch status.",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
		MemoryNamespaces:  []memory.MemoryNamespace{memory.UserNamespace("person-1")},
	})
	if errorValue != nil {
		t.Fatalf("expected launch to succeed: %v", errorValue)
	}
	if len(launchResult.MemoryFacts) != 1 || !containsMemoryFactContent(launchResult.MemoryFacts, "terse release notes") {
		t.Fatalf("expected pinned-only memory facts, got %+v", launchResult.MemoryFacts)
	}
}

func containsMemoryFactContent(memoryFacts []memory.MemoryFact, expectedText string) bool {
	for _, memoryFact := range memoryFacts {
		if strings.Contains(memoryFact.Content, expectedText) {
			return true
		}
	}
	return false
}
