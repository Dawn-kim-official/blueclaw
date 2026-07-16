import { describe, expect, test } from 'bun:test';
import { JSONParseError, TypeValidationError } from 'ai';

import { StructuredOutputDiagnosticCategory } from '@blueclaw/protocol';

import { classifySDKDError } from '../src/errors.ts';

describe('sdkd error diagnostics', () => {
  test('classifies JSON parsing without exposing generated content', () => {
    const generatedContent = 'private generated content';
    const error = classifySDKDError(new JSONParseError({
      text: generatedContent,
      cause: new SyntaxError('provider details'),
    }));

    expect(error).toMatchObject({
      code: 'structured_output_invalid',
      allowLegacyFallback: false,
      diagnostic: { category: StructuredOutputDiagnosticCategory.JSONParse },
    });
    expect(JSON.stringify(error.diagnostic)).not.toContain(generatedContent);
  });

  test('classifies schema validation without exposing values or schema details', () => {
    const generatedValue = { private: 'generated value' };
    const error = classifySDKDError(new TypeValidationError({
      value: generatedValue,
      cause: new Error('schema and tool details'),
    }));

    expect(error).toMatchObject({
      code: 'structured_output_invalid',
      allowLegacyFallback: false,
      diagnostic: { category: StructuredOutputDiagnosticCategory.SchemaValidation },
    });
    expect(JSON.stringify(error.diagnostic)).not.toContain(generatedValue.private);
  });
});
