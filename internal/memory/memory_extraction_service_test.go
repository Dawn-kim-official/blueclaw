package memory

import (
	"context"
	"strings"
	"testing"

	"blueclaw/internal/llm"
)

func TestMemoryExtractionStoresWorkspaceMemoryWithConservativeDefaultLabel(t *testing.T) {
	memoryService := &MemoryService{}
	extractionService := NewMemoryExtractionService(staticExtractionModel{
		content: `{"candidates":[{"scopeType":"workspace","subjectPersonID":"","title":"company-card-policy","memoryType":"policy","content":"회사 법인카드는 재무팀만 사용한다.","confidence":0.92,"securityLevelRank":0,"requiredClasses":[]}]}`,
	}, memoryService)

	records, errorValue := extractionService.ExtractAndStore(context.Background(), MemoryExtractionInput{
		PersonID:                 "person-1",
		Prompt:                   "우리 회사 법인카드는 재무팀만 써",
		ConversationID:           "dm-1",
		DefaultSecurityLevelRank: 10,
		DefaultRequiredClasses:   []string{"internal"},
	})
	if errorValue != nil {
		t.Fatalf("expected extraction to succeed: %v", errorValue)
	}
	if len(records) != 1 {
		t.Fatalf("expected one memory record, got %d", len(records))
	}
	if records[0].ScopeType != ScopeTypeWorkspace {
		t.Fatalf("expected workspace memory, got %q", records[0].ScopeType)
	}
	if records[0].SecurityLevelRank != 10 || !testContainsString(records[0].RequiredClasses, "internal") {
		t.Fatalf("expected conservative default label, got rank=%d classes=%v", records[0].SecurityLevelRank, records[0].RequiredClasses)
	}
}

func TestMemoryExtractionRejectsThirdPartyUserMemory(t *testing.T) {
	memoryService := &MemoryService{}
	extractionService := NewMemoryExtractionService(staticExtractionModel{
		content: `{"candidates":[{"scopeType":"user","subjectPersonID":"person-2","title":"name","memoryType":"profile","content":"person-2의 이름은 민수다.","confidence":0.95,"securityLevelRank":0,"requiredClasses":[]}]}`,
	}, memoryService)

	records, errorValue := extractionService.ExtractAndStore(context.Background(), MemoryExtractionInput{
		PersonID:       "person-1",
		Prompt:         "민수 이름은 민수야",
		ConversationID: "channel-1",
	})
	if errorValue != nil {
		t.Fatalf("expected extraction to succeed: %v", errorValue)
	}
	if len(records) != 0 {
		t.Fatalf("expected third-party user memory to be rejected, got %d", len(records))
	}
}

func TestWorkspaceMemoryRecordIDDoesNotDependOnSpeaker(t *testing.T) {
	candidate := memoryCandidate{
		ScopeType:  ScopeTypeWorkspace,
		Title:      "company-card-policy",
		MemoryType: "policy",
		Content:    "회사 법인카드는 재무팀만 사용한다.",
		Confidence: 0.95,
	}
	firstID := memoryRecordID(MemoryExtractionInput{PersonID: "person-1", ConversationID: "dm-1"}, candidate)
	secondID := memoryRecordID(MemoryExtractionInput{PersonID: "person-2", ConversationID: "channel-1"}, candidate)
	if firstID != secondID {
		t.Fatalf("expected workspace memory id to ignore speaker and source conversation, got %q and %q", firstID, secondID)
	}
}

func testContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type staticExtractionModel struct {
	content string
}

func (model staticExtractionModel) GenerateResponse(context.Context, string) (string, error) {
	return model.content, nil
}

func (model staticExtractionModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name != "blueclaw_memory_extraction" {
		return llm.StructuredResponse{Content: `{"reply":"ok"}`}, nil
	}
	if !strings.Contains(request.Messages[0].Content, "workspace") {
		return llm.StructuredResponse{}, nil
	}
	return llm.StructuredResponse{Content: model.content}, nil
}
