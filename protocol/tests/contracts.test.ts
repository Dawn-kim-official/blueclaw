import { describe, expect, test } from 'bun:test';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

import {
  askInteractionSchema,
  capabilityDescriptorSchema,
  languageModelMessagePartSchema,
  languageModelMessageSchema,
  protocolSchemas,
  structuredResponseSchema,
  type ProtocolSchemaName,
} from '../src/index.ts';

const fixturesDirectory = fileURLToPath(new URL('../fixtures/', import.meta.url));

const fixtureSchemaNames = {
  'agent-action': 'agent-action',
  'agent-message': 'agent-message',
  'capability-descriptor': 'capability-descriptor',
  'capability-registry-response': 'capability-registry-response',
  'connector-runtime-result': 'connector-runtime-result',
  'platform-auto-resume-event': 'platform-inbound-event',
  'platform-inbound-event': 'platform-inbound-event',
  'structured-response': 'structured-response',
  'structured-response-request': 'structured-response-request',
  'task-artifact': 'task-artifact',
  'task-attempt': 'task-attempt',
  'task-event': 'task-event',
  'task-run': 'task-run',
  'task-schedule': 'task-schedule',
  'task-schedule-interval': 'task-schedule',
  'task-schedule-once': 'task-schedule',
  'tool-invoke-request': 'tool-invoke-request',
  'tool-invoke-response': 'tool-invoke-response',
} satisfies Record<string, ProtocolSchemaName>;

describe('closed protocol values', () => {
  test('rejects values outside canonical enums', () => {
    expect(languageModelMessageSchema.safeParse({ role: 'developer' }).success).toBe(false);
    expect(languageModelMessagePartSchema.safeParse({ type: 'audio' }).success).toBe(false);
    expect(structuredResponseSchema.safeParse({
      provider: 'openrouter',
      model: 'model',
      content: '{}',
      selectedBackend: 'remote',
      finishReason: 'stop',
      constraintMode: 'unknown',
    }).success).toBe(false);
    expect(structuredResponseSchema.safeParse({
      provider: '',
      model: '',
      content: '',
      selectedBackend: '',
      finishReason: 'length',
    }).success).toBe(false);
    expect(askInteractionSchema.safeParse({ interactionID: 'ask-1', taskRunID: 'task-1', kind: 'choice' }).success).toBe(false);
    expect(askInteractionSchema.safeParse({ interactionID: 'ask-1', taskRunID: 'task-1', kind: 'ask_choice_single' }).success).toBe(false);
    expect(askInteractionSchema.safeParse({ interactionID: 'ask-1', taskRunID: 'task-1', kind: 'ask_choice_multiple' }).success).toBe(false);
    expect(askInteractionSchema.safeParse({
      interactionID: 'ask-1',
      taskRunID: 'task-1',
      kind: 'ask_input',
      selectionMode: 'any',
    }).success).toBe(false);
    expect(capabilityDescriptorSchema.safeParse({
      name: 'calendar.add',
      version: '1',
      privacyClass: 'workspace_calendar',
      estimatedLatency: 'instant',
      requiresUserPresence: false,
      worksOffline: false,
    }).success).toBe(false);
  });
});

describe('protocol fixtures', () => {
  test('accepts every valid fixture', async () => {
    const fixtures = await readFixtureBundle('valid');
    expect(Object.keys(fixtures).sort(compareCodeUnits)).toEqual(Object.keys(fixtureSchemaNames).sort(compareCodeUnits));
    for (const [fixtureName, documents] of Object.entries(fixtures)) {
      const schema = schemaForFixture(fixtureName);
      for (const document of documents) expect(schema.safeParse(document).success).toBe(true);
    }
  });

  test('rejects every invalid fixture', async () => {
    const fixtures = await readFixtureBundle('invalid');
    for (const [fixtureName, documents] of Object.entries(fixtures)) {
      const schema = schemaForFixture(fixtureName);
      for (const document of documents) expect(schema.safeParse(document).success).toBe(false);
    }
  });
});

async function readFixtureBundle(kind: 'valid' | 'invalid'): Promise<Record<string, unknown[]>> {
  return JSON.parse(await readFile(`${fixturesDirectory}/${kind}.json`, 'utf8'));
}

function compareCodeUnits(left: string, right: string): number {
  if (left < right) return -1;
  if (left > right) return 1;
  return 0;
}

function schemaForFixture(fixtureName: string) {
  const schemaName = fixtureSchemaNames[fixtureName as keyof typeof fixtureSchemaNames];
  if (!schemaName) throw new Error(`Fixture does not name a protocol schema: ${fixtureName}`);
  return protocolSchemas[schemaName];
}
