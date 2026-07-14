import { describe, expect, test } from 'bun:test';
import type { LanguageModelV3GenerateResult, LanguageModelV3Usage } from '@ai-sdk/provider';
import { APICallError, RetryError } from 'ai';
import type { StructuredResponseRequest } from '@blueclaw/protocol';
import { MockLanguageModelV3 } from 'ai/test';

import type { SDKDConfiguration } from '../src/configuration.ts';
import {
  createStructuredResponseGenerator,
  type ProviderLanguageModelFactory,
} from '../src/provider.ts';

const structuredRequest: StructuredResponseRequest = {
  executionMode: 'auto',
  model: 'remote-model',
  messages: [{ role: 'user', content: 'Return ok.' }],
  structuredOutputSchema: {
    name: 'provider_test_output',
    document: {
      type: 'object',
      properties: { ok: { type: 'boolean' } },
      required: ['ok'],
      additionalProperties: false,
    },
    isStrictlyEnforced: true,
  },
  generationOptions: {
    maxTokens: 128,
    seed: 7,
    temperature: 0,
  },
};

describe('sdkd provider adapter', () => {
  test('selects the requested device route and normalizes structured output and usage', async () => {
    const llamaModel = successfulLanguageModel('served-local-model', { ok: true }, {
      inputTokens: { total: 12.9, noCache: 8, cacheRead: 4.8, cacheWrite: -2 },
      outputTokens: { total: 5.7, text: 4, reasoning: 1.9 },
    });
    const remoteModel = successfulLanguageModel('unused-remote-model', { ok: false });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration('remote-first'),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateStructuredResponse({ ...structuredRequest, executionMode: 'device' });

    expect(response).toEqual({
      provider: 'llama.cpp',
      model: 'served-local-model',
      content: '{"ok":true}',
      selectedBackend: 'device',
      finishReason: 'stop',
      constraintMode: 'llama_json_schema',
      usage: {
        promptTokens: 12,
        completionTokens: 5,
        totalTokens: 18,
        cachedPromptTokens: 4,
        cacheWriteTokens: 0,
        reasoningTokens: 1,
      },
    });
    expect(llamaModel.doGenerateCalls).toHaveLength(1);
    expect(remoteModel.doGenerateCalls).toHaveLength(0);
    expect(llamaModel.doGenerateCalls[0]?.maxOutputTokens).toBe(128);
    expect(llamaModel.doGenerateCalls[0]?.seed).toBe(7);
    expect(llamaModel.doGenerateCalls[0]?.temperature).toBe(0);
  });

  test('honors auto route order and falls back after a retryable provider failure', async () => {
    const routeAttempts: string[] = [];
    const llamaModel = apiFailingLanguageModel('llama.cpp', true, routeAttempts);
    const remoteModel = successfulLanguageModel('served-remote-model', { ok: true }, undefined, () => {
      routeAttempts.push('openrouter');
    });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration('local-first'),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateStructuredResponse(structuredRequest);

    expect(routeAttempts).toEqual(['llama.cpp', 'openrouter']);
    expect(response.provider).toBe('openrouter');
    expect(response.selectedBackend).toBe('remote');
    expect(response.constraintMode).toBe('openai_json_schema');
  });

  test('falls back only after retryable provider failures', async () => {
    const routeAttempts: string[] = [];
    const llamaModel = apiFailingLanguageModel('llama.cpp', true, routeAttempts);
    const remoteModel = successfulLanguageModel('served-remote-model', { ok: true }, undefined, () => {
      routeAttempts.push('openrouter');
    });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration('local-first'),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateStructuredResponse(structuredRequest);

    expect(routeAttempts).toEqual(['llama.cpp', 'openrouter']);
    expect(response.provider).toBe('openrouter');
  });

  test('falls back when RetryError wraps a retryable provider failure', async () => {
    const routeAttempts: string[] = [];
    const llamaModel = retryFailingLanguageModel('llama.cpp', true, routeAttempts);
    const remoteModel = successfulLanguageModel('served-remote-model', { ok: true }, undefined, () => {
      routeAttempts.push('openrouter');
    });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration('local-first'),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateStructuredResponse(structuredRequest);

    expect(routeAttempts).toEqual(['llama.cpp', 'openrouter']);
    expect(response.provider).toBe('openrouter');
  });

  test('does not fall back when RetryError wraps a non-retryable provider failure', async () => {
    const routeAttempts: string[] = [];
    const llamaModel = retryFailingLanguageModel('llama.cpp', false, routeAttempts);
    const remoteModel = successfulLanguageModel('unused-remote-model', { ok: true });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration('local-first'),
      languageModelFactory(llamaModel, remoteModel),
    );

    await expect(generateStructuredResponse(structuredRequest)).rejects.toThrow('provider request failed');
    expect(routeAttempts).toEqual(['llama.cpp']);
    expect(remoteModel.doGenerateCalls).toHaveLength(0);
  });

  test('does not fall back after non-retryable provider failures', async () => {
    const routeAttempts: string[] = [];
    const llamaModel = apiFailingLanguageModel('llama.cpp', false, routeAttempts);
    const remoteModel = successfulLanguageModel('unused-remote-model', { ok: true });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration('local-first'),
      languageModelFactory(llamaModel, remoteModel),
    );

    await expect(generateStructuredResponse(structuredRequest)).rejects.toThrow('provider request failed');
    expect(routeAttempts).toEqual(['llama.cpp']);
    expect(remoteModel.doGenerateCalls).toHaveLength(0);
  });

  test('does not route or allow legacy fallback for a non-retryable 500 response', async () => {
    const routeAttempts: string[] = [];
    const llamaModel = apiFailingLanguageModel('llama.cpp', false, routeAttempts, 500);
    const remoteModel = successfulLanguageModel('unused-remote-model', { ok: true });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration('local-first'),
      languageModelFactory(llamaModel, remoteModel),
    );

    try {
      await generateStructuredResponse(structuredRequest);
      throw new Error('expected provider failure');
    } catch (errorValue) {
      expect(errorValue).toMatchObject({
        code: 'provider_response_invalid',
        status: 502,
        allowLegacyFallback: false,
      });
    }
    expect(routeAttempts).toEqual(['llama.cpp']);
    expect(remoteModel.doGenerateCalls).toHaveLength(0);
  });

  test('stops after the first successful auto route', async () => {
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: false });
    const remoteModel = successfulLanguageModel('served-remote-model', { ok: true });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration('remote-first'),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateStructuredResponse(structuredRequest);

    expect(response.provider).toBe('openrouter');
    expect(remoteModel.doGenerateCalls).toHaveLength(1);
    expect(llamaModel.doGenerateCalls).toHaveLength(0);
  });

  test('keeps auto and remote routing local in local-only mode', async () => {
    const llamaModel = successfulLanguageModel('local-model', { ok: true });
    const remoteModel = successfulLanguageModel('remote-model', { ok: true });
    const configuration = { ...completeConfiguration('remote-first'), localOnly: true };
    const generateStructuredResponse = createStructuredResponseGenerator(
      configuration,
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateStructuredResponse(structuredRequest);

    expect(response.selectedBackend).toBe('device');
    expect(remoteModel.doGenerateCalls).toHaveLength(0);
    await expect(generateStructuredResponse({ ...structuredRequest, executionMode: 'remote' })).rejects.toThrow(
      'remote routing is disabled by local-only mode',
    );
  });

  test('rejects provider output that violates the requested schema', async () => {
    const invalidModel = successfulLanguageModel('invalid-model', { ok: 'not-a-boolean' });
    const fallbackModel = successfulLanguageModel('fallback-model', { ok: true });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration('local-first'),
      languageModelFactory(invalidModel, fallbackModel),
    );

    await expect(generateStructuredResponse(structuredRequest)).rejects.toThrow();
    expect(fallbackModel.doGenerateCalls).toHaveLength(0);
  });

  test('returns the last provider failure after exhausting fallback routes', async () => {
    const routeAttempts: string[] = [];
    const llamaModel = apiFailingLanguageModel('llama.cpp', true, routeAttempts);
    const remoteModel = apiFailingLanguageModel('openrouter', true, routeAttempts);
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration('local-first'),
      languageModelFactory(llamaModel, remoteModel),
    );

    await expect(generateStructuredResponse(structuredRequest)).rejects.toThrow('provider request failed');
    expect(routeAttempts).toEqual(['llama.cpp', 'openrouter']);
  });

  test('fails before provider execution when the requested route is unavailable', async () => {
    const model = successfulLanguageModel('unused-model', { ok: true });
    const modelFactory = languageModelFactory(model, model);
    const configuration = completeConfiguration('remote-first');
    const noRouteConfiguration = { ...configuration, llamaBaseURL: undefined, llamaModel: undefined, openRouterAPIKey: undefined };
    const generateStructuredResponse = createStructuredResponseGenerator(noRouteConfiguration, modelFactory);

    await expect(generateStructuredResponse(structuredRequest)).rejects.toThrow(
      'auto routing requires an OpenRouter or llama.cpp configuration',
    );
    await expect(generateStructuredResponse({ ...structuredRequest, executionMode: 'companion' })).rejects.toThrow(
      'companion language model routing is provided by InternKim',
    );
    await expect(generateStructuredResponse({ ...structuredRequest, executionMode: 'remote' })).rejects.toThrow(
      'remote routing requires OPENROUTER_API_KEY',
    );
    expect(model.doGenerateCalls).toHaveLength(0);
  });
});

