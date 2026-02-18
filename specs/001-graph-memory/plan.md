# Implementation Plan: Graph Memory System

**Branch**: `001-graph-memory` | **Date**: 2026-02-19 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/001-graph-memory/spec.md`

## Summary

Replace the hybrid filesystem + SQLite memory system with a pure-SQLite graph
database. Memories become typed nodes (`fact`, `preference`, `episode`) with
expiration semantics. Nodes are connected by directed, labeled edges. The
`recall` tool expands each search hit one hop in the graph before returning
results. A new `connect` tool lets the agent link memories. The daemon wires
cleanup on startup and heartbeat.

## Technical Context

**Language/Version**: Go 1.22+
**Primary Dependencies**: mattn/go-sqlite3 (CGo), sqlite-vec (vendored C files)
**Storage**: SQLite — `~/.blueclaw/db/memory.db` (single file, three tables)
**Testing**: `go test ./...` with `CGO_ENABLED=1` for memory package
**Target Platform**: macOS (darwin) + Linux
**Project Type**: Single Go module (`cmd/` + `internal/`)
**Performance Goals**: Recall completes within a single agent turn; no latency
regression vs. current TopK-only path
**Constraints**: No new external dependencies. CGo permitted for sqlite-vec only.
No file exceeds 300 lines (constitution IV).
**Scale/Scope**: Single-user daemon; memory set expected < 10k nodes in practice

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Simplicity First | PASS | GraphStore consolidates Store + SearchIndex into one struct. Net line-count decrease. New `connect` tool is additive. |
| II. Type Safety | PASS | `MemoryType` is a typed string constant; no `any` except at the tool JSON boundary. All new methods use concrete types. |
| III. Test-First | PASS | TDD order defined in quickstart.md. Each new function gets a failing test before implementation. |
| IV. Clean Code | PASS | GraphStore methods are single-responsibility and < 20 lines each. Package split preserved. |

No violations. Complexity Tracking table not required.

## Project Structure

### Documentation (this feature)

```text
specs/001-graph-memory/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/
│   ├── remember.md
│   ├── recall.md
│   └── connect.md
└── tasks.md             # Phase 2 output (/speckit.tasks — not yet created)
```

### Source Code (repository root)

```text
internal/memory/
├── graph_store.go      # CREATE: GraphStore, schema, all node/edge/cleanup methods
├── search.go           # UPDATE: TopK uses memories table; add SaveEmbedding
├── embedding.go        # KEEP: EmbeddingGenerator, EmbeddingClient, SidecarProcess
├── vec_register.go     # KEEP: CGo registration
├── store.go            # DELETE: filesystem store superseded
└── promotion.go        # DELETE: logic absorbed into graph_store.go

internal/tool/
├── remember.go         # UPDATE: type param, title rename, GraphStore dependency
├── recall.go           # UPDATE: 1-hop expansion, no file-listing fallback
├── connect.go          # CREATE: new connect tool
└── [rest unchanged]

internal/daemon/
└── daemon.go           # UPDATE: GraphStore construction, cleanup on start

internal/heartbeat/
└── heartbeat.go        # UPDATE: cleanupFn func() error field + call

internal/memory/ (test files)
├── graph_store_test.go # CREATE
├── search_test.go      # UPDATE
├── store_test.go       # DELETE
└── promotion_test.go   # DELETE

