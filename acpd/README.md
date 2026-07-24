# Blueclaw acpd

ACP agent sidecar that runs InternKim inside Buzz. The `buzz-acp` harness spawns this process over stdio (`BUZZ_ACP_AGENT_COMMAND`), and acpd bridges the Agent Client Protocol to the Blueclaw runtime.

```
buzz-acp ──stdio ACP──> acpd ──HTTP──> blueclaw POST /connectors/buzz/events
blueclaw ──HTTP /v1/platform/buzz/*──> acpd ──buzz CLI──> Buzz relay
```

- `session/prompt` parses the harness prompt grammar (`[Context]`, `[Buzz event]` blocks) into normalized inbound events. The turn stays open until Blueclaw dispatches a non-checkpoint reply, mirrored back as `session/update`.
- Replies are delivered deterministically through `buzz messages send --reply-to <anchor>`; `replyTargetID` encodes `<channelUUID>/<anchorEventID>`.
- `session/cancel` cancels the in-flight Blueclaw task run and resolves the turn with `stopReason: cancelled`.
- `identity.resolve` maps a Nostr pubkey to `display_name` and `nip05` via `buzz users get`; the nip05 handle is treated as the account email for person linking.

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `ACPD_BLUECLAW_BASE_URL` | `http://127.0.0.1:8080` | Blueclaw HTTP server (events + task cancel). |
| `ACPD_LISTEN_PORT` | `18091` | Outbound platform surface for `ChatdPlatformAdapter("buzz")`. |
| `ACPD_BUZZ_COMMAND` | `buzz` | Buzz CLI binary. Relay credentials arrive via the harness environment (`BUZZ_RELAY_URL`, `BUZZ_PRIVATE_KEY`, `BUZZ_AUTH_TAG`). |
| `ACPD_MAXIMUM_TURN_HOLD_SECONDS` | `3300` | Safety cap before an accepted turn ends on its own; replies still deliver through the CLI afterwards. |

Blueclaw side: set `connectors.buzz.enabled: true` (optional `endpoint`, `timeoutSecond`) in the runtime configuration. Run the harness with `BUZZ_ACP_AGENTS=1`; multiple agent subprocesses would contend for the listen port.

```bash
bun install
bun test
bun run typecheck
```
