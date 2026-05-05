package memory

import (
	"context"
	"testing"
	"time"

	"blueclaw/internal/policy"
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

func TestMemoryServiceRanksAfterPolicyFiltering(t *testing.T) {
	memoryService := &MemoryService{}
	olderTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newerTime := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	memoryService.StoreMemoryFact(MemoryFact{
		FactID:            "inaccessible",
		ScopeType:         ScopeTypeWorkspace,
		NamespaceID:       WorkspaceNamespace("default", 80, []string{"finance"}).NamespaceID,
		Content:           "프로젝트 오로라 예산은 비공개다.",
		Score:             50,
		SecurityLevelRank: 80,
		RequiredClasses:   []string{"finance"},
		ValidAt:           newerTime,
	})
	memoryService.StoreMemoryFact(MemoryFact{
		FactID:      "query-match",
		ScopeType:   ScopeTypeUser,
		NamespaceID: UserNamespace("person-1").NamespaceID,
		Content:     "사용자는 오로라 프로젝트를 우선한다.",
		Score:       0.1,
		ValidAt:     olderTime,
	})
	memoryService.StoreMemoryFact(MemoryFact{
		FactID:      "high-score",
		ScopeType:   ScopeTypeUser,
		NamespaceID: UserNamespace("person-1").NamespaceID,
		Content:     "사용자는 간결한 설계를 선호한다.",
		Score:       0.9,
		ValidAt:     newerTime,
	})
	memoryService.StoreMemoryFact(MemoryFact{
		FactID:      "old-low-score",
		ScopeType:   ScopeTypeUser,
		NamespaceID: UserNamespace("person-1").NamespaceID,
		Content:     "사용자는 월요일 오전 회의를 피한다.",
		Score:       0.2,
		ValidAt:     olderTime,
	})

	memoryFacts, errorValue := memoryService.SearchMemory(context.Background(), MemorySearchRequest{
		Query:                   "오로라 프로젝트",
		ReaderPersonID:          "person-1",
		ReaderSecurityLevelRank: 10,
		ReaderGrantedClasses:    []string{"internal"},
		Limit:                   2,
		Namespaces: []MemoryNamespace{
			UserNamespace("person-1"),
			WorkspaceNamespace("default", 80, []string{"finance"}),
		},
	})
	if errorValue != nil {
		t.Fatalf("expected search to succeed: %v", errorValue)
	}

	if len(memoryFacts) != 2 {
		t.Fatalf("expected limit after ranking, got %d", len(memoryFacts))
	}
	if memoryFacts[0].FactID != "query-match" {
		t.Fatalf("expected query match first, got %+v", memoryFacts)
	}
	if memoryFacts[1].FactID != "high-score" {
		t.Fatalf("expected score-ranked fact second, got %+v", memoryFacts)
	}
	if containsMemory(memoryFacts, "프로젝트 오로라 예산은 비공개다.") {
		t.Fatal("expected inaccessible high-score memory to be filtered before ranking")
	}
}

func TestMemoryServiceFiltersPrivateAndCircleResources(t *testing.T) {
	memoryService := &MemoryService{}
	privateNamespace := PrivatePersonNamespace("person-1")
	financeNamespace := CircleNamespace("default", "finance")
	memoryService.StoreMemoryFact(MemoryFact{
		FactID:      "private",
		ScopeType:   ScopeTypePrivate,
		NamespaceID: privateNamespace.NamespaceID,
		Content:     "민수와 봇의 1:1 메모다.",
	})
	memoryService.StoreMemoryFact(MemoryFact{
		FactID:      "finance",
		ScopeType:   ScopeTypeCircle,
		NamespaceID: financeNamespace.NamespaceID,
		Content:     "재무 circle 자료다.",
	})

	ownerFacts, errorValue := memoryService.SearchMemory(context.Background(), MemorySearchRequest{
		ReaderPersonID: "person-1",
		ReaderCircles:  []string{"staff", "finance"},
		Namespaces:     []MemoryNamespace{privateNamespace, financeNamespace},
	})
	if errorValue != nil {
		t.Fatalf("expected search to succeed: %v", errorValue)
	}
	if !containsMemory(ownerFacts, "민수와 봇의 1:1 메모다.") || !containsMemory(ownerFacts, "재무 circle 자료다.") {
		t.Fatalf("expected owner finance access, got %+v", ownerFacts)
	}

	otherFacts, errorValue := memoryService.SearchMemory(context.Background(), MemorySearchRequest{
		ReaderPersonID: "person-2",
		ReaderCircles:  []string{"staff"},
		Namespaces:     []MemoryNamespace{privateNamespace, financeNamespace},
	})
	if errorValue != nil {
		t.Fatalf("expected search to succeed: %v", errorValue)
	}
	if containsMemory(otherFacts, "민수와 봇의 1:1 메모다.") {
		t.Fatal("expected other person not to read private memory")
	}
	if containsMemory(otherFacts, "재무 circle 자료다.") {
		t.Fatal("expected finance non-member not to read finance memory")
	}
}

func TestMemoryServiceAppliesResourceAccessRulesBeforeRanking(t *testing.T) {
	memoryService := &MemoryService{}
	resourceAccessRules := []policy.ResourceAccessPolicy{{
		Resource: "memory:workspace",
		Actions:  []string{"read"},
		Circles:  []string{"admin"},
	}}
	memoryService.StoreMemoryFact(MemoryFact{
		FactID:      "workspace-secret",
		ScopeType:   ScopeTypeWorkspace,
		NamespaceID: WorkspaceNamespace("default", 0, nil).NamespaceID,
		Content:     "워크스페이스 공용 메모지만 rule로 막는다.",
		Score:       100,
	})
	memoryService.StoreMemoryFact(MemoryFact{
		FactID:      "private",
		ScopeType:   ScopeTypePrivate,
		NamespaceID: PrivatePersonNamespace("person-1").NamespaceID,
		Content:     "낮은 점수의 개인 메모다.",
		Score:       1,
	})

	memoryFacts, errorValue := memoryService.SearchMemory(context.Background(), MemorySearchRequest{
		ReaderPersonID:      "person-1",
		ReaderCircles:       []string{"staff"},
		ResourceAccessRules: resourceAccessRules,
		Namespaces: []MemoryNamespace{
			WorkspaceNamespace("default", 0, nil),
			PrivatePersonNamespace("person-1"),
		},
	})
	if errorValue != nil {
		t.Fatalf("expected search to succeed: %v", errorValue)
	}
	if containsMemory(memoryFacts, "워크스페이스 공용 메모지만 rule로 막는다.") {
		t.Fatal("expected resource access rule to filter workspace memory before ranking")
	}
	if !containsMemory(memoryFacts, "낮은 점수의 개인 메모다.") {
		t.Fatalf("expected private memory to remain, got %+v", memoryFacts)
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
