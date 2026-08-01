import { timingSafeEqual } from 'node:crypto';
import { z } from 'zod';

import {
  protocolVersion,
  chatCompletionRequestSchema,
  chatCompletionResponseSchema,
  structuredResponseRequestSchema,
  structuredResponseSchema,
  structuredOutputDiagnosticSchema,
  protocolIdentitySchema,
  type StructuredOutputDiagnostic,
} from '@blueclaw/protocol';
import { buildProtocolArtifacts } from '@blueclaw/protocol/artifacts';

import type { LLMDConfiguration } from './configuration.ts';
import { classifyLLMDError, diagnoseResponseValidation } from './errors.ts';
import type { ChatCompletionGenerator, StructuredResponseGenerator } from './provider.ts';

const protocolManifest = buildProtocolArtifacts().manifest;
const protocolIdentity = protocolIdentitySchema.parse({
  aggregateProtocolHash: protocolManifest.aggregateHash,
  protocolVersion,
});
const healthResponseSchema = protocolIdentitySchema.extend({ status: z.literal('ok') });

type HandlerDependencies = {
  configuration: LLMDConfiguration;
  generateStructuredResponse: StructuredResponseGenerator;
  generateChatCompletion?: ChatCompletionGenerator;
};

const totalRequestTimeoutMs = 300_000;

export function createLLMDHandler(dependencies: HandlerDependencies) {
  return async function handleRequest(request: Request): Promise<Response> {
    const url = new URL(request.url);
    if (request.method === 'GET' && url.pathname === '/health') {
      return Response.json(healthResponseSchema.parse({ ...protocolIdentity, status: 'ok' }));
    }
    if (url.pathname !== '/v1/llm/structured' && url.pathname !== '/v1/llm/chat') return errorResponse(404, 'route_not_found');
    if (request.method !== 'POST') return errorResponse(405, 'method_not_allowed');
    if (!hasValidAuthorization(request, dependencies.configuration.authKey)) {
      return errorResponse(401, 'unauthorized');
    }

    const requestDocument = await parseJSONBody(request);
    if (!requestDocument.success) return errorResponse(400, requestDocument.error);
    const requestDeadlineSignal = AbortSignal.any([request.signal, AbortSignal.timeout(totalRequestTimeoutMs)]);
    if (url.pathname === '/v1/llm/chat') {
      return handleChatRequest(requestDocument.value, requestDeadlineSignal, dependencies);
    }
    const parsedRequest = structuredResponseRequestSchema.safeParse(requestDocument.value);
    if (!parsedRequest.success) return errorResponse(400, 'invalid_structured_response_request');

    try {
      const response = await dependencies.generateStructuredResponse(parsedRequest.data, requestDeadlineSignal);
      const parsedResponse = structuredResponseSchema.safeParse(response);
      if (!parsedResponse.success) {
        return errorResponse(502, 'provider_response_invalid', false, diagnoseResponseValidation(parsedResponse.error));
      }
      return Response.json(parsedResponse.data);
    } catch (errorValue) {
      const llmdError = classifyLLMDError(errorValue);
      return errorResponse(llmdError.status, llmdError.code, llmdError.allowLegacyFallback, llmdError.diagnostic);
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
    if (!parsedResponse.success) {
      return errorResponse(502, 'provider_response_invalid', false, diagnoseResponseValidation(parsedResponse.error));
    }
    return Response.json(parsedResponse.data);
  } catch (errorValue) {
    const llmdError = classifyLLMDError(errorValue);
    return errorResponse(llmdError.status, llmdError.code, llmdError.allowLegacyFallback, llmdError.diagnostic);
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

function errorResponse(
  status: number,
  code: string,
  allowLegacyFallback = false,
  diagnostic?: StructuredOutputDiagnostic,
): Response {
  const parsedDiagnostic = structuredOutputDiagnosticSchema.safeParse(diagnostic);
  const error = parsedDiagnostic.success
    ? { code, allowLegacyFallback, diagnostic: parsedDiagnostic.data }
    : { code, allowLegacyFallback };
  return Response.json({ error }, { status });
}
