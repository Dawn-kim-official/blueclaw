package memory

import (
	"context"
	"testing"
)

func TestMemoryServiceSeparatesUserWorkspaceAndConversationNamespaces(t *testing.T) {
	memoryService := &MemoryService{}
	memoryService.StoreMemoryFact(MemoryFact{
		ScopeType:   ScopeTypeUser,
		NamespaceID: "user:person-1",
		Content:     "사용자의 이름은 민수다.",
	})
	memoryService.StoreMemoryFact(MemoryFact{
		ScopeType:         ScopeTypeWorkspace,
		NamespaceID:       WorkspaceNamespace("default", 50, []string{"finance"}).NamespaceID,
		Content:           "회사 법인카드는 재무팀만 쓴다.",
		SecurityLevelRank: 50,
		RequiredClasses:   []string{"finance"},
	})
	memoryService.StoreMemoryFact(MemoryFact{
		ScopeType:         ScopeTypeConversation,
		NamespaceID:       ConversationNamespace("channel-1", 10, []string{"internal"}).NamespaceID,
		Content:           "이 채널은 릴리즈 회의용이다.",
		SecurityLevelRank: 10,
		RequiredClasses:   []string{"internal"},
	})

	personOneFacts, errorValue := memoryService.SearchMemory(context.Background(), MemorySearchRequest{
		ReaderPersonID:          "person-1",
		ReaderSecurityLevelRank: 100,
		ReaderGrantedClasses:    []string{"internal", "finance"},
		Namespaces: []MemoryNamespace{
			UserNamespace("person-1"),
			WorkspaceNamespace("default", 50, []string{"finance"}),
			ConversationNamespace("channel-1", 10, []string{"internal"}),
		},
	})
	if errorValue != nil {
		t.Fatalf("expected search to succeed: %v", errorValue)
	}
	if len(personOneFacts) != 3 {
		t.Fatalf("expected user, workspace, and conversation memory, got %d", len(personOneFacts))
	}

	personTwoFacts, errorValue := memoryService.SearchMemory(context.Background(), MemorySearchRequest{
		ReaderPersonID:          "person-2",
		ReaderSecurityLevelRank: 100,
		ReaderGrantedClasses:    []string{"internal", "finance"},
		Namespaces: []MemoryNamespace{
			UserNamespace("person-2"),
			WorkspaceNamespace("default", 50, []string{"finance"}),
			ConversationNamespace("channel-1", 10, []string{"internal"}),
		},
	})
	if errorValue != nil {
		t.Fatalf("expected search to succeed: %v", errorValue)
	}
	if containsMemory(personTwoFacts, "사용자의 이름은 민수다.") {
		t.Fatal("expected other user not to read person-1 user memory")
	}

	lowAccessFacts, errorValue := memoryService.SearchMemory(context.Background(), MemorySearchRequest{
		ReaderPersonID:          "person-1",
		ReaderSecurityLevelRank: 10,
		ReaderGrantedClasses:    []string{"internal"},
		Namespaces: []MemoryNamespace{
			UserNamespace("person-1"),
			WorkspaceNamespace("default", 50, []string{"finance"}),
			ConversationNamespace("channel-2", 10, []string{"internal"}),
		},
	})
	if errorValue != nil {
		t.Fatalf("expected search to succeed: %v", errorValue)
	}
	if containsMemory(lowAccessFacts, "회사 법인카드는 재무팀만 쓴다.") {
		t.Fatal("expected workspace memory to respect security classes")
	}
	if containsMemory(lowAccessFacts, "이 채널은 릴리즈 회의용이다.") {
		t.Fatal("expected conversation memory to stay in its conversation")
	}
}

func containsMemory(memoryFacts []MemoryFact, content string) bool {
	for _, memoryFact := range memoryFacts {
		if memoryFact.Content == content {
			return true
		}
	}
	return false
}
