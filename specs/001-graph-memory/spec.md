# Feature Specification: Graph Memory System

**Feature Branch**: `001-graph-memory`
**Created**: 2026-02-19
**Status**: Draft
**Input**: User description: "so update to this new relational graph node based memory system"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Agent Saves Typed Memories (Priority: P1)

The assistant can categorize what it remembers — a user's name is a fact, a
recurring preference is a preference, and something that happened last Tuesday
is an episode. Each type carries appropriate longevity: facts and preferences
never auto-expire, while episodes expire after a short window unless revisited.
This prevents the assistant from cluttering its memory with stale one-off events
while keeping permanent truths indefinitely.

**Why this priority**: Typed memory is the foundational change that enables all
other behaviors. Without it, expiration, promotion, and graph traversal have no
basis to operate on.

**Independent Test**: A user can ask the assistant to remember three things
(one fact, one preference, one episode), then confirm the assistant correctly
labels each and that the episode has an expiration while the others do not.

**Acceptance Scenarios**:

1. **Given** a user states "my name is Lee", **When** the assistant calls
   `remember`, **Then** the memory is saved as type `fact` with no expiration.
2. **Given** a user says "I prefer concise answers", **When** the assistant
   calls `remember`, **Then** the memory is saved as type `preference` with no
   expiration.
3. **Given** a user says "I had a meeting with Alex yesterday", **When** the
   assistant calls `remember`, **Then** the memory is saved as type `episode`
   with an expiration 7 days from now.
4. **Given** an episode memory already exists, **When** the assistant calls
   `remember` with the same title, **Then** the content is updated and the
   expiration is reset.

---

### User Story 2 - Agent Recalls Memories with Relational Context (Priority: P2)

When the assistant searches its memory, it does not just return isolated hits.
It also surfaces memories that are directly related to each result — one hop
away in the memory graph. This gives the assistant richer context without
requiring it to issue multiple queries.

**Why this priority**: Without graph expansion, the assistant sees fragments in
isolation. A memory about "Lee's project deadline" is much more useful when the
assistant also sees the connected "Lee prefers async updates" and "Lee's team
is distributed" memories retrieved alongside it.

**Independent Test**: Create two memories connected by a relation. Ask the
assistant to recall one of them. Verify the response includes both the matched
memory and its connected neighbor.

**Acceptance Scenarios**:

1. **Given** two memories exist and are connected with the relation `extends`,
   **When** the assistant calls `recall` with a query matching the first,
   **Then** the response includes both memories.
2. **Given** a memory has no connections, **When** the assistant calls `recall`,
   **Then** only the matching memory is returned (no error, no empty expansion).
3. **Given** a recall returns multiple top-K hits each with different neighbors,
   **Then** all unique neighbors are included in the result set.
4. **Given** a connection is directed from memory A to memory B, **When**
   recalling on a query matching B, **Then** memory A is included as a neighbor
   (expansion is bidirectional).

---

### User Story 3 - Agent Connects Related Memories (Priority: P3)

The assistant can draw a labeled edge between two existing memories to record
that one updates, extends, or derives from another. Over time this builds a
lightweight knowledge graph that captures how pieces of information relate,
enabling richer recall results.

**Why this priority**: Graph structure is only useful if connections can be
created. Without this, the graph expansion in User Story 2 returns nothing
for connected nodes.

**Independent Test**: Save two memories, connect them with a labeled relation,
then recall one and verify the other appears as a neighbor.

**Acceptance Scenarios**:

1. **Given** two memories with titles A and B, **When** the assistant creates
   an edge from A to B labeled `updates`, **Then** a directed connection is
   stored.
2. **Given** a connection from A to B already exists, **When** the assistant
   tries to create another from A to B, **Then** the existing connection is
   replaced (no duplicate edge).
3. **Given** a memory is deleted, **Then** all connections involving it are
   also removed automatically.

---

### User Story 4 - Short-Term Memories Expire and Promote Automatically (Priority: P4)

Episode memories that are never revisited are automatically removed when they
pass their expiration date. Episodes that get recalled frequently are promoted
to permanent status, removing their expiration. Each recall also extends the
expiration window so a recently active episode stays alive.

**Why this priority**: Without automatic expiration, the memory grows unbounded
with stale episode data. Without promotion, frequently referenced episodes risk
deletion. This keeps the memory set relevant over time.

**Independent Test**: Create an episode memory with an expiration in the past.
Trigger cleanup. Verify it is removed. Separately, recall an episode 3 times
and verify it has no expiration afterward.

