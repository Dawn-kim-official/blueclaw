import { describe, expect, test } from 'bun:test';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

import {
  askInteractionSchema,
  ChatCompletionFinishReason,
  chatCompletionMessageSchema,
  chatCompletionRequestSchema,
  chatCompletionResponseSchema,
  capabilityDescriptorSchema,
  languageModelMessagePartSchema,
  languageModelMessageSchema,
  protocolSchemas,
  structuredResponseSchema,
  StructuredOutputDiagnosticCategory,
  structuredOutputDiagnosticSchema,
  type ProtocolSchemaName,
} from '../src/index.ts';

const fixturesDirectory = fileURLToPath(new URL('../fixtures/', import.meta.url));

const fixtureSchemaNames = {
  'agent-action': 'agent-action',
  'agent-message': 'agent-message',
  'capability-descriptor': 'capability-descriptor',
  'capability-registry-response': 'capability-registry-response',
  'chat-completion-request': 'chat-completion-request',
  'chat-completion-response': 'chat-completion-response',
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
  test('keeps structured output diagnostics closed and content-free', () => {
    expect(structuredOutputDiagnosticSchema.parse({
      category: StructuredOutputDiagnosticCategory.FinishReason,
      finishReason: 'length',
    })).toEqual({
      category: StructuredOutputDiagnosticCategory.FinishReason,
      finishReason: ChatCompletionFinishReason.Length,
    });
    expect(structuredOutputDiagnosticSchema.safeParse({ category: 'provider_message' }).success).toBe(false);
    expect(structuredOutputDiagnosticSchema.safeParse({
      category: StructuredOutputDiagnosticCategory.SchemaValidation,
      finishReason: 'stop',
    }).success).toBe(false);
    expect(structuredOutputDiagnosticSchema.safeParse({
      category: StructuredOutputDiagnosticCategory.JSONParse,
      content: 'generated text',
    }).success).toBe(false);
  });

  test('rejects values outside canonical enums', () => {
    expect(languageModelMessageSchema.safeParse({ role: 'developer' }).success).toBe(false);
    expect(languageModelMessagePartSchema.safeParse({ type: 'audio' }).success).toBe(false);
    expect(chatCompletionMessageSchema.safeParse({ role: 'developer' }).success).toBe(false);
    expect(chatCompletionMessageSchema.safeParse({
      role: 'assistant',
      toolCalls: [{
        id: 'call-1',
        type: 'custom',
        function: { name: 'lookup', arguments: '{}' },
      }],
    }).success).toBe(false);
    expect(chatCompletionRequestSchema.safeParse({
      executionMode: 'invalid',
      messages: [],
      parallelToolCalls: false,
    }).success).toBe(false);
    expect(chatCompletionRequestSchema.safeParse({
      executionMode: 'auto',
      messages: [],
      parallelToolCalls: false,
      generationOptions: { seed: 41, temperature: 0.2, maxTokens: 256 },
    }).success).toBe(true);
    expect(chatCompletionRequestSchema.safeParse({
      executionMode: 'auto',
      messages: [],
      parallelToolCalls: false,
      generationOptions: { maxTokens: -1 },
    }).success).toBe(false);
    expect(chatCompletionResponseSchema.safeParse({
      finishReason: 'paused',
      provider: 'provider',
      model: 'model',
      message: { role: 'assistant', content: 'done' },
      selectedBackend: 'remote',
    }).success).toBe(false);
    expect(chatCompletionResponseSchema.safeParse({
      finishReason: 'stop',
      provider: '',
      model: 'model',
      message: { role: 'assistant', content: 'done' },
      selectedBackend: 'remote',
    }).success).toBe(false);
    expect(chatCompletionResponseSchema.safeParse({
      finishReason: 'stop',
      provider: 'provider',
      model: 'model',
      message: { role: 'assistant', content: 'done' },
      selectedBackend: 'companion',
    }).success).toBe(false);
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

  test('enforces chat completion response semantics', () => {
    const toolCall = {
      id: 'call-1',
      type: 'function',
      function: { name: 'lookup', arguments: '{"city":"Seoul"}' },
    };
    const responseDocument = {
      provider: 'openrouter',
      model: 'model',
      message: { role: 'assistant', content: 'done' },
      selectedBackend: 'remote',
    };
    for (const finishReason of ['stop', 'length', 'content_filter', 'error', 'other', 'unknown']) {
      expect(chatCompletionResponseSchema.safeParse({ ...responseDocument, finishReason }).success).toBe(true);
    }
    expect(chatCompletionResponseSchema.safeParse({
      ...responseDocument,
      finishReason: 'tool_calls',
      message: { ...responseDocument.message, content: '', toolCalls: [toolCall] },
    }).success).toBe(true);
    expect(chatCompletionResponseSchema.safeParse({
      ...responseDocument,
      message: { ...responseDocument.message, role: 'tool' },
    }).success).toBe(false);
    expect(chatCompletionResponseSchema.safeParse({
      ...responseDocument,
      finishReason: 'tool_calls',
    }).success).toBe(false);
    expect(chatCompletionResponseSchema.safeParse({
      ...responseDocument,
      message: {
        ...responseDocument.message,
        toolCalls: [{ ...toolCall, id: ' ' }],
      },
    }).success).toBe(false);
    expect(chatCompletionResponseSchema.safeParse({
      ...responseDocument,
      message: {
        ...responseDocument.message,
        toolCalls: [{ ...toolCall, function: { ...toolCall.function, name: '' } }],
      },
    }).success).toBe(false);
    expect(chatCompletionResponseSchema.safeParse({
      ...responseDocument,
      message: {
        ...responseDocument.message,
        toolCalls: [{ ...toolCall, function: { ...toolCall.function, arguments: '[]' } }],
      },
    }).success).toBe(false);
    expect(chatCompletionResponseSchema.safeParse({
      ...responseDocument,
      message: {
        ...responseDocument.message,
        toolCalls: [{ ...toolCall, function: { ...toolCall.function, arguments: '{invalid' } }],
      },
    }).success).toBe(false);
    expect(chatCompletionResponseSchema.safeParse({ ...responseDocument, finishReason: 'paused' }).success).toBe(false);
  });

  test('rejects chat identity collisions at the protocol boundary', () => {
    const requestDocument = {
      executionMode: 'auto',
      messages: [{ role: 'user', content: 'Use a tool.' }],
      parallelToolCalls: false,
    };
    const tool = {
      type: 'function',
      function: { name: 'lookup', parameters: { type: 'object' } },
    };
    expect(chatCompletionRequestSchema.safeParse({
      ...requestDocument,
      tools: [tool, { ...tool, function: { ...tool.function, name: ' lookup ' } }],
    }).success).toBe(false);
    expect(chatCompletionRequestSchema.safeParse({
      ...requestDocument,
      tools: [{ ...tool, function: { ...tool.function, name: ' ' } }],
    }).success).toBe(false);
    expect(chatCompletionRequestSchema.safeParse({
      ...requestDocument,
      messages: [
        { role: 'assistant', toolCalls: [{ id: 'call-1', type: 'function', function: { name: 'lookup', arguments: '{}' } }] },
        { role: 'assistant', toolCalls: [{ id: 'call-1', type: 'function', function: { name: 'other', arguments: '{}' } }] },
      ],
    }).success).toBe(false);
    expect(chatCompletionRequestSchema.safeParse({
      ...requestDocument,
      messages: [{ role: 'tool', toolCallId: ' ', content: 'result' }],
    }).success).toBe(false);

    expect(chatCompletionResponseSchema.safeParse({
      finishReason: 'tool_calls',
      provider: 'provider',
      model: 'model',
      message: {
        role: 'assistant',
        toolCalls: [
          { id: 'call-1', type: 'function', function: { name: 'lookup', arguments: '{}' } },
          { id: 'call-1', type: 'function', function: { name: 'other', arguments: '{}' } },
        ],
      },
      selectedBackend: 'remote',
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
