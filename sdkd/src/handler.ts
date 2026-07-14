import { timingSafeEqual } from 'node:crypto';

import {
  protocolVersion,
  structuredResponseRequestSchema,
  structuredResponseSchema,
} from '@blueclaw/protocol';
import { buildProtocolArtifacts } from '@blueclaw/protocol/artifacts';

import type { SDKDConfiguration } from './configuration.ts';
import type { StructuredResponseGenerator } from './provider.ts';

const protocolManifest = buildProtocolArtifacts().manifest;

type HandlerDependencies = {
  configuration: SDKDConfiguration;
  generateStructuredResponse: StructuredResponseGenerator;
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
    if (url.pathname !== '/v1/llm/structured') return errorResponse(404, 'route_not_found');
    if (request.method !== 'POST') return errorResponse(405, 'method_not_allowed');
    if (!hasValidAuthorization(request, dependencies.configuration.authKey)) {
      return errorResponse(401, 'unauthorized');
    }

    const requestDocument = await parseJSONBody(request);
    if (!requestDocument.success) return errorResponse(400, requestDocument.error);
    const parsedRequest = structuredResponseRequestSchema.safeParse(requestDocument.value);
    if (!parsedRequest.success) return errorResponse(400, 'invalid_structured_response_request');

    try {
      const response = await dependencies.generateStructuredResponse(parsedRequest.data);
      const parsedResponse = structuredResponseSchema.safeParse(response);
      if (!parsedResponse.success) return errorResponse(502, 'invalid_provider_response');
      return Response.json(parsedResponse.data);
    } catch (errorValue) {
      return errorResponse(502, providerErrorCode(errorValue));
    }
  };
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

function providerErrorCode(errorValue: unknown): string {
  if (!(errorValue instanceof Error)) return 'provider_request_failed';
  const normalizedName = errorValue.name.trim().toLowerCase().replaceAll(/[^a-z0-9]+/g, '_');
  return normalizedName ? `provider_${normalizedName}` : 'provider_request_failed';
}

function errorResponse(status: number, code: string): Response {
  return Response.json({ error: { code } }, { status });
}