internal/tool/ (test files)
├── remember_test.go    # UPDATE
├── recall_test.go      # UPDATE (currently missing — create)
└── connect_test.go     # CREATE
```

**Structure Decision**: Single Go module, flat `internal/` packages. No new
packages introduced. Follows existing layout.

## Implementation Steps

### Step 1: GraphStore — schema + core node operations

**Files**: `internal/memory/graph_store.go`, `internal/memory/graph_store_test.go`

Create `GraphStore` with:
- `NewGraphStore(databasePath string) (*GraphStore, error)` — open DB, register vec,
  create schema (memories, memory_connections, vec_memories tables).
- `Save(title, content string, memType MemoryType, expiresAt *time.Time) (int64, error)`
  — INSERT OR REPLACE into memories; return row ID.
- `Load(title string) (Memory, error)` — SELECT by title.
- `IncrementRecall(id int64) error` — UPDATE recall_count + last_recalled_at.
- `ExtendExpiration(id int64, duration time.Duration) error` — UPDATE expires_at.
- `Promote(id int64) error` — SET expires_at = NULL.
- `CleanupExpired() error` — DELETE FROM memories WHERE expires_at < CURRENT_TIMESTAMP.
- `Close() error` — close DB.

Test table covers: save+load round-trip, upsert on duplicate title, type constraint
rejection, cleanup deletes expired only, promote sets expires_at to NULL, fact never
deleted by cleanup.

### Step 2: GraphStore — edge operations

**Files**: `internal/memory/graph_store.go` (continued), `internal/memory/graph_store_test.go`

Add:
- `Connect(fromID, toID int64, relation string) error`
  — INSERT OR REPLACE into memory_connections.
- `Neighbors(id int64) ([]Memory, error)`
  — SELECT from memories WHERE id IN (outgoing to_ids UNION incoming from_ids).

Test table covers: create edge, replace on duplicate pair, neighbors returns both
directions, cascade delete on memory removal.

### Step 3: Search — TopK + SaveEmbedding using GraphStore

**Files**: `internal/memory/search.go`, `internal/memory/search_test.go`

Refactor `SearchIndex` → methods on `GraphStore`:
- `SaveEmbedding(id int64, embedding []float32) error`
  — INSERT OR REPLACE into vec_memories.
- `TopK(queryEmbedding []float32, k int) ([]Memory, error)`
  — vec_memories MATCH query JOIN memories.

Remove `SearchIndex` struct, `memory_metadata` table, `UpdateRecallCount`,
`UpdateStorage` methods. These are replaced by `GraphStore` methods.

Update `search_test.go` to use `NewGraphStore`.

### Step 4: Delete superseded files

**Files**: `internal/memory/store.go`, `internal/memory/promotion.go`,
`internal/memory/store_test.go`, `internal/memory/promotion_test.go`

Delete all four. Verify `go build ./...` passes (no dangling references before
touching tools).

### Step 5: Update remember tool

**Files**: `internal/tool/remember.go`, `internal/tool/remember_test.go`

Changes:
- Replace `store *memory.Store` + `searchIndex *memory.SearchIndex` constructor
  args with `graphStore *memory.GraphStore`.
- Add `type` to ParameterSchema (required enum: fact/preference/episode).
- Rename `subject` → `title` in schema and Execute.
- Compute default `expiresAt` from type.
- Call `graphStore.Save(title, content, memType, expiresAt)` → get `id`.
- Call `graphStore.SaveEmbedding(id, embedding)`.
- Embed title only (not `title+" "+content`).

Update tests: add type param to all test cases; verify episode gets expiration,
fact/preference get nil.

### Step 6: Update recall tool

**Files**: `internal/tool/recall.go`, `internal/tool/recall_test.go`

Changes:
- Replace `store + searchIndex` with `graphStore`.
- Remove `recallByListing` fallback method.
- After TopK: call `graphStore.Neighbors(id)` for each hit.
- Handle recall state per hit: promote or extend + increment.
- Build deduplicated result set with `source` field (`search` vs `neighbor`).

Update tests: create memories + connect them; verify neighbor appears in output.

### Step 7: Create connect tool

**Files**: `internal/tool/connect.go`, `internal/tool/connect_test.go`

Implement `ConnectTool`:
- `Name()` → `"connect"`
- ParameterSchema: `from_title`, `to_title`, `relation` (all required strings).
- Execute: validate non-empty + from ≠ to; load both IDs; call `graphStore.Connect`.

Tests: happy path, self-loop rejected, missing title returns error.

### Step 8: Update daemon

**File**: `internal/daemon/daemon.go`

Changes:
- Replace `store := memory.NewStore(...)` with `graphStore, err := memory.NewGraphStore(databasePath)`.
- Remove `store` from all tool constructors.
- Call `graphStore.CleanupExpired()` after construction (before daemon.run).
- Pass `graphStore.CleanupExpired` as `cleanupFn` to `heartbeat.NewService`.
- Register `tool.NewConnectTool(graphStore)`.
- Defer `graphStore.Close()` (replaces `defer daemon.searchIndex.Close()`).
- Remove `searchIndex` field from `Daemon` struct; add `graphStore` field.

### Step 9: Update heartbeat service

**File**: `internal/heartbeat/heartbeat.go`

Changes:
- Add `cleanupFn func() error` field to `Service` struct.
- Update `NewService` signature: add `cleanupFn func() error` parameter.
- At start of `executeHeartbeat`: if `cleanupFn != nil`, call it; log warning on error.

### Step 10: Integration verification

```bash
CGO_ENABLED=1 go build ./...
CGO_ENABLED=1 go test ./...
go test -race ./...
go vet ./...
staticcheck ./...
```

Manually verify: start daemon, ask agent to remember an episode, recall it 3×,
confirm it becomes permanent.

## Complexity Tracking

No constitution violations. Table omitted per governance rules.
