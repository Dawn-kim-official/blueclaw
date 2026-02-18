# Research: Graph Memory System

**Feature**: 001-graph-memory
**Date**: 2026-02-19

## supermemory.ai Reference Architecture

**Source**: https://supermemory.ai/docs/concepts/graph-memory

### Memory Type Alignment

Supermemory.ai confirms the three-type model aligns with established graph-memory
research:

| Type | Supermemory.ai description | Blueclaw behavior |
|------|---------------------------|-------------------|
| fact | "Persists until updated" | No expiration; updated in-place when recalled with new content |
| preference | "Strengthens with repetition" | No expiration; recall count incremented on each access |
| episode | "Decays unless significant" | 7-day default expiration; extended on each recall; promoted at recall ≥ 3 |

Key validation: all three type behaviors in the spec match supermemory.ai's model exactly.

### Edge Type Semantics (from supermemory.ai)

Supermemory.ai formally defines three edge relation labels:

| Label | Meaning | Supermemory.ai notes |
|-------|---------|----------------------|
| `updates` | New info contradicts existing memory | Supermemory preserves both nodes, flagging older as `isLatest=false` |
| `extends` | New details enrich without replacing | Both nodes remain equally current |
| `derives` | Agent/system infers a new fact from patterns | Derived node is not user-stated |

**Decision**: Adopt all three labels as the canonical recommended set, but allow any
non-empty string as the relation label (open vocabulary, per FR-006). This keeps
the system flexible while providing clear naming conventions in documentation.

**isLatest flag (considered, deferred)**:
Supermemory.ai adds an `isLatest` boolean to memories connected via `updates` edges.
This helps callers know which of two connected memories is the current truth.

- **Decision**: Not adopted in this iteration.
- **Rationale**: FR-013 already handles the common case — `remember` with an existing
  title updates the content in place, so no `updates` edge is created for simple edits.
  The `isLatest` flag is only meaningful if both old and new facts are preserved (a
  "revision history" pattern). The spec does not require revision history.
- **Revisit trigger**: If the agent begins using `updates` edges to preserve old facts
  alongside new ones, add `is_latest BOOLEAN NOT NULL DEFAULT TRUE` to the memories
  table.

### Embedding Strategy

**Decision**: Embed the memory **title** only (not title + content).
**Rationale**:
- Current `remember.go` embeds `subject+" "+content`. Title-only is simpler
  and the embedding dimension stays bounded regardless of content length.
- Supermemory.ai embeds titles for the graph layer and uses content for
  full-text recall; same split here.
- Changing to title-only also aligns with vec_memories being keyed by title,
  making the vector semantically coherent.
**Alternatives considered**: Embedding `title + content` (current behavior) — rejected
because long content dilutes the embedding signal for graph-based lookup.

## Schema Design

### Decision: Unified GraphStore (single DB connection)

**Decision**: Merge the current `Store` (filesystem) and `SearchIndex` (SQLite) into a
single `GraphStore` struct backed by one SQLite connection.

**Rationale**:
- The old Store used the filesystem; the new design is entirely SQLite. There is no
  reason to maintain two connections to the same file.
- A single connection simplifies transactions across `memories` and `vec_memories`
  in one atomic operation.
- Fewer constructor arguments passed to tools (one `*GraphStore` instead of
  `*Store + *SearchIndex`).

**Alternatives considered**:
- Keep Store + SearchIndex separate but both SQLite: rejected, unnecessary complexity.
- Use a repository pattern with interfaces: rejected, YAGNI — one implementation.

### Decision: expires_at NULL = permanent

**Decision**: `expires_at IS NULL` means the memory is permanent (never expires).
`expires_at IS NOT NULL` means the memory has a deadline.

**Rationale**: Matches constitution wording directly. NULL is a database-native way to
represent "no deadline" without an artificial sentinel value (e.g., year 9999).

### Decision: vec_memories.id = memories.id (shared primary key)

**Decision**: `vec_memories` rowid is the same integer as `memories.id`.
`JOIN memories ON memories.id = v.rowid` is the lookup path.

**Rationale**: Same as current `SearchIndex` design — rowid-based joins are fast
in SQLite and avoid a separate foreign key column.

## Cleanup Wiring

### Decision: CleanupFn callback in HeartbeatService

**Decision**: Add a `cleanupFn func() error` closure field to `HeartbeatService`.
The daemon passes `graphStore.CleanupExpired` at construction. The heartbeat
calls it at the start of each `executeHeartbeat`.

**Rationale**:
- Avoids importing `internal/memory` from `internal/heartbeat` while keeping the
  call typed and explicit.
- A closure is the simplest nullable dependency (nil = skip cleanup).
- Alternative (Cleaner interface in memory package): rejected — adds an import edge
  from heartbeat to memory with no benefit over a func type.

## Test Strategy

**Decision**: All memory package tests require `CGO_ENABLED=1`.
**Rationale**: sqlite-vec is compiled via CGo. This is already documented in MEMORY.md.

**Decision**: Delete `store_test.go` and `promotion_test.go`; replace with
`graph_store_test.go` covering all new behavior.
**Rationale**: The filesystem-based tests are entirely superseded.

**Decision**: Keep `search_test.go` but update it to use `GraphStore` methods.

## Migration

**Decision**: No migration of existing filesystem memory files.
**Rationale**: Stated as an assumption in the spec. The feature targets clean-slate.
Existing `short-term-memory/` and `long-term-memory/` directories in `~/.blueclaw/`
can be left in place; the new code will ignore them.
