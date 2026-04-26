# Blueclaw

Blueclaw is the daemon that runs inside `InternKim`, a dedicated hardware appliance for company AI automation.

## Current Working Model

- `InternKim` is a separate computer that the customer owns
- `InternKim` is a headless device with no attached display
- `Blueclaw` runs on `InternKim` as a long-lived daemon
- `InternKim` is reachable remotely through `Cloudflare Tunnel`
- remote admin access is expected to sit behind `Cloudflare Tunnel` with `Google OAuth`
- `InternKim` connector sidecars own Slack, Mattermost, and Signal platform credentials
- `Blueclaw` receives normalized connector events from `InternKim` and never stores platform tokens
- `Mattermost` is self-hosted inside `InternKim`
- `Mattermost` is an `InternKim`-native internal service, separate from the isolated `Blueclaw` guest
- `Mattermost` realtime WebSocket ingress, Slack Events API or Socket Mode, and Signal sessions run outside Blueclaw behind the capability boundary
- `Blueclaw` stores policy, memory, task state, backups, and artifacts on persistent workspace storage
- `Blueclaw` supports one-time, interval, and cron-based scheduled task execution
- `Blueclaw` may ask the user's main computer to open a browser instance when a flow requires direct user login or approval
- `Blueclaw` is configured and operated from the user's main computer through `SSH` and `HTTP API`
- the primary deployment model is `Blueclaw inside a long-lived Firecracker guest with only workspace mounted from the host`
- the primary development lab uses `macOS host -> Tart ARM Linux VM -> Firecracker -> Blueclaw`

## Architecture Picture

```text
        Slack
          ^
          |
          v
  +-----------------------------------+
  | InternKim hardware                |
  |                                   |
  |  Cloudflare Tunnel                |
  |  headless host OS                 |
  |    - tiny supervisor              |
  |    - capability sidecars          |
  |    - workspace volume             |
  |    - self-hosted Mattermost       |
  |                                   |
  |  isolated guest                   |
  |    - blueclaw daemon              |
  |    - postgres                     |
  |    - admin API                    |
  |    - task inbox API               |
  |    - policy engine                |
  |    - memory engine                |
  +-----------------------------------+
          ^                     ^
          |                     |
          |                     |
          v                     v
  +-----------------------------------+
  | user's main computer              |
  |    - SSH client                   |
  |    - admin/task UI client         |
  |    - companion bridge             |
  |    - browser instance             |
  |    - user approval surface        |
  +-----------------------------------+
```

## Main Computer Bridge

- `InternKim` and the user's main computer need a trusted communication channel
- the main computer runs a small companion bridge
- the bridge can launch a browser window or tab on the main computer
- login-required flows stay user-mediated instead of trying to bypass authentication
- `Blueclaw` can pause a task, ask for login or approval, and resume after the bridge reports completion
- scheduled automation should create new task runs instead of bypassing the task engine

## Control Plane

- `InternKim` has no local screen, so configuration happens from the user's main computer
- the primary control paths are `SSH`, `HTTP API`, and browser-based surfaces rendered on the main computer
- `Blueclaw` admin and task surfaces should be reachable through `SSH` port forwarding, authenticated API access, or a tightly scoped remote tunnel path
- the tunnel-protected admin surface should only admit the initial bootstrap administrator account or a `master admin` account
- `Google OAuth` is the outer remote access gate, and Blueclaw role checks are the inner authorization gate
- the physical appliance is the system of record, while the main computer is the operator console

## Messaging Plane

- `InternKim` owns Slack Events API or Socket Mode, Mattermost WebSocket, Signal sessions, platform tokens, and platform API calls
- `Blueclaw` exposes `/connectors/{platform}/events` as a normalized internal ingress endpoint for `InternKim` sidecars
- `Slack`, `Mattermost`, and `Signal` events enter Blueclaw through the same `ConnectorRuntime`
- Blueclaw keeps idempotency, invited-email authorization, task creation, progress orchestration, LLM reply decisions, and structured logs
- `Mattermost` is self-hosted inside `InternKim`
- `Mattermost` is an internal service separate from the isolated `Blueclaw` guest
- `InternKim` capability endpoints perform identity lookup, optional progress publication, reply send, and history fetch
- Slack typing is represented as optional InternKim progress, not as a Blueclaw platform API call
- InternKim sidecars suppress bot/self messages before forwarding events
- duplicate suppression, invited-email authorization, task creation, LLM reply generation, fallback replies, and structured logs remain shared by the connector core
- the messaging plane and the control plane should stay separated

## Connector Configuration

Runtime connector configuration is secretless. Blueclaw does not read Slack, Mattermost, or Signal tokens.

```json
{
  "capabilities": {
    "endpoint": "http://127.0.0.1:7781",
    "unixSocketPath": "/run/internkim/capability.sock",
    "timeoutSecond": 15
  },
  "connectors": {
    "mattermost": {},
    "slack": {},
    "signal": {
      "enabled": false
    }
  }
}
```

