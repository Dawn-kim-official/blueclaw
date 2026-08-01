package bluecollar

import "time"

// Scope names a memory fact's reach. The loop only compares and displays these,
// so it carries its own copy rather than depending on the service that stores
// them.
const (
	MemoryScopeUser         = "user"
	MemoryScopeWorkspace    = "workspace"
	MemoryScopeCircle       = "circle"
	MemoryScopeConversation = "conversation"
)

// MemoryFact is what the loop reads: recalled context to put in front of the
// model. Whoever recalls it converts into this shape at the boundary.
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
