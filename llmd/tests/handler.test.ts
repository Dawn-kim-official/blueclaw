import { describe, expect, test } from 'bun:test';
import {
  ChatCompletionFinishReason,
  ChatCompletionMessageRole,
  ExecutionMode,
  LanguageModelBackend,
  LanguageModelMessageRole,
  StructuredOutputDiagnosticCategory,
  StructuredOutputRepairStatus,
  StructuredOutputConstraintMode,
  StructuredOutputValidationCode,
  protocolIdentitySchema,
  protocolVersion,
  type ChatCompletionRequest,
  type ChatCompletionResponse,
  type StructuredResponse,
  type StructuredResponseRequest,
} from '@blueclaw/protocol';
import { buildProtocolArtifacts } from '@blueclaw/protocol/artifacts';

import { LLMDAutoRoute, type LLMDConfiguration } from '../src/configuration.ts';
import { LLMDError } from '../src/errors.ts';
import { createLLMDHandler } from '../src/handler.ts';

const configuration: LLMDConfiguration = {
  authKey: 'installation-key',
  autoRoute: LLMDAutoRoute.RemoteFirst,
  llamaAPIKey: 'local',
  llamaStructuredOutputsEnabled: false,
  localOnly: false,
  openRouterBaseURL: 'https://openrouter.ai/api/v1',
  socketPath: '/tmp/blueclaw-llmd-test.sock',
};

const requestDocument: StructuredResponseRequest = {
  executionMode: ExecutionMode.Remote,
  model: 'deepseek/deepseek-v4-flash',
  messages: [{ role: LanguageModelMessageRole.User, content: 'Return ok.' }],
  structuredOutputSchema: {
    name: 'test_output',
    document: {
      type: 'object',
      properties: { ok: { type: 'boolean' } },
      required: ['ok'],
      additionalProperties: false,
    },
    isStrictlyEnforced: true,
  },
};

const chatRequestDocument: ChatCompletionRequest = {
  executionMode: ExecutionMode.Remote,
  model: 'deepseek/deepseek-v4-flash',
  messages: [
    { role: ChatCompletionMessageRole.System, content: 'You are concise.' },
    { role: ChatCompletionMessageRole.User, content: 'Look up the answer.' },
  ],
  tools: [{
    type: 'function',
    function: {
      name: 'lookup',
      description: 'Look up a value.',
      parameters: { type: 'object', properties: { key: { type: 'string' } } },
    },
  }],
  toolChoice: { type: 'function', function: { name: 'lookup' } },
  parallelToolCalls: false,
};

