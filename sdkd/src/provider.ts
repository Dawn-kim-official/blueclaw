import { createOpenAICompatible } from '@ai-sdk/openai-compatible';
import type { JSONSchema7, JSONValue } from '@ai-sdk/provider';
import { createOpenRouter } from '@openrouter/ai-sdk-provider';
import type {
  ChatCompletionRequest,
  ChatCompletionResponse,
  StructuredResponse,
  StructuredResponseRequest,
} from '@blueclaw/protocol';
import {
  generateText,
  jsonSchema,
  Output,
  type ToolChoice,
  type LanguageModel,
  type ModelMessage,
} from 'ai';
import Ajv from 'ajv';

import type { SDKDConfiguration } from './configuration.ts';
import { classifySDKDError, isRetryableProviderError, SDKDError } from './errors.ts';

type ProviderRoute = {
  constraintMode?: 'llama_json_schema' | 'openai_json_schema';
  languageModel: LanguageModel;
  modelName: string;
  providerName: 'llama.cpp' | 'openrouter';
  selectedBackend: 'device' | 'remote';
};

export type ProviderLanguageModelFactory = {
  createLlamaLanguageModel(modelName: string, baseURL: string, apiKey: string, parallelToolCalls?: boolean): LanguageModel;
  createOpenRouterLanguageModel(modelName: string, baseURL: string, apiKey: string, parallelToolCalls?: boolean): LanguageModel;
};

export type StructuredResponseGenerator = (request: StructuredResponseRequest) => Promise<StructuredResponse>;
export type ChatCompletionGenerator = (request: ChatCompletionRequest, abortSignal?: AbortSignal) => Promise<ChatCompletionResponse>;

type ProviderRequest = StructuredResponseRequest | ChatCompletionRequest;

type DynamicTool = {
  description?: string;
  inputSchema: ReturnType<typeof jsonSchema>;
};

type DynamicToolSet = Record<string, DynamicTool>;
type ChatProviderMetadata = NonNullable<ChatCompletionResponse['providerMetadata']>;

export function createStructuredResponseGenerator(
  configuration: SDKDConfiguration,
  languageModelFactory: ProviderLanguageModelFactory = defaultLanguageModelFactory,
): StructuredResponseGenerator {
  return async request => {
    const routes = resolveProviderRoutes(request, configuration, languageModelFactory);
    let lastError: unknown;
    for (const route of routes) {
      try {
        return await generateForRoute(request, route, configuration.requestTimeoutMillisecond);
      } catch (errorValue) {
        lastError = errorValue;
        if (!isRetryableProviderError(errorValue)) break;
      }
    }
    if (lastError !== undefined) throw classifySDKDError(lastError);
    throw new SDKDError('configuration_invalid', 503, false, 'no configured language model route accepted the request');
  };
}

export function createChatCompletionGenerator(
  configuration: SDKDConfiguration,
  languageModelFactory: ProviderLanguageModelFactory = defaultLanguageModelFactory,
): ChatCompletionGenerator {
  return async (request, abortSignal) => {
    throwIfAborted(abortSignal);
    const routes = resolveProviderRoutes(request, configuration, languageModelFactory, false);
    let lastError: unknown;
    for (const route of routes) {
      throwIfAborted(abortSignal);
      try {
        return await generateChatForRoute(request, route, configuration.requestTimeoutMillisecond, abortSignal);
      } catch (errorValue) {
        lastError = errorValue;
        if (abortSignal?.aborted) throw errorValue;
        if (!isRetryableProviderError(errorValue)) break;
      }
    }
    if (lastError !== undefined) throw classifySDKDError(lastError);
    throw new SDKDError('configuration_invalid', 503, false, 'no configured language model route accepted the request');
  };
}

