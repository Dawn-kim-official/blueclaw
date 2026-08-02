<img src="assets/blueclaw.logo.svg" alt="Blueclaw" width="112">

# Blueclaw

**A host that runs agents as the person who asked.**

Blueclaw is a host that runs agents for a team's chat and task workflows. It
accepts normalized messages from chat connectors, turns them into durable task
runs with approvals and an event ledger, and executes every tool call as an
unprivileged POSIX user derived from the person who asked. The agent loop is a
replaceable component behind a Go interface; the host owns identity, isolation,
the task store, the tool catalog, and delivery.

It is the runtime inside InternKim, an on-premise AI automation appliance. That
is one deployment of it, not what it is.

## Architecture

Three layers, split so the loop can be swapped without moving the boundary that
keeps people apart.

| Layer | Owns | In this repository |
|---|---|---|
| **blueclaw** (host) | connectors, POSIX isolation, task store, approvals, policy, tool catalog, memory, delivery | yes |
| **harness** | the agent loop: route a turn, run it, answer, classify | `internal/bluecollar` today, moving to a private repository |
| **`agentcontract`** | the types both sides compile against, and the harness port | yes, `agentcontract/` |

```text
  chat platform / HTTP ingress
            |
            v
  blueclaw (Go host)
    connectors · policy · task store · approvals · tool catalog · POSIX projection
            |
            +-- agentcontract.Harness --+-- internal/bluecollar   (Go, in-process)
            |                           |
            |                           +-- AI SDK harness via llmd   (not built)
            |
            +-- tool execution --> blueclaw-posix-helper --> requester UID/GID/groups
```

The port is `agentcontract.Harness` (`agentcontract/harness.go:5`): nine methods
— `RunTurn`, `RouteTurn`, `RunAgentRequest`, `CompleteLaunchFailure`,
`GenerateReply`, `GenerateReplyWithContext`, `ClassifyAddressing`,
`ClassifyActiveTaskFollowUp`, `RefreshSkillIndex`. Everything else the host
needs — task events, cancellation, run lookup — it takes from the task store
directly rather than through the harness.

`internal/bluecollar` is the only implementation. `internal/bluecollar/contract.go:5`
asserts it satisfies the port and re-exports the moved types as aliases, so the
138 files that name `AgentTurnRequest`, `VisibleContext`, `InstructionBundle`
and friends did not have to change when the definitions moved to
`agentcontract/`.

**bluecollar is not part of this repository's open-source surface.** It is
scheduled to move to a private repository; `agentcontract` stays public because
blueclaw cannot build without it.

Whichever harness runs the loop, tool execution stays inside blueclaw behind the
POSIX boundary below. The harness decides *what* to call; blueclaw decides *who*
it runs as. A harness that executes tools in its own process is not an
acceptable integration.

### Gaps in the split, as of today

- The connector runtime still holds a concrete `*bluecollar.AgentKernel`
  (`internal/connectors/runtime.go:354`, `:410`) rather than
  `agentcontract.Harness`. The port compiles and is asserted, but it is not yet
  the only path from host to loop.
