package memory

import "testing"

func TestMemoryServiceSeparatesUserWorkspaceAndConversationScopes(t *testing.T) {
	memoryService := &MemoryService{}
	memoryService.StoreDerivedMemory(MemoryRecord{
		ScopeType:         ScopeTypeUser,
		ScopePersonID:     "person-1",
		ContentCiphertext: []byte("사용자의 이름은 민수다."),
	})
	memoryService.StoreDerivedMemory(MemoryRecord{
		ScopeType:         ScopeTypeWorkspace,
		ContentCiphertext: []byte("회사 법인카드는 재무팀만 쓴다."),
		SecurityLevelRank: 50,
		RequiredClasses:   []string{"finance"},
	})
	memoryService.StoreDerivedMemory(MemoryRecord{
		ScopeType:           ScopeTypeConversation,
		ScopeConversationID: "channel-1",
		ContentCiphertext:   []byte("이 채널은 릴리즈 회의용이다."),
		SecurityLevelRank:   10,
		RequiredClasses:     []string{"internal"},
	})

	personOneRecords := memoryService.SearchMemory(MemorySearchRequest{
		ReaderPersonID:          "person-1",
		ReaderSecurityLevelRank: 100,
		ReaderGrantedClasses:    []string{"internal", "finance"},
		ConversationID:          "channel-1",
	})
	if len(personOneRecords) != 3 {
		t.Fatalf("expected user, workspace, and conversation memory, got %d", len(personOneRecords))
	}

	personTwoRecords := memoryService.SearchMemory(MemorySearchRequest{
		ReaderPersonID:          "person-2",
		ReaderSecurityLevelRank: 100,
		ReaderGrantedClasses:    []string{"internal", "finance"},
		ConversationID:          "channel-1",
	})
	if containsMemory(personTwoRecords, "사용자의 이름은 민수다.") {
		t.Fatal("expected other user not to read person-1 user memory")
	}

	lowAccessRecords := memoryService.SearchMemory(MemorySearchRequest{
		ReaderPersonID:          "person-1",
		ReaderSecurityLevelRank: 10,
		ReaderGrantedClasses:    []string{"internal"},
		ConversationID:          "channel-2",
	})
	if containsMemory(lowAccessRecords, "회사 법인카드는 재무팀만 쓴다.") {
		t.Fatal("expected workspace memory to respect security classes")
	}
	if containsMemory(lowAccessRecords, "이 채널은 릴리즈 회의용이다.") {
		t.Fatal("expected conversation memory to stay in its conversation")
	}
}

func containsMemory(memoryRecords []MemoryRecord, content string) bool {
	for _, memoryRecord := range memoryRecords {
		if string(memoryRecord.ContentCiphertext) == content {
			return true
		}
	}
	return false
}
