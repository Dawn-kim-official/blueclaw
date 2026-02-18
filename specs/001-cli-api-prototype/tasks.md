# Tasks: CLI & API Prototype

**Input**: Design documents from `/specs/001-cli-api-prototype/`
**Prerequisites**: plan.md, spec.md, data-model.md, contracts/daemon-api.yaml, contracts/tool-ipc.md, research.md

**Organization**: Tasks grouped by user story. Each story is independently testable after Phase 2 foundation completes.

## TDD Discipline (Constitution Principle III — NON-NEGOTIABLE)

Every implementation task follows the TDD cycle: **write a failing test → make it pass → refactor**. Each test function MUST cover happy path, error path, and at least one edge case using table-driven tests. `go test ./...` MUST pass after every task.

Test files live alongside source files per Go convention: `internal/memory/store.go` → `internal/memory/store_test.go`.

Dedicated integration test tasks are listed explicitly where the constitution requires them (container lifecycle, memory round-trip, tool execution loop, IPC).

## Format: `[ID] [P?] [Story?] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: Which user story (US1–US5) this task belongs to

---

## Phase 1: Setup

**Purpose**: Initialize Go project, install dependencies, create directory structure

- [x] T001 Initialize Go module and add dependencies (kong, go-cron, yaml.v3, docker/client, mattn/go-sqlite3, sqlite-vec-go-bindings) in go.mod
- [x] T002 Create directory structure per plan.md: cmd/blueclaw/, internal/{configuration,daemon,container,agent,provider,tool,memory,heartbeat,scheduler,initialize}/
- [x] T003 Create Kong CLI scaffold with subcommand structs (DaemonCommand, ChatCommand, TasksCommand, InitCommand) in cmd/blueclaw/main.go

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core interfaces, implementations, and shared infrastructure that ALL user stories depend on

**CRITICAL**: No user story work can begin until this phase is complete

### Interfaces (parallelizable)

- [x] T004 [P] Implement Configuration struct with YAML loading, defaults, and env var overrides (BLUECLAW_*, ANTHROPIC_API_KEY, etc.) in internal/configuration/configuration.go
- [x] T005 [P] Define ContainerRuntime interface (CreateContainer, StartContainer, StopContainer, RemoveContainer, ExecInContainer, IsAvailable) in internal/container/runtime.go
- [x] T006 [P] Define LLMProvider interface (SendMessage with tool call support, ListModels, Name) and provider factory function in internal/provider/provider.go
- [x] T007 [P] Define Tool interface (Name, Description, Parameters schema, Execute) and Registry (Register, Get, ListForLLM) in internal/tool/tool.go

### Tests for interfaces

- [x] T008 [P] Test configuration: YAML loading, env var overrides, defaults, missing file fallback, invalid YAML error in internal/configuration/configuration_test.go
- [x] T009 [P] Test tool registry: register, get by name, list for LLM schema, duplicate name error, get unknown returns error in internal/tool/tool_test.go

### Implementations (depend on interfaces above)

- [x] T010 Implement Docker runtime using docker/client SDK (image pull, container create/start/stop/remove, socket bind-mount, network access) in internal/container/docker.go
- [x] T011 [P] Implement Apple Container runtime using os/exec wrapping `container` CLI (create, start, stop, list, socket bind-mount) in internal/container/apple.go
- [x] T012 Implement Anthropic provider with Messages API (system prompt, tool definitions, tool_use/tool_result message flow, streaming) in internal/provider/anthropic.go
- [x] T013 [P] Implement OpenAI-compatible provider (chat completions with function calling, works for OpenAI/Gemini/DeepSeek) in internal/provider/openai.go

### Initialization

- [x] T014 Implement blueclaw init command: create ~/.blueclaw/ directories (short-term-memory, long-term-memory, sessions, cron, outbox), scaffold config.yaml, download EmbeddingGemma 300M GGUF from Hugging Face in internal/initialize/initialize.go
- [x] T015 Test init command: creates all directories, idempotent re-run, config.yaml has correct defaults in internal/initialize/initialize_test.go

