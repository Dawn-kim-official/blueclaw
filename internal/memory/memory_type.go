package memory

import "time"

const (
	ScopeTypeUser         = "user"
	ScopeTypeWorkspace    = "workspace"
	ScopeTypeConversation = "conversation"
)

type ContentSegment struct {
	ContentSegmentID     string    `json:"contentSegmentID"`
	SourceConversationID string    `json:"sourceConversationID"`
	OwnerPersonID        string    `json:"ownerPersonID"`
	ContentCiphertext    []byte    `json:"contentCiphertext"`
	SecurityLevelRank    int       `json:"securityLevelRank"`
	RequiredClasses      []string  `json:"requiredClasses"`
	OccurredAt           time.Time `json:"occurredAt"`
	ExpiresAt            time.Time `json:"expiresAt"`
}

type MemoryRecord struct {
	MemoryRecordID       string    `json:"memoryRecordID"`
	ScopeType            string    `json:"scopeType"`
	ScopePersonID        string    `json:"scopePersonID"`
	ScopeConversationID  string    `json:"scopeConversationID"`
	SourceConversationID string    `json:"sourceConversationID"`
	Title                string    `json:"title"`
	MemoryType           string    `json:"memoryType"`
	SourcePlatform       string    `json:"sourcePlatform"`
	SourceMessageID      string    `json:"sourceMessageID"`
	ContentCiphertext    []byte    `json:"contentCiphertext"`
	SecurityLevelRank    int       `json:"securityLevelRank"`
	RequiredClasses      []string  `json:"requiredClasses"`
	UpdatedAt            time.Time `json:"updatedAt"`
}
