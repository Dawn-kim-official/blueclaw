import { timingSafeEqual } from 'node:crypto';

import {
  protocolVersion,
  chatCompletionRequestSchema,
  chatCompletionResponseSchema,
  structuredResponseRequestSchema,
  structuredResponseSchema,
} from '@blueclaw/protocol';
import { buildProtocolArtifacts } from '@blueclaw/protocol/artifacts';

import type { SDKDConfiguration } from './configuration.ts';
import { classifySDKDError } from './errors.ts';
import type { ChatCompletionGenerator, StructuredResponseGenerator } from './provider.ts';

const protocolManifest = buildProtocolArtifacts().manifest;

type HandlerDependencies = {
  configuration: SDKDConfiguration;
  generateStructuredResponse: StructuredResponseGenerator;
  generateChatCompletion?: ChatCompletionGenerator;
};

export function createSDKDHandler(dependencies: HandlerDependencies) {
  return async function handleRequest(request: Request): Promise<Response> {
    const url = new URL(request.url);
    if (request.method === 'GET' && url.pathname === '/health') {
      return Response.json({
        aggregateProtocolHash: protocolManifest.aggregateHash,
        protocolVersion,
        status: 'ok',
      });
    }
    if (url.pathname !== '/v1/llm/structured' && url.pathname !== '/v1/llm/chat') return errorResponse(404, 'route_not_found');
    if (request.method !== 'POST') return errorResponse(405, 'method_not_allowed');
    if (!hasValidAuthorization(request, dependencies.configuration.authKey)) {
      return errorResponse(401, 'unauthorized');
    }

    const requestDocument = await parseJSONBody(request);
    if (!requestDocument.success) return errorResponse(400, requestDocument.error);
    if (url.pathname === '/v1/llm/chat') {
      return handleChatRequest(requestDocument.value, request.signal, dependencies);
    }
    const parsedRequest = structuredResponseRequestSchema.safeParse(requestDocument.value);
    if (!parsedRequest.success) return errorResponse(400, 'invalid_structured_response_request');

    try {
      const response = await dependencies.generateStructuredResponse(parsedRequest.data);
      const parsedResponse = structuredResponseSchema.safeParse(response);
      if (!parsedResponse.success) {
        return errorResponse(502, 'provider_response_invalid', false);
      }
      return Response.json(parsedResponse.data);
    } catch (errorValue) {
      const sdkdError = classifySDKDError(errorValue);
      return errorResponse(sdkdError.status, sdkdError.code, sdkdError.allowLegacyFallback);
    }
  };
}

async function handleChatRequest(value: unknown, abortSignal: AbortSignal, dependencies: HandlerDependencies): Promise<Response> {
  if (!dependencies.generateChatCompletion) return errorResponse(503, 'configuration_invalid');
  const parsedRequest = chatCompletionRequestSchema.safeParse(value);
  if (!parsedRequest.success) return errorResponse(400, 'invalid_chat_completion_request');
  try {
    const response = await dependencies.generateChatCompletion(parsedRequest.data, abortSignal);
    const parsedResponse = chatCompletionResponseSchema.safeParse(response);
    if (!parsedResponse.success) return errorResponse(502, 'provider_response_invalid', false);
    return Response.json(parsedResponse.data);
  } catch (errorValue) {
    const sdkdError = classifySDKDError(errorValue);
    return errorResponse(sdkdError.status, sdkdError.code, sdkdError.allowLegacyFallback);
  }
}

function hasValidAuthorization(request: Request, expectedKey: string): boolean {
  const authorization = request.headers.get('authorization') ?? '';
  const providedKey = authorization.startsWith('Bearer ') ? authorization.slice('Bearer '.length) : '';
  const providedBytes = Buffer.from(providedKey);
  const expectedBytes = Buffer.from(expectedKey);
  if (providedBytes.length !== expectedBytes.length) return false;
  return timingSafeEqual(providedBytes, expectedBytes);
}

async function parseJSONBody(request: Request): Promise<
  { success: true; value: unknown } | { success: false; error: string }
> {
  try {
    return { success: true, value: await request.json() };
  } catch {
    return { success: false, error: 'invalid_json' };
  }
}

function errorResponse(status: number, code: string, allowLegacyFallback = false): Response {
  return Response.json({ error: { code, allowLegacyFallback } }, { status });
}
