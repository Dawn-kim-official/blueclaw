# Tasks: Graph Memory System

**Input**: Design documents from `/specs/001-graph-memory/`
**Prerequisites**: plan.md ✅, spec.md ✅, research.md ✅, data-model.md ✅, contracts/ ✅, quickstart.md ✅

**Organization**: Tasks grouped by user story to enable independent implementation and testing.

## Format: `[ID] [P?] [Story?] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: User story this task belongs to (US1=Typed Memories, US2=Recall+Graph, US3=Connect, US4=Expiry+Promotion)

---

## Phase 1: Setup

**Purpose**: Define shared types and constants that every subsequent phase depends on.

- [x] T001 Create `internal/memory/graph_store.go` with `Memory` struct, `Connection` struct, `MemoryType` string type, `MemoryTypeFact`/`MemoryTypePreference`/`MemoryTypeEpisode` constants, and `PromotionThreshold = 3`, `DefaultEpisodeTTL = 7 * 24 * time.Hour`, `ExpirationExtension = 7 * 24 * time.Hour` constants (fields per data-model.md)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Full GraphStore infrastructure. ALL user stories depend on this phase.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

- [x] T002 Write failing tests for `NewGraphStore` (opens DB, creates three tables: memories, memory_connections, vec_memories) and `Close` in `internal/memory/graph_store_test.go`
- [x] T003 Implement `NewGraphStore(databasePath string) (*GraphStore, error)` and `createSchema` in `internal/memory/graph_store.go` — calls `ensureVecRegistered()`, opens DB with WAL mode, creates all three tables per data-model.md schema; makes T002 pass
- [x] T004 [P] Write failing tests for `Save` (upsert by title, type stored, episode gets expires_at, fact/preference get NULL expires_at, duplicate title updates content) and `Load` (by title) in `internal/memory/graph_store_test.go`
- [x] T005 [P] Write failing tests for `Connect` (edge upserted, replace on duplicate pair, self-loop rejected) and `Neighbors` (returns both outgoing `to_id` and incoming `from_id` neighbors) in `internal/memory/graph_store_test.go`
- [x] T006 [P] Write failing tests for `SaveEmbedding` (upsert into vec_memories) and `TopK` (returns memories ordered by distance) in `internal/memory/search_test.go` — use `NewGraphStore` instead of `NewSearchIndex`
- [x] T007 Implement `Save` (INSERT OR REPLACE + compute expires_at from type) and `Load` (SELECT by title) in `internal/memory/graph_store.go`; makes T004 pass
- [x] T008 Implement `Connect` (INSERT OR REPLACE into memory_connections; reject from_id == to_id) and `Neighbors` (UNION of outgoing to_ids and incoming from_ids via memory_connections JOIN memories) in `internal/memory/graph_store.go`; makes T005 pass
- [x] T009 Refactor `internal/memory/search.go` — delete `SearchIndex` struct and `memory_metadata` table creation; add `SaveEmbedding(id int64, embedding []float32) error` and `TopK(queryEmbedding []float32, k int) ([]Memory, error)` as methods on `*GraphStore`; makes T006 pass

**Checkpoint**: GraphStore fully operational — `CGO_ENABLED=1 go test ./internal/memory/...` passes

---

## Phase 3: User Story 1 — Agent Saves Typed Memories (Priority: P1) 🎯 MVP

**Goal**: Agent can call `remember(title, content, type)` and the memory is stored with correct type and default expiration.

**Independent Test**: Ask agent to remember one fact, one preference, one episode. Verify fact and preference have no expiration; episode has an expiration ~7 days out.

### Implementation for User Story 1

- [x] T010 [US1] Write failing tests for `RememberTool` in `internal/tool/remember_test.go` — covers: missing type returns error, invalid type returns error, fact saved with nil expires_at, episode saved with expires_at ≈ now+7d, duplicate title updates in place, embedding called with title only (not title+content)
- [x] T011 [US1] Update `internal/tool/remember.go` — replace `store *memory.Store + searchIndex *memory.SearchIndex` with `graphStore *memory.GraphStore`; add `"type"` to `ParameterSchema` (required enum); rename `"subject"` → `"title"`; compute `expiresAt` from type; call `graphStore.Save` then `graphStore.SaveEmbedding`; embed title only; makes T010 pass

