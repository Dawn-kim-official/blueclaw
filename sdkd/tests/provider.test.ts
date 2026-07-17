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
  StructuredOutputDiagnosticCategory,
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
  generationOptions: { maxTokens: 128, seed: 7, temperature: 0 },
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
    expect(call?.maxOutputTokens).toBe(128);
    expect(call?.seed).toBe(7);
    expect(call?.temperature).toBe(0);
    expect(JSON.stringify(call?.prompt)).toContain('call-1');
    expect(JSON.stringify(call?.prompt)).toContain('answer');
    expect(llamaModel.doGenerateCalls).toHaveLength(0);
  });

  test('rejects schema-invalid native tool arguments without fallback', async () => {
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = toolCallLanguageModel('served-remote-model', [{ toolName: 'lookup', input: '{' }]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(SDKDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    try {
      await generateChatCompletion(chatRequest);
      throw new Error('expected invalid tool arguments');
    } catch (errorValue) {
      expect(errorValue).toMatchObject({
        code: 'provider_response_invalid',
        allowLegacyFallback: false,
        diagnostic: { category: StructuredOutputDiagnosticCategory.JSONParse },
      });
    }
    expect(llamaModel.doGenerateCalls).toHaveLength(0);
  });

  test('repairs nested invalid native tool arguments once on the same route', async () => {
    const request = nestedChatRequest();
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = sequencedToolCallLanguageModel('served-remote-model', [
      '{"details":{"count":"wrong"}}',
      '{"details":{"count":2}}',
    ]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(SDKDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateChatCompletion(request);

    expect(response.message.toolCalls?.[0]?.function.arguments).toBe('{"details":{"count":2}}');
    expect(remoteModel.doGenerateCalls).toHaveLength(2);
    expect(remoteModel.doGenerateCalls[1]?.toolChoice).toEqual({ type: 'tool', toolName: 'lookup' });
    expect(JSON.stringify(remoteModel.doGenerateCalls[1]?.prompt)).toContain(
      'Validation failure: data/details/count must be number',
    );
    expect(llamaModel.doGenerateCalls).toHaveLength(0);
  });

  test('repairs unknown native tool arguments once without changing the provider schema', async () => {
    const request = openTaskChatRequest();
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = sequencedToolCallLanguageModel('served-remote-model', [
      '{"task":{"title":"ship","unexpected":{"malformed":true}},"items":[{"name":"first","unknown":["bad"]}],"optionalNote":"keep"}',
      '{"task":{"title":"ship","priority":2},"items":[{"name":"first","label":"primary"}],"optionalNote":"keep"}',
    ]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(SDKDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateChatCompletion(request);
    const providerTool = remoteModel.doGenerateCalls[0]?.tools?.[0];
    const providerSchema = providerTool?.type === 'function' ? providerTool.inputSchema : undefined;

    expect(response.message.toolCalls?.[0]?.function.arguments).toBe(
      '{"task":{"title":"ship","priority":2},"items":[{"name":"first","label":"primary"}],"optionalNote":"keep"}',
    );
    expect(remoteModel.doGenerateCalls).toHaveLength(2);
    expect(llamaModel.doGenerateCalls).toHaveLength(0);
    expect(JSON.stringify(providerSchema)).toBe(JSON.stringify(request.tools?.[0]?.function.parameters));
    const repairProviderTool = remoteModel.doGenerateCalls[1]?.tools?.[0];
    const repairProviderSchema = repairProviderTool?.type === 'function' ? repairProviderTool.inputSchema : undefined;
    expect(JSON.stringify(repairProviderSchema)).toBe(JSON.stringify(request.tools?.[0]?.function.parameters));
    expect(providerSchema).not.toHaveProperty('additionalProperties');
    const repairPrompt = JSON.stringify(remoteModel.doGenerateCalls[1]?.prompt);
    expect(repairPrompt).toContain('Malformed arguments');
    expect(repairPrompt).toContain('unexpected');
    expect(repairPrompt).toContain('Validation category: schema_validation');
    expect(repairPrompt).toContain('Validation failure: data/task must NOT have additional properties');
    expect(repairPrompt).toContain('\\"additionalProperties\\":false');
    expect((repairPrompt.match(/additionalProperties/g) ?? []).length).toBeGreaterThanOrEqual(3);
  });

  test('removes optional non-nullable properties through nested objects and arrays', async () => {
    const request = nullNormalizationChatRequest();
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = sequencedToolCallLanguageModel('served-remote-model', [JSON.stringify({
      optionalText: null,
      requiredText: 'keep',
      nullableText: null,
      nested: { optionalCount: null, requiredCount: 2 },
      rows: [{ optionalLabel: null, requiredLabel: 'first', nullableLabel: null }],
    })]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(SDKDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateChatCompletion(request);

    expect(response.message.toolCalls?.[0]?.function.arguments).toBe(JSON.stringify({
      requiredText: 'keep',
      nullableText: null,
      nested: { requiredCount: 2 },
      rows: [{ requiredLabel: 'first', nullableLabel: null }],
    }));
    expect(remoteModel.doGenerateCalls).toHaveLength(1);
    expect(llamaModel.doGenerateCalls).toHaveLength(0);
  });

  test('keeps required nulls, array elements, additional property values, and unknown keys for validation', async () => {
    const request = nullNormalizationChatRequest();
    const invalidArguments = JSON.stringify({
      requiredText: null,
      nullableText: null,
      nested: { requiredCount: 2 },
      rows: [null],
      metadata: { extra: null },
      unknown: null,
    });
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = sequencedToolCallLanguageModel('served-remote-model', [invalidArguments, invalidArguments]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(SDKDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    await expect(generateChatCompletion(request)).rejects.toMatchObject({
      code: 'provider_response_invalid',
      diagnostic: { category: StructuredOutputDiagnosticCategory.SchemaValidation },
    });

    const repairPrompt = JSON.stringify(remoteModel.doGenerateCalls[1]?.prompt);
    expect(repairPrompt).toContain('data must NOT have additional properties');
    expect(repairPrompt).toContain('data/requiredText must be string');
    expect(repairPrompt).toContain('data/rows/0 must be object');
    expect(repairPrompt).toContain('data/metadata/extra must be string');
    expect(remoteModel.doGenerateCalls).toHaveLength(2);
    expect(llamaModel.doGenerateCalls).toHaveLength(0);
  });

  test('fails closed for permanent unknown native tool arguments without an alternate route', async () => {
    const request = openTaskChatRequest();
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = sequencedToolCallLanguageModel('served-remote-model', [
      '{"task":{"title":"ship","unexpected":{"malformed":true}},"items":[{"name":"first"}]}',
      '{"task":{"title":"ship","unexpected":{"stillMalformed":true}},"items":[{"name":"first"}]}',
    ]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(SDKDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    await expect(generateChatCompletion(request)).rejects.toMatchObject({
      code: 'provider_response_invalid',
      allowLegacyFallback: false,
      diagnostic: { category: StructuredOutputDiagnosticCategory.SchemaValidation },
    });
    expect(remoteModel.doGenerateCalls).toHaveLength(2);
    expect(llamaModel.doGenerateCalls).toHaveLength(0);
  });

  test('preserves explicit open object properties while closing only omitted properties for repair', async () => {
    const request = explicitOpenChatRequest();
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = sequencedToolCallLanguageModel('served-remote-model', [
      '{"metadata":{"source":"model"},"labels":{"team":"blueclaw"},"unexpected":true}',
      '{"metadata":{"source":"model","extra":true},"labels":{"team":"blueclaw","owner":"sdkd"}}',
    ]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(SDKDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const response = await generateChatCompletion(request);

    const providerTool = remoteModel.doGenerateCalls[0]?.tools?.[0];
    const providerSchema = providerTool?.type === 'function' ? providerTool.inputSchema : undefined;
    const repairProviderTool = remoteModel.doGenerateCalls[1]?.tools?.[0];
    const repairProviderSchema = repairProviderTool?.type === 'function' ? repairProviderTool.inputSchema : undefined;
    const repairPrompt = JSON.stringify(remoteModel.doGenerateCalls[1]?.prompt);
    expect(JSON.stringify(providerSchema)).toBe(JSON.stringify(request.tools?.[0]?.function.parameters));
    expect(JSON.stringify(repairProviderSchema)).toBe(JSON.stringify(request.tools?.[0]?.function.parameters));
    expect(repairPrompt).toContain('additionalProperties');
    expect(repairPrompt).toContain('true');
    expect(repairPrompt).toContain('string');
    expect(repairPrompt).toContain('false');
    expect(repairPrompt).toContain('\\"additionalProperties\\":true');
    expect(repairPrompt).toContain('\\"additionalProperties\\":{\\"type\\":\\"string\\"}');
    expect(repairPrompt).toContain('\\"additionalProperties\\":false');
    expect(response.message.toolCalls?.[0]?.function.arguments).toBe(
      '{"metadata":{"source":"model","extra":true},"labels":{"team":"blueclaw","owner":"sdkd"}}',
    );
  });

  test('rejects permanently invalid native tool arguments without an alternate route', async () => {
    const request = nestedChatRequest();
    const llamaModel = successfulLanguageModel('unused-local-model', { ok: true });
    const remoteModel = sequencedToolCallLanguageModel('served-remote-model', [
      '{"details":{"count":"wrong"}}',
      '{"details":{"count":"still-wrong"}}',
    ]);
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(SDKDAutoRoute.RemoteFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    await expect(generateChatCompletion(request)).rejects.toMatchObject({
      code: 'provider_response_invalid',
      allowLegacyFallback: false,
      diagnostic: { category: StructuredOutputDiagnosticCategory.SchemaValidation },
    });
    expect(remoteModel.doGenerateCalls).toHaveLength(2);
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

  test('disables parallel tool calls for structured routes and preserves chat values', async () => {
    const structuredCalls: ProviderFactoryCall[] = [];
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(SDKDAutoRoute.RemoteFirst),
      recordingLanguageModelFactory(
        successfulLanguageModel('device-model', { ok: true }),
        successfulLanguageModel('remote-model', { ok: true }),
        structuredCalls,
      ),
    );

    await generateStructuredResponse({ ...structuredRequest, executionMode: ExecutionMode.Remote });
    await generateStructuredResponse({ ...structuredRequest, executionMode: ExecutionMode.Device });

    expect(structuredCalls).toEqual([
      { provider: 'openrouter', parallelToolCalls: false },
      { provider: 'llama.cpp', parallelToolCalls: false },
    ]);

    const chatCalls: ProviderFactoryCall[] = [];
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(SDKDAutoRoute.RemoteFirst),
      recordingLanguageModelFactory(
        chatLanguageModel('device-model'),
        chatLanguageModel('remote-model'),
        chatCalls,
      ),
    );

    await generateChatCompletion({ ...chatRequest, executionMode: ExecutionMode.Remote, parallelToolCalls: true });
    await generateChatCompletion({ ...chatRequest, executionMode: ExecutionMode.Device, parallelToolCalls: false });

    expect(chatCalls).toEqual([
      { provider: 'openrouter', parallelToolCalls: true },
      { provider: 'llama.cpp', parallelToolCalls: false },
    ]);
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

  test('cancels chat repair without using a fallback route', async () => {
    const abortController = new AbortController();
    let generationCount = 0;
    let resolveRepairStarted: (() => void) | undefined;
    const repairStarted = new Promise<void>(resolve => {
      resolveRepairStarted = resolve;
    });
    const model = new MockLanguageModelV3({
      modelId: 'repairing-model',
      doGenerate: async options => {
        generationCount += 1;
        if (generationCount === 1) return toolCallGeneration('repairing-model', 'lookup', '{"details":{"count":"wrong"}}');
        expect(options.abortSignal).toBeDefined();
        resolveRepairStarted?.();
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
    const fallbackModel = successfulLanguageModel('unused-model', { ok: true });
    const generateChatCompletion = createChatCompletionGenerator(
      completeConfiguration(SDKDAutoRoute.LocalFirst),
      languageModelFactory(model, fallbackModel),
    );

    const responsePromise = generateChatCompletion(nestedChatRequest(), abortController.signal);
    await repairStarted;
    abortController.abort();

    await expect(responsePromise).rejects.toThrow('aborted');
    expect(generationCount).toBe(2);
    expect(fallbackModel.doGenerateCalls).toHaveLength(0);
  });

  test('passes structured cancellation to the model and does not fall back after abort', async () => {
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
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(SDKDAutoRoute.LocalFirst),
      languageModelFactory(llamaModel, remoteModel),
    );

    const responsePromise = generateStructuredResponse(
      { ...structuredRequest, executionMode: ExecutionMode.Auto },
      abortController.signal,
    );
    await started;
    abortController.abort();

    await expect(responsePromise).rejects.toThrow('aborted');
    expect(routeAttempts).toEqual(['llama.cpp']);
    expect(remoteModel.doGenerateCalls).toHaveLength(0);
  });

  test('rejects pre-aborted structured requests before route resolution', async () => {
    const abortController = new AbortController();
    abortController.abort();
    const model = successfulLanguageModel('unused-model', { ok: true });
    const generateStructuredResponse = createStructuredResponseGenerator(
      { ...completeConfiguration(SDKDAutoRoute.RemoteFirst), llamaBaseURL: undefined, llamaModel: undefined, openRouterAPIKey: undefined },
      languageModelFactory(model, model),
    );

    await expect(generateStructuredResponse(structuredRequest, abortController.signal)).rejects.toThrow('aborted');
    expect(model.doGenerateCalls).toHaveLength(0);
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
      constraintMode: StructuredOutputConstraintMode.NativeToolCall,
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
    expect(llamaModel.doGenerateCalls[0]?.tools?.map(tool => tool.name)).toEqual(['provider_test_output']);
    expect(llamaModel.doGenerateCalls[0]?.toolChoice).toEqual({ type: 'tool', toolName: 'provider_test_output' });
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
    expect(response.constraintMode).toBe(StructuredOutputConstraintMode.NativeToolCall);
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

    await expect(generateStructuredResponse(structuredRequest)).rejects.toMatchObject({
      code: 'structured_output_invalid',
      diagnostic: { category: StructuredOutputDiagnosticCategory.SchemaValidation },
    });
    expect(fallbackModel.doGenerateCalls).toHaveLength(0);
  });

  test('distinguishes malformed JSON from schema validation failures', async () => {
    const model = toolCallLanguageModel('invalid-model', [{
      toolName: 'provider_test_output',
      input: '{',
    }]);
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(SDKDAutoRoute.LocalFirst),
      languageModelFactory(model, successfulLanguageModel('unused-model', { ok: true })),
    );

    await expect(generateStructuredResponse(structuredRequest)).rejects.toMatchObject({
      code: 'structured_output_invalid',
      diagnostic: { category: StructuredOutputDiagnosticCategory.JSONParse },
    });
  });

  test('repairs malformed structured output with one same-route generation', async () => {
    const model = malformedThenValidLanguageModel('repaired-model', { ok: true });
    const fallbackModel = successfulLanguageModel('unused-model', { ok: false });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(SDKDAutoRoute.LocalFirst),
      languageModelFactory(model, fallbackModel),
    );

    const response = await generateStructuredResponse({ ...structuredRequest, executionMode: ExecutionMode.Device });

    expect(response.content).toBe('{"ok":true}');
    expect(model.doGenerateCalls).toHaveLength(2);
    expect(model.doGenerateCalls[0]?.toolChoice).toEqual({ type: 'tool', toolName: 'provider_test_output' });
    expect(model.doGenerateCalls[1]?.toolChoice).toEqual({ type: 'tool', toolName: 'provider_test_output' });
    expect(model.doGenerateCalls[1]?.maxOutputTokens).toBe(128);
    expect(model.doGenerateCalls[1]?.seed).toBe(7);
    expect(model.doGenerateCalls[1]?.temperature).toBe(0);
    expect(fallbackModel.doGenerateCalls).toHaveLength(0);
  });

  test('repairs structured output with the closed schema and validation category', async () => {
    const request = nestedStructuredRequest();
    const model = sequencedStructuredToolCallLanguageModel('repaired-model', [
      '{"details":{"count":"wrong"}}',
      '{"details":{"count":2}}',
    ]);
    const fallbackModel = successfulLanguageModel('unused-model', { ok: false });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(SDKDAutoRoute.LocalFirst),
      languageModelFactory(model, fallbackModel),
    );

    const response = await generateStructuredResponse({ ...request, executionMode: ExecutionMode.Device });

    expect(response.content).toBe('{"details":{"count":2}}');
    expect(model.doGenerateCalls).toHaveLength(2);
    expect(model.doGenerateCalls[1]?.toolChoice).toEqual({ type: 'tool', toolName: 'provider_test_output' });
    const providerTool = model.doGenerateCalls[0]?.tools?.[0];
    const providerSchema = providerTool?.type === 'function' ? providerTool.inputSchema : undefined;
    const repairProviderTool = model.doGenerateCalls[1]?.tools?.[0];
    const repairProviderSchema = repairProviderTool?.type === 'function' ? repairProviderTool.inputSchema : undefined;
    const repairPrompt = JSON.stringify(model.doGenerateCalls[1]?.prompt);
    expect(JSON.stringify(providerSchema)).toBe(JSON.stringify(request.structuredOutputSchema.document));
    expect(JSON.stringify(repairProviderSchema)).toBe(JSON.stringify(request.structuredOutputSchema.document));
    expect(repairPrompt).toContain('Malformed arguments');
    expect(repairPrompt).toContain('Closed JSON schema');
    expect(repairPrompt).toContain('Validation category: schema_validation');
    expect(repairPrompt).toContain('\\"additionalProperties\\":false');
    expect((repairPrompt.match(/additionalProperties/g) ?? []).length).toBeGreaterThanOrEqual(2);
    expect(fallbackModel.doGenerateCalls).toHaveLength(0);
  });

  test('fails closed for permanently invalid structured output without an alternate route', async () => {
    const model = sequencedStructuredToolCallLanguageModel('invalid-model', [
      '{"details":{"count":"wrong"}}',
      '{"details":{"count":"still-wrong"}}',
    ]);
    const fallbackModel = successfulLanguageModel('unused-model', { ok: true });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(SDKDAutoRoute.LocalFirst),
      languageModelFactory(model, fallbackModel),
    );

    await expect(generateStructuredResponse({ ...nestedStructuredRequest(), executionMode: ExecutionMode.Device })).rejects.toMatchObject({
      code: 'structured_output_invalid',
      diagnostic: { category: StructuredOutputDiagnosticCategory.SchemaValidation },
    });
    expect(model.doGenerateCalls).toHaveLength(2);
    expect(fallbackModel.doGenerateCalls).toHaveLength(0);
  });

  test('rejects structured output without exactly one matching tool call', async () => {
    for (const toolCalls of [
      [],
      [{ toolName: 'other_output', input: '{}' }],
      [
        { toolName: 'provider_test_output', input: '{"ok":true}' },
        { toolName: 'provider_test_output', input: '{"ok":true}' },
      ],
    ]) {
      const model = toolCallLanguageModel('invalid-model', toolCalls);
      const generateStructuredResponse = createStructuredResponseGenerator(
        completeConfiguration(SDKDAutoRoute.LocalFirst),
        languageModelFactory(model, successfulLanguageModel('unused-model', { ok: true })),
      );

      await expect(generateStructuredResponse(structuredRequest)).rejects.toMatchObject({
        code: 'structured_output_invalid',
        diagnostic: { category: StructuredOutputDiagnosticCategory.ToolCallContract },
      });
      expect(model.doGenerateCalls).toHaveLength(1);
    }
  });

  test('cancels structured repair without using a fallback route', async () => {
    const abortController = new AbortController();
    let generationCount = 0;
    let resolveRepairStarted: (() => void) | undefined;
    const repairStarted = new Promise<void>(resolve => {
      resolveRepairStarted = resolve;
    });
    const model = new MockLanguageModelV3({
      modelId: 'repairing-model',
      doGenerate: async options => {
        generationCount += 1;
        if (generationCount === 1) return toolCallGeneration('repairing-model', 'provider_test_output', '{');
        expect(options.abortSignal).toBeDefined();
        resolveRepairStarted?.();
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
    const fallbackModel = successfulLanguageModel('unused-model', { ok: true });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(SDKDAutoRoute.LocalFirst),
      languageModelFactory(model, fallbackModel),
    );

    const responsePromise = generateStructuredResponse(
      { ...structuredRequest, executionMode: ExecutionMode.Auto },
      abortController.signal,
    );
    await repairStarted;
    abortController.abort();

    await expect(responsePromise).rejects.toThrow('aborted');
    expect(generationCount).toBe(2);
    expect(fallbackModel.doGenerateCalls).toHaveLength(0);
  });

  test('rejects undeclared quality review evidence fields', async () => {
    const qualityReviewRequest: StructuredResponseRequest = {
      ...structuredRequest,
      structuredOutputSchema: {
        ...structuredRequest.structuredOutputSchema,
        document: {
          type: 'object',
          properties: {
            qualityReview: {
              type: 'array',
              items: {
                type: 'object',
                properties: { evidenceIDs: { type: 'array', items: { type: 'string' } } },
                additionalProperties: false,
              },
            },
          },
          required: ['qualityReview'],
          additionalProperties: false,
        },
      },
    };
    const invalidModel = successfulLanguageModel('invalid-model', {
      qualityReview: [{ evidence: 'obs-1' }],
    });
    const fallbackModel = successfulLanguageModel('fallback-model', {
      qualityReview: [{ evidenceIDs: ['obs-1'] }],
    });
    const generateStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(SDKDAutoRoute.LocalFirst),
      languageModelFactory(invalidModel, fallbackModel),
    );

    await expect(generateStructuredResponse(qualityReviewRequest)).rejects.toThrow();
    expect(fallbackModel.doGenerateCalls).toHaveLength(0);

    const generateValidStructuredResponse = createStructuredResponseGenerator(
      completeConfiguration(SDKDAutoRoute.LocalFirst),
      languageModelFactory(fallbackModel, invalidModel),
    );
    const response = await generateValidStructuredResponse(qualityReviewRequest);

    expect(response.content).toBe('{"qualityReview":[{"evidenceIDs":["obs-1"]}]}');
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

type ProviderFactoryCall = {
  provider: 'llama.cpp' | 'openrouter';
  parallelToolCalls: boolean | undefined;
};

function recordingLanguageModelFactory(
  llamaModel: MockLanguageModelV3,
  openRouterModel: MockLanguageModelV3,
  calls: ProviderFactoryCall[],
): ProviderLanguageModelFactory {
  return {
    createLlamaLanguageModel(_modelName, _baseURL, _apiKey, parallelToolCalls) {
      calls.push({ provider: 'llama.cpp', parallelToolCalls });
      return llamaModel;
    },
    createOpenRouterLanguageModel(_modelName, _baseURL, _apiKey, parallelToolCalls) {
      calls.push({ provider: 'openrouter', parallelToolCalls });
      return openRouterModel;
    },
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

function toolCallLanguageModel(
  modelID: string,
  toolCalls: Array<{ toolName: string; input: string }>,
): MockLanguageModelV3 {
  return new MockLanguageModelV3({
    modelId: modelID,
    doGenerate: async () => ({
      content: toolCalls.map((toolCall, index) => ({
        type: 'tool-call' as const,
        toolCallId: `call-${index}`,
        toolName: toolCall.toolName,
        input: toolCall.input,
      })),
      finishReason: { unified: 'tool-calls', raw: 'tool_calls' },
      usage: defaultUsage(),
      response: { modelId: modelID },
      warnings: [],
    }),
  });
}

function sequencedToolCallLanguageModel(modelID: string, inputs: string[]): MockLanguageModelV3 {
  let generationCount = 0;
  return new MockLanguageModelV3({
    modelId: modelID,
    doGenerate: async () => {
      const input = inputs[Math.min(generationCount, inputs.length - 1)];
      generationCount += 1;
      return toolCallGeneration(modelID, 'lookup', input ?? '{}');
    },
  });
}

function sequencedStructuredToolCallLanguageModel(modelID: string, inputs: string[]): MockLanguageModelV3 {
  let generationCount = 0;
  return new MockLanguageModelV3({
    modelId: modelID,
    doGenerate: async () => {
      const input = inputs[Math.min(generationCount, inputs.length - 1)];
      generationCount += 1;
      return toolCallGeneration(modelID, 'provider_test_output', input ?? '{}');
    },
  });
}

function malformedThenValidLanguageModel(modelID: string, output: unknown): MockLanguageModelV3 {
  let generationCount = 0;
  return new MockLanguageModelV3({
    modelId: modelID,
    doGenerate: async () => {
      generationCount += 1;
      if (generationCount === 1) return toolCallGeneration(modelID, 'provider_test_output', '{');
      return successfulGeneration(modelID, output, defaultUsage());
    },
  });
}

function toolCallGeneration(modelID: string, toolName: string, input: string): LanguageModelV3GenerateResult {
  return {
    content: [{ type: 'tool-call', toolCallId: 'structured-output-call', toolName, input }],
    finishReason: { unified: 'tool-calls', raw: 'tool_calls' },
    usage: defaultUsage(),
    response: { modelId: modelID },
    warnings: [],
  };
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

function nestedChatRequest(): ChatCompletionRequest {
  return {
    ...chatRequest,
    tools: [{
      type: 'function',
      function: {
        name: 'lookup',
        description: 'Look up a value.',
        parameters: {
          type: 'object',
          properties: {
            details: {
              type: 'object',
              properties: { count: { type: 'number' } },
              required: ['count'],
              additionalProperties: false,
            },
          },
          required: ['details'],
          additionalProperties: false,
        },
      },
    }],
  };
}

function openTaskChatRequest(): ChatCompletionRequest {
  return {
    ...chatRequest,
    tools: [{
      type: 'function',
      function: {
        name: 'lookup',
        description: 'Look up a task.',
        parameters: {
          type: 'object',
          properties: {
            task: {
              type: 'object',
              properties: {
                title: { type: 'string' },
                priority: { type: 'number' },
              },
              required: ['title'],
            },
            items: {
              type: 'array',
              items: {
                type: 'object',
                properties: {
                  name: { type: 'string' },
                  label: { type: 'string' },
                },
                required: ['name'],
              },
            },
            optionalNote: { type: 'string' },
          },
          required: ['task', 'items'],
        },
      },
    }],
  };
}

function explicitOpenChatRequest(): ChatCompletionRequest {
  return {
    ...chatRequest,
    tools: [{
      type: 'function',
      function: {
        name: 'lookup',
        description: 'Look up a task.',
        parameters: {
          type: 'object',
          properties: {
            metadata: { type: 'object', additionalProperties: true },
            labels: { type: 'object', additionalProperties: { type: 'string' } },
          },
          required: ['metadata', 'labels'],
        },
      },
    }],
  };
}

function nullNormalizationChatRequest(): ChatCompletionRequest {
  return {
    ...chatRequest,
    tools: [{
      type: 'function',
      function: {
        name: 'lookup',
        description: 'Look up a value.',
        parameters: {
          type: 'object',
          properties: {
            optionalText: { type: 'string' },
            requiredText: { type: 'string' },
            nullableText: { type: ['string', 'null'] },
            nested: {
              type: 'object',
              properties: {
                optionalCount: { type: 'number' },
                requiredCount: { type: 'number' },
              },
              required: ['requiredCount'],
            },
            rows: {
              type: 'array',
              items: {
                type: 'object',
                properties: {
                  optionalLabel: { type: 'string' },
                  requiredLabel: { type: 'string' },
                  nullableLabel: { type: ['string', 'null'] },
                },
                required: ['requiredLabel'],
              },
            },
            metadata: { type: 'object', additionalProperties: { type: 'string' } },
          },
          required: ['requiredText', 'nested', 'rows'],
        },
      },
    }],
  };
}

function nestedStructuredRequest(): StructuredResponseRequest {
  return {
    ...structuredRequest,
    structuredOutputSchema: {
      ...structuredRequest.structuredOutputSchema,
      document: {
        type: 'object',
        properties: {
          details: {
            type: 'object',
            properties: { count: { type: 'number' } },
            required: ['count'],
          },
        },
        required: ['details'],
      },
    },
  };
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
    content: [{
      type: 'tool-call',
      toolCallId: 'structured-output-call',
      toolName: 'provider_test_output',
      input: JSON.stringify(output),
    }],
    finishReason: { unified: 'tool-calls', raw: 'tool_calls' },
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
