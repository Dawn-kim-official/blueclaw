# Data Model: CLI & API Prototype

**Branch**: `001-cli-api-prototype` | **Date**: 2026-02-18

## Entities

### Configuration

Stored at `~/.blueclaw/config.yaml`. Loaded once at daemon startup.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| llmProvider | string | "anthropic" | Active LLM provider (anthropic, openai, gemini, deepseek) |
| anthropicApiKey | string | "" | Anthropic API key (or via ANTHROPIC_API_KEY env) |
| openaiApiKey | string | "" | OpenAI API key (or via OPENAI_API_KEY env) |
| geminiApiKey | string | "" | Gemini API key (or via GEMINI_API_KEY env) |
| deepseekApiKey | string | "" | DeepSeek API key (or via DEEPSEEK_API_KEY env) |
| containerRuntime | string | "apple" | Container runtime (apple, docker) |
| apiPort | int | 8080 | TCP port for HTTP API |
| heartbeatInterval | string | "30m" | Duration between heartbeat wakeups |
| idleTimeout | string | "5m" | Container idle timeout before shutdown |
| memoryTopK | int | 5 | Number of results returned by recall |
| model | string | "" | LLM model name (provider-specific default if empty) |
| embeddingPort | int | 8990 | Port for llama-server sidecar |

### Session

In-memory, managed by the daemon. Persisted to `~/.blueclaw/sessions/` as JSON files for recovery.

| Field | Type | Description |
|-------|------|-------------|
| id | string | UUID, generated on session creation |
| containerID | string | ID of the associated running container |
| createdAt | timestamp | When the session started |
| lastActivityAt | timestamp | Updated on every message, used for idle timeout |
| messages | []Message | Ordered conversation history |

**State transitions**: `active` → `idle` (no messages for idle timeout period) → `terminated` (container stopped)

### Message

Part of a Session. Not independently persisted.

| Field | Type | Description |
|-------|------|-------------|
| role | string | "user", "assistant", or "tool" |
| content | string | Message text |
| timestamp | timestamp | When the message was sent/received |
| toolCalls | []ToolCall | Tool calls made by the assistant (if any) |

### ToolCall

Part of a Message.

| Field | Type | Description |
|-------|------|-------------|
| id | string | Unique call ID for matching results |
| name | string | Tool name (remember, recall, schedule) |
| arguments | map | Tool-specific arguments |
| result | string | Tool execution result |

### Memory

Markdown file in `~/.blueclaw/short-term-memory/` or `~/.blueclaw/long-term-memory/`.

| Field | Type | Description |
|-------|------|-------------|
| subject | string | Memory topic, used as filename (slugified) |
| content | string | Markdown content of the memory |
| recallCount | int | Number of times recalled via the recall tool |
| createdAt | timestamp | When the memory was created |
| lastRecalledAt | timestamp | When the memory was last recalled |
| storage | string | "short-term" or "long-term" |

**Metadata**: Stored as YAML frontmatter in the Markdown file.

**State transitions**: `short-term` → `long-term` (when recallCount >= 3). `short-term` → `deleted` (when age > 7 days and not promoted).

### Embedding

Stored in SQLite-vec database at `~/.blueclaw/memory.db`.

| Field | Type | Description |
|-------|------|-------------|
| rowid | integer | Primary key, matches memory_metadata.id |
| vector | float[768] | 768-dimensional embedding from EmbeddingGemma 300M |

### MemoryMetadata

Stored in SQLite at `~/.blueclaw/memory.db` alongside embeddings.

| Field | Type | Description |
|-------|------|-------------|
| id | integer | Primary key (used as rowid in vec table) |
| subject | string | Memory subject (unique) |
| filePath | string | Path to the Markdown file |
| storage | string | "short-term" or "long-term" |
| recallCount | integer | Number of times recalled |
| createdAt | timestamp | When the memory was created |
| lastRecalledAt | timestamp | When last recalled |

### ScheduledJob

Stored in `~/.blueclaw/cron/jobs.json`.

| Field | Type | Description |
|-------|------|-------------|
| id | string | UUID |
| cronExpression | string | Standard cron expression (e.g., "0 9 * * 1") |
| prompt | string | The prompt to send to the agent when triggered |
| createdAt | timestamp | When the job was created |
| lastRunAt | timestamp | When the job last executed |
| nextRunAt | timestamp | Calculated next execution time |

### ProactiveMessage

Stored in `~/.blueclaw/outbox/` as JSON files until delivered.

| Field | Type | Description |
|-------|------|-------------|
| id | string | UUID |
| source | string | "heartbeat" or "cron:{jobID}" |
| content | string | Agent's output text |
| createdAt | timestamp | When the message was generated |
| delivered | bool | Whether the message has been shown to the user |

## Relationships

```text
Session 1──* Message
Message 0──* ToolCall
Memory 1──1 Embedding (via MemoryMetadata.id = Embedding.rowid)
ScheduledJob 1──* ProactiveMessage (via source field)
```

## Storage Layout

```text
~/.blueclaw/
├── config.yaml
├── SOUL.md
├── HEARTBEAT.md
├── daemon.sock
├── memory.db                    # SQLite + sqlite-vec (embeddings + metadata)
├── short-term-memory/
│   ├── project-deadlines.md
│   └── user-preferences.md
├── long-term-memory/
│   └── favorite-tools.md
├── sessions/
│   └── {session-id}.json
├── cron/
│   └── jobs.json
└── outbox/
    └── {message-id}.json
```