- `/connectors/mattermost/events`, `/connectors/slack/events`, and `/connectors/signal/events` accept normalized events from InternKim sidecars
- platform request signatures, bot tokens, WebSocket credentials, Signal session secrets, and platform reply credentials stay in InternKim sidecars
- Blueclaw capability adapters call `POST /v1/platform/{platform}/identity.resolve`, `reply.send`, and `history.fetch`; `progress.start` and `progress.stop` are optional
- inbound bodies use opaque `conversationID`, `messageID`, `senderID`, `replyTargetID`, `prompt`, and recent `context.messages`
- connector logs use `connector.<platform>.<stage>` event names
- persistent logs default to `/workspace/.blueclaw/logs` and are retained for 7 days unless configured otherwise

Minimal normalized event body:

```json
{
  "conversationID": "opaque-conversation-id",
  "messageID": "opaque-message-id",
  "senderID": "opaque-sender-id",
  "replyTargetID": "opaque-reply-target-id",
  "prompt": "current user message",
  "context": {
    "messages": [
      {
        "speaker": "admin",
        "text": "previous visible message"
      }
    ],
    "hasMoreBefore": true,
    "historyCursor": "opaque-history-cursor"
  }
}
```

## Language Model Configuration

Blueclaw uses a single secretless LLM provider named `capabilityLLM`. OpenRouter keys, LiteRT-LM model runtime, local GPU selection, and provider fallback policy are owned by InternKim capability services.

```json
{
  "capabilities": {
    "endpoint": "http://127.0.0.1:7781",
    "unixSocketPath": "/run/internkim/capability.sock",
    "timeoutSecond": 15
  },
  "languageModel": {
    "defaultProvider": "capabilityLLM",
    "capability": {
      "model": "gemma-4-E4B-it",
      "executionMode": "auto"
    }
  }
}
```

- Blueclaw sends only `model`, `executionMode`, `messages`, and `structuredOutputSchema` to `POST /v1/llm/structured`
- Blueclaw never adds an `Authorization` header for LLM capability calls
- `executionMode` is `local`, `remote`, or `auto`; InternKim decides whether that maps to OpenRouter, LiteRT-LM, Jetson GPU, or another provider
- `tools/blueclaw-litert-wrapper` is kept as an InternKim-side reference utility, not as a Blueclaw product runtime dependency
- if the capability LLM endpoint fails, the connector sends a short model-configuration failure reply and writes the detailed error to the persistent log

## Migration Goal

- teams already using `Slack` should be able to adopt `Blueclaw` without losing their historical collaboration context
- `InternKim` should support migrating eligible `Slack` workspace data into the self-hosted `Mattermost` service
- the target operator experience is `export from Slack -> transform -> import into local Mattermost -> continue with Blueclaw on top`
- `Blueclaw` can orchestrate and monitor this migration flow, but it should not assume it can bypass Slack export permissions or plan limits

## Security Boundary

- the preferred model is `Blueclaw entire runtime inside one isolated guest`
- the primary isolation boundary is the long-lived `Firecracker` guest
- the host exposes only one writable shared path, `workspace`
- the guest root filesystem stays immutable
- persistent application data lives under `workspace/.blueclaw`
- tools executed by `Blueclaw` run directly inside the guest boundary
- the user main computer is not the primary execution environment for agent work
- the user main computer is mainly for browser handoff, approval, and interactive login
- remote access should expose as little privileged surface as possible, even when `Cloudflare Tunnel` is enabled
- tunnel reachability alone is not sufficient for admin access
- admin actions should require both successful `Google OAuth` and membership in the allowed administrator account set

## Why This Model Fits

- it matches the product shape of a dedicated appliance
- it keeps long-lived secrets and memory on `InternKim`
- it allows local `Mattermost` hosting without depending on a third-party team chat host
- it allows the user to complete real web logins on their own machine
- it gives a clean trust boundary between host, guest, workspace, and desktop bridge
- it uses `Firecracker` as the primary guest runtime when the target hardware supports it

## Open Decisions

- how the main computer bridge authenticates to `InternKim`
- whether browser handoff uses a local bridge app, a local web UI, or both
- whether `Jetson Orin Nano Super` has a reliable enough `KVM` path for the primary guest runtime

## Status

- this README captures the current architecture picture
- it is intentionally a working design note, not a final product contract

## Development Lab

- the default development lane uses a single `M4 + macOS 15+` host
- the host acts as the main computer for companion, browser, and operator access
- `Tart` provides the ARM Linux virtual machine that simulates `InternKim`
- `Firecracker` runs inside that Linux virtual machine
- `Blueclaw` runs only inside the `Firecracker` guest
- `Mattermost` stays outside the guest and inside the Linux virtual machine
- `Docker` and `Apple container` are not part of this lab topology
