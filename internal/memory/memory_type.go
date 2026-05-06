package memory

import "time"

const (
	ScopeTypeUser         = "user"
	ScopeTypeWorkspace    = "workspace"
	ScopeTypeConversation = "conversation"
	ScopeTypeCircle       = "circle"
	ScopeTypePrivate      = "private"
)

const (
	MemorySourceKindFact    = "fact"
	MemorySourceKindNode    = "node"
	MemorySourceKindEpisode = "episode"
)

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

type MemoryIngestionResult struct {
	EpisodeID      string `json:"episodeID"`
	NamespaceCount int    `json:"namespaceCount"`
}

type MemoryFact struct {
	FactID            string    `json:"factID"`
	ScopeType         string    `json:"scopeType"`
	NamespaceID       string    `json:"namespaceID"`
	Content           string    `json:"content"`
	Score             float64   `json:"score"`
	SourceEpisodeID   string    `json:"sourceEpisodeID"`
	SourceKind        string    `json:"sourceKind"`
	ValidAt           time.Time `json:"validAt"`
	SecurityLevelRank int       `json:"securityLevelRank"`
	RequiredClasses   []string  `json:"requiredClasses"`
}

type MemoryHealth struct {
	Configured         bool   `json:"configured"`
	Reachable          bool   `json:"reachable"`
	LastSearchError    string `json:"lastSearchError,omitempty"`
	LastIngestionError string `json:"lastIngestionError,omitempty"`
	Error              string `json:"error,omitempty"`
}