function resolveProviderRoutes(
  request: ProviderRequest,
  configuration: SDKDConfiguration,
  languageModelFactory: ProviderLanguageModelFactory,
  requireStructuredOutputs = true,
): ProviderRoute[] {
  const parallelToolCalls = 'parallelToolCalls' in request && typeof request.parallelToolCalls === 'boolean'
    ? request.parallelToolCalls
    : undefined;
  if (request.executionMode === 'device') return [createLlamaRoute(configuration, languageModelFactory, requireStructuredOutputs, parallelToolCalls)];
  if (request.executionMode === 'remote') {
    if (configuration.localOnly) {
      throw new SDKDError('policy_remote_disabled', 403, false, 'remote routing is disabled by local-only mode');
    }
    return [createOpenRouterRoute(request, configuration, languageModelFactory, parallelToolCalls)];
  }
  if (request.executionMode === 'companion') throw new Error('companion language model routing is provided by InternKim');
  const routes = configuration.localOnly
    ? [optionalLlamaRoute(configuration, languageModelFactory, requireStructuredOutputs, parallelToolCalls)]
    : configuration.autoRoute === 'local-first'
      ? [
          optionalLlamaRoute(configuration, languageModelFactory, requireStructuredOutputs, parallelToolCalls),
          optionalOpenRouterRoute(request, configuration, languageModelFactory, parallelToolCalls),
        ]
      : [
          optionalOpenRouterRoute(request, configuration, languageModelFactory, parallelToolCalls),
          optionalLlamaRoute(configuration, languageModelFactory, requireStructuredOutputs, parallelToolCalls),
        ];
  const configuredRoutes = routes.filter(route => route !== undefined);
  if (configuredRoutes.length === 0) {
    throw new SDKDError('configuration_invalid', 503, false, 'auto routing requires an OpenRouter or llama.cpp configuration');
  }
  return configuredRoutes;
}

function optionalLlamaRoute(
  configuration: SDKDConfiguration,
  languageModelFactory: ProviderLanguageModelFactory,
  requireStructuredOutputs = true,
  parallelToolCalls?: boolean,
): ProviderRoute | undefined {
  if (!configuration.llamaBaseURL || !configuration.llamaModel) {
    return undefined;
  }
  if (requireStructuredOutputs && !configuration.llamaStructuredOutputsEnabled) return undefined;
  return createLlamaRoute(configuration, languageModelFactory, requireStructuredOutputs, parallelToolCalls);
}

function optionalOpenRouterRoute(
  request: ProviderRequest,
  configuration: SDKDConfiguration,
  languageModelFactory: ProviderLanguageModelFactory,
  parallelToolCalls?: boolean,
): ProviderRoute | undefined {
  if (!configuration.openRouterAPIKey) return undefined;
  return createOpenRouterRoute(request, configuration, languageModelFactory, parallelToolCalls);
}

function createLlamaRoute(
  configuration: SDKDConfiguration,
  languageModelFactory: ProviderLanguageModelFactory,
  requireStructuredOutputs = true,
  parallelToolCalls?: boolean,
): ProviderRoute {
  if (!configuration.llamaBaseURL || !configuration.llamaModel) {
    throw new SDKDError('configuration_invalid', 503, false, 'device routing requires BLUECLAW_SDKD_LLAMA_BASE_URL and BLUECLAW_SDKD_LLAMA_MODEL');
  }
  if (requireStructuredOutputs && !configuration.llamaStructuredOutputsEnabled) {
    throw new SDKDError('configuration_invalid', 503, false, 'device structured output routing requires explicit conformance enablement');
  }
  return {
    constraintMode: 'llama_json_schema',
    languageModel: languageModelFactory.createLlamaLanguageModel(
      configuration.llamaModel,
      configuration.llamaBaseURL,
      configuration.llamaAPIKey,
      parallelToolCalls,
    ),
    modelName: configuration.llamaModel,
    providerName: 'llama.cpp',
    selectedBackend: 'device',
  };
}

function createOpenRouterRoute(
  request: ProviderRequest,
  configuration: SDKDConfiguration,
  languageModelFactory: ProviderLanguageModelFactory,
  parallelToolCalls?: boolean,
): ProviderRoute {
  if (!configuration.openRouterAPIKey) {
    throw new SDKDError('configuration_invalid', 503, false, 'remote routing requires OPENROUTER_API_KEY');
  }
  const modelName = request.model?.trim();
  if (!modelName) throw new SDKDError('request_invalid', 400, false, 'remote routing requires a model');
  return {
    constraintMode: 'openai_json_schema',
    languageModel: languageModelFactory.createOpenRouterLanguageModel(
      modelName,
      configuration.openRouterBaseURL,
      configuration.openRouterAPIKey,
      parallelToolCalls,
    ),
    modelName,
    providerName: 'openrouter',
    selectedBackend: 'remote',
  };
}

