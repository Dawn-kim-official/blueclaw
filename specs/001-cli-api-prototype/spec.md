# Feature Specification: CLI & API Prototype

**Feature Branch**: `001-cli-api-prototype`
**Created**: 2026-02-18
**Status**: Draft
**Input**: User description: "build a working prototype with no skill support and no whatsapp like chat feature. just CLI/api only."

## Clarifications

### Session 2026-02-18

- Q: How does the AI backend execute inside the container? → A: Blueclaw runs a bounded agentic loop (like picoclaw's AgentLoop) inside the container that calls LLM provider APIs directly (Anthropic, OpenAI, Gemini, DeepSeek). The container provides OS-level isolation for tool execution. The host orchestrates container lifecycle and passes messages in/out.
- Q: How are memories created and retrieved? → A: Memory is agent-driven via two built-in tools: `remember` (save a subject + content as a memory file and embedding) and `recall` (search memories by query via top-K vector similarity). The AI agent decides during conversation when to use them. These are built-in tools exposed via IPC, not part of the extensible skill system.
- Q: What is the container lifecycle? → A: One container per session, kept alive with an idle timeout. The container starts on first message, stays running for follow-up messages within the session, and shuts down after the idle timeout expires (e.g., 5 minutes of inactivity).
- Q: What is the CLI interaction mode? → A: Daemon-based architecture. A persistent daemon process (`blueclaw daemon`) manages containers, sessions, and memory. The CLI (`blueclaw chat "msg"` for one-shot, `blueclaw chat` for interactive REPL) and HTTP API are both thin clients that connect to the daemon. Like picoclaw's gateway mode.
- Q: Can the AI be proactive (send messages without being asked)? → A: Yes — proactivity is a core value. The daemon supports both a periodic heartbeat (agent wakes up on a schedule to check conditions, like picoclaw's HeartbeatService) and user-defined cron tasks (scheduled jobs the agent executes and reports on, like nanobot's CronService).
- Q: Is there a UI? → A: No GUI. TUI only if necessary. All interaction is through CLI, API, or future messaging channels.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Send a Message via CLI and Get a Response (Priority: P1)

A persistent daemon (`blueclaw daemon`) runs in the background, managing containers, sessions, and memory. The user sends messages via the CLI, which connects to the daemon. `blueclaw chat "msg"` sends a single message and prints the response. `blueclaw chat` (no args) opens an interactive REPL for multi-turn conversation. The daemon routes messages to a containerized agentic loop and returns the AI's response.

**Why this priority**: This is the fundamental value proposition. A user must be able to talk to an AI agent running in a container and get a response back.

**Independent Test**: Can be fully tested by starting the daemon, running `blueclaw chat "Hello"`, and verifying a response is returned.

**Acceptance Scenarios**:

1. **Given** the daemon is running, **When** the user runs `blueclaw chat "What is 2+2?"`, **Then** the daemon routes the message to a container running the agentic loop and prints the response to stdout.
2. **Given** the daemon is running, **When** the user runs `blueclaw chat` (no args), **Then** an interactive REPL opens where the user can send multiple messages in the same session.
3. **Given** the daemon is not running, **When** the user runs `blueclaw chat`, **Then** the CLI prints a clear error telling the user to start the daemon first.
4. **Given** no container runtime is available, **When** the daemon starts, **Then** it exits with a clear error explaining which runtime is missing and how to install it.
5. **Given** the configured LLM provider is unreachable, **When** the user sends a message, **Then** the system returns an error within 10 seconds indicating the provider is unavailable.

---

### User Story 2 - Memory Persistence Across Sessions (Priority: P2)

The AI agent has access to two built-in tools: `remember` and `recall`. During a conversation, the agent decides when something is worth saving (e.g., user preferences, project context, decisions made) and calls `remember` with a subject and content. When the user returns in a later session and asks about a past topic, the agent calls `recall` with a query and receives the most relevant memories via vector similarity search. Memories are stored as Markdown files in `~/.blueclaw/short-term-memory/` and promoted to `~/.blueclaw/long-term-memory/` when recalled multiple times.

**Why this priority**: Memory transforms a stateless chatbot into a persistent assistant. Without it, every session starts from zero.

**Independent Test**: Can be tested by having the agent call `remember` in one session, then calling `recall` in a new session and verifying the saved memory is returned.

**Acceptance Scenarios**:

1. **Given** the agent is in a conversation, **When** the agent calls `remember` with subject "project deadlines" and content describing the deadlines, **Then** a Markdown file is created in short-term memory and its embedding is stored in the vector database.
2. **Given** memories exist from a prior session, **When** the agent calls `recall` with query "What were the deadlines?", **Then** the system returns the top-K most relevant memories ranked by vector similarity.
3. **Given** a memory in short-term storage has been recalled 3 or more times, **When** the promotion cycle runs, **Then** the memory file is moved to long-term storage.
4. **Given** short-term memory files older than 7 days exist and have not been promoted, **When** the cleanup cycle runs, **Then** those files are deleted.
5. **Given** 50 memories exist, **When** the agent calls `recall`, **Then** results are returned in under 2 seconds.

---

### User Story 3 - Proactive Agent Behavior (Priority: P3)

The daemon periodically wakes the AI agent via a heartbeat (e.g., every 30 minutes). The agent reads a `HEARTBEAT.md` file containing standing instructions (e.g., "check if any deadlines are approaching," "summarize unread notifications") and acts on them. The agent can also execute user-defined scheduled tasks via a cron system — the user (or the agent itself) can create recurring jobs like "every Monday at 9am, summarize my weekly goals." Results from heartbeats and cron jobs are queued and delivered to the user on their next CLI session or pushed via the API.

**Why this priority**: Proactivity is a core differentiator. An assistant that only responds when spoken to is fundamentally less useful than one that anticipates needs and acts on a schedule.

**Independent Test**: Can be tested by configuring a heartbeat with a simple instruction, waiting for the interval to pass, and verifying the agent produced output.

**Acceptance Scenarios**:

1. **Given** a `HEARTBEAT.md` exists at `~/.blueclaw/HEARTBEAT.md` with instructions, **When** the heartbeat interval elapses, **Then** the daemon wakes the agent, which reads and acts on the instructions.
2. **Given** the agent has produced proactive output, **When** the user opens a CLI session, **Then** queued messages from the agent are displayed before the prompt.
3. **Given** the user asks the agent to "remind me every day at 9am to review my tasks", **When** the agent calls a `schedule` tool with a cron expression and prompt, **Then** the daemon registers the scheduled job and executes it at the specified time.
4. **Given** a scheduled job exists, **When** the user runs `blueclaw tasks`, **Then** the system lists all active scheduled jobs with their next run time.
5. **Given** a scheduled job fails (e.g., LLM provider unreachable), **When** the next heartbeat runs, **Then** the agent is informed of the failure so it can notify the user.

---

### User Story 4 - Send a Message via HTTP API (Priority: P4)

The daemon exposes an HTTP API on a configurable port. A developer sends a POST request with a message payload and receives the AI response as JSON. The API connects to the same daemon that the CLI uses — both are thin clients to the same backend.

**Why this priority**: The API extends reach beyond terminal users but depends on the same daemon and container infrastructure built in P1.

**Independent Test**: Can be tested by sending a `curl` request to the daemon's API endpoint and verifying a JSON response is returned.

**Acceptance Scenarios**:

1. **Given** the daemon is running, **When** a client sends `POST /v1/chat` with `{"message": "Hello"}`, **Then** the daemon returns a JSON response with the AI's reply and a 200 status code.
2. **Given** the daemon is running, **When** a client sends a request with an invalid or missing message field, **Then** it returns a 400 status with a descriptive error.
3. **Given** the daemon is running, **When** a client sends a request, **Then** the response includes a session identifier that can be used for follow-up messages in the same conversation.
4. **Given** the daemon is started with `blueclaw daemon`, **Then** the HTTP API is available on the configured port (default 8080) and the daemon logs the address to stdout.

---

### User Story 5 - Agent Identity via SOUL.md (Priority: P5)

A user creates a `SOUL.md` file that defines the agent's personality, name, tone, and behavioral boundaries. When the agent responds, its behavior reflects the SOUL.md contents. For example, a SOUL.md specifying "You are a concise technical assistant" produces shorter, more direct responses than one specifying "You are a friendly conversational companion."

**Why this priority**: Personality customization differentiates Blueclaw from raw API wrappers but is not required for basic functionality.

**Independent Test**: Can be tested by creating two different SOUL.md files and verifying the agent's responses differ in tone accordingly.

**Acceptance Scenarios**:

1. **Given** a SOUL.md exists at `~/.blueclaw/SOUL.md`, **When** the user sends a message, **Then** the SOUL.md content is included in the system prompt sent to the AI backend.
2. **Given** no SOUL.md exists, **When** the user sends a message, **Then** the system uses a sensible default personality (neutral, helpful assistant).
3. **Given** the user modifies SOUL.md between sessions, **When** a new session starts, **Then** the agent reflects the updated personality immediately.

---

### Edge Cases

- What happens when the container runtime crashes mid-response? The system MUST detect the failure and return an error to the user rather than hanging indefinitely. A timeout of 60 seconds per response applies.
- What happens when the embedding model (llama.cpp) fails to load? The system MUST fall back to operating without memory search and warn the user that memory is unavailable.
- What happens when the SQLite database is corrupted? The system MUST detect the corruption on startup, warn the user, and offer to create a fresh database (preserving the Markdown memory files).
- What happens when disk space is insufficient for new memories? The system MUST check available space before writing and warn the user when storage is below a threshold.
- What happens when multiple CLI sessions run simultaneously? The daemon supports multiple concurrent sessions, each with its own conversation context and container. Memory storage is shared and concurrent writes MUST not corrupt the database (SQLite WAL mode).
- What happens when the daemon crashes? The CLI and API MUST detect the disconnection and display a clear error. Containers managed by the daemon MUST be cleaned up on daemon restart.
- What happens when a heartbeat or cron job runs while the user is in an active CLI session? Proactive output MUST be queued and not interrupt the active conversation. It is delivered after the current session ends or on the next session.
- What happens when many cron jobs are scheduled for the same time? The daemon MUST execute them sequentially to avoid spawning too many containers simultaneously.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST run its agentic loop (LLM calls + tool execution) inside a sandboxed container (Apple Container on macOS or Docker). One container per session, kept alive with a configurable idle timeout (default 5 minutes). The host orchestrates container lifecycle and passes messages in/out.
- **FR-002**: System MUST support at least one LLM provider (Anthropic, OpenAI, Gemini, or DeepSeek) configurable at runtime without recompilation.
- **FR-003**: System MUST provide a daemon process (`blueclaw daemon`) that runs persistently, manages containers, sessions, memory, and exposes the HTTP API.
- **FR-004**: System MUST provide a CLI client (`blueclaw chat "msg"` for one-shot, `blueclaw chat` for interactive REPL) that connects to the daemon and sends/receives messages.
- **FR-004a**: System MUST maintain conversation context within a session so follow-up messages reference prior exchanges.
- **FR-005**: System MUST expose a `remember` tool to the AI agent that accepts a subject and content, writes a Markdown file to `~/.blueclaw/short-term-memory/` (filename derived from subject), and stores its vector embedding in the SQLite-vec database.
- **FR-006**: System MUST expose a `recall` tool to the AI agent that accepts a query string, generates its embedding locally using EmbeddingGemma 300M via llama.cpp, and returns the top-K most relevant memories ranked by vector similarity.
- **FR-007**: The `remember` and `recall` tools MUST be exposed to the containerized AI agent via HTTP over a Unix domain socket mounted into the container at `/run/blueclaw/ipc.sock`. The daemon serves tool endpoints on this socket.
- **FR-008**: System MUST promote short-term memories to `~/.blueclaw/long-term-memory/` when they are referenced 3 or more times.
- **FR-009**: System MUST discard short-term memories older than 7 days that have not been promoted.
- **FR-010**: The daemon MUST run a heartbeat service that wakes the agent at a configurable interval (default 30 minutes) to read and act on `~/.blueclaw/HEARTBEAT.md`.
- **FR-011**: The daemon MUST expose a `schedule` tool to the AI agent that accepts a cron expression and a prompt, and registers a recurring job that triggers the agent at the specified time.
- **FR-012**: The daemon MUST queue proactive agent output and deliver it to the user on their next CLI session or via the API.
- **FR-013**: The daemon MUST provide a `blueclaw tasks` command that lists all active scheduled jobs with their next run time.
- **FR-014**: The daemon MUST expose an HTTP API with a `POST /v1/chat` endpoint accepting JSON and returning JSON.
- **FR-015**: System MUST load a SOUL.md file from `~/.blueclaw/SOUL.md` (if present) and include its contents in the system prompt.
- **FR-016**: System MUST provide a `blueclaw init` command that creates the `~/.blueclaw/` directory structure, downloads the embedding model, and runs initial setup.

### Key Entities

- **Daemon**: The persistent background process that orchestrates everything. Manages container lifecycle, sessions, memory, and exposes the HTTP API. The CLI is a thin client to the daemon.
- **Session**: A sequence of messages between user and agent within a single session. Has an identifier, start time, and ordered list of messages. One container per session, kept alive with idle timeout.
- **Message**: A single exchange unit. Has a role (user or assistant), content (text), and timestamp.
- **Memory**: A Markdown file created by the AI agent via the `remember` tool. Has a subject (filename), content, recall count, creation date, and last-recalled date. Stored in short-term memory initially, promoted to long-term memory after 3+ recalls.
- **Embedding**: A vector representation of a memory's content. Linked to a memory by subject, stored in SQLite-vec for similarity search.
- **Scheduled Job**: A recurring task registered via the `schedule` tool. Has a cron expression, a prompt for the agent, creation date, last run time, and next run time. Stored by the daemon.
- **Proactive Message**: Output produced by the agent during a heartbeat or cron job when no user is actively connected. Queued by the daemon and delivered on next CLI session or via API.
- **Agent Configuration**: Runtime settings including LLM provider selection, container runtime preference, API port, heartbeat interval, and model parameters. Stored in `~/.blueclaw/config.yaml`.

### Assumptions

- The user has Docker or Apple Container runtime pre-installed. Blueclaw does not install container runtimes.
- The user has an active API key for at least one supported LLM provider.
- The EmbeddingGemma model GGUF file is downloaded during `blueclaw init` from Hugging Face.
- Default top-K for memory search is 5 results.
- Default short-term memory retention is 7 days.
- Default promotion threshold is 3 references.
- Default API port is 8080.
- Default container idle timeout is 5 minutes.
- Default heartbeat interval is 30 minutes.
- Cron jobs are stored in `~/.blueclaw/cron/jobs.json`.
- No GUI. TUI only if necessary (e.g., interactive REPL). All interaction is terminal-based or via API.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user can install Blueclaw, run `blueclaw init`, and have a working setup within 5 minutes (excluding model download time).
- **SC-002**: A user can send a message via CLI and receive a response within 15 seconds on first message (including container startup), and within 5 seconds on subsequent messages.
- **SC-003**: Memory search returns relevant results for 80% of queries where matching memories exist (measured by manual evaluation of top-5 results).
- **SC-004**: The HTTP API handles 10 concurrent requests without errors or response degradation.
- **SC-005**: The system operates with less than 500MB of resident memory during normal usage (excluding the container's own memory).
- **SC-006**: A user who has never seen the tool can complete the setup and hold a multi-turn conversation by following only the output of `blueclaw --help` and `blueclaw init`.