function completeConfiguration(autoRoute: SDKDConfiguration['autoRoute']): SDKDConfiguration {
  return {
    authKey: 'installation-key',
    autoRoute,
    llamaAPIKey: 'local-key',
    llamaBaseURL: 'http://127.0.0.1:8080/v1',
    llamaModel: 'local-model',
    llamaStructuredOutputsEnabled: true,
    localOnly: false,
    openRouterAPIKey: 'remote-key',
    openRouterBaseURL: 'https://openrouter.invalid/api/v1',
    requestTimeoutMillisecond: 1000,
    socketPath: '/tmp/blueclaw-sdkd-provider-test.sock',
  };
}

function languageModelFactory(
  llamaModel: MockLanguageModelV3,
  openRouterModel: MockLanguageModelV3,
): ProviderLanguageModelFactory {
  return {
    createLlamaLanguageModel: () => llamaModel,
    createOpenRouterLanguageModel: () => openRouterModel,
  };
}

function successfulLanguageModel(
  modelID: string,
  output: unknown,
  usage: LanguageModelV3Usage = defaultUsage(),
  onGenerate: () => void = () => {},
): MockLanguageModelV3 {
  return new MockLanguageModelV3({
    modelId: modelID,
    doGenerate: async () => {
      onGenerate();
      return successfulGeneration(modelID, output, usage);
    },
  });
}

