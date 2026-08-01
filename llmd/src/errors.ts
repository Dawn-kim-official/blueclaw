import {
  ChatCompletionFinishReason,
  StructuredOutputDiagnosticCategory,
  StructuredOutputValidationCode,
  type StructuredOutputDiagnostic,
} from '@blueclaw/protocol';
import type { ZodError, core } from 'zod';
import { APICallError, JSONParseError, NoObjectGeneratedError, RetryError, TypeValidationError } from 'ai';

export type LLMDErrorCode =
  | 'configuration_invalid'
  | 'policy_remote_disabled'
  | 'provider_rate_limited'
  | 'provider_response_invalid'
  | 'provider_unavailable'
  | 'request_aborted'
  | 'request_invalid'
  | 'structured_output_invalid';

export class LLMDError extends Error {
  constructor(
    readonly code: LLMDErrorCode,
    readonly status: number,
    readonly allowLegacyFallback: boolean,
    message: string,
    readonly diagnostic?: StructuredOutputDiagnostic,
    readonly providerMessage?: string,
  ) {
    super(message);
    this.name = 'LLMDError';
  }
}


const VALIDATION_FIELD_SEGMENT = /^[A-Za-z0-9_.$~-]+$/;

// A response we reject at our own boundary must say which field failed. Without
// this the caller sees only provider_response_invalid and has to guess.
export function diagnoseResponseValidation(errorValue: ZodError): StructuredOutputDiagnostic {
  return {
    category: StructuredOutputDiagnosticCategory.SchemaValidation,
    validationIssues: errorValue.issues.slice(0, 8).map(describeValidationIssue),
  };
}

function describeValidationIssue(issue: core.$ZodIssue): { fieldPath: string; code: StructuredOutputValidationCode } {
  return { fieldPath: validationFieldPath(issue.path), code: validationIssueCode(issue) };
}

function validationFieldPath(path: readonly PropertyKey[]): string {
  const segments = path
    .map((segment) => String(segment))
    .filter((segment) => VALIDATION_FIELD_SEGMENT.test(segment));
  return `/${segments.join('/')}`;
}

function validationIssueCode(issue: core.$ZodIssue): StructuredOutputValidationCode {
  if (issue.code === 'unrecognized_keys') return StructuredOutputValidationCode.AdditionalProperty;
  if (issue.code !== 'invalid_type') return StructuredOutputValidationCode.Other;
  return issue.input === undefined ? StructuredOutputValidationCode.Required : StructuredOutputValidationCode.Type;
}


const MAXIMUM_PROVIDER_MESSAGE_LENGTH = 600;

// The provider's own rejection text is transport diagnostics, not model output:
// without it a caller sees provider_response_invalid and nothing else. Model
// output travels the structured_output_invalid path and keeps its diagnostic.
function providerDiagnosticMessage(message: string): string | undefined {
  const trimmedMessage = message.trim();
  if (trimmedMessage === '') return undefined;
  return trimmedMessage.slice(0, MAXIMUM_PROVIDER_MESSAGE_LENGTH);
}

export function classifyLLMDError(errorValue: unknown): LLMDError {
  if (errorValue instanceof LLMDError) return errorValue;
  if (errorValue instanceof DOMException && errorValue.name === 'AbortError') {
    return new LLMDError('request_aborted', 499, false, errorValue.message);
  }
  if (RetryError.isInstance(errorValue)) return classifyLLMDError(errorValue.lastError);
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
      return new LLMDError('provider_rate_limited', 429, true, errorValue.message);
    }
    if (isRetryable) {
      return new LLMDError('provider_unavailable', 503, true, errorValue.message);
    }
    return new LLMDError('provider_response_invalid', 502, false, errorValue.message, undefined, providerDiagnosticMessage(errorValue.message));
  }
  const message = errorMessage(errorValue);
  return new LLMDError('provider_response_invalid', 502, false, message, undefined, providerDiagnosticMessage(message));
}

function structuredOutputError(errorValue: Error, diagnostic: StructuredOutputDiagnostic): LLMDError {
  return new LLMDError('structured_output_invalid', 422, false, errorValue.message, diagnostic);
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
