<img src="assets/blueclaw.logo.svg" alt="blueclaw" width="112">

# blueclaw — a POSIX-isolated agent host

**blueclaw is a POSIX-isolated agent host: a Go daemon that runs an AI agent
harness on behalf of the person who asked, executes every tool call as that
person's own unprivileged Linux user, holds side-effecting calls at an approval
gate, and writes every step to a durable event ledger.** It is not an agent and
not a model. It is the process the agent runs inside, and it owns the parts an
agent should not be trusted with: identity, isolation, task state, the tool
catalog, and delivery.

The agent loop is a replaceable component behind a Go interface
(`agentcontract.Harness`, 3 methods and shrinking). One implementation ships in this
repository. Swapping it does not move the isolation boundary, because tool
execution never leaves blueclaw.

**Disambiguation.** This project is unrelated to the several other things named
"blueclaw" in the agent-infrastructure space: `blueclaw.org` /
`clawd-conroy/blueclaw` (an open social protocol for AI agents built on AT
Protocol and A2A), `blueclaw.network` (compute for agent workloads), and
`blueclaw.app` (a hosted AI agent dashboard). This repository is
`github.com/Dawn-kim-official/blueclaw`, a self-hosted Go daemon. The GitHub
organization `github.com/blueclaw` belongs to someone else and is not connected
to this project.

## What blueclaw is, and what it is not

| It is | It is not |
|---|---|
| a host process that runs an agent harness | an agent, an agent loop, or a model |
| a POSIX identity boundary around tool execution | a container runtime or a sandbox technology |
| a durable task store with an append-only event ledger | a chat client |
| an MCP client that mounts external tool servers into the catalog | an MCP server (not built; see Project status) |
| a Go daemon you build from source and self-host | a hosted service or a packaged binary release |

blueclaw is the runtime inside InternKim, an on-premise AI automation appliance.
That is one deployment of it, not what it is.

## Why an agent host, not another agent

Agent harnesses are already abundant: Claude Code, Codex, opencode, Gemini CLI,
and the loop bundled here. They all run as whoever started them. On a shared
machine that means one Unix account for every requester, no per-person file
separation, and no record of which human authorized which side effect.

blueclaw takes the opposite split. The harness decides *what* to call. blueclaw
decides *who it runs as*, *whether it runs at all*, and *what is written down*.
A harness that executes tools inside its own process is not an acceptable
integration, because it takes back the only thing the host exists to provide.