describe('llmd handler', () => {
  test('reports protocol health without provider credentials', async () => {
    const handler = createLLMDHandler({ configuration, generateStructuredResponse: async () => responseDocument() });
    const response = await handler(new Request('http://llmd/health'));
    const body = await response.json();
    const protocolManifest = buildProtocolArtifacts().manifest;

    expect(response.status).toBe(200);
    expect(body).toEqual({
      ...protocolIdentitySchema.parse({
        protocolVersion: body.protocolVersion,
        aggregateProtocolHash: body.aggregateProtocolHash,
      }),
      status: 'ok',
    });
    expect(body).toEqual({
      protocolVersion,
      aggregateProtocolHash: protocolManifest.aggregateHash,
      status: 'ok',
    });
  });

  test('requires the installation key', async () => {
    const handler = createLLMDHandler({ configuration, generateStructuredResponse: async () => responseDocument() });
    const response = await handler(structuredRequest());

    expect(response.status).toBe(401);
    expect(await response.json()).toEqual({
      error: { code: 'unauthorized', allowLegacyFallback: false },
    });
  });

  test('validates ingress and egress contracts', async () => {
    let observedRequest: StructuredResponseRequest | undefined;
    const handler = createLLMDHandler({
      configuration,
      generateStructuredResponse: async request => {
        observedRequest = request;
        return responseDocument();
      },
    });
    const response = await handler(structuredRequest('installation-key'));

    expect(response.status).toBe(200);
    expect(observedRequest).toEqual(requestDocument);
    expect(await response.json()).toEqual(responseDocument());
  });

  test('passes the structured request abort signal to the generator', async () => {
    const abortController = new AbortController();
    let observedAbortSignal: AbortSignal | undefined;
    const handler = createLLMDHandler({
      configuration,
      generateStructuredResponse: async (_request, abortSignal) => {
        observedAbortSignal = abortSignal;
        return responseDocument();
      },
    });

    const response = await handler(structuredRequest('installation-key', abortController.signal));

    expect(response.status).toBe(200);
    expect(observedAbortSignal).toBe(abortController.signal);
  });

  test('classifies aborted structured generation without allowing fallback', async () => {
    const handler = createLLMDHandler({
      configuration,
      generateStructuredResponse: async () => {
        throw new DOMException('The operation was aborted', 'AbortError');
      },
    });

    const response = await handler(structuredRequest('installation-key'));

    expect(response.status).toBe(499);
    expect(await response.json()).toEqual({
      error: { code: 'request_aborted', allowLegacyFallback: false },
    });
  });

  test('preserves fallback policy in provider errors', async () => {
    const retryableHandler = createLLMDHandler({
      configuration,
      generateStructuredResponse: async () => {
        throw new LLMDError('provider_unavailable', 503, true, 'unavailable');
      },
    });
    const retryableResponse = await retryableHandler(structuredRequest('installation-key'));

    expect(retryableResponse.status).toBe(503);
    expect(await retryableResponse.json()).toEqual({
      error: { code: 'provider_unavailable', allowLegacyFallback: true },
    });

    const contractHandler = createLLMDHandler({
      configuration,
      generateStructuredResponse: async () => {
        throw new LLMDError('structured_output_invalid', 422, false, 'invalid');
      },
    });
    const contractResponse = await contractHandler(structuredRequest('installation-key'));

    expect(contractResponse.status).toBe(422);
    expect(await contractResponse.json()).toEqual({
      error: { code: 'structured_output_invalid', allowLegacyFallback: false },
    });
  });

  test('returns only closed structured output diagnostics', async () => {
    const handler = createLLMDHandler({
      configuration,
      generateStructuredResponse: async () => {
        throw new LLMDError(
          'structured_output_invalid',
          422,
          false,
          'generated content and provider details',
          { category: StructuredOutputDiagnosticCategory.FinishReason, finishReason: ChatCompletionFinishReason.Length },
        );
      },
    });

    const response = await handler(structuredRequest('installation-key'));

    expect(response.status).toBe(422);
    expect(await response.json()).toEqual({
      error: {
        code: 'structured_output_invalid',
        allowLegacyFallback: false,
        diagnostic: { category: 'finish_reason', finishReason: 'length' },
      },
    });
  });

  test('keeps contract and policy failures closed', async () => {
    const testCases = [
      new LLMDError('structured_output_invalid', 422, false, 'schema invalid'),
      new LLMDError('policy_remote_disabled', 403, false, 'remote disabled'),
      new LLMDError('configuration_invalid', 400, false, 'configuration invalid'),
      new LLMDError('request_invalid', 400, false, 'request invalid'),
    ];

    for (const expectedError of testCases) {
      const handler = createLLMDHandler({
        configuration,
        generateStructuredResponse: async () => {
          throw expectedError;
        },
      });
      const response = await handler(structuredRequest('installation-key'));

      expect(response.status).toBe(expectedError.status);
      expect(await response.json()).toEqual({
        error: { code: expectedError.code, allowLegacyFallback: false },
      });
    }
  });

  test('rejects malformed protocol requests before provider execution', async () => {
    let callCount = 0;
    const handler = createLLMDHandler({
      configuration,
      generateStructuredResponse: async () => {
        callCount += 1;
        return responseDocument();
      },
    });
    const response = await handler(new Request('http://llmd/v1/llm/structured', {
      method: 'POST',
      headers: { authorization: 'Bearer installation-key', 'content-type': 'application/json' },
      body: JSON.stringify({ executionMode: 'remote' }),
    }));

    expect(response.status).toBe(400);
    expect(callCount).toBe(0);
  });

  test('rejects non-canonical structured output boundaries before provider execution', async () => {
    let callCount = 0;
    const handler = createLLMDHandler({
      configuration,
      generateStructuredResponse: async () => {
        callCount += 1;
        return responseDocument();
      },
    });
    const invalidDocuments = [
      {
        ...requestDocument,
        structuredOutputSchema: { ...requestDocument.structuredOutputSchema, name: 'structured result' },
      },
      {
        ...requestDocument,
        structuredOutputSchema: { ...requestDocument.structuredOutputSchema, isStrictlyEnforced: false },
      },
      {
        ...requestDocument,
        structuredOutputSchema: {
          ...requestDocument.structuredOutputSchema,
          document: {
            type: 'object',
            properties: { nested: { type: 'object', properties: {} } },
            additionalProperties: false,
          },
        },
      },
    ];

    for (const document of invalidDocuments) {
      const response = await handler(new Request('http://llmd/v1/llm/structured', {
        method: 'POST',
        headers: { authorization: 'Bearer installation-key', 'content-type': 'application/json' },
        body: JSON.stringify(document),
      }));
      expect(response.status).toBe(400);
      expect(await response.json()).toEqual({
        error: { code: 'invalid_structured_response_request', allowLegacyFallback: false },
      });
    }
    expect(callCount).toBe(0);
  });

  test('validates chat ingress and egress contracts', async () => {
    let observedRequest: ChatCompletionRequest | undefined;
    const handler = createLLMDHandler({
      configuration,
      generateStructuredResponse: async () => responseDocument(),
      generateChatCompletion: async request => {
        observedRequest = request;
        return chatResponseDocument();
      },
    });
    const response = await handler(chatRequest('installation-key'));

    expect(response.status).toBe(200);
    expect(observedRequest).toEqual(chatRequestDocument);
    expect(await response.json()).toEqual(chatResponseDocument());
  });

  test('passes the chat request abort signal to the generator', async () => {
    const abortController = new AbortController();
    let observedAbortSignal: AbortSignal | undefined;
    const handler = createLLMDHandler({
      configuration,
      generateStructuredResponse: async () => responseDocument(),
      generateChatCompletion: async (_request, abortSignal) => {
        observedAbortSignal = abortSignal;
        return chatResponseDocument();
      },
    });

    const response = await handler(chatRequest('installation-key', abortController.signal));

    expect(response.status).toBe(200);
    expect(observedAbortSignal).toBe(abortController.signal);
  });

  test('classifies aborted chat generation without allowing fallback', async () => {
    const handler = createLLMDHandler({
      configuration,
      generateStructuredResponse: async () => responseDocument(),
      generateChatCompletion: async () => {
        throw new DOMException('The operation was aborted', 'AbortError');
      },
    });

    const response = await handler(chatRequest('installation-key'));

    expect(response.status).toBe(499);
    expect(await response.json()).toEqual({
      error: { code: 'request_aborted', allowLegacyFallback: false },
    });
  });

  test('returns safe diagnostics for schema-invalid chat tool arguments', async () => {
    const handler = createLLMDHandler({
      configuration,
      generateStructuredResponse: async () => responseDocument(),
      generateChatCompletion: async () => {
        throw new LLMDError(
          'provider_response_invalid',
          502,
          false,
          'provider returned schema-invalid tool arguments',
          {
            category: StructuredOutputDiagnosticCategory.SchemaValidation,
            toolName: 'task.add',
            validationIssues: [
              { fieldPath: '/prompt', code: StructuredOutputValidationCode.Required },
              { fieldPath: '/', code: StructuredOutputValidationCode.AdditionalProperty },
            ],
            repairStatus: StructuredOutputRepairStatus.Failed,
          },
        );
      },
    });

    const response = await handler(chatRequest('installation-key'));

    expect(response.status).toBe(502);
    expect(await response.json()).toEqual({
      error: {
        code: 'provider_response_invalid',
        allowLegacyFallback: false,
        diagnostic: {
          category: StructuredOutputDiagnosticCategory.SchemaValidation,
          toolName: 'task.add',
          validationIssues: [
            { fieldPath: '/prompt', code: StructuredOutputValidationCode.Required },
            { fieldPath: '/', code: StructuredOutputValidationCode.AdditionalProperty },
          ],
          repairStatus: StructuredOutputRepairStatus.Failed,
        },
      },
    });
  });

  test('keeps chat tool choice contract failures closed', async () => {
    const handler = createLLMDHandler({
      configuration,
      generateStructuredResponse: async () => responseDocument(),
      generateChatCompletion: async () => {
        throw new LLMDError(
          'structured_output_invalid',
          422,
          false,
          'chat generation did not satisfy required tool choice',
          {
            category: StructuredOutputDiagnosticCategory.FinishReason,
            finishReason: ChatCompletionFinishReason.Stop,
          },
        );
      },
    });

    const response = await handler(chatRequest('installation-key'));

    expect(response.status).toBe(422);
    expect(await response.json()).toEqual({
      error: {
        code: 'structured_output_invalid',
        allowLegacyFallback: false,
        diagnostic: {
          category: StructuredOutputDiagnosticCategory.FinishReason,
          finishReason: ChatCompletionFinishReason.Stop,
        },
      },
    });
  });

  test('rejects malformed chat requests before provider execution', async () => {
    let callCount = 0;
    const handler = createLLMDHandler({
      configuration,
      generateStructuredResponse: async () => responseDocument(),
      generateChatCompletion: async () => {
        callCount += 1;
        return chatResponseDocument();
      },
    });
    const response = await handler(new Request('http://llmd/v1/llm/chat', {
      method: 'POST',
      headers: { authorization: 'Bearer installation-key', 'content-type': 'application/json' },
      body: JSON.stringify({ executionMode: 'remote', messages: [] }),
    }));

    expect(response.status).toBe(400);
    expect(callCount).toBe(0);
  });

  test('rejects chat identity collisions before provider execution', async () => {
    let callCount = 0;
    const handler = createLLMDHandler({
      configuration,
      generateStructuredResponse: async () => responseDocument(),
      generateChatCompletion: async () => {
        callCount += 1;
        return chatResponseDocument();
      },
    });
    const duplicateToolNameRequest = {
      ...chatRequestDocument,
      tools: [
        {
          type: 'function' as const,
          function: { name: 'lookup', parameters: { type: 'object' } },
        },
        {
          type: 'function' as const,
          function: { name: ' lookup ', parameters: { type: 'object' } },
        },
      ],
    };
    const duplicateToolCallIDRequest = {
      ...chatRequestDocument,
      messages: [
        { role: 'assistant' as const, toolCalls: [{ id: 'call-1', type: 'function' as const, function: { name: 'lookup', arguments: '{}' } }] },
        { role: 'assistant' as const, toolCalls: [{ id: 'call-1', type: 'function' as const, function: { name: 'other', arguments: '{}' } }] },
      ],
    };

    for (const requestDocument of [duplicateToolNameRequest, duplicateToolCallIDRequest]) {
      const response = await handler(new Request('http://llmd/v1/llm/chat', {
        method: 'POST',
        headers: { authorization: 'Bearer installation-key', 'content-type': 'application/json' },
        body: JSON.stringify(requestDocument),
      }));
      expect(response.status).toBe(400);
    }
    expect(callCount).toBe(0);
  });

  test('rejects duplicate response tool call IDs at the egress boundary', async () => {
    const handler = createLLMDHandler({
      configuration,
      generateStructuredResponse: async () => responseDocument(),
      generateChatCompletion: async () => ({
        ...chatResponseDocument(),
        finishReason: ChatCompletionFinishReason.ToolCalls,
        message: {
          role: 'assistant' as const,
          content: '',
          toolCalls: [
            { id: 'call-1', type: 'function' as const, function: { name: 'lookup', arguments: '{}' } },
            { id: 'call-1', type: 'function' as const, function: { name: 'other', arguments: '{}' } },
          ],
        },
      }),
    });

    const response = await handler(chatRequest('installation-key'));

    expect(response.status).toBe(502);
  });
});

