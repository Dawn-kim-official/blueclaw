import { createOpenAICompatible } from '@ai-sdk/openai-compatible';
import type { JSONSchema7 } from '@ai-sdk/provider';
import { createOpenRouter } from '@openrouter/ai-sdk-provider';
import type { StructuredResponse, StructuredResponseRequest } from '@blueclaw/protocol';
import {
  generateText,
  jsonSchema,
  Output,
  type LanguageModel,
  type ModelMessage,
} from 'ai';
import Ajv from 'ajv';

import type { SDKDConfiguration } from './configuration.ts';
import { classifySDKDError, isRetryableProviderError, SDKDError } from './errors.ts';

type ProviderRoute = {
  constraintMode: 'llama_json_schema' | 'openai_json_schema';
  languageModel: LanguageModel;
  modelName: string;
  providerName: 'llama.cpp' | 'openrouter';
  selectedBackend: 'device' | 'remote';
};

export type ProviderLanguageModelFactory = {
  createLlamaLanguageModel(modelName: string, baseURL: string, apiKey: string): LanguageModel;
  createOpenRouterLanguageModel(modelName: string, baseURL: string, apiKey: string): LanguageModel;
};

export type StructuredResponseGenerator = (request: StructuredResponseRequest) => Promise<StructuredResponse>;

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

function resolveProviderRoutes(
  request: StructuredResponseRequest,
  configuration: SDKDConfiguration,
  languageModelFactory: ProviderLanguageModelFactory,
): ProviderRoute[] {
  if (request.executionMode === 'device') return [createLlamaRoute(configuration, languageModelFactory)];
  if (request.executionMode === 'remote') {
    if (configuration.localOnly) {
      throw new SDKDError('policy_remote_disabled', 403, false, 'remote routing is disabled by local-only mode');
    }
    return [createOpenRouterRoute(request, configuration, languageModelFactory)];
  }
  if (request.executionMode === 'companion') throw new Error('companion language model routing is provided by InternKim');
  const routes = configuration.localOnly
    ? [optionalLlamaRoute(configuration, languageModelFactory)]
    : configuration.autoRoute === 'local-first'
      ? [
          optionalLlamaRoute(configuration, languageModelFactory),
          optionalOpenRouterRoute(request, configuration, languageModelFactory),
        ]
      : [
          optionalOpenRouterRoute(request, configuration, languageModelFactory),
          optionalLlamaRoute(configuration, languageModelFactory),
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
): ProviderRoute | undefined {
  if (!configuration.llamaBaseURL || !configuration.llamaModel || !configuration.llamaStructuredOutputsEnabled) {
    return undefined;
  }
  return createLlamaRoute(configuration, languageModelFactory);
}

function optionalOpenRouterRoute(
  request: StructuredResponseRequest,
  configuration: SDKDConfiguration,
  languageModelFactory: ProviderLanguageModelFactory,
): ProviderRoute | undefined {
  if (!configuration.openRouterAPIKey) return undefined;
  return createOpenRouterRoute(request, configuration, languageModelFactory);
}

function createLlamaRoute(
  configuration: SDKDConfiguration,
  languageModelFactory: ProviderLanguageModelFactory,
): ProviderRoute {
  if (!configuration.llamaBaseURL || !configuration.llamaModel) {
    throw new SDKDError('configuration_invalid', 503, false, 'device routing requires BLUECLAW_SDKD_LLAMA_BASE_URL and BLUECLAW_SDKD_LLAMA_MODEL');
  }
  if (!configuration.llamaStructuredOutputsEnabled) {
    throw new SDKDError('configuration_invalid', 503, false, 'device structured output routing requires explicit conformance enablement');
  }
  return {
    constraintMode: 'llama_json_schema',
    languageModel: languageModelFactory.createLlamaLanguageModel(
      configuration.llamaModel,
      configuration.llamaBaseURL,
      configuration.llamaAPIKey,
    ),
    modelName: configuration.llamaModel,
    providerName: 'llama.cpp',
    selectedBackend: 'device',
  };
}

function createOpenRouterRoute(
  request: StructuredResponseRequest,
  configuration: SDKDConfiguration,
  languageModelFactory: ProviderLanguageModelFactory,
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
    ),
    modelName,
    providerName: 'openrouter',
    selectedBackend: 'remote',
  };
}

const defaultLanguageModelFactory: ProviderLanguageModelFactory = {
  createLlamaLanguageModel(modelName, baseURL, apiKey) {
    const provider = createOpenAICompatible({
      apiKey,
      baseURL,
      name: 'llama',
      supportsStructuredOutputs: true,
    });
    return provider.chatModel(modelName);
  },
  createOpenRouterLanguageModel(modelName, baseURL, apiKey) {
    const provider = createOpenRouter({
      apiKey,
      baseURL,
      compatibility: 'strict',
    });
    return provider.chat(modelName);
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

function systemContent(request: StructuredResponseRequest): string | undefined {
  const systemMessages = request.messages
    .filter(message => message.role === 'system')
    .map(textContent)
    .filter(Boolean);
  return systemMessages.length > 0 ? systemMessages.join('\n\n') : undefined;
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
