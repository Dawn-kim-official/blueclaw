package bluecollar

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