**Acceptance Scenarios**:

1. **Given** an episode memory whose expiration has passed, **When** the daemon
   runs cleanup, **Then** that memory is deleted.
2. **Given** an episode memory with a future expiration, **When** the assistant
   recalls it, **Then** the expiration is extended by 7 days from now.
3. **Given** an episode memory recalled 3 or more times, **When** the
   assistant recalls it, **Then** its expiration is cleared and it becomes
   permanent.
4. **Given** the daemon starts up, **Then** cleanup runs before the daemon
   accepts any requests.
5. **Given** a fact or preference memory, **When** cleanup runs, **Then** it
   is never deleted regardless of age.

---

### Edge Cases

- What happens when `recall` is called with a query that matches no memories?
  Returns an empty result set without error.
- What happens when a memory title contains special characters or is very long?
  Titles are stored as-is; search operates on the embedded vector representation.
- What happens when the database is missing or corrupt on daemon startup?
  The daemon fails with a clear error before accepting any requests.
- What happens when two memories are connected and one is later updated?
  The connection remains; only the content of the node changes.
- What happens when a connection is created between a memory and itself?
  Self-loops are rejected with a validation error.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST support three and only three memory types: `fact`,
  `preference`, and `episode`. The agent MUST provide the type when calling
  `remember`.
- **FR-002**: Fact and preference memories MUST have no expiration and MUST
  never be auto-deleted by the expiration mechanism.
- **FR-003**: Episode memories MUST default to an expiration of 7 days from
  creation when no explicit expiration is provided.
- **FR-004**: System MUST store all memories in a single persistent database.
  No memory data may be stored in filesystem files.
- **FR-005**: The `recall` tool MUST perform semantic search and expand each
  result by fetching all directly connected memories in both directions (1-hop),
  returning the union of top-K hits and their neighbors.
- **FR-006**: System MUST support creating a directed, labeled relational edge
  between any two existing memories. Labels MUST be non-empty strings
  (e.g., `updates`, `extends`, `derives`).
- **FR-007**: Only one edge may exist between any ordered pair of memories.
  Creating a duplicate edge MUST replace the existing one.
- **FR-008**: Self-referential edges (a memory connected to itself) MUST be
  rejected with a validation error.
- **FR-009**: Deleting a memory MUST automatically delete all edges involving
  that memory.
- **FR-010**: On each recall of an episode memory, its expiration MUST be
  extended by 7 days and its recall count incremented.
- **FR-011**: When an episode memory's recall count reaches 3, it MUST be
  promoted: its expiration is cleared and it becomes permanent.
- **FR-012**: The daemon MUST run expired-memory cleanup on startup and at each
  heartbeat cycle.
- **FR-013**: The `remember` tool called with a title that already exists MUST
  update the existing memory's content rather than creating a duplicate.

### Key Entities

- **Memory**: A typed knowledge node with a unique title, content body, memory
  type (fact / preference / episode), recall count, optional expiration datetime,
  creation timestamp, and last-recalled timestamp.
- **Memory Connection**: A directed edge from one memory to another, labeled
  with a relation describing how the two nodes relate. Each connection belongs
  to exactly one ordered pair of memories.
- **Memory Embedding**: A vector representation of a memory's title used for
  semantic similarity search. Associated 1-to-1 with a memory node.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: After the assistant recalls an episode memory 3 times, the memory
  has no expiration and is never auto-deleted by cleanup.
- **SC-002**: When the assistant performs a `recall`, connected memories appear
  in the result within the same response — no additional tool calls required.
- **SC-003**: Expired episode memories are fully removed within one heartbeat
  cycle of their expiration datetime passing.
- **SC-004**: The assistant correctly applies the default expiration rule to all
  three memory types in 100% of cases.
- **SC-005**: All memory data survives a daemon restart without loss or
  corruption.
- **SC-006**: Recall results are deterministic: the same query issued twice
  returns the same set of memories under unchanged data.

## Assumptions

- The EmbeddingGemma llama.cpp sidecar is already operational and accessible;
  embedding generation is out of scope for this feature.
- The agent is responsible for choosing the correct memory type based on
  context; the system does not infer the type automatically.
- The default expiration extension on recall (7 days) and the promotion
  threshold (recall count ≥ 3) are fixed constants that do not require runtime
  configuration in this iteration.
- The existing `remember` and `recall` tool names are preserved; only their
  signatures and behavior change.
- Migration of existing filesystem memory files to the new database is out of
  scope; this feature targets a clean-slate deployment.