The mechanical claim is narrow and testable: tool execution runs as an
unprivileged POSIX user derived from the requester's identity, and
[POSIX](https://pubs.opengroup.org/onlinepubs/9799919799/) ownership and mode
bits are the only access boundary. There is no executable allowlist, no denied
command list, and no denied path prefix anywhere in the execution path. A
command the requester may not run is not refused by blueclaw; it fails at the
kernel.

## How it works

A *connector* is a platform adapter that normalizes an inbound message. A *task
run* is a durable record of one unit of work. The *event ledger* is the
append-only sequence of events belonging to a task run. An *approval gate* holds
a tool call until a human authorizes it. The *POSIX projection* is the mapping
from policy objects to real Linux users and groups.

```text
  chat platform / HTTP ingress
            |
            v
  blueclaw (Go daemon)
    connectors · policy · task store · approvals · tool catalog · POSIX projection
            |
            +-- agentcontract.Harness --+-- internal/bluecollar   (Go, in-process)
            |
            +-- tool execution --> blueclaw-posix-helper --> requester UID/GID/groups
```

### Intake: connectors normalize a message

Five connector adapters are registered at boot: `mattermost`, `slack`,
`signal`, `api`, and `buzz` (`internal/app/application.go`). Each turns a
platform-specific payload into the same normalized conversation turn. The `api`
connector needs no chat platform at all and is the way to drive blueclaw from
`curl` or a test.

Intake resolves the sender to a person in the policy document. That resolved
person, not the daemon account, is the identity every later step runs under.

### Task runs and the event ledger

A task run is a row in the task store with one of nine statuses
(`taskstate/task_type.go`): `planned`, `running`, `waiting_user_input`,
`waiting_approval`, `blocked`, `interrupted`, `completed`, `failed`,
`cancelled`.

Every step appends an event through `TaskEventService.AppendTaskEvent`
(`taskstate/task_event_service.go`). Event names follow a fixed wire grammar —
`tool.<name>.requested`, `tool.<name>.result`, `approval.pending_call`,
`approval.executed`, `agent.task_launched` — so a reader can reconstruct what
happened without access to the harness's internal types. The ledger is the
contract between host and harness, and the host is the side that reads it.

### Approval gates

An approval gate pauses a task run before a side-effecting tool call executes,
records `approval.pending_call` with the exact call, and re-executes that call
verbatim when the approval arrives (`internal/bluecollar/approval_gate.go`,
`taskstate.TaskRunService.PauseTaskRun`). Because the held call is persisted,
approval survives a daemon restart and does not block a live request.

Which calls are gated comes from descriptor metadata, not from the tool's name.
`toolcontract` carries an `ApprovalScope` and one of 14 `SideEffectClass` values
per tool (`toolcontract/registry.go`), from `none` and `read` through
`workspace_write`, `external_send`, `site_publish`, and `destructive`.

The gate is implemented inside the bundled harness today. Which layer should own
approval once external harnesses plug in is an open question — see Project
status.

### POSIX identity projection

Every person in the policy document projects to a real Linux user and every
circle to a real group (`internal/security/posix_identity.go`):

| Policy object | Linux object | Symbol |
|---|---|---|
| person | `bc_person_<shortID>` user with a primary group of the same name | `LinuxPersonUserName` |
| circle | `bc_circle_<circleID>` group | `LinuxCircleGroupName` |
| everyone | `bc_shared` supplementary group | `posixSharedGroupName` |
| service internals | `blueclaw` user | `blueclawServiceUserName` |

Names are lowercased, reduced to `[a-z0-9_-]`, and capped at 31 characters. A
lossy or truncated normalization gets a hash suffix, so two people cannot
collide onto one account (`shortenedLinuxName`).

`POSIXStateForPolicy` compiles the policy into the users, groups, and directory
modes the daemon applies at every boot:

| Path | Owner:group | Mode |
|---|---|---|
| `<workspace>/private/people/<personID>` (and `tmp/`, `artifacts/`) | the person | `0700` |
| `<workspace>/circles/<circleID>` | `blueclaw`:`bc_circle_<id>` | `2770` |
| `<workspace>/shared` | `blueclaw`:`bc_shared` | `2755` |
| `<workspace>/shared/public`, `<workspace>/shared/cache/**` | `blueclaw`:`bc_shared` | `2775` |
| `<workspace>/private`, `<workspace>/private/people`, `<workspace>/circles` | `blueclaw`:`blueclaw` | `0711` |

`<workspace>/.blueclaw` — the daemon's own state, logs, configuration, and
identity map — appears in no projected directory entry, so it is never chowned
or chgrped to a task user and stays owned by the service account.

UIDs and GIDs are allocated from 100000 upward through a persisted allocation
table (`cmd/blueclaw-posix-helper/main.go`), so a person keeps the same numeric
identity across restarts and reprovisions and existing file ownership does not
drift.

## Quickstart

There is no packaged install path in this repository: no Makefile, no
Dockerfile, no service unit, no release binaries. Running blueclaw means
building from source. The appliance tooling that provisions, packages, and
deploys it lives in a separate private repository.

Requirements: Go 1.26, [Bun](https://bun.sh) 1.3, Postgres, and one
OpenAI-compatible model endpoint — Ollama, vLLM, LM Studio, OpenRouter, or
anything else speaking that API.

**1. Start `llmd`, the model sidecar.** It holds the provider credentials, so it
runs beside the daemon rather than inside it.

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

`LLAMA_BASE_URL` points at any OpenAI-compatible server; the name is historical.
`http://127.0.0.1:11434/v1` is Ollama. For a hosted provider, set
`OPENROUTER_API_KEY` and drop the local variables. `llmd/README.md` lists every
setting.

Treat a local model as a development convenience. The runtime asks for
structured output natively and falls back to a forced tool call when the server
rejects that; Ollama treats the forced choice as a hint, so a model may answer
in prose and fail the turn. Small models also struggle with the larger runtime
schemas.

**2. Start the daemon.** Copy `config/runtime.standalone.example.json`, set your
Postgres connection string and the llmd socket and key paths, then:

```bash
go run ./cmd/blueclaw --runtime runtime.json --policy config/policy.example.json
curl -s localhost:8081/admin/api/health | jq '.status, .protocolIdentity.passed'
```

`cmd/blueclaw` takes exactly two flags, `--runtime` and `--policy`; everything
else is configuration. The 29 migrations under `migrations/` are applied in
order at boot.

A standalone deployment reports `capabilityd: not_configured` and checks only
`llmd`. There is no capability service, so the calendar, task, mail, and site
operations an appliance supplies are simply absent. The agent loop, skills, the
terminal, and files work.

**3. Enable per-person POSIX isolation.** The projection is applied only when
`terminal.posixHelperPath` is set. Build and install the setuid helper, then
point the configuration at it:

```bash
go build -o /usr/local/bin/blueclaw-posix-helper ./cmd/blueclaw-posix-helper
sudo chown root:root /usr/local/bin/blueclaw-posix-helper
sudo chmod 4755 /usr/local/bin/blueclaw-posix-helper
```

The daemon then synchronizes users, groups, and directory modes from the policy
document at every boot (`internal/app/application.go`).

**4. Give it work.** Address a person from your policy by email through the
`api` connector:

```bash
curl -s -X POST localhost:8081/connectors/api/events -H 'content-type: application/json' \
  -d '{"conversationID":"dm:api:you","messageID":"m1","senderID":"admin@example.com",
       "replyTargetID":"dm:api:you","prompt":"Say hello in one short sentence."}'

curl -s 'localhost:8081/agent/api/replies?conversationID=dm:api:you'
```

To watch a whole scenario instead, the lab runner drives the agent loop and
writes every request, response, tool call, and artifact to a directory:

```bash
go run ./cmd/blueclaw-lab virtual-session --scenario presentation \
  --artifact-dir .artifacts/blueclaw-e2e --live-llm --llm-unix-socket /tmp/llmd.sock
```

Live runs spend money, so they are never enabled by configuration alone. The
explicit `--live-llm` flag or `BLUECLAW_E2E_LIVE=1` is required
(`cmd/blueclaw-lab/main.go`). Scenario names resolve through
`e2e.BuiltinScenario`; the scenarios are defined in `internal/e2e/scenarios.go`,
and `--scenario-file` loads one from JSON instead.

## Harnesses

A *harness* is the agent loop: it runs a turn and reports what happened. In
blueclaw a harness is anything satisfying `agentcontract.Harness`
(`agentcontract/harness.go`), a single method:

```go
type Harness interface {
	RunTurn(context.Context, AgentTurnRequest) (AgentTurnResult, error)
}
```

The port was deliberately shrunk. It began at nine methods, most of which were
the host asking a model a question rather than the host asking an agent to
work — classifying whether a chat message was addressed to the bot, writing a
reply sentence, refreshing a skill index, routing a turn, completing a launch
failure. No external harness could honestly answer those, which made the port
unimplementable by anything blueclaw did not ship. Those now live in the host
(`internal/intake`, `internal/reply`, `internal/agentruntime`); see Project
status.

Everything else the host needs — task events, cancellation, run lookup — it
takes from the task store directly rather than through the harness.

The harness is injected at the top, not constructed inside the host. `main`
passes a factory:

```go
application := app.NewApplication(runtimeConfiguration, *policyPath, nil)
```

`cmd/blueclaw` passes no bundled factory: the daemon hosts an agent, it does not
ship one. With nothing attached, `internal/harnessselection` refuses to start and
names the harnesses a person can install — `claude-code` or `codex` — and setup
offers only the agents it found on this machine. `agentcontract` stays public
because blueclaw cannot build without it. See Project status for what is planned.

`internal/acpharness`, built on `coder/acp-go-sdk`, is blueclaw acting as an
[ACP](https://agentclientprotocol.com) *client* — the direction that plugs an
external agent into blueclaw's sandbox, publishing the requester's tool
catalog over MCP and running the external agent's tool calls as the
requester's POSIX identity.

## blueclaw versus Claude Code, Codex, opencode, and Gemini CLI

These are agent harnesses. blueclaw is the host they are intended to run inside.
The comparison is not "which is better"; it is "which layer owns what".

| Concern | Claude Code, Codex, opencode, Gemini CLI | blueclaw |
|---|---|---|
| Agent loop | yes, that is the product | delegated to a harness behind an interface |
| Model choice | yes | delegated to `llmd`, swappable mid-run |
| Runs as | the operating system user who started it | an unprivileged user derived from the requester |
| Multi-person separation | none; one process, one account | per-person Linux user, group, and `0700` home |
| Approval | in-process prompt, lost on exit | persisted `approval.pending_call`, re-executed verbatim later |
| Audit record | terminal scrollback and local session files | append-only event ledger in Postgres, per task run |
| Inbound surface | a terminal | chat connectors and HTTP ingress |
| Work lifetime | one interactive session | durable task runs across restarts |

What is true today: the harness that plugs into blueclaw is the bundled
`internal/bluecollar` loop. Running Claude Code, Codex, opencode, or Gemini CLI
inside blueclaw is the goal of the project, not a shipped feature. The interface
they would satisfy exists and is asserted; the adapter that speaks to them does
not. See Project status.

## Security model

Report security problems through [SECURITY.md](SECURITY.md), not a public issue.

### Applying the identity

`CommandGuardrailService.BuildCommandPlan`
(`internal/security/command_guardrail_service.go`) builds the plan;
`applyPOSIXRunner` rewrites it to invoke the setuid helper:

```text
blueclaw-posix-helper exec --uid <uid> --gid <gid> --groups <gids> --cwd <dir> -- <argv>
```

The helper (`cmd/blueclaw-posix-helper/main.go`) is installed `root:root 4755`.
It authorizes only a real UID of `root` or `blueclaw`
(`authorizeHelperCaller`, `isAuthorizedHelperCaller`), then calls `setgroups`,
`setgid`, and `setuid` in that order (`applyIdentity`) before `syscall.Exec`.
After that call the process is the requester and cannot regain privilege.

File tools are not a separate code path. `file_read`, `file_write`, `file_edit`
and the rest build a shell command and run it through the same requester
primitive (`internal/agentruntime/requester_shell.go`), starting in the
requester's own `$HOME` (`requesterShellScript`), so tilde expansion, globs, and
relative paths carry native POSIX semantics instead of a hand-written path
vocabulary.

### What the guardrail actually enforces

`TerminalConfiguration` (`internal/config/runtime_configuration.go`) has 9
fields: mode, sandbox provider, workspace root, POSIX helper path, timeout,
output cap, session cap, and the network and interactive-shell switches. None of
them is a list of commands or paths. What the guardrail does is structural:

| Check | Where |
|---|---|
| refuses to execute at all when the daemon is effectively root | `BuildCommandPlan` |
| resolves the working directory against the workspace root | `resolveWorkingDirectoryPath` |
| replaces the environment with a fixed allowlist of variable *names* (not values) and forces a canonical `PATH` | `sanitizeEnvironmentVariables` |
| caps the timeout | `timeoutSecond` |
| requires bubblewrap when `terminal.mode` is `sandbox` | `BuildCommandPlan` |

Executable resolution is a `PATH` lookup, not a permission decision. An absolute
path is resolved verbatim through `EvalSymlinks`; a bare name is searched in the
canonical runtime `PATH` (`resolveExecutablePath`).

Two things in this path *look* like string filters and are not access decisions.
`requesterShellOutcome.failureCode` (`internal/agentruntime/requester_shell.go`)
matches stderr text to classify an already-failed command into a diagnostic
code, and shell arguments are quoted (`shellPathArgument`) as serialization.
Neither runs before the kernel decides.

### Known gaps in the boundary

- The projection is applied only when `terminal.posixHelperPath` is configured.
  With it empty, `applyPOSIXRunner` returns the plan unchanged and everything
  runs as the daemon user. The shipped `config/runtime.standalone.example.json`
  does not set it, and the projection needs Linux — on macOS the daemon runs
  everything as itself.
- `cmd/bluecollar` deliberately uses `DirectWorkspaceActorFactory`
  (`internal/security/direct_workspace_actor.go`), which has no projection at
  all. It is a single-directory batch runner, not a multi-person host.
- `internal/access/access.go` still exposes `CanAccess`, a Go-side ACL check
  consulted before exposing capability and MCP tools and before memory reads. It
  is a migration leftover: the intended boundary is the POSIX actor, and this
  pre-check is slated for removal rather than extension. It is a hard blocker on
  publishing, because the POSIX-only claim above is not fully true while it
  exists.
- The POSIX separation tests are Linux-only through the `_linux` filename
  constraint (`tests/integration/posix_separation_linux_test.go`). A green suite
  on macOS says nothing about the isolation boundary.

## Configuration

Two files, both passed as flags. `--runtime` points at a runtime configuration
(start from `config/runtime.standalone.example.json`); `--policy` points at a
policy document (`config/policy.example.json`). The policy document is the
source of the people and circles the POSIX projection compiles.

The terminal section is the one that decides whether isolation is on:

```json
{
  "terminal": {
    "mode": "native",
    "workspaceRootPath": "/workspace",
    "posixHelperPath": "/usr/local/bin/blueclaw-posix-helper",
    "timeoutSecond": 600,
    "allowNetwork": true,
    "allowInteractiveShell": false
  }
}
```

External [MCP](https://modelcontextprotocol.io) servers mount into the tool
catalog through `mcpServers` in the same runtime configuration
(`MCPServerConfiguration`, `internal/config/runtime_configuration.go`). Each
server's tools can carry a result contract and policy metadata, so an external
tool participates in the same approval and evidence rules as a built-in one.

## Repository layout

| Path | What lives there |
|---|---|
| `agentcontract/` | the harness port and the turn, context, and instruction types both sides compile against |
| `toolcontract/` | tool descriptors, registry, validation, kernel tool names |
| `taskstate/` | task run, step, event, and artifact stores |
| `model/` | language model, chat completion, structured output, and embedding interfaces |
| `agenttest/` | scripted language model for deterministic tests |
| `cmd/` | 9 binaries; see the table below |
| `internal/` | host implementation: connectors, agent runtime, security, policy, identity, memory, HTTP, storage |
| `internal/bluecollar/` | the agent loop; scheduled to leave this repository |
| `internal/acpharness/` | blueclaw as an ACP client, plugging an external agent into the sandbox |
| `protocol/` | Zod contracts shared across processes; generates the JSON Schema artifacts |
| `llmd/` | AI SDK sidecar: structured output and chat generation over a Unix socket |
| `chatd/` | chat bridge and platform adapters (Mattermost, Buzz) |
| `admin/` | Svelte admin and task console sources |
| `web/` | the console built from `admin/`, committed for packaging |
| `migrations/` | 29 Postgres migrations, applied in order at boot |
| `tests/` | integration suite and fixtures |
| `lab/` | provisioning and scenario scripts for the development VM |
| `config/` | example policy, runtime, and lab configuration |
| `tools/` | Python sidecars, currently the Graphiti memory daemon |
| `docs/` | [architecture.md](docs/architecture.md) |

| Binary | Purpose |
|---|---|
| `cmd/blueclaw` | the daemon |
| `cmd/blueclaw-posix-helper` | setuid identity switch, POSIX state sync, filesystem operations |
| `cmd/blueclaw-lab` | development VM lifecycle and scenario runner |
| `cmd/blueclaw-supervisor` | boots and watches the Firecracker guest, proxies host and guest HTTP, handles workspace image sync and restore |
| `cmd/blueclaw-backup`, `cmd/blueclaw-restore` | workspace and database snapshot bundles |
| `cmd/blueclaw-guest-healthd`, `cmd/blueclaw-vsock-http-proxy` | guest health and host-to-guest transport |
| `cmd/bluecollar` | runs the agent loop alone against one directory, for benchmarking; no database, connectors, policy, or POSIX projection |

## Development

```bash
go build ./...
go vet ./...
go test ./...
```

```bash
bun install
bun run test
```

The four TypeScript packages are one Bun workspace, so a single `bun install` at
the root covers them. `bun run test` typechecks, then runs `protocol`, `llmd`,
`chatd`, and `admin` in turn. CI runs exactly these commands
(`.github/workflows/ci.yml`), with Postgres 16 as a service.

| Test tier | How it runs | Gate |
|---|---|---|
| Unit | `go test ./...`, `bun run test` | none; no external service needed |
| Integration | `go test ./tests/integration/...` | Postgres-backed cases skip unless `BLUECLAW_TEST_POSTGRES_URL` is set |
| Live model | same `go test` invocation | skipped unless `BLUECLAW_LLMD_LIVE_SOCKET` and `BLUECLAW_LLMD_LIVE_AUTH_KEY`, or `BLUECLAW_LIVE_LLM_TEST=1`, are set — these call a real model and cost money |
| Virtual session | `go run ./cmd/blueclaw-lab virtual-session` | requires `--live-llm` or `BLUECLAW_E2E_LIVE=1` |
| Fleet and VM | `go run ./cmd/blueclaw-lab vm-up`, `smoke-firecracker` | needs the development VM from `config/lab.example.json` |

Regenerating the cross-process contracts:

```bash
cd protocol && bun install && bun run generate && bun run build && bun test
```

`AGENTS.md` documents the conventions this codebase holds itself to:
descriptive names over abbreviations, no explanatory comments, one source of
truth per shared contract.

## Project status

Shipped and working: the daemon, the five connectors, the task store and event
ledger, the POSIX projection and setuid helper, the approval gate, the MCP
client, the `llmd` model sidecar with mid-run model swapping, and one in-process
harness.

Planned and **not built**. Nothing below is a feature you can use today.

| Planned | State |
|---|---|
| External harnesses (Claude Code, Codex, opencode, Gemini CLI) plugging in | not built. No second `Harness` implementation exists, and no adapter speaks to any external harness. The route — ACP client versus AI SDK harness adapter — is still an open decision. |
| CLI and terminal user interface | not built. Planned on `charm.land/bubbletea/v2`: task timeline, approval queue, live tool calls, harness selection. |
| MCP server exposing blueclaw's tool catalog | not built. blueclaw consumes MCP servers today; it does not publish one. |
| `internal/bluecollar` moving to its own repository | not done. The 131 files are still here. |
| Removal of the `internal/access` Go-side ACL pre-check | not done. See Known gaps in the boundary. |
| A harness port narrow enough for an external harness | done. Down from nine methods to one, `RunTurn`. Turn routing (deciding whether an inbound message becomes a task at all) and launch-failure completion are host policy now and live in `internal/agentruntime`. |

Publishing blockers, in order: remove the Go-side ACL pre-check so the
POSIX-only claim is true; complete a secrets and history audit; get at least one
external harness plugging in. Until a self-hoster without `internal/bluecollar`
has a working loop, this repository is a host with a hole where the agent should
be.

## FAQ

### Is this the same blueclaw as the AT Protocol agent project?

No. `blueclaw.org` and `clawd-conroy/blueclaw` are an open social protocol for
AI agents built on AT Protocol and A2A. `blueclaw.network` sells compute for
agent workloads. `blueclaw.app` is a hosted AI agent dashboard. This project is
`github.com/Dawn-kim-official/blueclaw`, an unrelated self-hosted Go daemon that
runs an agent harness under per-requester POSIX identity. The GitHub
organization `github.com/blueclaw` is not ours either.

### Can I run Claude Code or Codex inside blueclaw today?

No. That is the goal of the project and the reason the harness interface exists,
but no adapter to an external harness is written. The only working loop today is
the bundled `internal/bluecollar`.

### Is blueclaw an agent?

No. It runs one. blueclaw owns identity, isolation, the task store, approvals,
the tool catalog, and delivery. The harness owns the loop: route a turn, run it,
answer.

### How is this different from running an agent in a container?

A container isolates the agent from the host. blueclaw isolates requesters from
each other *inside* the workspace. Two people talking to the same daemon get
different Linux users, different `0700` home directories, and different group
memberships, so one person's agent run cannot read the other's files even though
both run in the same container. The two are complementary; blueclaw also
supports bubblewrap when `terminal.mode` is `sandbox`.

### What stops the agent from running a dangerous command?

Nothing in blueclaw, by design. There is no executable allowlist and no denied
command list. The command runs as an unprivileged user with that user's
permissions, and the kernel refuses what that user may not do. Wide or
irreversible tool calls are handled separately, by the approval gate, which
requires a human before the call executes.

### Does it need Linux?

For the isolation boundary, yes. The POSIX projection, the setuid helper, and
the separation tests are all Linux-only. blueclaw builds and runs on macOS for
development, but there it runs everything as the daemon user.

### Do I need Postgres and a chat platform?

Postgres, yes; the task store and event ledger live there. A chat platform, no.
The `api` connector accepts a JSON POST and returns replies over HTTP, which is
enough to drive a task run from `curl`.

### Is there a binary release or a Docker image?

No. There is no Makefile, Dockerfile, service unit, or release binary in this
repository. Build from source with `go build ./...`.

## Contributing

Issues and pull requests are welcome. For a security problem, follow
[SECURITY.md](SECURITY.md) instead of opening an issue.

## License

MIT. See [LICENSE](LICENSE).

The Mattermost adapter under `chatd/src/adapters/mattermost/` vendors
MIT-licensed third-party code; its license is kept alongside it.
