import { describe, expect, test } from 'bun:test';
import {
  ChatCompletionFinishReason,
  ChatCompletionMessageRole,
  ExecutionMode,
  LanguageModelBackend,
  LanguageModelMessageRole,
  StructuredOutputDiagnosticCategory,
  StructuredOutputConstraintMode,
  type ChatCompletionRequest,
  type ChatCompletionResponse,
  type StructuredResponse,
  type StructuredResponseRequest,
} from '@blueclaw/protocol';

import { SDKDAutoRoute, type SDKDConfiguration } from '../src/configuration.ts';
import { SDKDError } from '../src/errors.ts';
import { createSDKDHandler } from '../src/handler.ts';

const configuration: SDKDConfiguration = {
  authKey: 'installation-key',
  autoRoute: SDKDAutoRoute.RemoteFirst,
  llamaAPIKey: 'local',
  llamaStructuredOutputsEnabled: false,
  localOnly: false,
  openRouterBaseURL: 'https://openrouter.ai/api/v1',
  socketPath: '/tmp/blueclaw-sdkd-test.sock',
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

describe('sdkd handler', () => {
  test('reports protocol health without provider credentials', async () => {
    const handler = createSDKDHandler({ configuration, generateStructuredResponse: async () => responseDocument() });
    const response = await handler(new Request('http://sdkd/health'));
    const body = await response.json();

    expect(response.status).toBe(200);
    expect(body.status).toBe('ok');
    expect(body.aggregateProtocolHash).toMatch(/^[a-f0-9]{64}$/);
  });

  test('requires the installation key', async () => {
    const handler = createSDKDHandler({ configuration, generateStructuredResponse: async () => responseDocument() });
    const response = await handler(structuredRequest());

    expect(response.status).toBe(401);
    expect(await response.json()).toEqual({
      error: { code: 'unauthorized', allowLegacyFallback: false },
    });
  });

  test('validates ingress and egress contracts', async () => {
    let observedRequest: StructuredResponseRequest | undefined;
    const handler = createSDKDHandler({
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
    const handler = createSDKDHandler({
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
    const handler = createSDKDHandler({
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
    const retryableHandler = createSDKDHandler({
      configuration,
      generateStructuredResponse: async () => {
        throw new SDKDError('provider_unavailable', 503, true, 'unavailable');
      },
    });
    const retryableResponse = await retryableHandler(structuredRequest('installation-key'));

    expect(retryableResponse.status).toBe(503);
    expect(await retryableResponse.json()).toEqual({
      error: { code: 'provider_unavailable', allowLegacyFallback: true },
    });

    const contractHandler = createSDKDHandler({
      configuration,
      generateStructuredResponse: async () => {
        throw new SDKDError('structured_output_invalid', 422, false, 'invalid');
      },
    });
    const contractResponse = await contractHandler(structuredRequest('installation-key'));

    expect(contractResponse.status).toBe(422);
    expect(await contractResponse.json()).toEqual({
      error: { code: 'structured_output_invalid', allowLegacyFallback: false },
    });
  });

  test('returns only closed structured output diagnostics', async () => {
    const handler = createSDKDHandler({
      configuration,
      generateStructuredResponse: async () => {
        throw new SDKDError(
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
      new SDKDError('structured_output_invalid', 422, false, 'schema invalid'),
      new SDKDError('policy_remote_disabled', 403, false, 'remote disabled'),
      new SDKDError('configuration_invalid', 400, false, 'configuration invalid'),
      new SDKDError('request_invalid', 400, false, 'request invalid'),
    ];

    for (const expectedError of testCases) {
      const handler = createSDKDHandler({
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
    const handler = createSDKDHandler({
      configuration,
      generateStructuredResponse: async () => {
        callCount += 1;
        return responseDocument();
      },
    });
    const response = await handler(new Request('http://sdkd/v1/llm/structured', {
      method: 'POST',
      headers: { authorization: 'Bearer installation-key', 'content-type': 'application/json' },
      body: JSON.stringify({ executionMode: 'remote' }),
    }));

    expect(response.status).toBe(400);
    expect(callCount).toBe(0);
  });

  test('validates chat ingress and egress contracts', async () => {
    let observedRequest: ChatCompletionRequest | undefined;
    const handler = createSDKDHandler({
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
    const handler = createSDKDHandler({
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
    const handler = createSDKDHandler({
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

  test('rejects malformed chat requests before provider execution', async () => {
    let callCount = 0;
    const handler = createSDKDHandler({
      configuration,
      generateStructuredResponse: async () => responseDocument(),
      generateChatCompletion: async () => {
        callCount += 1;
        return chatResponseDocument();
      },
    });
    const response = await handler(new Request('http://sdkd/v1/llm/chat', {
      method: 'POST',
      headers: { authorization: 'Bearer installation-key', 'content-type': 'application/json' },
      body: JSON.stringify({ executionMode: 'remote', messages: [] }),
    }));

    expect(response.status).toBe(400);
    expect(callCount).toBe(0);
  });

  test('rejects chat identity collisions before provider execution', async () => {
    let callCount = 0;
    const handler = createSDKDHandler({
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
      const response = await handler(new Request('http://sdkd/v1/llm/chat', {
        method: 'POST',
        headers: { authorization: 'Bearer installation-key', 'content-type': 'application/json' },
        body: JSON.stringify(requestDocument),
      }));
      expect(response.status).toBe(400);
    }
    expect(callCount).toBe(0);
  });

  test('rejects duplicate response tool call IDs at the egress boundary', async () => {
    const handler = createSDKDHandler({
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
  return new Request('http://sdkd/v1/llm/structured', {
    method: 'POST',
    headers,
    body: JSON.stringify(requestDocument),
    signal,
  });
}

function chatRequest(authKey?: string, signal?: AbortSignal): Request {
  const headers: Record<string, string> = { 'content-type': 'application/json' };
  if (authKey) headers.authorization = `Bearer ${authKey}`;
  return new Request('http://sdkd/v1/llm/chat', {
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