const defaultLanguageModelFactory: ProviderLanguageModelFactory = {
  createLlamaLanguageModel(modelName, baseURL, apiKey, parallelToolCalls) {
    const provider = createOpenAICompatible({
      apiKey,
      baseURL,
      name: 'llama',
      supportsStructuredOutputs: true,
      transformRequestBody: parallelToolCalls === undefined
        ? undefined
        : requestBody => ({ ...requestBody, parallel_tool_calls: parallelToolCalls }),
    });
    return provider.chatModel(modelName);
  },
  createOpenRouterLanguageModel(modelName, baseURL, apiKey, parallelToolCalls) {
    const provider = createOpenRouter({
      apiKey,
      baseURL,
      compatibility: 'strict',
    });
    return provider.chat(modelName, parallelToolCalls === undefined ? undefined : { parallelToolCalls });
  },
};

async function generateForRoute(
  request: StructuredResponseRequest,
  route: ProviderRoute,
  timeoutMillisecond: number,
): Promise<StructuredResponse> {
  const outputSchema = createValidatedOutputSchema(request.structuredOutputSchema.document);
  const result = await generateText({
    model: route.languageModel,
    system: systemContent(request),
    messages: convertConversationMessages(request),
    output: Output.object({
      name: request.structuredOutputSchema.name,
      schema: outputSchema,
    }),
    maxOutputTokens: request.generationOptions?.maxTokens,
    maxRetries: 0,
    seed: request.generationOptions?.seed,
    temperature: request.generationOptions?.temperature,
    timeout: timeoutMillisecond,
  });
  if (result.finishReason !== 'stop') {
    throw new SDKDError('structured_output_invalid', 422, false, `structured output generation finished with ${result.finishReason}`);
  }
  return {
    provider: route.providerName,
    model: result.response.modelId || route.modelName,
    content: JSON.stringify(result.output),
    selectedBackend: route.selectedBackend,
    finishReason: result.finishReason,
    constraintMode: route.constraintMode,
    usage: {
      promptTokens: normalizeTokenCount(result.totalUsage.inputTokens),
      completionTokens: normalizeTokenCount(result.totalUsage.outputTokens),
      totalTokens: normalizeTokenCount(result.totalUsage.totalTokens),
      cachedPromptTokens: optionalTokenCount(result.totalUsage.inputTokenDetails.cacheReadTokens),
      cacheWriteTokens: optionalTokenCount(result.totalUsage.inputTokenDetails.cacheWriteTokens),
      reasoningTokens: optionalTokenCount(result.totalUsage.outputTokenDetails.reasoningTokens),
    },
  };
}

async function generateChatForRoute(
  request: ChatCompletionRequest,
  route: ProviderRoute,
  timeoutMillisecond: number,
  abortSignal?: AbortSignal,
): Promise<ChatCompletionResponse> {
  const tools = createChatTools(request);
  const result = await generateText({
    model: route.languageModel,
    system: systemContent(request),
    messages: convertChatMessages(request),
    tools: Object.keys(tools).length > 0 ? tools : undefined,
    toolChoice: convertToolChoice(request.toolChoice, Object.keys(tools)),
    maxRetries: 0,
    abortSignal,
    timeout: timeoutMillisecond,
  });
  return {
    provider: route.providerName,
    model: result.response.modelId || route.modelName,
    message: {
      role: 'assistant',
      content: result.text,
      toolCalls: result.toolCalls.map(toolCall => ({
        id: toolCall.toolCallId,
        type: 'function',
        function: {
          name: toolCall.toolName,
          arguments: JSON.stringify(toolCall.input) ?? '{}',
        },
      })),
    },
    selectedBackend: route.selectedBackend,
    finishReason: normalizeChatFinishReason(result.finishReason),
    usage: normalizeUsage(result.totalUsage),
    providerMetadata: serializableProviderMetadata(result.providerMetadata),
  };
}

function createChatTools(request: ChatCompletionRequest): DynamicToolSet {
  const tools: DynamicToolSet = {};
  for (const tool of request.tools ?? []) {
    const parameters = tool.function.parameters;
    if (!isJSONSchema(parameters)) {
      throw new SDKDError('request_invalid', 400, false, `tool ${tool.function.name} parameters must be a JSON schema object`);
    }
    tools[tool.function.name] = {
      description: tool.function.description,
      inputSchema: jsonSchema(parameters),
    };
  }
  return tools;
}

function throwIfAborted(abortSignal: AbortSignal | undefined): void {
  if (abortSignal?.aborted) throw new DOMException('The operation was aborted', 'AbortError');
}