function retryFailingLanguageModel(
  routeName: string,
  isRetryable: boolean,
  routeAttempts: string[],
): MockLanguageModelV3 {
  const apiCallError = providerAPICallError(isRetryable);
  return new MockLanguageModelV3({
    doGenerate: async () => {
      routeAttempts.push(routeName);
      throw new RetryError({
        message: 'provider retries failed',
        reason: isRetryable ? 'maxRetriesExceeded' : 'errorNotRetryable',
        errors: [apiCallError],
      });
    },
  });
}

function apiFailingLanguageModel(
  routeName: string,
  isRetryable: boolean,
  routeAttempts: string[],
  statusCode?: number,
): MockLanguageModelV3 {
  return new MockLanguageModelV3({
    doGenerate: async () => {
      routeAttempts.push(routeName);
      throw providerAPICallError(isRetryable, statusCode);
    },
  });
}

function providerAPICallError(isRetryable: boolean, statusCode?: number): APICallError {
  return new APICallError({
    message: 'provider request failed',
    url: 'https://provider.invalid',
    requestBodyValues: {},
    isRetryable,
    statusCode,
  });
}

function successfulGeneration(
  modelID: string,
  output: unknown,
  usage: LanguageModelV3Usage,
): LanguageModelV3GenerateResult {
  return {
    content: [{ type: 'text', text: JSON.stringify(output) }],
    finishReason: { unified: 'stop', raw: 'stop' },
    usage,
    response: { modelId: modelID },
    warnings: [],
  };
}

function defaultUsage(): LanguageModelV3Usage {
  return {
    inputTokens: { total: 10, noCache: 10, cacheRead: undefined, cacheWrite: undefined },
    outputTokens: { total: 5, text: 5, reasoning: undefined },
  };
}
