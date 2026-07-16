import {
  ChatCompletionFinishReason,
  StructuredOutputDiagnosticCategory,
  type StructuredOutputDiagnostic,
} from '@blueclaw/protocol';
import { APICallError, JSONParseError, NoObjectGeneratedError, RetryError, TypeValidationError } from 'ai';

export type SDKDErrorCode =
  | 'configuration_invalid'
  | 'policy_remote_disabled'
  | 'provider_rate_limited'
  | 'provider_response_invalid'
  | 'provider_unavailable'
  | 'request_aborted'
  | 'request_invalid'
  | 'structured_output_invalid';

export class SDKDError extends Error {
  constructor(
    readonly code: SDKDErrorCode,
    readonly status: number,
    readonly allowLegacyFallback: boolean,
    message: string,
    readonly diagnostic?: StructuredOutputDiagnostic,
  ) {
    super(message);
    this.name = 'SDKDError';
  }
}

export function classifySDKDError(errorValue: unknown): SDKDError {
  if (errorValue instanceof SDKDError) return errorValue;
  if (errorValue instanceof DOMException && errorValue.name === 'AbortError') {
    return new SDKDError('request_aborted', 499, false, errorValue.message);
  }
  if (RetryError.isInstance(errorValue)) return classifySDKDError(errorValue.lastError);
  if (NoObjectGeneratedError.isInstance(errorValue)) {
    return structuredOutputError(errorValue, diagnoseNoObjectGeneratedError(errorValue));
  }
  if (TypeValidationError.isInstance(errorValue)) {
    return structuredOutputError(errorValue, { category: StructuredOutputDiagnosticCategory.SchemaValidation });
  }
  if (JSONParseError.isInstance(errorValue)) {
    return structuredOutputError(errorValue, { category: StructuredOutputDiagnosticCategory.JSONParse });
  }
  if (APICallError.isInstance(errorValue)) {
    const isRetryable = isRetryableProviderError(errorValue);
    if (isRetryable && errorValue.statusCode === 429) {
      return new SDKDError('provider_rate_limited', 429, true, errorValue.message);
    }
    if (isRetryable) {
      return new SDKDError('provider_unavailable', 503, true, errorValue.message);
    }
    return new SDKDError('provider_response_invalid', 502, false, errorValue.message);
  }
  return new SDKDError('provider_response_invalid', 502, false, errorMessage(errorValue));
}

function structuredOutputError(errorValue: Error, diagnostic: StructuredOutputDiagnostic): SDKDError {
  return new SDKDError('structured_output_invalid', 422, false, errorValue.message, diagnostic);
}

function diagnoseNoObjectGeneratedError(errorValue: NoObjectGeneratedError): StructuredOutputDiagnostic {
  if (JSONParseError.isInstance(errorValue.cause)) {
    return { category: StructuredOutputDiagnosticCategory.JSONParse };
  }
  if (TypeValidationError.isInstance(errorValue.cause)) {
    return { category: StructuredOutputDiagnosticCategory.SchemaValidation };
  }
  return {
    category: StructuredOutputDiagnosticCategory.FinishReason,
    finishReason: normalizeFinishReason(errorValue.finishReason),
  };
}

function normalizeFinishReason(finishReason: string | undefined): ChatCompletionFinishReason {
  if (finishReason === undefined) return ChatCompletionFinishReason.Unknown;
  const normalizedFinishReason = finishReason.replaceAll('-', '_');
  return Object.values(ChatCompletionFinishReason).find(value => value === normalizedFinishReason)
    ?? ChatCompletionFinishReason.Unknown;
}

export function isRetryableProviderError(errorValue: unknown): boolean {
  if (RetryError.isInstance(errorValue)) return isRetryableProviderError(errorValue.lastError);
  if (APICallError.isInstance(errorValue)) return errorValue.isRetryable;
  return false;
}

function errorMessage(errorValue: unknown): string {
  return errorValue instanceof Error ? errorValue.message : 'provider request failed';
}