**Checkpoint**: All interfaces defined, both container runtimes implemented, at least one LLM provider working, init command functional, `go test ./...` passes

---

## Phase 3: User Story 1 — Send a Message via CLI and Get a Response (Priority: P1) MVP

**Goal**: User runs `blueclaw daemon` to start the background process, then `blueclaw chat "Hello"` to send a message and get an AI response. Interactive REPL with `blueclaw chat` (no args).

**Independent Test**: Start daemon, run `blueclaw chat "Hello"`, verify response printed to stdout. Run `blueclaw chat` for multi-turn REPL.

**FRs**: FR-001, FR-002, FR-003, FR-004, FR-004a
**Endpoints**: POST /v1/chat (Unix socket), DELETE /v1/sessions/{sessionID}
**Entities**: Session, Message, ToolCall

### Implementation

- [x] T016 [P] [US1] Implement Session and Message structs with JSON persistence (create, load, save, addMessage, updateActivity) in internal/agent/session.go
- [x] T017 [P] [US1] Implement prompt assembly: build system prompt from conversation history, inject tool definitions from registry in internal/agent/context.go
- [x] T018 [US1] Implement agentic loop: call LLMProvider → check for tool calls → execute tools via Registry → append tool results → repeat until no tool calls or iteration limit (max 10). Add 60-second context deadline per loop invocation for response timeout in internal/agent/loop.go
- [x] T019 [P] [US1] Implement idle timeout tracker: per-session timer, reset on activity, callback on expiry to stop container in internal/container/idle.go
- [x] T020 [US1] Implement daemon startup: load config, detect container runtime, create LLM provider, start HTTP server on Unix socket (~/.blueclaw/daemon.sock), register signal handlers in internal/daemon/daemon.go
- [x] T021 [US1] Implement HTTP handlers: POST /v1/chat (create/resume session, run agentic loop, return response), DELETE /v1/sessions/{sessionID} (stop container, remove session) in internal/daemon/server.go
- [x] T022 [US1] Wire ChatCommand: one-shot mode (POST to daemon, print response, exit) and REPL mode (loop: read stdin → POST to daemon → print response) with daemon-not-running detection in cmd/blueclaw/main.go
- [x] T023 [US1] Wire DaemonCommand to call daemon.Start() and InitCommand to call initialize.Run() in cmd/blueclaw/main.go

### Tests for US1

- [x] T024 [P] [US1] Test session persistence: create session, add messages, save to JSON, load from JSON, updateActivity timestamp in internal/agent/session_test.go
- [x] T025 [P] [US1] Test prompt assembly: with conversation history, with tool definitions, empty history, max context truncation in internal/agent/context_test.go
- [x] T026 [US1] Test agentic loop with mock LLMProvider: no tool calls (single response), one tool call round-trip, iteration limit reached, context deadline exceeded (60s timeout) in internal/agent/loop_test.go
- [x] T027 [P] [US1] Test idle timeout: fires after duration, resets on activity, cancel prevents firing in internal/container/idle_test.go
- [x] T028 [US1] Integration test: start daemon with mock provider, POST /v1/chat via Unix socket, verify session created and response returned, DELETE /v1/sessions cleans up in internal/daemon/server_test.go

**Checkpoint**: `blueclaw daemon` starts, `blueclaw chat "Hello"` returns an AI response, REPL works for multi-turn, `go test ./...` passes

---

## Phase 4: User Story 2 — Memory Persistence Across Sessions (Priority: P2)

**Goal**: AI agent can call `remember` to save memories as Markdown files with vector embeddings, and `recall` to search memories by semantic similarity. Memories promote from short-term to long-term after 3+ recalls.

**Independent Test**: Send a message that triggers `remember`, start a new session, send a query that triggers `recall`, verify the saved memory is returned.

