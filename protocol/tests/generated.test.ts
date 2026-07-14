import { describe, expect, test } from 'bun:test';
import { buildProtocolArtifacts, serializeArtifact } from '../src/artifacts.ts';
import { protocolSchemas, protocolVersion } from '../src/registry.ts';

describe('protocol artifacts', () => {
  test('include every registered schema with a stable hash', () => {
    const { manifest } = buildProtocolArtifacts();
    expect(manifest.protocolVersion).toBe(protocolVersion);
    expect(manifest.schemas.map(({ name }: { name: string }) => name)).toEqual(Object.keys(protocolSchemas).sort());
    expect(manifest.schemas.map(({ name }: { name: string }) => name)).toEqual(
      [...manifest.schemas.map(({ name }: { name: string }) => name)].sort(),
    );
    expect(manifest.aggregateHash).toMatch(/^[a-f0-9]{64}$/);
    expect(manifest.schemas.every(({ hash }: { hash: string }) => /^[a-f0-9]{64}$/.test(hash))).toBe(true);
  });

  test('serialize deterministically from current Zod schemas', () => {
    const firstArtifacts = buildProtocolArtifacts();
    const secondArtifacts = buildProtocolArtifacts();
    expect(serializeArtifact(firstArtifacts.manifest)).toBe(serializeArtifact(secondArtifacts.manifest));
    expect(firstArtifacts.schemas.map(({ schema }) => serializeArtifact(schema))).toEqual(
      secondArtifacts.schemas.map(({ schema }) => serializeArtifact(schema)),
    );
    for (const { fileName } of firstArtifacts.schemas) {
      expect(fileName).toEndWith('.schema.json');
    }
  });
});
