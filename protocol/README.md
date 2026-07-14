# Blueclaw Protocol

This package defines Blueclaw cross-process contracts with Zod and produces deterministic JSON Schema artifacts.

```bash
bun install
bun run generate
bun run build
bun test
```

`src/` is the contract source and `bun.lock` pins generation. Manifests and hashes are computed from Zod through the `@blueclaw/protocol/artifacts` export. `bun run generate` writes optional JSON Schema release artifacts to ignored `dist/`; generated schemas are not committed.

Changes must preserve the shared cases in `fixtures/valid.json` and `fixtures/invalid.json` until a versioned migration is available. Each bundle maps a schema case name to one or more documents so TypeScript and Go tests share compatibility evidence without a directory of one-case files. Provider calls, capability execution, task state, and platform delivery do not depend on this package at runtime yet.

The Zod schemas define the intended validated boundary. Existing Go DTOs still use permissive `encoding/json` decoding, so they can currently accept missing required fields, unknown enum values, and out-of-range numbers that Zod rejects. Do not weaken the schemas to match that decoder behavior. Enforce Zod validation at the sidecar ingress before switching traffic, then tighten individual Go boundaries only with compatibility fixtures and a versioned rollout.