**Depends on**: US1 (daemon + agentic loop must exist for tool execution)
**FRs**: FR-005, FR-006, FR-007, FR-008, FR-009
**IPC Endpoints**: POST /tools/remember, POST /tools/recall
**Entities**: Memory, Embedding, MemoryMetadata

### Implementation

- [x] T029 [US2] Implement llama-server sidecar management: start process on daemon startup, health check loop, graceful shutdown. Implement EmbeddingClient calling /v1/embeddings endpoint (single text → float[768] vector). When sidecar is down, return degraded status and empty embeddings (memory operates without search) in internal/memory/embedding.go
- [x] T030 [US2] Implement memory CRUD: write Markdown file with YAML frontmatter (subject, recallCount, createdAt, lastRecalledAt, storage), read/parse Markdown+frontmatter, slugify subject to filename, upsert SQLite metadata row. Check available disk space before writing (warn if below 100MB threshold) in internal/memory/store.go
- [x] T031 [US2] Implement sqlite-vec schema creation (memory_metadata table + vec_memories virtual table with float[768]), enable WAL mode for concurrent read safety, and top-K similarity search query in internal/memory/search.go
- [x] T032 [US2] Implement memory promotion (move file from short-term-memory/ to long-term-memory/, update metadata storage field when recallCount >= 3) and TTL cleanup (delete short-term files older than 7 days with recallCount < 3) in internal/memory/promotion.go
- [x] T033 [P] [US2] Implement remember tool: validate subject+content args, call store.Save() and embedding.Generate(), return saved file path in internal/tool/remember.go
- [x] T034 [P] [US2] Implement recall tool: validate query arg, call embedding.Generate() for query vector, call search.TopK(), increment recallCount for returned memories, return results in internal/tool/recall.go
- [x] T035 [US2] Add IPC tool endpoints to daemon server: POST /tools/remember and POST /tools/recall handlers that delegate to tool registry, mount IPC socket at ~/.blueclaw/ipc.sock in internal/daemon/server.go
- [x] T036 [US2] Integrate llama-server sidecar lifecycle into daemon startup (start after config load) and shutdown (stop before exit) in internal/daemon/daemon.go

### Tests for US2

- [x] T037 [P] [US2] Test memory CRUD: write and read Markdown with frontmatter, slugify edge cases (special chars, unicode), upsert metadata, disk space check in internal/memory/store_test.go
- [x] T038 [P] [US2] Test sqlite-vec: schema creation, insert embedding, top-K query returns correct order, empty database returns empty results, WAL mode enabled in internal/memory/search_test.go
- [x] T039 [US2] Test promotion: promote at recallCount=3 (file moved, metadata updated), skip at recallCount=2, TTL cleanup (delete >7 day old unpromoted, keep promoted) in internal/memory/promotion_test.go
- [x] T040 [US2] Test remember and recall tools with mock embedding client: remember saves file + embedding, recall queries and increments recallCount, recall with no matches returns empty in internal/tool/remember_test.go and internal/tool/recall_test.go
- [x] T041 [US2] Integration test: remember → recall round-trip through IPC endpoints, verify memory persists across mock sessions in internal/daemon/server_test.go

**Checkpoint**: Agent can `remember` a fact, new session can `recall` it via vector search, memories promote after 3 recalls, stale short-term memories are cleaned up, `go test ./...` passes

---

## Phase 5: User Story 3 — Proactive Agent Behavior (Priority: P3)

**Goal**: Daemon periodically wakes the agent via heartbeat (reads HEARTBEAT.md). Users can create cron jobs via `schedule` tool. Proactive output is queued in outbox and delivered on next CLI session.

**Independent Test**: Set heartbeat interval to 1 minute, write HEARTBEAT.md with a simple instruction, wait for heartbeat to fire, verify outbox contains agent output.

**Depends on**: US1 (daemon + agentic loop)
**FRs**: FR-010, FR-011, FR-012, FR-013
**IPC Endpoint**: POST /tools/schedule
**Daemon Endpoints**: GET /v1/tasks, GET /v1/outbox, DELETE /v1/outbox
**Entities**: ScheduledJob, ProactiveMessage