**Checkpoint**: `CGO_ENABLED=1 go test ./internal/tool/...` passes for remember tests

---

## Phase 4: User Story 2 — Agent Recalls Memories with Relational Context (Priority: P2)

**Goal**: `recall(query)` returns top-K semantic hits plus all 1-hop neighbors in both directions, with each result tagged `source: "search"` or `source: "neighbor"`.

**Independent Test**: Save two memories, connect them, call recall with a query matching one — verify both appear in the response without a second tool call.

### Implementation for User Story 2

- [x] T012 [US2] Write failing tests for `RecallTool` in `internal/tool/recall_test.go` — covers: top-K hit returned with `source:"search"`, connected neighbor returned with `source:"neighbor"`, bidirectional neighbor expansion, empty query returns error, no matches returns empty array without error, deduplication when a node is both a hit and a neighbor
- [x] T013 [US2] Update `internal/tool/recall.go` — replace `store + searchIndex` with `graphStore`; after TopK call `graphStore.Neighbors(id)` for each hit; build deduplicated result set with `source` field; update recall state (extend expiration or promote) per contracts/recall.md; remove `recallByListing` fallback; makes T012 pass

**Checkpoint**: `CGO_ENABLED=1 go test ./internal/tool/...` passes for recall tests

---

## Phase 5: User Story 3 — Agent Connects Related Memories (Priority: P3)

**Goal**: Agent can call `connect(from_title, to_title, relation)` to create a directed labeled edge between two memories.

**Independent Test**: Save two memories, connect them, recall one — verify the other appears as a neighbor.

### Implementation for User Story 3

- [x] T014 [US3] Write failing tests for `ConnectTool` in `internal/tool/connect_test.go` — covers: happy path stores edge, duplicate pair replaces edge, self-loop rejected with error, missing from_title returns error, missing to_title returns error, empty relation returns error
- [x] T015 [US3] Create `internal/tool/connect.go` — `ConnectTool` with `Name() "connect"`, ParameterSchema for `from_title`/`to_title`/`relation`, Execute validates inputs and calls `graphStore.Load` for both titles then `graphStore.Connect`; makes T014 pass

**Checkpoint**: `CGO_ENABLED=1 go test ./internal/tool/...` passes for connect tests

---

## Phase 6: User Story 4 — Expiration and Promotion (Priority: P4)

**Goal**: Episode memories expire automatically. Each recall extends the window. After 3 recalls, episode becomes permanent. Daemon runs cleanup on startup and heartbeat.

**Independent Test**: Create expired episode → trigger cleanup → verify deleted. Recall episode 3× → verify expires_at is NULL.

### Implementation for User Story 4

