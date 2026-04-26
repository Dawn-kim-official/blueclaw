package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"blueclaw/internal/llm"
)

type MemoryExtractionService struct {
	languageModel llm.LanguageModelProvider
	memoryService *MemoryService
}

type MemoryExtractionInput struct {
	PersonID                 string
	Prompt                   string
	ConversationID           string
	SourcePlatform           string
	SourceMessageID          string
	DefaultSecurityLevelRank int
	DefaultRequiredClasses   []string
}

type memoryExtractionResponse struct {
	Candidates []memoryCandidate `json:"candidates"`
}

type memoryCandidate struct {
	ScopeType         string   `json:"scopeType"`
	SubjectPersonID   string   `json:"subjectPersonID"`
	Title             string   `json:"title"`
	MemoryType        string   `json:"memoryType"`
	Content           string   `json:"content"`
	Confidence        float64  `json:"confidence"`
	SecurityLevelRank int      `json:"securityLevelRank"`
	RequiredClasses   []string `json:"requiredClasses"`
}

func NewMemoryExtractionService(languageModel llm.LanguageModelProvider, memoryService *MemoryService) *MemoryExtractionService {
	return &MemoryExtractionService{
		languageModel: languageModel,
		memoryService: memoryService,
	}
}

func (service *MemoryExtractionService) ExtractAndStore(ctx context.Context, input MemoryExtractionInput) ([]MemoryRecord, error) {
	if service == nil || service.languageModel == nil || service.memoryService == nil {
		return nil, nil
	}
	if strings.TrimSpace(input.PersonID) == "" || strings.TrimSpace(input.Prompt) == "" {
		return nil, nil
	}

	response, errorValue := service.languageModel.GenerateStructuredResponse(ctx, llm.StructuredResponseRequest{
		Messages: []llm.Message{
			{Role: "system", Content: memoryExtractionSystemPrompt()},
			{Role: "user", Content: input.Prompt},
		},
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:               "blueclaw_memory_extraction",
			Document:           memoryExtractionSchema(),
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return nil, errorValue
	}

	candidates, errorValue := decodeMemoryCandidates(response.Content)
	if errorValue != nil {
		return nil, errorValue
	}

	storedRecords := []MemoryRecord{}
	for _, candidate := range candidates {
		memoryRecord, isValid := service.buildMemoryRecord(input, candidate)
		if !isValid {
			continue
		}
		service.memoryService.StoreDerivedMemory(memoryRecord)
		storedRecords = append(storedRecords, memoryRecord)
	}
	return storedRecords, nil
}

func (service *MemoryExtractionService) buildMemoryRecord(input MemoryExtractionInput, candidate memoryCandidate) (MemoryRecord, bool) {
	scopeType := strings.TrimSpace(candidate.ScopeType)
	content := strings.TrimSpace(candidate.Content)
	title := strings.TrimSpace(candidate.Title)
	if candidate.Confidence < 0.65 || content == "" || title == "" {
		return MemoryRecord{}, false
	}

	memoryRecord := MemoryRecord{
		MemoryRecordID:       memoryRecordID(input, candidate),
		ScopeType:            scopeType,
		SourceConversationID: strings.TrimSpace(input.ConversationID),
		Title:                title,
		ContentCiphertext:    []byte(content),
		MemoryType:           firstNonEmpty(strings.TrimSpace(candidate.MemoryType), "derived"),
		SourcePlatform:       strings.TrimSpace(input.SourcePlatform),
		SourceMessageID:      strings.TrimSpace(input.SourceMessageID),
		SecurityLevelRank:    maximumInt(input.DefaultSecurityLevelRank, candidate.SecurityLevelRank),
		RequiredClasses:      unionStrings(input.DefaultRequiredClasses, candidate.RequiredClasses),
		UpdatedAt:            time.Now().UTC(),
	}

	switch scopeType {
	case ScopeTypeUser:
		if !isCurrentUserMemory(input, candidate) {
			return MemoryRecord{}, false
		}
		memoryRecord.ScopePersonID = input.PersonID
	case ScopeTypeWorkspace:
	case ScopeTypeConversation:
		if strings.TrimSpace(input.ConversationID) == "" {
			return MemoryRecord{}, false
		}
		memoryRecord.ScopeConversationID = input.ConversationID
	default:
		return MemoryRecord{}, false
	}

	return memoryRecord, true
}

func isCurrentUserMemory(input MemoryExtractionInput, candidate memoryCandidate) bool {
	subjectPersonID := strings.TrimSpace(candidate.SubjectPersonID)
	if subjectPersonID != "" && subjectPersonID != input.PersonID {
		return false
	}
	prompt := strings.ToLower(input.Prompt)
	firstPersonMarkers := []string{"내 ", "나는", "제가", "저는", "my ", "i am", "i'm"}
	for _, marker := range firstPersonMarkers {
		if strings.Contains(prompt, marker) {
			return true
		}
	}
	return subjectPersonID == input.PersonID
}

func decodeMemoryCandidates(content string) ([]memoryCandidate, error) {
	var response memoryExtractionResponse
	if errorValue := json.Unmarshal([]byte(content), &response); errorValue != nil {
		return nil, errorValue
	}
	if response.Candidates == nil {
		return nil, errors.New("memory extraction candidates missing")
	}
	return response.Candidates, nil
}

func memoryRecordID(input MemoryExtractionInput, candidate memoryCandidate) string {
	scopeKey := "workspace"
	switch strings.TrimSpace(candidate.ScopeType) {
	case ScopeTypeUser:
		scopeKey = strings.TrimSpace(input.PersonID)
	case ScopeTypeConversation:
		scopeKey = strings.TrimSpace(input.ConversationID)
	}
	key := strings.Join([]string{
		strings.TrimSpace(candidate.ScopeType),
		scopeKey,
		strings.ToLower(strings.TrimSpace(candidate.Title)),
	}, ":")
	sum := sha256.Sum256([]byte(key))
	return "memory:" + hex.EncodeToString(sum[:])
}

func memoryExtractionSystemPrompt() string {
	return "Extract durable Blueclaw memory candidates from the user message. Classify each candidate as user, workspace, or conversation. Use user only for first-person facts about the current sender, workspace for company or shared operational knowledge, and conversation for facts only meaningful in this conversation. Return no candidates for low-confidence or sensitive ambiguous claims."
}

func memoryExtractionSchema() string {
	return `{"type":"object","properties":{"candidates":{"type":"array","items":{"type":"object","properties":{"scopeType":{"type":"string","enum":["user","workspace","conversation"]},"subjectPersonID":{"type":"string"},"title":{"type":"string"},"memoryType":{"type":"string"},"content":{"type":"string"},"confidence":{"type":"number"},"securityLevelRank":{"type":"integer"},"requiredClasses":{"type":"array","items":{"type":"string"}}},"required":["scopeType","title","memoryType","content","confidence","securityLevelRank","requiredClasses"],"additionalProperties":false}}},"required":["candidates"],"additionalProperties":false}`
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func maximumInt(leftValue int, rightValue int) int {
	if leftValue > rightValue {
		return leftValue
	}
	return rightValue
}

func unionStrings(leftValues []string, rightValues []string) []string {
	valueByName := map[string]bool{}
	values := []string{}
	for _, value := range append(append([]string{}, leftValues...), rightValues...) {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" || valueByName[trimmedValue] {
			continue
		}
		valueByName[trimmedValue] = true
		values = append(values, trimmedValue)
	}
	return values
}