function structuredRequest(authKey?: string, signal?: AbortSignal): Request {
  const headers: Record<string, string> = { 'content-type': 'application/json' };
  if (authKey) headers.authorization = `Bearer ${authKey}`;
  return new Request('http://llmd/v1/llm/structured', {
    method: 'POST',
    headers,
    body: JSON.stringify(requestDocument),
    signal,
  });
}

function chatRequest(authKey?: string, signal?: AbortSignal): Request {
  const headers: Record<string, string> = { 'content-type': 'application/json' };
  if (authKey) headers.authorization = `Bearer ${authKey}`;
  return new Request('http://llmd/v1/llm/chat', {
    method: 'POST',
    headers,
    body: JSON.stringify(chatRequestDocument),
    signal,
  });
}

function responseDocument(): StructuredResponse {
  return {
    provider: 'openrouter',
    model: 'deepseek/deepseek-v4-flash',
    content: '{"ok":true}',
    selectedBackend: LanguageModelBackend.Remote,
    finishReason: 'stop',
    constraintMode: StructuredOutputConstraintMode.OpenAIJSONSchema,
    usage: { promptTokens: 10, completionTokens: 5, totalTokens: 15 },
  };
}

function chatResponseDocument(): ChatCompletionResponse {
  return {
    provider: 'openrouter',
    model: 'deepseek/deepseek-v4-flash',
    message: { role: 'assistant', content: 'The answer is ready.', toolCalls: [] },
    selectedBackend: LanguageModelBackend.Remote,
    finishReason: ChatCompletionFinishReason.Stop,
    usage: { promptTokens: 10, completionTokens: 5, totalTokens: 15 },
    providerMetadata: { route: 'remote' },
  };
}
