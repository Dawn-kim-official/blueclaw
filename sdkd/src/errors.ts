import { APICallError, JSONParseError, NoObjectGeneratedError, RetryError, TypeValidationError } from 'ai';

export type SDKDErrorCode =
  | 'configuration_invalid'
  | 'policy_remote_disabled'
  | 'provider_rate_limited'
  | 'provider_response_invalid'
  | 'provider_unavailable'
  | 'request_invalid'
  | 'structured_output_invalid';

export class SDKDError extends Error {
  constructor(
    readonly code: SDKDErrorCode,
    readonly status: number,
    readonly allowLegacyFallback: boolean,
    message: string,
  ) {
    super(message);
    this.name = 'SDKDError';
  }
}

export function classifySDKDError(errorValue: unknown): SDKDError {
  if (errorValue instanceof SDKDError) return errorValue;
  if (RetryError.isInstance(errorValue)) return classifySDKDError(errorValue.lastError);
  if (NoObjectGeneratedError.isInstance(errorValue) || TypeValidationError.isInstance(errorValue) || JSONParseError.isInstance(errorValue)) {
    return new SDKDError('structured_output_invalid', 422, false, errorMessage(errorValue));
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

export function isRetryableProviderError(errorValue: unknown): boolean {
  if (RetryError.isInstance(errorValue)) return isRetryableProviderError(errorValue.lastError);
  if (APICallError.isInstance(errorValue)) return errorValue.isRetryable;
  return false;
}

function errorMessage(errorValue: unknown): string {
  return errorValue instanceof Error ? errorValue.message : 'provider request failed';
}
