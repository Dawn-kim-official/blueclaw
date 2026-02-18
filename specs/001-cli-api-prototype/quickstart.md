# Quickstart: Blueclaw CLI & API Prototype

## Prerequisites

- Go 1.22+
- Docker or Apple Container (`container` CLI on macOS 26)
- An API key for at least one LLM provider (Anthropic, OpenAI, Gemini, or DeepSeek)
- `llama-server` binary (from llama.cpp) on PATH

## Build

```bash
go build -o blueclaw ./cmd/blueclaw
```

## Initialize

```bash
blueclaw init
```

This creates `~/.blueclaw/` with:
- `config.yaml` (edit to set your LLM provider and API key)
- `SOUL.md` (default personality, edit to customize)
- `HEARTBEAT.md` (default heartbeat instructions, edit to customize)
- Downloads EmbeddingGemma 300M GGUF model

## Configure

Edit `~/.blueclaw/config.yaml`:

```yaml
llmProvider: anthropic
containerRuntime: apple
apiPort: 8080
heartbeatInterval: 30m
idleTimeout: 5m
memoryTopK: 5
```

Set your API key via environment variable:

```bash
export ANTHROPIC_API_KEY=sk-...
```

## Start the Daemon

```bash
blueclaw daemon
```

The daemon starts the embedding server sidecar, begins listening on the Unix socket and TCP port, and starts the heartbeat service.

## Chat (One-shot)

```bash
blueclaw chat "What is the capital of France?"
```

## Chat (Interactive REPL)

```bash
blueclaw chat
> What is the capital of France?
Paris is the capital of France.
> Remember that I'm interested in European geography
Noted. I'll remember your interest in European geography.
> exit
```

## HTTP API

```bash
curl -X POST http://localhost:8080/v1/chat \
  -H "Content-Type: application/json" \
  -d '{"message": "Hello"}'
```

Response:
```json
{
  "sessionID": "abc-123",
  "response": "Hello! How can I help you today?"
}
```

## List Scheduled Tasks

```bash
blueclaw tasks
```

## Check Proactive Messages

When the daemon has produced output from heartbeat or cron jobs:

```bash
blueclaw chat
[Pending messages from your assistant]
- [2026-02-18 09:00] Your weekly goals review: ...
>
```

## Verify

1. `blueclaw daemon` starts without errors
2. `blueclaw chat "Hello"` returns a response
3. `curl localhost:8080/v1/health` returns `{"status": "healthy"}`