### Implementation

- [x] T042 [P] [US3] Implement outbox: write ProactiveMessage as JSON to ~/.blueclaw/outbox/, list undelivered messages, mark all as delivered (delete files) in internal/outbox/outbox.go
- [x] T043 [P] [US3] Implement HeartbeatService: periodic timer from config.heartbeatInterval, read ~/.blueclaw/HEARTBEAT.md, create ephemeral session, run agentic loop with HEARTBEAT.md content as prompt, write output to outbox in internal/heartbeat/heartbeat.go
- [x] T044 [US3] Implement CronService: load/save jobs from ~/.blueclaw/cron/jobs.json, register jobs with go-cron scheduler, on trigger create ephemeral session and run agentic loop with job prompt, write output to outbox, update lastRunAt/nextRunAt. Jobs MUST execute sequentially via a single-worker goroutine to avoid spawning too many containers in internal/scheduler/scheduler.go
- [x] T045 [US3] Implement schedule tool: validate cronExpression+prompt args, call CronService.AddJob(), return jobID and nextRunAt in internal/tool/schedule.go
- [x] T046 [US3] Add proactive endpoints to daemon server: GET /v1/tasks (list scheduled jobs), GET /v1/outbox (list pending messages), DELETE /v1/outbox (mark delivered), POST /tools/schedule (IPC) in internal/daemon/server.go
- [x] T047 [US3] Wire TasksCommand: GET /v1/tasks from daemon, format and print job list with next run times in cmd/blueclaw/main.go
- [x] T048 [US3] Integrate HeartbeatService and CronService lifecycle into daemon startup/shutdown in internal/daemon/daemon.go

### Tests for US3

- [x] T049 [P] [US3] Test outbox: write message, list returns it, clear removes files, list empty after clear in internal/outbox/outbox_test.go
- [x] T050 [P] [US3] Test CronService: add job persists to JSON, load restores jobs, invalid cron expression returns error, sequential execution (second job waits for first) in internal/scheduler/scheduler_test.go
- [x] T051 [US3] Test schedule tool: valid cron creates job, invalid cron returns INVALID_CRON error, missing prompt returns error in internal/tool/schedule_test.go

**Checkpoint**: Heartbeat fires on schedule and produces outbox messages, cron jobs can be created and execute on time, `blueclaw tasks` lists active jobs, `go test ./...` passes

---

## Phase 6: User Story 4 — Send a Message via HTTP API (Priority: P4)

**Goal**: Daemon exposes the same chat functionality over a TCP port (default 8080) for external HTTP clients.

**Independent Test**: `curl -X POST http://localhost:8080/v1/chat -d '{"message":"Hello"}'` returns JSON response.

**Depends on**: US1 (daemon server with Unix socket handlers already exist)
**FRs**: FR-014
**Endpoints**: POST /v1/chat (TCP), GET /v1/health

### Implementation

- [x] T052 [US4] Add TCP listener to daemon: serve the same http.ServeMux on config.apiPort alongside the Unix socket listener. Ensure concurrent request handling (each request in its own goroutine, standard net/http behavior) in internal/daemon/server.go
- [x] T053 [US4] Implement health endpoint: GET /v1/health returning HealthResponse (status, embeddingServer, containerRuntime, activeContainers, activeSessions) by checking subsystem status in internal/daemon/server.go
- [x] T054 [US4] Add JSON request validation (check Content-Type, validate required fields, return structured ErrorResponse with code) for all API endpoints in internal/daemon/server.go

### Tests for US4

- [x] T055 [US4] Test HTTP API: POST /v1/chat via TCP returns JSON response, missing message returns 400, invalid JSON returns 400, GET /v1/health returns subsystem status, concurrent requests handled without errors in internal/daemon/server_test.go

**Checkpoint**: `curl localhost:8080/v1/chat` works, `curl localhost:8080/v1/health` returns subsystem status, `go test ./...` passes

