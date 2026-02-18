# Research: CLI & API Prototype

**Branch**: `001-cli-api-prototype` | **Date**: 2026-02-18

## Container Runtime Abstraction

**Decision**: Abstract Docker and Apple Container behind a `ContainerRuntime` interface. Docker uses `github.com/docker/docker/client` (Go SDK). Apple Container uses `os/exec` wrapping the `container` CLI (no Go SDK exists).

**Rationale**: The Docker Go SDK is stable and well-documented. Apple's `container` tool is Swift-only with no Go bindings, so exec-based invocation is the only option. A common interface lets us swap runtimes without changing application logic.

**Alternatives considered**:
- Docker go-sdk (higher-level): Pre-v1.0, may introduce breaking changes. Rejected.
- Direct exec for both: Would lose Docker SDK's typed API. Rejected.

## Host-Container IPC

**Decision**: Unix domain socket mounted into the container. The daemon runs an HTTP server on the socket; the container connects to it for tool calls (`remember`, `recall`, `schedule`). stdout/stderr remain available for agent response streaming and logging.

**Rationale**: Unix sockets provide bidirectional, low-latency IPC with filesystem permissions for access control. Using HTTP over the socket means the same handlers serve both the CLI client and container tool calls.

**Alternatives considered**:
- stdin/stdout only: Single channel, no multiplexing, mixes IPC with logging. Rejected for tool calls.
- Filesystem-based IPC (file polling): Higher latency, race conditions. Rejected.
- gRPC over Unix socket: Adds protobuf codegen and grpc dependency. Overkill for single-user assistant. Rejected.

## Embedding Generation

**Decision**: Run `llama-server` as a managed sidecar process. The daemon starts it on startup, health-checks it, and calls its OpenAI-compatible `/v1/embeddings` HTTP endpoint for embedding generation.

**Rationale**: Avoids CGo entirely for the embedding pipeline. The sidecar is fault-isolated (crash doesn't kill the daemon), supports batch embedding, and can be updated independently. The HTTP overhead (~5-20ms) is negligible compared to embedding computation time.

**Alternatives considered**:
- CGo bindings (go-llama.cpp): Breaks cross-compilation, fragile fork ecosystem, C++ crash kills Go process. Rejected.
- llama-embedding CLI subprocess: Per-call process spawn overhead (~50-200ms). Rejected.

## Vector Storage

**Decision**: sqlite-vec via CGo bindings (`github.com/asg017/sqlite-vec-go-bindings/cgo` + `github.com/mattn/go-sqlite3`). Embedding dimension: 768 (EmbeddingGemma 300M default). Column type: `float[768]`.

**Rationale**: sqlite-vec is purpose-built for this use case. The Go bindings are official. CGo is acceptable here because mattn/go-sqlite3 is the most battle-tested SQLite driver in the Go ecosystem, and sqlite-vec requires it.

**Alternatives considered**:
- WASM-based sqlite-vec (ncruces): Avoids CGo but less mature. Rejected for now.
- Separate vector DB (Chroma, Milvus): External service dependency, violates simplicity principle. Rejected.

## CLI Framework

**Decision**: `alecthomas/kong`. Struct-based CLI definition with zero transitive dependencies.

**Rationale**: Subcommands are struct fields tagged with `cmd:""`. No code generation, no builder pattern. Aligns with the type-safety principle (CLI structure is a typed struct). Zero transitive dependencies aligns with simplicity principle.

**Alternatives considered**:
- cobra: Imperative builder pattern, pulls pflag/viper, larger binary. Rejected.
- stdlib flag: No subcommand support. Rejected.
- urfave/cli: Imperative like cobra with fewer features. Rejected.

## Daemon-CLI IPC

**Decision**: HTTP over Unix domain socket (`~/.blueclaw/daemon.sock`) using stdlib `net/http`. The same `http.ServeMux` serves both the Unix socket (for CLI) and a TCP port (for HTTP API).

**Rationale**: The daemon already needs an HTTP API (FR-014). Using HTTP over a Unix socket for CLI communication means one set of handlers, familiar semantics, and zero additional dependencies. This is the same pattern Docker uses for its daemon-CLI communication.

**Alternatives considered**:
- Raw Unix socket: Hand-roll wire protocol. Rejected.
- TCP localhost: Exposes a port unnecessarily. Rejected.
- gRPC: Adds protobuf + grpc dependencies. Rejected.

## Cron Scheduling

**Decision**: `netresearch/go-cron` (maintained fork of robfig/cron v3). Jobs persisted in `~/.blueclaw/cron/jobs.json`.

**Rationale**: Cron expression parsing is non-trivial and well-solved by this library. The fork fixes critical bugs (DST handling, concurrent access panics) in the unmaintained original.

**Alternatives considered**:
- robfig/cron: Unmaintained since 2020, known panic bugs. Rejected.
- stdlib time.Ticker: Cannot express "every Monday at 9am". Rejected.
- go-co-op/gocron: Larger API surface than needed. Rejected.

## Configuration

**Decision**: `gopkg.in/yaml.v3` with direct struct unmarshal. Defaults in code, file at `~/.blueclaw/config.yaml`, environment variable overrides (`BLUECLAW_*`).

**Rationale**: ~50 lines of code handles defaults, file loading, and env overrides. A configuration framework adds no meaningful reduction.

**Alternatives considered**:
- viper: Force-lowercases keys, dozens of transitive deps. Rejected.
- koanf: Good library but still an abstraction over yaml.Unmarshal + os.Getenv. Rejected.

## Process Management

**Decision**: Stdlib `os/signal` for graceful shutdown. launchd plist (macOS) or systemd unit (Linux) for backgrounding and restart-on-crash. No Go library.

**Rationale**: The OS service manager provides backgrounding, log management, and restart-on-crash for free. A single launchd plist is simpler than an abstraction library.

**Alternatives considered**:
- kardianos/service: Adds dependency to abstract over launchd/systemd. Not justified for v1. Rejected.

## Dependency Budget

| Package | Purpose | Transitive Deps |
|---------|---------|-----------------|
| `alecthomas/kong` | CLI framework | 0 |
| `netresearch/go-cron` | Cron scheduling | 0 |
| `gopkg.in/yaml.v3` | Config parsing | 0 |
| `github.com/docker/docker/client` | Docker container management | ~10 (moby ecosystem) |
| `github.com/mattn/go-sqlite3` | SQLite driver (CGo) | 0 |
| `github.com/asg017/sqlite-vec-go-bindings/cgo` | sqlite-vec extension | 0 |

Total: 6 direct dependencies. CGo required for sqlite-vec only. llama-server is an external binary, not a Go dependency.
