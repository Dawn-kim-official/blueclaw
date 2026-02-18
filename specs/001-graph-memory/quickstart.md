# Developer Quickstart: Graph Memory System

**Feature**: 001-graph-memory
**Date**: 2026-02-19

## What changes

The memory system moves from a hybrid SQLite-index + filesystem-files design to
a pure-SQLite graph database. Three new behaviors are introduced:

1. Memories are typed (`fact`, `preference`, `episode`) with type-specific expiration.
2. Memories can be connected by directed, labeled edges.
3. `recall` returns top-K results plus their 1-hop graph neighbors.

## Running tests

```bash
# Memory package (requires CGo for sqlite-vec)
CGO_ENABLED=1 go test ./internal/memory/...

# Tool package
go test ./internal/tool/...

# All tests
CGO_ENABLED=1 go test ./...
go test -race ./...
```

## Key files after implementation

```text
internal/memory/
├── graph_store.go      # NEW: GraphStore, schema, Save/Load/Connect/Neighbors/Cleanup
├── search.go           # UPDATED: TopK + SaveEmbedding (memory_metadata → memories)
├── embedding.go        # UNCHANGED
├── vec_register.go     # UNCHANGED
├── store.go            # DELETED
└── promotion.go        # DELETED

internal/tool/
├── remember.go         # UPDATED: adds type param, renames subject→title
├── recall.go           # UPDATED: adds 1-hop expansion, removes file fallback
└── connect.go          # NEW: connect tool

internal/daemon/
└── daemon.go           # UPDATED: GraphStore, cleanup on start, register connect tool

internal/heartbeat/
└── heartbeat.go        # UPDATED: accept cleanupFn, call before executeHeartbeat
```

## Construction flow (daemon.go)

```go
// Old
store := memory.NewStore(blueclawDirectory)
searchIndex, err := memory.NewSearchIndex(databasePath)
toolRegistry.Register(tool.NewRememberTool(store, searchIndex, embeddingClient))
toolRegistry.Register(tool.NewRecallTool(store, searchIndex, embeddingClient, config.MemoryTopK))

// New
graphStore, err := memory.NewGraphStore(databasePath)
if err != nil { return fmt.Errorf("opening graph store: %w", err) }
defer graphStore.Close()
if err := graphStore.CleanupExpired(); err != nil {
    log.Printf("warning: memory cleanup failed: %v", err)
}
toolRegistry.Register(tool.NewRememberTool(graphStore, embeddingClient))
toolRegistry.Register(tool.NewRecallTool(graphStore, embeddingClient, config.MemoryTopK))
toolRegistry.Register(tool.NewConnectTool(graphStore))
heartbeatService := heartbeat.NewService(..., graphStore.CleanupExpired)
```

## TDD order

1. `graph_store_test.go` — Save, Load, Connect, Neighbors, CleanupExpired, Promote
2. `search_test.go` — update to use GraphStore.TopK
3. `tool/remember_test.go` — update for new type param
4. `tool/recall_test.go` — add graph expansion assertions
5. `tool/connect_test.go` — new, covers happy path + self-loop + missing titles
6. `daemon_test.go` — integration: cleanup called on startup

## Agent usage examples

```
remember(title="Lee's name", content="The user's name is Lee", type="fact")
remember(title="Lee prefers concise answers", content="User said: be brief", type="preference")
remember(title="Team meeting 2026-02-19", content="Discussed Q1 roadmap", type="episode")

connect(from_title="Q1 roadmap priorities", to_title="Team meeting 2026-02-19", relation="derives")

recall(query="what does Lee prefer")
# Returns: "Lee prefers concise answers" (search hit)
#          "Lee's name" (if connected as neighbor)
```