function convertToolChoice(toolChoice: unknown, toolNames: string[]): ToolChoice<DynamicToolSet> | undefined {
  if (toolChoice === undefined || toolChoice === null) return undefined;
  if (toolChoice === 'auto' || toolChoice === 'none' || toolChoice === 'required') return toolChoice;
  if (!isRecord(toolChoice) || toolChoice.type !== 'function' || !isRecord(toolChoice.function)) {
    throw new SDKDError('request_invalid', 400, false, 'tool choice must be auto, none, required, or a function choice');
  }
  const toolName = toolChoice.function.name;
  if (typeof toolName !== 'string' || toolName.trim() === '') {
    throw new SDKDError('request_invalid', 400, false, 'tool choice function name is required');
  }
  if (!toolNames.includes(toolName)) {
    throw new SDKDError('request_invalid', 400, false, `tool choice references unknown tool ${toolName}`);
  }
  return { type: 'tool', toolName };
}

function convertChatMessages(request: ChatCompletionRequest): ModelMessage[] {
  const toolNames = toolNamesByCallID(request);
  return request.messages.filter(message => message.role !== 'system').map(message => {
    if (message.role === 'user') return { role: 'user', content: message.content ?? '' };
    if (message.role === 'assistant') return assistantMessage(message);
    const toolName = toolNames.get(message.toolCallId ?? '');
    if (!toolName) throw new SDKDError('request_invalid', 400, false, `tool result ${message.toolCallId ?? ''} has no matching tool call`);
    return {
      role: 'tool',
      content: [{
        type: 'tool-result' as const,
        toolCallId: message.toolCallId ?? '',
        toolName,
        output: toolResultOutput(message.content ?? ''),
      }],
    };
  });
}

function assistantMessage(message: ChatCompletionRequest['messages'][number]): ModelMessage {
  const toolCalls = message.toolCalls ?? [];
  if (toolCalls.length === 0) return { role: 'assistant', content: message.content ?? '' };
  const content: Array<{ type: 'text'; text: string } | { type: 'tool-call'; toolCallId: string; toolName: string; input: unknown }> = [];
  if (message.content) content.push({ type: 'text', text: message.content });
  for (const toolCall of toolCalls) {
    content.push({
      type: 'tool-call',
      toolCallId: toolCall.id,
      toolName: toolCall.function.name,
      input: parseToolArguments(toolCall.function.arguments),
    });
  }
  return { role: 'assistant', content };
}

function toolNamesByCallID(request: ChatCompletionRequest): Map<string, string> {
  const toolNames = new Map<string, string>();
  for (const message of request.messages) {
    for (const toolCall of message.toolCalls ?? []) toolNames.set(toolCall.id, toolCall.function.name);
  }
  return toolNames;
}

function toolResultOutput(content: string) {
  const parsedContent = parseJSONValue(content);
  return isJSONValue(parsedContent) ? { type: 'json' as const, value: parsedContent } : { type: 'text' as const, value: content };
}

function parseToolArguments(argumentsText: string): unknown {
  const parsedArguments = parseJSONValue(argumentsText);
  if (parsedArguments === undefined) throw new SDKDError('request_invalid', 400, false, 'tool call arguments must be valid JSON');
  return parsedArguments;
}

function parseJSONValue(value: string): unknown {
  try {
    return JSON.parse(value) as unknown;
  } catch {
    return undefined;
  }
}

function normalizeChatFinishReason(finishReason: string): 'stop' | 'length' | 'tool_calls' | 'content_filter' | 'error' | 'other' | 'unknown' {
  const normalizedFinishReason = finishReason.replaceAll('-', '_');
  if (normalizedFinishReason === 'stop' || normalizedFinishReason === 'length' || normalizedFinishReason === 'tool_calls' || normalizedFinishReason === 'content_filter' || normalizedFinishReason === 'error' || normalizedFinishReason === 'other' || normalizedFinishReason === 'unknown') {
    return normalizedFinishReason;
  }
  return 'unknown';
}

type UsageDocument = {
  inputTokens?: number;
  outputTokens?: number;
  totalTokens?: number;
  inputTokenDetails?: { cacheReadTokens?: number; cacheWriteTokens?: number };
  outputTokenDetails?: { reasoningTokens?: number };
};

function normalizeUsage(usage: UsageDocument) {
  return {
    promptTokens: normalizeTokenCount(usage.inputTokens),
    completionTokens: normalizeTokenCount(usage.outputTokens),
    totalTokens: normalizeTokenCount(usage.totalTokens),
    cachedPromptTokens: optionalTokenCount(usage.inputTokenDetails?.cacheReadTokens),
    cacheWriteTokens: optionalTokenCount(usage.inputTokenDetails?.cacheWriteTokens),
    reasoningTokens: optionalTokenCount(usage.outputTokenDetails?.reasoningTokens),
  };
}