- No second `Harness` implementation exists. See
  [Harness selection](#harness-selection).

## The security boundary

Blueclaw's claim is narrow and mechanical: **tool execution runs as an
unprivileged POSIX user derived from the requester's identity, and POSIX
ownership and mode bits are the only access boundary.** There is no executable
allowlist, no denied-command list, and no denied-path prefix check anywhere in
the execution path.

### Identity projection

Every person in the policy document projects to a real Linux user and every
circle to a real group (`internal/security/posix_identity.go`):

| Policy object | Linux object | Code |
|---|---|---|
| person | `bc_person_<shortID>` user, and a primary group of the same name | `LinuxPersonUserName`, `posix_identity.go:243` |
| circle | `bc_circle_<circleID>` group | `LinuxCircleGroupName`, `posix_identity.go:247` |
| everyone | `bc_shared` supplementary group | `posixSharedGroupName`, `posix_identity.go:16`, applied at `:61` |
| service internals | `blueclaw` user | `blueclawServiceUserName`, `posix_identity.go:15` |

Names are lowercased and reduced to `[a-z0-9_-]`, capped at 31 characters, and a
lossy or truncated normalization gets a hash suffix so two people cannot
collide onto one account (`shortenedLinuxName`, `posix_identity.go:251-266`).

`POSIXStateForPolicy` (`posix_identity.go:161`) compiles the policy into the
users, groups, and directory modes the host applies at boot:

| Path | Owner:group | Mode |
|---|---|---|
| `<workspace>/private/people/<personID>` (and `tmp/`, `artifacts/`) | the person | `0700` |
| `<workspace>/circles/<circleID>` | `blueclaw`:`bc_circle_<id>` | `2770` |
| `<workspace>/shared` | `blueclaw`:`bc_shared` | `2755` |
| `<workspace>/shared/public`, `<workspace>/shared/cache/**` | `blueclaw`:`bc_shared` | `2775` |
| `<workspace>/private`, `<workspace>/private/people`, `<workspace>/circles` | `blueclaw`:`blueclaw` | `0711` |

`<workspace>/.blueclaw` — the runtime's own state, logs, config, and identity
map — appears in no projected directory entry, so it is never chowned or
chgrped to a task user and stays owned by the service account.

UIDs and GIDs are allocated from 100000 through a persisted allocation table
(`cmd/blueclaw-posix-helper/main.go:515-597`), so a person keeps the same
numeric identity across restarts and reprovisions and existing file ownership
does not drift.

### Applying the identity

`CommandGuardrailService.BuildCommandPlan`
(`internal/security/command_guardrail_service.go:23`) builds the plan;
`applyPOSIXRunner` (`:101`) rewrites it to invoke the setuid helper:

```
blueclaw-posix-helper exec --uid <uid> --gid <gid> --groups <gids> --cwd <dir> -- <argv>
```

The helper (`cmd/blueclaw-posix-helper/main.go`) is installed `root:root 4755`.
It authorizes only a real UID of `root` or `blueclaw`
(`authorizeHelperCaller:64`, `isAuthorizedHelperCaller:75`), then calls
`setgroups` → `setgid` → `setuid` in that order (`applyIdentity:271`) before
`syscall.Exec` (`:171`). After that call the process is the requester and cannot
regain privilege.

File tools are not a separate code path. `file_read`, `file_write`, `file_edit`
and the rest build a shell command and run it through the same requester
primitive (`internal/agentruntime/requester_shell.go:24`), starting in the
requester's own `$HOME` (`requesterShellScript:45`), so tilde expansion, globs,
and relative paths carry native POSIX semantics instead of a hand-written path
vocabulary.

### What the guardrail actually enforces

`TerminalConfiguration` (`internal/config/runtime_configuration.go:321`) has ten
fields: mode, sandbox provider, workspace root, helper path, timeout, output
cap, session cap, and the network and interactive-shell switches. None of them
is a list of commands or paths. What the guardrail does is structural:

| Check | Code |
|---|---|
| refuses to execute at all when the daemon is effectively root | `command_guardrail_service.go:24` |
| resolves the working directory against the workspace root | `:33`, `resolveWorkingDirectoryPath:191` |
| replaces the environment with a fixed allowlist of variable *names* (not values) and forces a canonical `PATH` | `sanitizeEnvironmentVariables:219` |
| caps the timeout | `timeoutSecond:140` |
| requires bubblewrap when `terminal.mode` is `sandbox` | `:61` |

Executable resolution is a `PATH` lookup, not a permission decision: an absolute
path is resolved verbatim through `EvalSymlinks`, a bare name is searched in the
canonical runtime `PATH` (`resolveExecutablePath:158`). A command the actor may
not run is not refused by blueclaw; it fails at the kernel.

Two things in this path *look* like string filters and are not access
decisions. `requesterShellOutcome.failureCode`
(`internal/agentruntime/requester_shell.go:81`) matches stderr text to classify
an already-failed command into a diagnostic code, and shell arguments are
quoted (`shellPathArgument:56`) as serialization. Neither runs before the
kernel decides.

### Gaps in the boundary, as of today

- The projection is applied only when `terminal.posixHelperPath` is configured;
  with it empty, `applyPOSIXRunner` returns the plan unchanged
  (`command_guardrail_service.go:102`) and everything runs as the daemon user.
  The shipped `config/runtime.standalone.example.json` does not set it, and the
  projection needs Linux — on macOS the daemon runs everything as itself.
- `cmd/bluecollar` deliberately uses `DirectWorkspaceActorFactory`
  (`internal/security/direct_workspace_actor.go:21`), which has no projection at
  all. It is a single-directory harness runner, not a multi-person host.
- `internal/access/access.go:22` is a Go-side ACL check still consulted before
  exposing capability and MCP tools and before memory reads. It is a migration
  leftover: the intended boundary is the POSIX actor, and this pre-check is
  slated for removal rather than extension.

Report security problems through [SECURITY.md](SECURITY.md), not a public issue.

## Install and run

There is no packaged install path in this repository: no Makefile, no
Dockerfile, no service unit, no release binaries. Running it means building from
source. The appliance tooling that provisions, packages, and deploys Blueclaw
lives in a separate private repository.

You need Go 1.26, [Bun](https://bun.sh) 1.3, Postgres, and one
OpenAI-compatible model endpoint — Ollama, vLLM, LM Studio, OpenRouter, or
anything else speaking that API.

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

`LLAMA_BASE_URL` points at any OpenAI-compatible server; the name is historical.
`http://127.0.0.1:11434/v1` is Ollama. The runtime asks for structured output
natively and falls back to a forced tool call when the server rejects that, and
not every server enforces the forced choice either — Ollama treats it as a hint,
so a model may answer in prose and fail the turn. Small models also struggle
with the larger runtime schemas, so treat a local model as a development
convenience. For a hosted provider, set `OPENROUTER_API_KEY` and drop the local
variables. `llmd/README.md` lists every setting.

**2. Start the daemon.** Copy `config/runtime.standalone.example.json`, set your
Postgres connection string and the llmd socket and key paths, then:

```bash
go run ./cmd/blueclaw --runtime runtime.json --policy config/policy.example.json
curl -s localhost:8081/admin/api/health | jq '.status, .protocolIdentity.passed'
```

`cmd/blueclaw` takes exactly two flags, `--runtime` and `--policy`
(`cmd/blueclaw/main.go:12-13`); everything else is configuration. Migrations
under `migrations/` are applied in order at boot.

A standalone deployment reports `capabilityd: not_configured` and checks only
`llmd`. There is no capability service, so the calendar, task, mail, and site
operations an appliance supplies are simply absent; the agent loop, skills, the
terminal, and files work.

To get per-person POSIX isolation on a standalone host, build and install the
helper as well, and set `terminal.posixHelperPath` to it:

```bash
go build -o /usr/local/bin/blueclaw-posix-helper ./cmd/blueclaw-posix-helper
sudo chown root:root /usr/local/bin/blueclaw-posix-helper
sudo chmod 4755 /usr/local/bin/blueclaw-posix-helper
```

The daemon then synchronizes users, groups, and directory modes from the policy
document at every boot (`internal/app/application.go:108-112`).

**3. Give it work.** The `api` connector needs no chat platform. Address a
person from your policy by email:

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

Live runs spend money, so they are never enabled by configuration alone; the
explicit `--live-llm` flag or `BLUECLAW_E2E_LIVE=1` is required
(`cmd/blueclaw-lab/main.go:178`, `:260`). Scenario names resolve through
`e2e.BuiltinScenario` (`internal/e2e/virtual_session.go:674`) and the scenarios
themselves are defined in `internal/e2e/scenarios.go`; `--scenario-file` loads
one from JSON instead.

### Other binaries

| Binary | Purpose |
|---|---|
| `cmd/blueclaw` | the daemon |
| `cmd/blueclaw-posix-helper` | setuid identity switch, POSIX state sync, filesystem operations |
| `cmd/blueclaw-lab` | development VM lifecycle and scenario runner |
| `cmd/blueclaw-supervisor` | host side: boots and watches the Firecracker guest, proxies host↔guest HTTP, handles workspace image sync and restore |
| `cmd/blueclaw-backup`, `cmd/blueclaw-restore` | workspace and database snapshot bundles |
| `cmd/blueclaw-guest-healthd`, `cmd/blueclaw-vsock-http-proxy` | guest health and host↔guest transport |
| `cmd/bluecollar` | runs the agent loop alone against one directory, for benchmarking; no database, connectors, policy, or POSIX projection |

## Harness selection

There is no harness selection mechanism today. `internal/bluecollar` is the only
`agentcontract.Harness` implementation, and `internal/app/application.go:166`
constructs it directly.

The plan is that bluecollar moves to a private repository and the public option
becomes an AI SDK harness adapter reached through the `llmd` sidecar, which
already runs on the Vercel AI SDK and can front `@ai-sdk/harness-claude-code`,
`-codex`, `-opencode` and similar. That adapter is not written. Until it is, a
self-hoster who does not have bluecollar access has no loop, and this is the
open question the split has to answer before the repository is genuinely usable
standalone.

What is settled: whichever harness is selected, tools execute in blueclaw under
the requester's POSIX identity. The port must not grow concepts from the
external-agent path (an outside agent joining the chat over ACP) — that is a
different layer with a different owner of identity and tools.

## Repository layout

| Path | What lives there |
|---|---|
| `agentcontract/` | the harness port and the turn/context/instruction types both sides compile against |
| `toolcontract/` | tool descriptors, registry, validation, kernel tool names |
| `taskstate/` | task run, step, event, and artifact stores |
| `cmd/` | binaries: daemon, supervisor, POSIX helper, backup/restore, lab runner, standalone harness runner |
| `internal/` | host implementation: connectors, agent runtime, security, policy, identity, memory, HTTP, storage |
| `internal/bluecollar/` | the agent loop; scheduled to leave this repository |
| `protocol/` | Zod contracts shared across processes; generates the JSON Schema artifacts |
| `llmd/` | AI SDK sidecar: structured output and chat generation over a Unix socket |
| `chatd/` | chat bridge and platform adapters (Mattermost, Buzz) |
| `admin/` | Svelte admin and task console sources |
| `web/` | the console built from `admin/`, committed for packaging |
| `migrations/` | Postgres schema, applied in order at boot |
| `tests/` | integration suite and fixtures |
| `lab/` | provisioning and scenario scripts for the development VM |
| `config/` | example policy, runtime, and lab configuration |
| `tools/` | Python sidecars, currently the Graphiti memory daemon |
| `docs/` | [architecture.md](docs/architecture.md) |

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
the root covers them; `bun run test` typechecks then runs `protocol`, `llmd`,
`chatd`, and `admin` in turn. CI runs exactly these commands
(`.github/workflows/ci.yml`).

Test tiers:

| Tier | How it runs | Gate |
|---|---|---|
| Unit | `go test ./...`, `bun run test` | none; no external service needed |
| Integration | `go test ./tests/integration/...` | Postgres-backed cases skip unless `BLUECLAW_TEST_POSTGRES_URL` is set |
| Live LLM | same `go test` invocation | skipped unless `BLUECLAW_LLMD_LIVE_SOCKET` + `BLUECLAW_LLMD_LIVE_AUTH_KEY`, or `BLUECLAW_LIVE_LLM_TEST=1`, is set — these call a real model and cost money |
| Virtual session | `go run ./cmd/blueclaw-lab virtual-session` | requires `--live-llm` or `BLUECLAW_E2E_LIVE=1` |
| Fleet / VM | `go run ./cmd/blueclaw-lab vm-up`, `smoke-firecracker` | needs the development VM from `config/lab.example.json` |

The POSIX separation checks are Linux-only through the `_linux` filename
constraint (`tests/integration/posix_separation_linux_test.go`); on macOS they do not run,
so a green local suite there says nothing about the isolation boundary.

Regenerating the cross-process contracts:

```bash
cd protocol && bun install && bun run generate && bun run build && bun test
```

`AGENTS.md` documents the conventions this codebase holds itself to —
descriptive names over abbreviations, no explanatory comments, one source of
truth per shared contract.

## Contributing

Issues and pull requests are welcome. For a security problem, follow
[SECURITY.md](SECURITY.md) instead of opening an issue.

## License

MIT. See [LICENSE](LICENSE).

The Mattermost adapter under `chatd/src/adapters/mattermost/` vendors
MIT-licensed third-party code; its license is kept alongside it.