- [x] T016 [US4] Write failing tests for expiration/promotion methods in `internal/memory/graph_store_test.go` — covers: `IncrementRecall` increments recall_count and sets last_recalled_at, `ExtendExpiration` sets expires_at to ~now+duration, `Promote` sets expires_at to NULL, `CleanupExpired` deletes memories with expires_at < now, `CleanupExpired` does not delete fact/preference (NULL expires_at), `CleanupExpired` does not delete episode with future expiration
- [x] T017 [US4] Implement `IncrementRecall(id int64) error`, `ExtendExpiration(id int64, duration time.Duration) error`, `Promote(id int64) error`, and `CleanupExpired() error` in `internal/memory/graph_store.go`; makes T016 pass
- [x] T018 [US4] Update `internal/heartbeat/heartbeat.go` — add `cleanupFn func() error` field to `Service` struct; update `NewService` signature to accept `cleanupFn func() error` as last parameter; call `cleanupFn` (if non-nil) at start of `executeHeartbeat`, logging warning on error
- [x] T019 [US4] Update `internal/daemon/daemon.go` — replace `store := memory.NewStore(...)` and `searchIndex, err := memory.NewSearchIndex(...)` with `graphStore, err := memory.NewGraphStore(databasePath)`; call `graphStore.CleanupExpired()` after construction (log warning, don't fail); pass `graphStore.CleanupExpired` to `heartbeat.NewService`; add `graphStore` field to `Daemon` struct; remove `searchIndex` field; register `tool.NewConnectTool(graphStore)`; update `tool.NewRememberTool` and `tool.NewRecallTool` constructors to use `graphStore`; defer `graphStore.Close()`

**Checkpoint**: `CGO_ENABLED=1 go test ./internal/memory/... && go build ./...` passes

---

## Phase 7: Polish & Cleanup

**Purpose**: Remove superseded code, verify full build, ensure clean state.

- [x] T020 Delete `internal/memory/store.go` and `internal/memory/store_test.go`
- [x] T021 [P] Delete `internal/memory/promotion.go` and `internal/memory/promotion_test.go`
- [x] T022 Verify full build passes: `CGO_ENABLED=1 go build ./...`
- [x] T023 Run full test suite: `CGO_ENABLED=1 go test ./... && go test -race ./... && go vet ./... && staticcheck ./...`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — start immediately
- **Foundational (Phase 2)**: Depends on Phase 1 — BLOCKS all user stories
- **User Story Phases (3–6)**: All depend on Phase 2; can proceed in priority order
- **Polish (Phase 7)**: Depends on all user story phases complete

### User Story Dependencies

| Story | Depends on | Notes |
|-------|-----------|-------|
| US1 (P1) | Phase 2 only | Independent MVP |
| US2 (P2) | Phase 2 + US1 (remember must work to populate memories for recall tests) | |
| US3 (P3) | Phase 2 only | Connect is independent; Neighbors used in tests is from Phase 2 |
| US4 (P4) | Phase 2 + US1 (save needed), US2 (recall updates state) | Last story |

### Within Each Phase (TDD order)

1. Write failing test(s) first
2. Implement to make tests pass
3. Confirm tests pass before moving on
4. Commit after each logical group

### Parallel Opportunities

- T004, T005, T006 can all be written simultaneously (different test cases, same file — single author)
- T007, T008 can be implemented in parallel (different methods in same file)
- T009 is independent of T007/T008 (different file)
- T020 and T021 (Phase 7 deletions) can happen in parallel

---

## Parallel Example: Phase 2 (Foundational)

```bash
# After T003 (GraphStore + schema), all three test-writing tasks can run together:
T004: Write Save+Load tests
T005: Write Connect+Neighbors tests
T006: Write SaveEmbedding+TopK tests

# Then implement in sequence (same file for graph_store.go):
T007: Save+Load
T008: Connect+Neighbors
T009: search.go refactor (independent file, can overlap with T007/T008)
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup (T001)
2. Complete Phase 2: Foundational (T002–T009)
3. Complete Phase 3: User Story 1 (T010–T011)
4. **STOP and VALIDATE**: `CGO_ENABLED=1 go test ./internal/memory/... ./internal/tool/...`

### Incremental Delivery

1. Phase 1 + 2 → GraphStore foundation ready
2. Phase 3 (US1) → typed remember works — MVP deliverable
3. Phase 4 (US2) → recall returns graph context
4. Phase 5 (US3) → agent can connect memories
5. Phase 6 (US4) → expiration + cleanup wired end-to-end
6. Phase 7 → clean build, all tests green

---

## Notes

- All memory package tasks require `CGO_ENABLED=1` (sqlite-vec)
- `[P]` = different files or independent code paths, no merge conflicts
- Deletion tasks in Phase 7 are safe only after daemon.go is updated in T019
- `store.go` and `promotion.go` still compile fine until Phase 7 — don't delete early
- Verify `go build ./...` at every checkpoint before moving to the next phase
