# Implementation Plan: CLI & API Prototype

**Branch**: `001-cli-api-prototype` | **Date**: 2026-02-18 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/001-cli-api-prototype/spec.md`

## Summary

Build a working Blueclaw prototype with daemon-based architecture, CLI/API interaction, containerized agentic loop, vector-backed memory (`remember`/`recall`), proactive behavior (heartbeat + cron), and SOUL.md personality. No skill system, no messaging channels.

The daemon (`blueclaw daemon`) is the core process — it manages container lifecycle, sessions, memory (sqlite-vec + llama-server sidecar), heartbeat/cron services, and exposes an HTTP API. The CLI (`blueclaw chat`) and HTTP API are thin clients communicating over a Unix domain socket and TCP port respectively.

## Technical Context

**Language/Version**: Go 1.22+
**Primary Dependencies**: kong (CLI), go-cron (scheduling), yaml.v3 (config), docker/client (containers), mattn/go-sqlite3 + sqlite-vec-go-bindings (vector storage)
**Storage**: SQLite + sqlite-vec (`~/.blueclaw/memory.db`), Markdown files (memories), JSON files (sessions, cron jobs, outbox)
**Testing**: `go test` with table-driven tests, integration tests for container/memory/IPC
**Target Platform**: macOS (Apple Container primary), Linux (Docker)
**Project Type**: Single project (cmd/ + internal/)
**Performance Goals**: <15s first message (cold container start), <5s subsequent. <2s memory recall.
**Constraints**: <500MB resident memory (daemon only). No GUI/TUI unless necessary.
**Scale/Scope**: Single user, local-only. Hundreds of memories, multiple concurrent sessions supported (each with own container).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Simplicity First | PASS | 6 direct dependencies, flat cmd/internal structure, no config framework |
| II. Type Safety | PASS | All interfaces typed (ContainerRuntime, LLMProvider, Tool). No any/interface{} in public APIs. CGo only for sqlite-vec. |
| III. Test-First | PASS | Table-driven tests planned for all packages. Integration tests for container lifecycle, memory, IPC, tool loop. |
| IV. Clean Code | PASS | No abbreviations in naming. Single-responsibility functions. Max 300 lines per file. |
| Architecture: Runtime isolation | PASS | Agentic loop runs inside container. Tool IPC over mounted Unix socket. |
| Architecture: Memory system | PASS | Agent-driven remember/recall tools. sqlite-vec + llama-server sidecar. |
| Architecture: LLM providers | PASS | Runtime-configurable via config.yaml. Provider interface abstracts all backends. |

**Post-Phase 1 re-check**: PASS. No violations introduced during design.

## Project Structure

### Documentation (this feature)

```text
specs/001-cli-api-prototype/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── daemon-api.yaml
│   └── tool-ipc.md
└── tasks.md
```

### Source Code (repository root)

```text
cmd/
└── blueclaw/
    └── main.go                  # Kong CLI entry point

internal/
├── configuration/
│   └── configuration.go         # YAML config loading + env overrides
├── daemon/
│   ├── daemon.go                # Daemon startup, subsystem orchestration
│   ├── server.go                # HTTP handlers (Unix socket + TCP)
│   └── outbox.go                # Proactive message queuing/delivery
├── container/
│   ├── runtime.go               # ContainerRuntime interface
│   ├── docker.go                # Docker implementation
│   ├── apple.go                 # Apple Container implementation
│   └── idle.go                  # Idle timeout tracker
├── agent/
│   ├── loop.go                  # Agentic loop (call LLM → tools → repeat)
│   ├── context.go               # Prompt assembly (SOUL.md + memory + history)
│   └── session.go               # Session management
├── provider/
│   ├── provider.go              # LLMProvider interface
│   ├── anthropic.go             # Anthropic implementation
│   └── openai.go                # OpenAI-compatible implementation
├── tool/
│   ├── tool.go                  # Tool interface + registry
│   ├── remember.go              # remember tool
│   ├── recall.go                # recall tool
│   └── schedule.go              # schedule tool
├── memory/
│   ├── store.go                 # Memory CRUD (Markdown files + SQLite metadata)
│   ├── embedding.go             # llama-server sidecar client
│   ├── search.go                # sqlite-vec similarity search
│   └── promotion.go             # Short-term → long-term promotion + cleanup
├── heartbeat/
│   └── heartbeat.go             # HeartbeatService (reads HEARTBEAT.md)
├── scheduler/
│   └── scheduler.go             # CronService (job persistence + execution)
└── initialize/
    └── initialize.go            # blueclaw init (directory setup, model download)
```

**Structure Decision**: Standard Go project layout with `cmd/` for the binary entry point and `internal/` for all packages. Flat internal structure — each package is a direct child of `internal/`, no nesting. This matches the constitution's requirement for flat module structure.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| CGo (sqlite-vec) | sqlite-vec requires CGo via mattn/go-sqlite3 | WASM alternative is less mature; no pure-Go sqlite-vec binding exists |
| llama-server sidecar | Embedding generation requires llama.cpp | CGo bindings rejected (fragile forks, breaks cross-compilation, crash kills daemon) |
| Docker SDK dependency | Docker runtime needs typed Go SDK | exec-based wrapping loses type safety and error handling granularity |
| No Channel interface | Constitution mandates Channel interface; deferred because CLI and API share the same HTTP handler code path | Will be introduced when a third transport (messaging) is added |
