<!--
Sync Impact Report
Version change: 1.1.1 → 1.2.0
Modified sections:
  - Architecture Constraints / Memory system: complete rewrite
    (file-based two-tier → SQLite graph with typed nodes, expiration, 1-hop traversal)
  - Architecture Constraints / Execution model: removed "iteration limit" phrase
  - Architecture Constraints / Channels: softened to future aspiration
  - Architecture Constraints / Skills: marked Clawhub as planned
Added:
  - Memory type definitions (fact, preference, episode)
  - Graph traversal algorithm description
  - SQLite schema (in constitution prose, not literal SQL)
  - Expiration + promotion logic
Removed:
  - Filesystem memory files (short-term-memory/, long-term-memory/ directories)
  - Implied two-tier directory model
Templates requiring updates:
  - .specify/templates/plan-template.md ✅ no changes needed (generic)
  - .specify/templates/spec-template.md ✅ no changes needed (generic)
  - .specify/templates/tasks-template.md ✅ no changes needed (generic)
Follow-up TODOs:
  - Wire CleanupExpiredMemories() into daemon startup + heartbeat (code change, not constitution)
  - Update spec.md (001) to reflect new memory architecture in User Story 2
  - Re-examine remember/recall tool signatures for 'type' parameter addition
-->
# Blueclaw Constitution

## Core Principles

### I. Simplicity First
- The entire codebase MUST be small enough to understand in minutes,
  not hours. If a newcomer cannot grasp the architecture in one
  sitting, it is too complex.
- Standard library first. External dependencies MUST be justified.
  Each dependency added increases the surface area for breakage.
- No configuration sprawl. Behavior is changed through code
  modification, not layered config files.
- YAGNI: do not build for hypothetical futures. Solve the problem
  at hand with the minimum viable approach.
- Prefer flat module structure over deep nesting. A single `cmd/`
  and `internal/` tree MUST suffice until proven otherwise.

### II. Type Safety
- All public function signatures MUST use concrete types or
  narrowly-scoped interfaces. `any` and `interface{}` are
  prohibited except at serialization boundaries.
- Errors MUST be explicit return values, never panics for
  recoverable conditions. Every error MUST be handled or
  deliberately propagated with context via `fmt.Errorf` wrapping.
- Channel, tool, and provider interfaces MUST be typed. Message
  payloads passed between components use defined structs, never
  raw `map[string]any`.
- CGo usage is permitted only for sqlite-vec bindings (via
  mattn/go-sqlite3). All other native interop MUST be justified
  and approved.

### III. Test-First (NON-NEGOTIABLE)
- TDD cycle: write a failing test, make it pass, refactor.
  No production code without a corresponding test.
- Table-driven tests are the default pattern for Go. Each test
  function MUST cover happy path, error path, and at least one
  edge case.
- Integration tests are required for: container lifecycle,
  memory remember/recall, channel message round-trips, tool
  execution loop, and skill loading.
- `go test ./...` MUST pass before any commit is accepted.

### IV. Clean Code
- Code MUST be self-documenting through descriptive naming.
  Comments are permitted only for non-obvious "why" rationale,
  never for "what" the code does.
- Functions MUST have a single responsibility and fit within
  10-20 lines when possible.
- No abbreviations: `message` not `msg`, `response` not `resp`,
  `container` not `ctr`, `configuration` not `cfg`.
- Early returns and guard clauses over nested conditionals.
  Maximum nesting depth is 2 levels.
- Initialisms follow Go convention with one exception: leading
  initialisms in camelCase are lowercased (`idToken`, `urlParams`),
  trailing initialisms are uppercased (`userID`, `callbackURL`).

## Architecture Constraints

- **Reference projects**: Inspired by nanobot (HKUDS) and
  picoclaw (sipeed). Blueclaw follows the same agentic loop
  pattern but adds container isolation and vector-based memory.
