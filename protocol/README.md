# Blueclaw Protocol

This package defines Blueclaw cross-process contracts with Zod and produces deterministic JSON Schema artifacts.

```bash
bun install
bun run generate
bun run build
bun test
```

`src/` is the contract source. `generated/` and `bun.lock` are checked in so Go services and packaged runtimes can consume the same protocol without installing TypeScript dependencies.

Changes must preserve the shared fixtures under `fixtures/` until a versioned migration is available. Provider calls, capability execution, task state, and platform delivery do not depend on this package at runtime yet.

The Zod schemas define the intended validated boundary. Existing Go DTOs still use permissive `encoding/json` decoding, so they can currently accept missing required fields, unknown enum values, and out-of-range numbers that Zod rejects. Do not weaken the schemas to match that decoder behavior. Enforce Zod validation at the sidecar ingress before switching traffic, then tighten individual Go boundaries only with compatibility fixtures and a versioned rollout.
