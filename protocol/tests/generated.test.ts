import { describe, expect, test } from 'bun:test';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

import { buildProtocolArtifacts, serializeArtifact } from '../scripts/artifacts.ts';
import { protocolSchemas, protocolVersion } from '../src/registry.ts';

const generatedDirectory = fileURLToPath(new URL('../generated/', import.meta.url));

describe('generated protocol artifacts', () => {
  test('include every registered schema with a stable hash', async () => {
    const manifest = JSON.parse(await readFile(`${generatedDirectory}/manifest.json`, 'utf8'));
    expect(manifest.protocolVersion).toBe(protocolVersion);
    expect(manifest.schemas.map(({ name }: { name: string }) => name)).toEqual(Object.keys(protocolSchemas).sort());
    expect(manifest.schemas.map(({ name }: { name: string }) => name)).toEqual(
      [...manifest.schemas.map(({ name }: { name: string }) => name)].sort(),
    );
    expect(manifest.aggregateHash).toMatch(/^[a-f0-9]{64}$/);
    expect(manifest.schemas.every(({ hash }: { hash: string }) => /^[a-f0-9]{64}$/.test(hash))).toBe(true);
  });

  test('match the current Zod schemas', async () => {
    const { manifest, schemas } = buildProtocolArtifacts();
    expect(await readFile(`${generatedDirectory}/manifest.json`, 'utf8')).toBe(serializeArtifact(manifest));
    for (const { fileName, schema } of schemas) {
      expect(await readFile(`${generatedDirectory}/json-schema/${fileName}`, 'utf8')).toBe(serializeArtifact(schema));
    }
  });
});