- **Execution model**: Blueclaw runs an agentic loop
  (like picoclaw's AgentLoop) that calls LLM provider APIs
  directly. The loop iterates: call LLM → check for tool calls
  → execute tools → feed results back → repeat until done.
- **Runtime isolation**: The agentic loop and tool execution
  MUST run inside Apple Container (macOS) or Docker. The
  container provides OS-level isolation for tool execution
  (shell commands, file access). The host process orchestrates
  container lifecycle and passes messages in/out.
- **Memory system**: All memory is stored in a single SQLite
  database (`~/.blueclaw/db/memory.db`) using sqlite-vec for
  vector search. No filesystem memory files are used. Memory
  has three typed node variants:
  - **fact** — externally stated truths (names, relationships,
    facts the user declares). Long-term by default (no expiration).
  - **preference** — inferred patterns from repeated behavior or
    explicit preference statements. Long-term by default.
  - **episode** — time-bound experiences or events. Short-term by
    default (carries an expiration datetime). Promoted to long-term
    after sufficient recall.
  Memories are connected by directed relational edges stored in a
  `memory_connections` table. Each edge carries a relation label
  (e.g. `updates`, `extends`, `derives`). This forms a lightweight
  graph inside SQLite. Embeddings are generated from memory titles
  by EmbeddingGemma (768-dim, GGUF) running as a local llama.cpp
  sidecar. No external embedding API calls. Memory is agent-driven
  via two built-in tools:
  - `remember(title, content, type)` — saves a typed memory node
    and its title embedding. Episode memories default to a 7-day
    expiration; fact and preference memories default to no expiration.
  - `recall(query)` — performs semantic search on title embeddings
    (top-K), then expands each result one hop in both directions
    across `memory_connections`, returning the enriched context set
    to the LLM.
  Expiration management: memories with a non-null `expires_at` are
  automatically deleted when the datetime passes. On each recall,
  a short-term memory's expiration is extended. After
  `recall_count >= 3` a short-term memory is promoted to long-term
  (expiration set to NULL). The daemon MUST invoke cleanup on
  startup and during each heartbeat cycle.
- **SOUL.md**: Each agent instance MUST have a `SOUL.md` defining
  its personality, boundaries, and behavioral guidelines. Loaded
  into the system prompt on every LLM call (like picoclaw).
- **Channels**: CLI, HTTP API, and messaging (WhatsApp/Telegram)
  are interaction channels. A unified `Channel` interface is the
  target architecture; this is a future aspiration and not yet
  implemented.
- **Skills**: Extensible through a skill system. Users add skills
  manually by placing them in the skills directory. A Clawhub
  registry for skill discovery is planned but not yet implemented.
  Skills MUST be sandboxed and declare their required permissions.
- **LLM providers**: Anthropic, OpenAI, Google Gemini, DeepSeek,
  and any future provider. Provider selection MUST be a runtime
  configuration, not a compile-time decision.

## Development Workflow

- **Branching**: One feature branch per issue. Branch name format:
  `<issue-number>-<short-description>`.
- **Commits**: Atomic commits. Each commit MUST leave the tree in
  a passing state (`go test ./...` and `go vet ./...`).
- **Code review**: All changes to `main` require review. The
  reviewer MUST verify constitution compliance.
- **Quality gates**: `go vet`, `staticcheck`, and `go test -race`
  MUST pass in CI before merge.
- **File size**: No single `.go` file SHOULD exceed 300 lines.
  Files approaching this limit MUST be split by responsibility.

## Governance

- This constitution supersedes all other development practices.
  When a conflict arises between convenience and a principle,
  the principle wins.
- Amendments require: (1) a written proposal describing the
  change and rationale, (2) review, and (3) a version bump
  following semantic versioning (MAJOR for principle
  removals/redefinitions, MINOR for additions/expansions,
  PATCH for clarifications).
- Complexity MUST be justified. Any deviation from Simplicity
  First requires an entry in the Complexity Tracking table of
  the relevant plan document.
- Refer to `CLAUDE.md` for runtime development guidance and
  naming conventions.

**Version**: 1.2.0 | **Ratified**: 2026-02-18 | **Last Amended**: 2026-02-19