function serializableProviderMetadata(providerMetadata: unknown): ChatProviderMetadata | undefined {
  if (providerMetadata === undefined) return undefined;
  try {
    const serializedMetadata: unknown = JSON.parse(JSON.stringify(providerMetadata));
    if (isChatProviderMetadata(serializedMetadata)) return serializedMetadata;
  } catch {
    throw new SDKDError('provider_response_invalid', 502, false, 'provider metadata is not serializable');
  }
  throw new SDKDError('provider_response_invalid', 502, false, 'provider metadata is not serializable');
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

function isJSONValue(value: unknown): value is JSONValue {
  if (value === null || typeof value === 'string' || typeof value === 'boolean') return true;
  if (typeof value === 'number') return Number.isFinite(value);
  if (Array.isArray(value)) return value.every(isJSONValue);
  if (!isRecord(value)) return false;
  return Object.values(value).every(isJSONValue);
}

function isChatProviderMetadata(value: unknown): value is ChatProviderMetadata {
  if (value === null || typeof value === 'string' || typeof value === 'boolean') return true;
  if (typeof value === 'number') return Number.isFinite(value);
  if (Array.isArray(value)) return value.every(isChatProviderMetadata);
  if (!isRecord(value)) return false;
  return Object.values(value).every(isChatProviderMetadata);
}

function createValidatedOutputSchema(document: unknown) {
  if (!isJSONSchema(document)) {
    throw new SDKDError('request_invalid', 400, false, 'structured output schema must be a JSON object');
  }
  const ajv = new Ajv({ allErrors: true, strict: false });
  let validator;
  try {
    validator = ajv.compile(document);
  } catch (errorValue) {
    throw new SDKDError('request_invalid', 400, false, errorValue instanceof Error ? errorValue.message : 'structured output schema is invalid');
  }
  return jsonSchema(document, {
    validate(value) {
      if (validator(value)) return { success: true, value };
      return { success: false, error: new Error(ajv.errorsText(validator.errors)) };
    },
  });
}

function isJSONSchema(value: unknown): value is JSONSchema7 {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

function convertConversationMessages(request: StructuredResponseRequest): ModelMessage[] {
  return request.messages.filter(message => message.role !== 'system').map(message => {
    if (message.role === 'user') return { role: 'user', content: userContent(message) };
    const content = textContent(message);
    return { role: 'assistant', content };
  });
}

function systemContent(request: ProviderRequest): string | undefined {
  const systemMessages = request.messages
    .filter(message => message.role === 'system')
    .map(systemMessageContent)
    .filter(Boolean);
  return systemMessages.length > 0 ? systemMessages.join('\n\n') : undefined;
}

function systemMessageContent(message: ProviderRequest['messages'][number]): string {
  if (!('parts' in message)) return message.content ?? '';
  const content = [message.content ?? ''];
  if (!Array.isArray(message.parts)) return message.content ?? '';
  for (const part of message.parts) {
    if (isRecord(part) && part.type === 'image') throw new Error(`${message.role} messages cannot contain image parts`);
    if (isRecord(part) && typeof part.text === 'string') content.push(part.text);
  }
  return content.filter(Boolean).join('\n');
}

function userContent(message: StructuredResponseRequest['messages'][number]) {
  if (!message.parts || message.parts.length === 0) return message.content ?? '';
  const content = [];
  if (message.content) content.push({ type: 'text' as const, text: message.content });
  for (const part of message.parts) {
    if (part.type === 'text') content.push({ type: 'text' as const, text: part.text });
    if (part.type === 'image') content.push({ type: 'image' as const, image: part.dataBase64, mediaType: part.mimeType });
  }
  return content;
}

function textContent(message: StructuredResponseRequest['messages'][number]): string {
  const content = [message.content ?? ''];
  for (const part of message.parts ?? []) {
    if (part.type === 'image') throw new Error(`${message.role} messages cannot contain image parts`);
    content.push(part.text);
  }
  return content.filter(Boolean).join('\n');
}

function normalizeTokenCount(value: number | undefined): number {
  return Math.max(0, Math.trunc(value ?? 0));
}

function optionalTokenCount(value: number | undefined): number | undefined {
  if (value === undefined) return undefined;
  return normalizeTokenCount(value);
}
