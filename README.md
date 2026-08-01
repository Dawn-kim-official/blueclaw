# Blueclaw

**Blueclaw is the execution boundary for agents a whole company shares.** You
pick the model. You pick the loop. Blueclaw owns the part neither of those can
answer: *whose* permissions the work runs under.

An agent that serves one person can run as that person. An agent that serves a
team cannot — it needs to act as whoever asked, hold each person's files apart,
stop before irreversible things, and still be there after a restart. That is
what this repository is: per-person POSIX identity, a policy and approval model,
durable delivery, and an audit trail. It ships with an agent loop and an AI SDK
model sidecar, and both are replaceable.

It is also the runtime inside InternKim, an on-premise AI automation appliance —
that is one deployment of it, not what it is.

## What makes it different

**POSIX is the permission boundary, not a string filter.** Every person in the
policy projects to a real Linux user, every circle to a real group. When the
agent writes a file or runs a command on someone's behalf, a setuid helper drops
to that person's UID, GID, and supplementary groups first. There is no allowlist
of permitted commands and no denied-path substring check — an action the actor
may not take simply fails at the kernel. Two people sharing one Blueclaw cannot
read each other's work, and that is enforced by the kernel rather than by the
agent's good behavior.

**The model writes every sentence a human reads.** Replies, approval wording,
recovery direction, failure reports. Deterministic code validates schemas,
resolves identities, records effects, and gates wide or irreversible actions —
but it never composes a canned sentence and passes it off as the agent's answer.

**Finishing requires evidence, not a claim.** A task with required artifacts
stays open until an attachment points at a promoted durable file. A draft path,
a markdown link, or the model asserting it is done does not close it.

**Delivery is durable.** Inbound events persist before they run, replies enqueue
in an outbox keyed to the originating event, duplicates return the stored result
instead of re-running, and a restart mid-task resumes rather than dropping it.

**The layers above are yours.** The model reaches Blueclaw through `llmd`, an AI
SDK sidecar, so any OpenAI-compatible endpoint or hosted provider works and the
credentials never enter the guest. Domain operations — calendar, tasks, mail,
sites — are capability plugins declared in configuration; with none declared,
Blueclaw runs without them rather than failing.

See [docs/architecture.md](docs/architecture.md) for how these fit together.

## Repository layout

| Path | What lives there |
|---|---|
| `cmd/` | Binaries: the daemon, supervisor, POSIX helper, backup/restore, lab runner |
| `internal/` | Agent loop, task engine, policy, identity, memory, connectors, security |
| `protocol/` | Zod contracts shared across processes; generates the JSON Schema artifacts |
| `llmd/` | AI SDK sidecar — structured output and chat generation over a Unix socket |
| `chatd/` | Chat bridge and platform adapters (Mattermost, Buzz) |
| `admin/` | Svelte admin and task console |
| `migrations/` | Postgres schema, applied in order at boot |
| `tests/` | Integration suite and its fixtures |
| `lab/` | Provisioning and scenario scripts for the development VM |
| `config/` | Example policy, runtime, and lab configuration |
| `tools/` | Python sidecars, currently the Graphiti memory daemon |
| `docs/` | Architecture notes |
| `web/` | The admin console built from `admin/`, committed for packaging |

## Building

Go 1.26 and [Bun](https://bun.sh) 1.3 build everything here. The optional
memory sidecar under `tools/` is Python.

```bash
go build ./...
go test ./...
```

```bash
bun install
bun run test
```

The four TypeScript packages are one Bun workspace, so a single `bun install` at
the root covers all of them. `go test ./...` runs the unit suites next to their
sources and the integration suite under `tests/`; neither needs an external
service.

## Running it yourself

Blueclaw runs standalone against your own model. You need Postgres and one
OpenAI-compatible endpoint — Ollama, vLLM, LM Studio, OpenRouter, or anything
else that speaks that API.

**1. Start `llmd`, the model sidecar.** It holds the provider credentials, so it
runs beside the daemon rather than inside it:

```bash
cd llmd
printf 'a-local-secret' > /tmp/llmd-auth
BLUECLAW_LLMD_AUTH_KEY_PATH=/tmp/llmd-auth \
BLUECLAW_LLMD_SOCKET_PATH=/tmp/llmd.sock \
BLUECLAW_LLMD_LLAMA_BASE_URL=http://127.0.0.1:11434/v1 \
BLUECLAW_LLMD_LLAMA_MODEL=your-model \
BLUECLAW_LLMD_LLAMA_STRUCTURED_OUTPUTS_ENABLED=true \
BLUECLAW_LLMD_LOCAL_ONLY=true \
bun run src/main.ts
```

Point `LLAMA_BASE_URL` at any OpenAI-compatible server; the name is historical.
`http://127.0.0.1:11434/v1` is Ollama. The runtime asks for structured output
natively and falls back to a forced tool call when the server rejects that, and
not every server enforces the forced choice either — Ollama treats it as a hint,
so a model may answer in prose and fail the turn. Small
models also struggle with the larger runtime schemas, so treat a local model as
a development convenience and expect to raise the parameter count before real
work.
For a hosted provider instead, set `OPENROUTER_API_KEY` and drop the local
variables. `llmd/README.md` lists every setting.

**2. Start the daemon.** Copy `config/runtime.standalone.example.json`, set your
Postgres connection string and the llmd socket and key paths, then:

```bash
go run ./cmd/blueclaw --runtime runtime.json --policy config/policy.example.json
curl -s localhost:8081/admin/api/health | jq '.status, .protocolIdentity.passed'
```

A standalone deployment reports `capabilityd: not_configured` and checks only
`llmd`. There is no capability service, so the calendar, task, mail, and site
operations an appliance supplies are simply absent; everything else — the agent
loop, skills, the terminal, and files — works.

**3. Give it work.** The `api` connector needs no chat platform. Address a person
from your policy by email:

```bash
curl -s -X POST localhost:8081/connectors/api/events -H 'content-type: application/json' \
  -d '{"conversationID":"dm:api:you","messageID":"m1","senderID":"admin@example.com",
       "replyTargetID":"dm:api:you","prompt":"Say hello in one short sentence."}'

curl -s 'localhost:8081/agent/api/replies?conversationID=dm:api:you'
```

Every person in the policy projects to their own Linux user, so their files and
commands run as them. That projection needs Linux; on macOS the daemon runs
everything as itself.

To watch a whole scenario instead, the lab runner drives the agent loop and
writes every request, response, tool call, and artifact to a directory:

```bash
go run ./cmd/blueclaw-lab virtual-session --scenario presentation \
  --artifact-dir .artifacts/blueclaw-e2e --live-llm --llm-unix-socket /tmp/llmd.sock
```

Live runs spend money, so they are never enabled by configuration alone; the
explicit `--live-llm` flag (or `BLUECLAW_E2E_LIVE=1`) is required. Scenario
names are registered in `internal/e2e/virtual_session.go`.

## Contributing

Issues and pull requests are welcome. `AGENTS.md` documents the conventions this
codebase holds itself to — descriptive names over abbreviations, no explanatory
comments, one source of truth per shared contract — and CI runs the same build
and test commands listed above.

For a security problem, follow [SECURITY.md](SECURITY.md) instead of opening an
issue.

## License

MIT. See [LICENSE](LICENSE).

The Mattermost adapter under `chatd/src/adapters/mattermost/` vendors
MIT-licensed third-party code; its license is kept alongside it.
