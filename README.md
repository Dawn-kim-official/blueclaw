# Blueclaw

Blueclaw is the daemon that runs inside `InternKim`, a dedicated hardware appliance for company AI automation.

## Current Working Model

- `InternKim` is a separate computer that the customer owns
- `InternKim` is a headless device with no attached display
- `Blueclaw` runs on `InternKim` as a long-lived daemon
- `InternKim` is reachable remotely through `Cloudflare Tunnel`
- remote admin access is expected to sit behind `Cloudflare Tunnel` with `Google OAuth`
- `Slack` uses an Events API HTTP callback into `Blueclaw` and outbound Slack Web API calls for replies
- `Mattermost` is self-hosted inside `InternKim`
- `Mattermost` is an `InternKim`-native internal service, separate from the isolated `Blueclaw` guest
- `Mattermost` uses a local WebSocket listener for realtime ingress and HTTP API calls for typing and replies
- `Signal` is scaffolded behind the same connector contract but remains disabled for production in v1
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

- `Slack` is an external integration from `InternKim`
- Slack ingress uses the Events API callback at `/connectors/slack/events`
- Slack replies use the Slack Web API, currently `chat.postMessage`
- Slack Socket Mode is not the v1 production baseline, but the connector transport model can add it later
- `Mattermost` is self-hosted inside `InternKim`
- `Mattermost` is an internal service separate from the isolated `Blueclaw` guest
- `Blueclaw` connects to the local `Mattermost` service over the internal appliance network boundary
- Mattermost ingress uses WebSocket `posted` events, with `/connectors/mattermost/events` kept as an HTTP-compatible path
- Mattermost typing indicators are emitted while Blueclaw is generating a reply or running tools
- Slack does not provide the same typing indicator API, so Slack typing is a no-op in v1
- all connector ingress flows normalize into the same `ConnectorRuntime`
- platform adapters only parse payloads, resolve identity, publish typing or presence when supported, and send replies
- duplicate suppression, self-message suppression, invited-email authorization, task creation, LLM reply generation, fallback replies, and structured logs are shared by the connector core
- the messaging plane and the control plane should stay separated

## Connector Configuration

Runtime connector configuration is read from `runtime.json` or `runtime.yaml` through the same schema.

```json
{
  "connectors": {
    "mattermost": {
      "baseURL": "http://localhost:8065",
      "botTokenPath": "/run/secrets/mattermost-token",
      "webSocketURL": "ws://localhost:8065/api/v4/websocket"
    },
    "slack": {
      "baseURL": "https://slack.com/api",
      "botTokenPath": "/run/secrets/slack-token",
      "signingSecretPath": "/run/secrets/slack-signing-secret"
    },
    "signal": {
      "enabled": false
    }
  }
}
```

- Mattermost `webSocketURL` can be omitted when it can be derived from `baseURL`
- Slack `signingSecretPath` enables request signature verification for Events API callbacks
- `signal.enabled=true` is rejected in v1 because the adapter is scaffolded but not production-enabled
- connector logs use `connector.<platform>.<stage>` event names
- persistent logs default to `/workspace/.blueclaw/logs` and are retained for 7 days unless configured otherwise

## Language Model Configuration

Blueclaw replies use the configured language model before falling back to a short failure message.

```json
{
  "languageModel": {
    "defaultProvider": "openRouter",
    "fallbackProvider": "liteRTLM",
    "openRouter": {
      "baseURL": "https://openrouter.ai/api/v1/chat/completions",
      "modelName": "openai/gpt-4.1-mini",
      "apiKeyPath": "/run/secrets/openrouter-api-key",
      "requireParameters": true,
      "enableResponseHealing": true
    },
    "liteRTLM": {
      "wrapperPath": "/usr/local/bin/blueclaw-litert-wrapper",
      "wrapperArguments": ["--stdio"],
      "modelPath": "/workspace/.blueclaw/models/gemma-3n-E2B-it-int4.litertlm",
      "backend": "cpu",
      "constraintProvider": "llguidance"
    }
  }
}
```

- `openRouter.apiKeyPath` is preferred on the appliance because secrets are file-backed there
- `OPENROUTER_API_KEY` remains a development fallback when `apiKeyPath` is empty
- `fallbackProvider=liteRTLM` keeps local fallback available when the OpenRouter call fails
- if both providers fail, the connector sends a short model-configuration failure reply and writes the detailed error to the persistent log

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
