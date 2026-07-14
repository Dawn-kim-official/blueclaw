# Blueclaw SDKD

`sdkd` is an optional AI SDK 6 structured-output runtime. It exposes authenticated HTTP over a private Unix socket and routes requests to OpenRouter or an explicitly enabled llama.cpp endpoint.

```bash
bun install --frozen-lockfile
bun run build
bun test
```

Runtime credentials can be passed as direct development environment values or systemd credential files:

- `BLUECLAW_SDKD_AUTH_KEY` or `BLUECLAW_SDKD_AUTH_KEY_PATH`
- `OPENROUTER_API_KEY` or `OPENROUTER_API_KEY_PATH`
- `CREDENTIALS_DIRECTORY` with `sdkd-auth-key` and `openrouter-api-key`

The default socket is `/run/blueclaw-sdkd/sdkd.sock`. Local llama.cpp structured output is rejected unless `BLUECLAW_SDKD_LLAMA_STRUCTURED_OUTPUTS_ENABLED=true` is set after model and chat-template conformance verification.
