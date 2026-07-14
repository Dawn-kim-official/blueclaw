import { describe, expect, test } from 'bun:test';
import type { StructuredResponse, StructuredResponseRequest } from '@blueclaw/protocol';

import type { SDKDConfiguration } from '../src/configuration.ts';
import { SDKDError } from '../src/errors.ts';
import { createSDKDHandler } from '../src/handler.ts';

const configuration: SDKDConfiguration = {
  authKey: 'installation-key',
  autoRoute: 'remote-first',
  llamaAPIKey: 'local',
  llamaStructuredOutputsEnabled: false,
  localOnly: false,
  openRouterBaseURL: 'https://openrouter.ai/api/v1',
  requestTimeoutMillisecond: 60000,
  socketPath: '/tmp/blueclaw-sdkd-test.sock',
};

const requestDocument: StructuredResponseRequest = {
  executionMode: 'remote',
  model: 'deepseek/deepseek-v4-flash',
  messages: [{ role: 'user', content: 'Return ok.' }],
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
});

function structuredRequest(authKey?: string): Request {
  const headers: Record<string, string> = { 'content-type': 'application/json' };
  if (authKey) headers.authorization = `Bearer ${authKey}`;
  return new Request('http://sdkd/v1/llm/structured', {
    method: 'POST',
    headers,
    body: JSON.stringify(requestDocument),
  });
}

function responseDocument(): StructuredResponse {
  return {
    provider: 'openrouter',
    model: 'deepseek/deepseek-v4-flash',
    content: '{"ok":true}',
    selectedBackend: 'remote',
    finishReason: 'stop',
    constraintMode: 'openai_json_schema',
    usage: { promptTokens: 10, completionTokens: 5, totalTokens: 15 },
  };
}
