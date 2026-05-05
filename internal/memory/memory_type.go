package memory

import "time"

const (
	ScopeTypeUser         = "user"
	ScopeTypeWorkspace    = "workspace"
	ScopeTypeConversation = "conversation"
	ScopeTypeCircle       = "circle"
	ScopeTypePrivate      = "private"
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

type MemoryNamespace struct {
	NamespaceID         string   `json:"namespaceID"`
	ScopeType           string   `json:"scopeType"`
	ScopePersonID       string   `json:"scopePersonID,omitempty"`
	ScopeConversationID string   `json:"scopeConversationID,omitempty"`
	ScopeCircleID       string   `json:"scopeCircleID,omitempty"`
	SecurityLevelRank   int      `json:"securityLevelRank"`
	RequiredClasses     []string `json:"requiredClasses"`
}

type MemoryEpisode struct {
	EpisodeID       string            `json:"episodeID"`
	Platform        string            `json:"platform"`
	MessageID       string            `json:"messageID"`
	ConversationID  string            `json:"conversationID"`
	SenderPersonID  string            `json:"senderPersonID"`
	Prompt          string            `json:"prompt"`
	OccurredAt      time.Time         `json:"occurredAt"`
	Namespaces      []MemoryNamespace `json:"namespaces"`
	Source          string            `json:"source"`
	SourceReference string            `json:"sourceReference"`
}

type MemoryFact struct {
	FactID            string    `json:"factID"`
	ScopeType         string    `json:"scopeType"`
	NamespaceID       string    `json:"namespaceID"`
	Content           string    `json:"content"`
	Score             float64   `json:"score"`
	SourceEpisodeID   string    `json:"sourceEpisodeID"`
	ValidAt           time.Time `json:"validAt"`
	SecurityLevelRank int       `json:"securityLevelRank"`
	RequiredClasses   []string  `json:"requiredClasses"`
}