---

## Phase 7: User Story 5 — Agent Identity via SOUL.md (Priority: P5)

**Goal**: Agent personality is configured via ~/.blueclaw/SOUL.md. Contents are prepended to the system prompt. Falls back to a sensible default if missing.

**Independent Test**: Create a SOUL.md saying "You are a pirate", send a message, verify pirate-style response. Remove SOUL.md, verify neutral response.

**Depends on**: US1 (prompt assembly in internal/agent/context.go)
**FRs**: FR-015

### Implementation

- [x] T056 [US5] Add SOUL.md loading to prompt assembly: read ~/.blueclaw/SOUL.md if present, prepend to system prompt. Define hardcoded default personality if file missing in internal/agent/context.go
- [x] T057 [US5] Add default SOUL.md and HEARTBEAT.md generation to blueclaw init in internal/initialize/initialize.go

### Tests for US5

- [x] T058 [US5] Test SOUL.md loading: file present prepends to prompt, file missing uses default, file modified between sessions picks up changes, empty file uses default in internal/agent/context_test.go

**Checkpoint**: SOUL.md content influences agent responses, missing SOUL.md falls back to default personality, `go test ./...` passes

---

## Phase 8: Polish & Cross-Cutting Concerns

**Purpose**: Edge case handling, robustness, and final integration

### Edge Cases

- [x] T059 Implement graceful shutdown: catch SIGINT/SIGTERM, stop heartbeat and cron services, stop all containers, close llama-server sidecar, close Unix socket and TCP listeners in internal/daemon/daemon.go
- [x] T060 Add stale container cleanup on daemon restart: list containers with blueclaw label, stop and remove any orphaned containers from previous daemon instances in internal/daemon/daemon.go
- [x] T061 Add outbox delivery on CLI session start: before REPL prompt, GET /v1/outbox, display pending messages, DELETE /v1/outbox to mark delivered in cmd/blueclaw/main.go
- [x] T062 Add daemon-not-running error handling: CLI detects Unix socket connection failure and prints "Daemon is not running. Start it with: blueclaw daemon" in cmd/blueclaw/main.go
- [x] T063 Add SQLite integrity check on daemon startup: run `PRAGMA integrity_check` on memory.db, warn user if corrupted, offer to create fresh database (preserve Markdown memory files) in internal/memory/search.go
- [x] T064 Add embedding-unavailable degraded mode: when llama-server health check fails, log warning "memory search unavailable", remember tool still saves Markdown file (skip embedding), recall tool returns empty results with warning in internal/memory/embedding.go

### Validation

- [x] T065 Run quickstart.md end-to-end validation: build binary, run init, start daemon, chat one-shot, chat REPL with remember/recall, verify health endpoint, verify graceful shutdown

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 (Setup)**: No dependencies — start immediately
- **Phase 2 (Foundational)**: Depends on Phase 1 — BLOCKS all user stories
- **Phase 3 (US1)**: Depends on Phase 2 — MVP delivery target
- **Phase 4 (US2)**: Depends on US1 (daemon + agentic loop + IPC server)
- **Phase 5 (US3)**: Depends on US1 (daemon + agentic loop)
- **Phase 6 (US4)**: Depends on US1 (daemon server)
- **Phase 7 (US5)**: Depends on US1 (prompt assembly)
- **Phase 8 (Polish)**: Depends on all user stories complete

### User Story Dependencies

```text
Phase 1 → Phase 2 → US1 (P1) → US2 (P2)
                         ├────→ US3 (P3)
                         ├────→ US4 (P4)
                         └────→ US5 (P5)
                                        → Phase 8 (Polish)
```

- **US2, US3, US4, US5** can proceed in parallel after US1 completes
- **US4** and **US5** are small (3–4 tasks each) and can be done quickly
- **US2** and **US3** are larger and can be parallelized with each other

### Within Each User Story

