# Data Model: Graph Memory System

**Feature**: 001-graph-memory
**Date**: 2026-02-19

## SQLite Schema

All tables live in `~/.blueclaw/db/memory.db`.

```sql
-- Primary node table (replaces memory_metadata)
CREATE TABLE IF NOT EXISTS memories (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    type             TEXT NOT NULL CHECK(type IN ('fact', 'preference', 'episode')),
    title            TEXT NOT NULL UNIQUE,
    content          TEXT NOT NULL,
    recall_count     INTEGER NOT NULL DEFAULT 0,
    expires_at       DATETIME NULL,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_recalled_at DATETIME NULL
);

-- Directed relational edges
CREATE TABLE IF NOT EXISTS memory_connections (
    from_id    INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    to_id      INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    relation   TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (from_id, to_id)
);

-- sqlite-vec virtual table (rowid = memories.id)
CREATE VIRTUAL TABLE IF NOT EXISTS vec_memories USING vec0(
    id        INTEGER PRIMARY KEY,
    embedding FLOAT[768]
);
```

### Expiration defaults by type

| Type | Default expires_at |
|------|--------------------|
| fact | NULL (permanent) |
| preference | NULL (permanent) |
| episode | NOW + 7 days |

### Constraints

- `memories.title` is UNIQUE — duplicate titles update in place (FR-013).
- `memory_connections` PRIMARY KEY `(from_id, to_id)` — one edge per ordered pair
  (FR-007); upsert replaces relation label.
- `ON DELETE CASCADE` — deleting a memory removes all its edges (FR-009).
- Self-loops `(from_id = to_id)` are rejected by the application layer before
  any SQL is executed (FR-008).

## Go Types

### internal/memory package

```go
type MemoryType string

const (
    MemoryTypeFact       MemoryType = "fact"
    MemoryTypePreference MemoryType = "preference"
    MemoryTypeEpisode    MemoryType = "episode"
)

type Memory struct {
    ID             int64
    Type           MemoryType
    Title          string
    Content        string
    RecallCount    int
    ExpiresAt      *time.Time  // nil = permanent
    CreatedAt      time.Time
    LastRecalledAt *time.Time
}

type Connection struct {
    FromID    int64
    ToID      int64
    Relation  string
    CreatedAt time.Time
}

type GraphStore struct {
    database *sql.DB
}
```

### Constants

```go
const (
    PromotionThreshold      = 3
    DefaultEpisodeTTL       = 7 * 24 * time.Hour
    ExpirationExtension     = 7 * 24 * time.Hour
    EmbeddingDimension      = 768  // unchanged
)
```

## GraphStore Methods

| Method | Signature | Description |
|--------|-----------|-------------|
| `NewGraphStore` | `(databasePath string) (*GraphStore, error)` | Open/create DB, register vec, run createSchema |
| `Save` | `(title, content string, memType MemoryType, expiresAt *time.Time) (int64, error)` | Upsert memory node; returns ID |
| `SaveEmbedding` | `(id int64, embedding []float32) error` | Upsert into vec_memories |
| `Load` | `(title string) (Memory, error)` | Load by title |
| `TopK` | `(queryEmbedding []float32, k int) ([]Memory, error)` | Vec search joined with memories |
| `Neighbors` | `(id int64) ([]Memory, error)` | 1-hop expansion in both directions |
| `IncrementRecall` | `(id int64) error` | recall_count++ and last_recalled_at = NOW |
| `ExtendExpiration` | `(id int64, duration time.Duration) error` | expires_at = NOW + duration |
| `Promote` | `(id int64) error` | expires_at = NULL |
| `Connect` | `(fromID, toID int64, relation string) error` | Upsert edge |
| `CleanupExpired` | `() error` | DELETE memories WHERE expires_at < NOW |
| `Close` | `() error` | Close DB |

## Package File Layout (internal/memory/)

| File | Action | Contents |
|------|--------|----------|
| `graph_store.go` | CREATE | GraphStore struct, NewGraphStore, schema creation, Save, Load, Connect, Neighbors, IncrementRecall, ExtendExpiration, Promote, CleanupExpired, Close |
| `search.go` | REPLACE | TopK (vec_memories JOIN memories), SaveEmbedding; remove SearchIndex/memory_metadata |
| `embedding.go` | KEEP | EmbeddingGenerator interface, EmbeddingClient, SidecarProcess |
| `vec_register.go` | KEEP | CGo vec extension registration |
| `store.go` | DELETE | File-based store superseded |
| `promotion.go` | DELETE | Filesystem promotion superseded; promotion logic moves into graph_store.go |

## Tool Interface Changes

### remember tool

**Before**: `(subject string, content string)`
**After**: `(title string, content string, type string)`

- `type` is required; must be `fact`, `preference`, or `episode`
- `title` renames `subject` for consistency with the data model
- Constructor: `NewRememberTool(graphStore *memory.GraphStore, embedding memory.EmbeddingGenerator)`

### recall tool

**Before**: `(query string)` → top-K only, file-based fallback
**After**: `(query string)` → top-K + 1-hop graph expansion; no file fallback

- Constructor: `NewRecallTool(graphStore *memory.GraphStore, embedding memory.EmbeddingGenerator, topK int)`

### connect tool (new)

`(from_title string, to_title string, relation string)` → stores directed edge

- Constructor: `NewConnectTool(graphStore *memory.GraphStore)`

## State Transitions: episode lifecycle

```
episode created
    └── expires_at = NOW + 7d
        ├── recalled (< 3 times)
        │     └── expires_at extended +7d from NOW
        ├── recalled (recall_count reaches 3)
        │     └── expires_at = NULL  (promoted to permanent)
        └── not recalled until expires_at passes
              └── daemon cleanup deletes memory
```
