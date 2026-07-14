import { describe, expect, test } from 'bun:test';
import { readdir, readFile } from 'node:fs/promises';
import { basename, join } from 'node:path';
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
      constraintMode: 'unknown',
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
    const fixturePaths = await listFixturePaths('valid');
    expect(fixturePaths.map(fixtureName).sort(compareCodeUnits)).toEqual(Object.keys(fixtureSchemaNames).sort(compareCodeUnits));
    for (const fixturePath of fixturePaths) {
      const schema = schemaForFixture(fixturePath);
      expect(schema.safeParse(await readFixture(fixturePath)).success).toBe(true);
    }
  });

  test('rejects every invalid fixture', async () => {
    for (const fixturePath of await listFixturePaths('invalid')) {
      const schema = schemaForFixture(fixturePath);
      expect(schema.safeParse(await readFixture(fixturePath)).success).toBe(false);
    }
  });
});

async function listFixturePaths(kind: 'valid' | 'invalid'): Promise<string[]> {
  const directory = join(fixturesDirectory, kind);
  return (await readdir(directory))
    .filter(fileName => fileName.endsWith('.json'))
    .sort(compareCodeUnits)
    .map(fileName => join(directory, fileName));
}

function fixtureName(fixturePath: string): string {
  return basename(fixturePath, '.json');
}

async function readFixture(fixturePath: string): Promise<unknown> {
  return JSON.parse(await readFile(fixturePath, 'utf8'));
}

function compareCodeUnits(left: string, right: string): number {
  if (left < right) return -1;
  if (left > right) return 1;
  return 0;
}

function schemaForFixture(fixturePath: string) {
  const fixtureName = basename(fixturePath, '.json');
  const schemaName = fixtureSchemaNames[fixtureName as keyof typeof fixtureSchemaNames];
  if (!schemaName) throw new Error(`Fixture does not name a protocol schema: ${fixtureName}`);
  return protocolSchemas[schemaName];
}