- Write tests first (TDD: tests MUST fail before implementation)
- Structs and storage before business logic
- Business logic before HTTP handlers
- HTTP handlers before CLI wiring
- Core implementation before integration with daemon lifecycle
- `go test ./...` MUST pass at the end of each story

### Parallel Opportunities

**Phase 2** (after interfaces):
```text
T008 (config test) ║ T009 (registry test)       — independent test files
T010 (Docker) ║ T011 (Apple Container)           — both implement ContainerRuntime
T012 (Anthropic) ║ T013 (OpenAI)                 — both implement LLMProvider
```

**Phase 3** (US1):
```text
T016 (session) ║ T017 (context) ║ T019 (idle)   — independent packages
T024 (session test) ║ T025 (context test) ║ T027 (idle test) — independent test files
```

**Phase 4** (US2):
```text
T033 (remember tool) ║ T034 (recall tool)        — both implement Tool interface
T037 (store test) ║ T038 (search test)            — independent test files
```

**Phase 5** (US3):
```text
T042 (outbox) ║ T043 (heartbeat)                 — independent subsystems
T049 (outbox test) ║ T050 (cron test)            — independent test files
```

**After US1 completes**:
```text
US2 ║ US3 ║ US4 ║ US5                            — all can proceed in parallel
```

---

## Implementation Strategy

### MVP First (US1 Only)

1. Complete Phase 1: Setup
2. Complete Phase 2: Foundational (CRITICAL — blocks everything)
3. Complete Phase 3: US1 — CLI Chat
4. **STOP and VALIDATE**: `go test ./...` then `blueclaw init && blueclaw daemon &` then `blueclaw chat "Hello"`
5. This is a shippable prototype

### Incremental Delivery

1. Setup + Foundational → Foundation ready
2. US1 → CLI chat works → **MVP**
3. US2 → Memory works → agent remembers across sessions
4. US3 → Proactive behavior → agent acts on schedule
5. US4 → HTTP API → external integrations possible
6. US5 → SOUL.md → personality customization
7. Polish → robustness and edge cases

---

## Architecture Note: Channel Interface

The constitution mandates a Channel interface for interaction channels. This prototype defers the Channel abstraction because both CLI and API are thin HTTP clients to the same daemon — they share the same handler code path. A Channel interface will be introduced when a third transport (e.g., WhatsApp/Telegram) is added, at which point the HTTP handler logic will be extracted behind the interface. This is tracked as a future concern, not a current violation.

---

## Summary

| Phase | Story | Tasks | Tests | Parallel |
|-------|-------|-------|-------|----------|
| Phase 1: Setup | — | 3 | 0 | — |
| Phase 2: Foundational | — | 9 | 3 | T004–T007, T008∥T009, T010∥T011, T012∥T013 |
| Phase 3: US1 — CLI Chat | P1 | 8 | 5 | T016∥T017∥T019, T024∥T025∥T027 |
| Phase 4: US2 — Memory | P2 | 8 | 5 | T033∥T034, T037∥T038 |
| Phase 5: US3 — Proactive | P3 | 7 | 3 | T042∥T043, T049∥T050 |
| Phase 6: US4 — HTTP API | P4 | 3 | 1 | — |
| Phase 7: US5 — SOUL.md | P5 | 2 | 1 | — |
| Phase 8: Polish | — | 7 | 0 | — |
| **Total** | | **47** | **18** | **65 total** |

---

## Notes

- TDD is enforced per constitution Principle III (NON-NEGOTIABLE): every implementation task starts with a failing test
- Each [P] task touches a different file with no dependencies on incomplete tasks
- Commit after each task or logical group
- `go test ./...` MUST pass before any commit
- Stop at any checkpoint to validate the story independently
- FR-007 (tool IPC) is covered by T035 (IPC endpoints) + T021 (Unix socket setup)
- FR-016 (blueclaw init) is covered by T014 + T057
- Edge cases addressed: response timeout (T018), embedding fallback (T029/T064), SQLite corruption (T063), disk space (T030), stale containers (T060), daemon crash detection (T062), sequential cron (T044)
