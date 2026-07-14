import { z } from 'zod';

import { jsonValueSchema, nonNegativeIntegerSchema } from './common.ts';

export const languageModelMessagePartSchema = z.discriminatedUnion('type', [
  z.looseObject({
    type: z.literal('text'),
    text: z.string(),
  }),
  z.looseObject({
    type: z.literal('image'),
    text: z.string().optional(),
    mimeType: z.string(),
    dataBase64: z.string(),
  }),
]);

export const languageModelMessageSchema = z.looseObject({
  role: z.enum(['system', 'user', 'assistant']),
  content: z.string().optional(),
  parts: z.array(languageModelMessagePartSchema).optional(),
});

export const structuredOutputSchemaSchema = z.looseObject({
  name: z.string(),
  document: jsonValueSchema,
  isStrictlyEnforced: z.boolean(),
});

export const generationOptionsSchema = z.looseObject({
  seed: z.number().int().optional(),
  temperature: z.number().optional(),
  maxTokens: nonNegativeIntegerSchema.optional(),
});

export const requestContextSchema = z.looseObject({
  requesterPersonID: z.string().optional(),
  requesterEmail: z.string().optional(),
  requesterName: z.string().optional(),
  requesterPlatformUserID: z.string().optional(),
  conversationID: z.string().optional(),
  platform: z.string().optional(),
});

export const structuredResponseRequestSchema = z.looseObject({
  model: z.string().optional(),
  executionMode: z.enum(['device', 'companion', 'remote', 'auto']),
  context: requestContextSchema.optional(),
  messages: z.array(languageModelMessageSchema),
  structuredOutputSchema: structuredOutputSchemaSchema,
  generationOptions: generationOptionsSchema.optional(),
});

export const languageModelUsageSchema = z.looseObject({
  promptTokens: nonNegativeIntegerSchema,
  completionTokens: nonNegativeIntegerSchema,
  totalTokens: nonNegativeIntegerSchema,
  cachedPromptTokens: nonNegativeIntegerSchema.optional(),
  cacheWriteTokens: nonNegativeIntegerSchema.optional(),
  reasoningTokens: nonNegativeIntegerSchema.optional(),
  costUSD: z.number().nonnegative().optional(),
  upstreamInferenceCostUSD: z.number().nonnegative().optional(),
});

export const structuredResponseSchema = z.looseObject({
  provider: z.string().trim().min(1),
  model: z.string().trim().min(1),
  content: z.string().min(1),
  selectedBackend: z.enum(['device', 'remote']),
  finishReason: z.literal('stop'),
  constraintMode: z.enum([
    'openai_json_schema',
    'llama_json_schema',
    'llama_gbnf',
    'litert_llguidance_json_schema',
    'native_tool_call',
    'prompted_json',
  ]).optional(),
  usage: languageModelUsageSchema.optional(),
});

export type StructuredResponseRequest = z.infer<typeof structuredResponseRequestSchema>;
export type StructuredResponse = z.infer<typeof structuredResponseSchema>;
