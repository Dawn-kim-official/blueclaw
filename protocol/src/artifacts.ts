import { createHash } from 'node:crypto';

import { z } from 'zod';

import { buildCapabilityToolCatalog } from './capability_tools.ts';
import { protocolSchemas, protocolVersion } from './registry.ts';

type SchemaArtifact = {
  fileName: string;
  hash: string;
  name: string;
  schema: Record<string, unknown>;
};

type CatalogArtifact = {
  fileName: string;
  hash: string;
  catalog: ReturnType<typeof buildCapabilityToolCatalog>;
};

export type ProtocolManifest = {
  aggregateHash: string;
  capabilityToolCatalog: Pick<CatalogArtifact, 'fileName' | 'hash'>;
  protocolVersion: string;
  schemas: Array<Pick<SchemaArtifact, 'fileName' | 'hash' | 'name'>>;
};

export function buildProtocolArtifacts() {
  const schemas = Object.entries(protocolSchemas)
    .sort(([left], [right]) => compareCodeUnits(left, right))
    .map(([name, schema]) => buildSchemaArtifact(name, schema));
  const capabilityToolCatalog = buildCatalogArtifact();
  const artifactHashes = [
    ...schemas.map(({ name, fileName, hash }) => `${name}:${fileName}:${hash}`),
    `capability-tool-catalog:${capabilityToolCatalog.fileName}:${capabilityToolCatalog.hash}`,
  ];
  const aggregateHash = calculateHash(artifactHashes.join('\n'));
  return {
    capabilityToolCatalog,
    manifest: {
      aggregateHash,
      capabilityToolCatalog: withoutCatalog(capabilityToolCatalog),
      protocolVersion,
      schemas: schemas.map(withoutSchema),
    },
    schemas,
  };
}

export function serializeArtifact(value: unknown): string {
  return `${JSON.stringify(sortValue(value), null, 2)}\n`;
}

function buildSchemaArtifact(name: string, schema: z.ZodType): SchemaArtifact {
  const jsonSchema = z.toJSONSchema(schema) as Record<string, unknown>;
  const document = {
    ...jsonSchema,
    $id: `https://schemas.blueclaw.dev/${protocolVersion}/${name}.schema.json`,
  };
  return {
    fileName: `${name}.schema.json`,
    hash: calculateHash(serializeArtifact(document)),
    name,
    schema: document,
  };
}

function buildCatalogArtifact(): CatalogArtifact {
  const catalog = buildCapabilityToolCatalog(protocolVersion);
  return {
    fileName: 'capability-tools.json',
    hash: calculateHash(serializeArtifact(catalog)),
    catalog,
  };
}

function withoutSchema({ fileName, hash, name }: SchemaArtifact) {
  return { fileName, hash, name };
}

function withoutCatalog({ fileName, hash }: CatalogArtifact) {
  return { fileName, hash };
}

function calculateHash(value: string): string {
  return createHash('sha256').update(value).digest('hex');
}

function sortValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(sortValue);
  if (!value || typeof value !== 'object') return value;
  return Object.fromEntries(
    Object.entries(value)
      .sort(([left], [right]) => compareCodeUnits(left, right))
      .map(([key, entryValue]) => [key, sortValue(entryValue)]),
  );
}

function compareCodeUnits(left: string, right: string): number {
  if (left < right) return -1;
  if (left > right) return 1;
  return 0;
}
