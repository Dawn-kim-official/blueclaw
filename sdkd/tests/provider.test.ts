import { describe, expect, test } from 'bun:test';
import { createOpenAICompatible } from '@ai-sdk/openai-compatible';
import type { LanguageModelV3GenerateResult, LanguageModelV3Usage } from '@ai-sdk/provider';
import { APICallError, RetryError } from 'ai';
import { createOpenRouter } from '@openrouter/ai-sdk-provider';
import {
  ChatCompletionFinishReason,
  ChatCompletionMessageRole,
  ExecutionMode,
  LanguageModelBackend,
  LanguageModelMessageRole,
  StructuredOutputConstraintMode,
  type ChatCompletionRequest,
  type StructuredResponseRequest,
} from '@blueclaw/protocol';
import { MockLanguageModelV3 } from 'ai/test';

import { SDKDAutoRoute, type SDKDConfiguration } from '../src/configuration.ts';
import {
  createChatCompletionGenerator,
  createStructuredResponseGenerator,
  type ProviderLanguageModelFactory,
} from '../src/provider.ts';

const structuredRequest: StructuredResponseRequest = {
  executionMode: ExecutionMode.Auto,
  model: 'remote-model',
  messages: [{ role: LanguageModelMessageRole.User, content: 'Return ok.' }],
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

const chatRequest: ChatCompletionRequest = {
  executionMode: ExecutionMode.Auto,
  model: 'remote-model',
  messages: [
    { role: ChatCompletionMessageRole.System, content: 'You are concise.' },
    { role: ChatCompletionMessageRole.User, content: 'Look up the answer.' },
    {
      role: ChatCompletionMessageRole.Assistant,
      toolCalls: [{ id: 'call-1', type: 'function', function: { name: 'lookup', arguments: '{"key":"value"}' } }],
    },
    { role: ChatCompletionMessageRole.Tool, toolCallId: 'call-1', content: '{"answer":42}' },
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

describe('sdkd provider adapter', () => {
  test('generates chat completions with native tools and provider metadata', async () => {
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = chatLanguageModel('served-remote-model');
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(SDKDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateChatCompletion(chatRequest);
    const call = remoteModel.doGenerateCalls[0];

    expect(response).toEqual({
      provider: 'openrouter',
      model: 'served-remote-model',
      message: {
        role: ChatCompletionMessageRole.Assistant,
        content: '',
        toolCalls: [{
          id: 'call-2',
          type: 'function',
          function: { name: 'lookup', arguments: '{"key":"result"}' },
        }],
      },
      selectedBackend: LanguageModelBackend.Remote,
      finishReason: ChatCompletionFinishReason.ToolCalls,
      usage: { promptTokens: 10, completionTokens: 5, totalTokens: 15 },
      providerMetadata: { openrouter: { trace: 'test' } },
    });
    expect(call?.tools?.map(tool => tool.name)).toEqual(['lookup']);
    expect(call?.toolChoice).toEqual({ type: 'tool', toolName: 'lookup' });
    expect(call?.providerOptions).toBeUndefined();
    expect(JSON.stringify(call?.prompt)).toContain('call-1');
    expect(JSON.stringify(call?.prompt)).toContain('answer');
    expect(llamaModel.doGenerateCalls).toHaveLength(0);
  });

  test('allows chat device routing without structured-output enablement', async () => {
    const llamaModel = chatLanguageModel('served-local-model');
    const remoteModel = successfulLanguageModel('unused-remote-model', { ok: true });
    const generateChatCompletion = createChatCompletionGenerator(
      { ...completeConfiguration(SDKDAutoRoute.RemoteFirst), llamaStructuredOutputsEnabled: false },
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateChatCompletion({ ...chatRequest, executionMode: ExecutionMode.Device });

    expect(response.selectedBackend).toBe(LanguageModelBackend.Device);
    expect(llamaModel.doGenerateCalls).toHaveLength(1);
    expect(remoteModel.doGenerateCalls).toHaveLength(0);
  });

  test('falls back for chat after a retryable provider failure', async () => {
    const routeAttempts: string[] = [];
    const llamaModel = apiFailingLanguageModel('llama.cpp', true, routeAttempts);
    const remoteModel = chatLanguageModel('served-remote-model');
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(SDKDAutoRoute.LocalFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateChatCompletion({ ...chatRequest, executionMode: ExecutionMode.Auto });

    expect(routeAttempts).toEqual(['llama.cpp']);
    expect(response.selectedBackend).toBe(LanguageModelBackend.Remote);
    expect(remoteModel.doGenerateCalls).toHaveLength(1);
  });

  test('keeps automatic chat routing local in local-only mode', async () => {
    const llamaModel = chatLanguageModel('served-local-model');
    const remoteModel = chatLanguageModel('unused-remote-model');
    const generateChatCompletion = createChatCompletionGenerator(
      { ...completeConfiguration(SDKDAutoRoute.RemoteFirst), localOnly: true },
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateChatCompletion({ ...chatRequest, executionMode: ExecutionMode.Auto });

    expect(response.selectedBackend).toBe(LanguageModelBackend.Device);
    expect(llamaModel.doGenerateCalls).toHaveLength(1);
    expect(remoteModel.doGenerateCalls).toHaveLength(0);
    await expect(generateChatCompletion({ ...chatRequest, executionMode: ExecutionMode.Remote })).rejects.toThrow(
      'remote routing is disabled by local-only mode',
    );
  });

  test('writes parallel tool calls using the provider wire field', async () => {
    const requestBodies: Array<Record<string, unknown>> = [];
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(SDKDAutoRoute.RemoteFirst),
      wireLanguageModelFactory(requestBodies),
    );

    await generateChatCompletion({ ...chatRequest, executionMode: ExecutionMode.Remote, parallelToolCalls: false });
    await generateChatCompletion({ ...chatRequest, executionMode: ExecutionMode.Device, parallelToolCalls: true });
    const localOnlyGenerator = createChatCompletionGenerator(
      { ...completeConfiguration(SDKDAutoRoute.RemoteFirst), localOnly: true },
      wireLanguageModelFactory(requestBodies),
    );
    await localOnlyGenerator({ ...chatRequest, executionMode: ExecutionMode.Auto, parallelToolCalls: false });

    expect(requestBodies).toHaveLength(3);
    expect(requestBodies[0]?.parallel_tool_calls).toBe(false);
    expect(requestBodies[0]?.parallelToolCalls).toBeUndefined();
    expect(requestBodies[1]?.parallel_tool_calls).toBe(true);
    expect(requestBodies[1]?.parallelToolCalls).toBeUndefined();
    expect(requestBodies[2]?.parallel_tool_calls).toBe(false);
    expect(requestBodies[2]?.parallelToolCalls).toBeUndefined();
  });

  test('passes cancellation to the model and does not fall back after abort', async () => {
    const abortController = new AbortController();
    const routeAttempts: string[] = [];
    let resolveStarted: (() => void) | undefined;
    const started = new Promise<void>(resolve => {
      resolveStarted = resolve;
    });
    const llamaModel = new MockLanguageModelV3({
      doGenerate: async options => {
        routeAttempts.push('llama.cpp');
        expect(options.abortSignal).toBeDefined();
        resolveStarted?.();
        return new Promise((_, reject) => {
          if (options.abortSignal?.aborted) {
            reject(new DOMException('The operation was aborted', 'AbortError'));
            return;
          }
          options.abortSignal?.addEventListener('abort', () => {
            reject(new DOMException('The operation was aborted', 'AbortError'));
          }, { once: true });
        });
      },
    });
    const remoteModel = successfulLanguageModel('unused-remote-model', { ok: true });
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(SDKDAutoRoute.LocalFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const responsePromise = generateChatCompletion({ ...chatRequest, executionMode: ExecutionMode.Auto }, abortController.signal);
    await started;
    abortController.abort();

    await expect(responsePromise).rejects.toThrow('aborted');
    expect(routeAttempts).toEqual(['llama.cpp']);
    expect(remoteModel.doGenerateCalls).toHaveLength(0);
  });

  test('selects the requested device route and normalizes structured output and usage', async () => {
    const llamaModel = successfulLanguageModel('served-local-model', { ok: true }, {
      inputTokens: { total: 12.9, noCache: 8, cacheRead: 4.8, cacheWrite: -2 },
      outputTokens: { total: 5.7, text: 4, reasoning: 1.9 },
    });
    const remoteModel = successfulLanguageModel('unused-remote-model', { ok: false });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(SDKDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateStructuredResponse({ ...structuredRequest, executionMode: ExecutionMode.Device });

    expect(response).toEqual({
      provider: 'llama.cpp',
      model: 'served-local-model',
      content: '{"ok":true}',
      selectedBackend: LanguageModelBackend.Device,
      finishReason: 'stop',
      constraintMode: StructuredOutputConstraintMode.LlamaJSONSchema,
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
      completeConfiguration(SDKDAutoRoute.LocalFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateStructuredResponse(structuredRequest);

    expect(routeAttempts).toEqual(['llama.cpp', 'openrouter']);
    expect(response.provider).toBe('openrouter');
    expect(response.selectedBackend).toBe(LanguageModelBackend.Remote);
    expect(response.constraintMode).toBe(StructuredOutputConstraintMode.OpenAIJSONSchema);
  });

  test('falls back only after retryable provider failures', async () => {
    const routeAttempts: string[] = [];
    const llamaModel = apiFailingLanguageModel('llama.cpp', true, routeAttempts);
    const remoteModel = successfulLanguageModel('served-remote-model', { ok: true }, undefined, () => {
      routeAttempts.push('openrouter');
    });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(SDKDAutoRoute.LocalFirst),
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
      completeConfiguration(SDKDAutoRoute.LocalFirst),
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
      completeConfiguration(SDKDAutoRoute.LocalFirst),
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
      completeConfiguration(SDKDAutoRoute.LocalFirst),
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
      completeConfiguration(SDKDAutoRoute.LocalFirst),
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
      completeConfiguration(SDKDAutoRoute.RemoteFirst),
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
    const configuration = { ...completeConfiguration(SDKDAutoRoute.RemoteFirst), localOnly: true };
    const generateStructuredResponse = createStructuredResponseGenerator(
      configuration,
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateStructuredResponse(structuredRequest);

    expect(response.selectedBackend).toBe(LanguageModelBackend.Device);
    expect(remoteModel.doGenerateCalls).toHaveLength(0);
    await expect(generateStructuredResponse({ ...structuredRequest, executionMode: ExecutionMode.Remote })).rejects.toThrow(
      'remote routing is disabled by local-only mode',
    );
  });

  test('rejects provider output that violates the requested schema', async () => {
    const invalidModel = successfulLanguageModel('invalid-model', { ok: 'not-a-boolean' });
    const fallbackModel = successfulLanguageModel('fallback-model', { ok: true });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(SDKDAutoRoute.LocalFirst),
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
      completeConfiguration(SDKDAutoRoute.LocalFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    await expect(generateStructuredResponse(structuredRequest)).rejects.toThrow('provider request failed');
    expect(routeAttempts).toEqual(['llama.cpp', 'openrouter']);
  });

  test('fails before provider execution when the requested route is unavailable', async () => {
    const model = successfulLanguageModel('unused-model', { ok: true });
    const modelFactory = languageModelFactory(model, model);
    const configuration = completeConfiguration(SDKDAutoRoute.RemoteFirst);
    const noRouteConfiguration = { ...configuration, llamaBaseURL: undefined, llamaModel: undefined, openRouterAPIKey: undefined };
    const generateStructuredResponse = createStructuredResponseGenerator(noRouteConfiguration, modelFactory);

    await expect(generateStructuredResponse(structuredRequest)).rejects.toThrow(
      'auto routing requires an OpenRouter or llama.cpp configuration',
    );
    await expect(generateStructuredResponse({ ...structuredRequest, executionMode: ExecutionMode.Companion })).rejects.toThrow(
      'companion language model routing is provided by InternKim',
    );
    await expect(generateStructuredResponse({ ...structuredRequest, executionMode: ExecutionMode.Remote })).rejects.toThrow(
      'remote routing requires OPENROUTER_API_KEY',
    );
    expect(model.doGenerateCalls).toHaveLength(0);
  });
});

function completeConfiguration(autoRoute: SDKDAutoRoute): SDKDConfiguration {
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

function wireLanguageModelFactory(requestBodies: Array<Record<string, unknown>>): ProviderLanguageModelFactory {
  const fetch = Object.assign(
    async (_input: string | URL | Request, init?: BunFetchRequestInit) => {
      const body = init?.body;
      if (typeof body !== 'string') throw new Error('wire test request body must be a string');
      const parsedBody: unknown = JSON.parse(body);
      if (!isRecord(parsedBody)) throw new Error('wire test request body must be an object');
      requestBodies.push(parsedBody);
      return new Response(JSON.stringify({
        id: 'wire-test',
        choices: [{ index: 0, message: { role: 'assistant', content: 'ok' }, finish_reason: 'stop' }],
        usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 },
      }), { headers: { 'content-type': 'application/json' } });
    },
    { preconnect: globalThis.fetch.preconnect },
  );
  return {
    createLlamaLanguageModel(modelName, baseURL, apiKey, parallelToolCalls) {
      const provider = createOpenAICompatible({
        apiKey,
        baseURL,
        name: 'llama-wire-test',
        supportsStructuredOutputs: true,
        fetch,
        transformRequestBody: parallelToolCalls === undefined
          ? undefined
          : requestBody => ({ ...requestBody, parallel_tool_calls: parallelToolCalls }),
      });
      return provider.chatModel(modelName);
    },
    createOpenRouterLanguageModel(modelName, baseURL, apiKey, parallelToolCalls) {
      const provider = createOpenRouter({ apiKey, baseURL, compatibility: 'strict', fetch });
      return provider.chat(modelName, parallelToolCalls === undefined ? undefined : { parallelToolCalls });
    },
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

function chatLanguageModel(modelID: string): MockLanguageModelV3 {
  return new MockLanguageModelV3({
    modelId: modelID,
    doGenerate: async () => ({
      content: [{
        type: 'tool-call',
        toolCallId: 'call-2',
        toolName: 'lookup',
        input: '{"key":"result"}',
      }],
      finishReason: { unified: 'tool-calls', raw: 'tool_calls' },
      usage: defaultUsage(),
      response: { modelId: modelID, headers: { 'x-test': 'ok' } },
      providerMetadata: { openrouter: { trace: 'test' } },
      warnings: [],
    }),
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

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}
