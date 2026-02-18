# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Blueclaw is an ultra-lightweight personal AI assistant written in Go, inspired by nanobot (HKUDS) and picoclaw (sipeed). It runs a bounded agentic loop that calls LLM provider APIs directly (Anthropic, OpenAI, Gemini, DeepSeek), executes tools, and iterates until done. Unlike its reference projects, Blueclaw adds container isolation (Apple Container or Docker) for tool execution and vector-based memory (SQLite-vec + EmbeddingGemma 300M via llama.cpp). Users interact through CLI, HTTP API, or messaging channels (WhatsApp/Telegram).

## Commands

```bash
go build ./...
go test ./...
go test -race ./...
go test -run TestFunctionName ./path/to/package
go vet ./...
staticcheck ./...
```

## Architecture

**Agentic loop**: Call LLM → check for tool calls → execute tools inside container → feed results back → repeat until done or iteration limit. Modeled after picoclaw's AgentLoop.

**Daemon architecture**: `blueclaw daemon` runs persistently, manages containers, sessions, memory, and exposes the HTTP API. CLI (`blueclaw chat`) and API are thin clients to the daemon. Like picoclaw's gateway mode.

**Data flow**: CLI/API client → Daemon → Container (agentic loop + tool execution) → Response

**Memory system** (`~/.blueclaw/`): Agent-driven via two built-in tools:
- `remember` — saves subject + content as Markdown file in `short-term-memory/`, stores vector embedding
- `recall` — searches memories by query via top-K vector similarity from SQLite-vec
- Short-term entries auto-discarded by age; promoted to `long-term-memory/` after 3+ recalls
- Embeddings generated locally by EmbeddingGemma 300M QAT (GGUF) through llama.cpp — no external API calls

**Proactive behavior**: The daemon runs a HeartbeatService (reads `HEARTBEAT.md` at configurable interval, default 30min) and a CronService (user-defined scheduled tasks via `schedule` tool). Proactive output is queued and delivered on next CLI session or via API.

**SOUL.md**: Personality/boundary definition loaded into system prompt on every LLM call (like picoclaw).

**Skills**: Plugin system extensible via Clawhub (registry) or manual placement.

**LLM providers**: Runtime-configurable. Anthropic, OpenAI, Gemini, DeepSeek, etc.

## Code Standards

- Go with `cmd/` and `internal/` layout. No deep nesting.
- No `any`/`interface{}` except at serialization boundaries. Typed structs for all message payloads.
- Explicit error returns with `fmt.Errorf` wrapping. No panics for recoverable conditions.
- CGo permitted only for llama.cpp bindings.
- No abbreviations: `message` not `msg`, `response` not `resp`, `container` not `ctr`.
- Leading initialisms lowercased in camelCase (`idToken`, `urlParams`), trailing uppercased (`userID`, `callbackURL`).
- Functions: single responsibility, 10-20 lines. Max nesting depth: 2 levels. Early returns over nested conditionals.
- Self-documenting code. Comments only for non-obvious "why" rationale.
- No file exceeds 300 lines.
- Standard library first. Every external dependency must be justified.

## Testing

- TDD: failing test → make it pass → refactor. No production code without a test.
- Table-driven tests as default pattern.
- Each test covers happy path, error path, and at least one edge case.
- Integration tests required for: container lifecycle, memory remember/recall, channel message round-trips, tool execution loop, skill loading.

## Constitution

Project governance is defined in `.specify/memory/constitution.md` (v1.1.0). The constitution supersedes all other practices. Deviations from Simplicity First must be justified in the plan's Complexity Tracking table.

## Active Technologies
- Go 1.22+ + kong (CLI), go-cron (scheduling), yaml.v3 (config), docker/client (containers), mattn/go-sqlite3 + sqlite-vec-go-bindings (vector storage) (001-cli-api-prototype)
- SQLite + sqlite-vec (`~/.blueclaw/memory.db`), Markdown files (memories), JSON files (sessions, cron jobs, outbox) (001-cli-api-prototype)

## Recent Changes
- 001-cli-api-prototype: Added Go 1.22+ + kong (CLI), go-cron (scheduling), yaml.v3 (config), docker/client (containers), mattn/go-sqlite3 + sqlite-vec-go-bindings (vector storage)
