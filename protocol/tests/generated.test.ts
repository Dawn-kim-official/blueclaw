import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

import { describe, expect, test } from 'bun:test';
import Ajv2020 from 'ajv/dist/2020.js';
import { z } from 'zod';

import { buildProtocolArtifacts, serializeArtifact } from '../src/artifacts.ts';
import { protocolSchemas, protocolVersion } from '../src/registry.ts';

const generatedDirectory = fileURLToPath(new URL('../generated/', import.meta.url));
const fixturesDirectory = fileURLToPath(new URL('../fixtures/', import.meta.url));

describe('protocol artifacts', () => {
  test('include every registered schema with a stable hash', () => {
    const { capabilityToolCatalog, manifest } = buildProtocolArtifacts();
    expect(manifest.protocolVersion).toBe(protocolVersion);
    expect(manifest.capabilityToolCatalog.fileName).toBe('capability-tools.json');
    expect(manifest.capabilityToolCatalog.hash).toBe(capabilityToolCatalog.hash);
    expect(capabilityToolCatalog.catalog.tools.map(tool => tool.name)).toEqual([
      'task.add',
      'task.list',
      'task.definitions',
      'task.update',
      'task.delete',
      'calendar.add',
      'calendar.list',
      'calendar.update',
      'calendar.delete',
      'message.context',
      'message.search',
      'message.send',
      'message.update',
      'message.delete',
      'channel.update',
      'web.search',
      'site.serve',
      'site.list',
      'site.unserve',
      'document.read',
      'image.read',
      'browser.open',
      'browser.snapshot',
      'browser.screenshot',
      'browser.click',
      'artifact.review',
    ]);
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

  test('keeps generated closed schema validation equivalent to runtime validation', async () => {
    const structuredOutputRequestSchema = JSON.parse(await readFile(
      `${generatedDirectory}/json-schema/structured-response-request.schema.json`,
      'utf8',
    ));
    const capabilityDescriptorSchema = JSON.parse(await readFile(
      `${generatedDirectory}/json-schema/capability-descriptor.schema.json`,
      'utf8',
    ));
    const fixtureCatalog = z.record(z.string(), z.array(z.json())).parse(
      JSON.parse(await readFile(`${fixturesDirectory}/valid.json`, 'utf8')),
    );
    const structuredOutputRequestValidator = new Ajv2020({ strict: false }).compile(structuredOutputRequestSchema);
    const capabilityDescriptorValidator = new Ajv2020({ strict: false }).compile(capabilityDescriptorSchema);
    const closedStructuredOutputRequest = {
      executionMode: 'remote',
      messages: [],
      structuredOutputSchema: {
        name: 'structured.enum-result',
        document: {
          type: 'object',
          properties: { state: { enum: ['ready', 'blocked'] } },
          required: ['state'],
          additionalProperties: false,
        },
        isStrictlyEnforced: true,
      },
    };
    const openStructuredOutputRequest = {
      ...closedStructuredOutputRequest,
      structuredOutputSchema: {
        ...closedStructuredOutputRequest.structuredOutputSchema,
        document: {
          ...closedStructuredOutputRequest.structuredOutputSchema.document,
          properties: {
            state: {
              type: 'object',
              properties: { label: { type: 'string' } },
            },
          },
        },
      },
    };
    const validCapabilityDescriptor = protocolSchemas['capability-descriptor'].parse(
      fixtureCatalog['capability-descriptor']?.[0],
    );
    const capabilityInputSchema = z.record(z.string(), z.unknown()).parse(validCapabilityDescriptor.inputSchema);
    const capabilityInputProperties = z.record(z.string(), z.unknown()).parse(capabilityInputSchema.properties);
    const openCapabilityDescriptor = {
      ...validCapabilityDescriptor,
      inputSchema: {
        ...capabilityInputSchema,
        properties: {
          ...capabilityInputProperties,
          nested: { type: 'object', properties: {} },
        },
      },
    };

    expect(structuredOutputRequestValidator(closedStructuredOutputRequest)).toBe(true);
    expect(structuredOutputRequestValidator(openStructuredOutputRequest)).toBe(false);
    expect(capabilityDescriptorValidator(validCapabilityDescriptor)).toBe(true);
    expect(capabilityDescriptorValidator(openCapabilityDescriptor)).toBe(false);
  });
});
