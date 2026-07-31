# Blueclaw

Blueclaw is an agent daemon for a company's own hardware. It sits in the team's
chat, takes work assigned to it, and carries that work to a finished artifact —
without the company's data, credentials, or memory ever leaving the appliance
it runs on.

It is the runtime inside InternKim, an on-premise AI automation appliance.
This repository is the daemon itself: the agent loop, the
policy and permission model, the task engine, the connector runtime, the chat
adapters, and the AI SDK sidecar.

## What makes it different

**POSIX is the permission boundary, not a string filter.** Every person in the
policy projects to a real Linux user, every circle to a real group. When the
agent writes a file or runs a command on someone's behalf, a setuid helper drops
to that person's UID, GID, and supplementary groups first. There is no allowlist
of permitted commands and no denied-path substring check — an action the actor
may not take simply fails at the kernel.

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
| `lab/` | Provisioning and scenario scripts for the development VM |
| `config/` | Example policy, runtime, and lab configuration |

## Building

Go 1.26 and [Bun](https://bun.sh) 1.3 are the only prerequisites.

```bash
go build ./...
go test ./internal/...
```

```bash
for package in protocol llmd chatd admin; do (cd $package && bun install); done
bun run test
```

`go test ./tests/...` additionally runs the integration suite, which starts a
Postgres container.

## Running

Blueclaw expects the appliance around it: a capability layer that holds the
provider credentials, connector sidecars that own the platform tokens, and a
workspace volume with the POSIX users its policy projects to. The provisioning
and deployment tooling for that appliance is not part of this repository, so
`go build` gives you a daemon that will start and immediately look for a
capability socket it cannot find.

The closest thing to a standalone run is the lab runner, which drives the full
agent loop through a scenario and writes every request, response, tool call, and
artifact to a directory you can inspect:

```bash
go run ./cmd/blueclaw-lab virtual-session \
  --scenario presentation \
  --artifact-dir .artifacts/blueclaw-e2e \
  --live-llm \
  --llm-unix-socket /run/internkim/capability.sock
```

It still needs a real provider behind that socket. Live runs spend money, so
they are never enabled by configuration alone — the explicit `--live-llm` flag
(or `BLUECLAW_E2E_LIVE=1`) is required, and the runner refuses to start without
it. Scenario names are registered in `internal/e2e/virtual_session.go`.

Configuration examples are in `config/`.

## Contributing

Issues and pull requests are welcome. `AGENTS.md` documents the conventions this
codebase holds itself to — descriptive names over abbreviations, no explanatory
comments, one source of truth per shared contract — and CI runs the same build
and test commands listed above.

## License

Apache License 2.0. See [LICENSE](LICENSE).

The Mattermost adapter under `chatd/src/adapters/mattermost/` vendors
MIT-licensed third-party code; its license is kept alongside it.
