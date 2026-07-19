import { z } from 'zod';

import { ExecutionMode, jsonValueSchema, nonNegativeIntegerSchema } from './common.ts';

export enum LanguageModelMessageRole {
  System = 'system',
  User = 'user',
  Assistant = 'assistant',
}

export enum ChatCompletionMessageRole {
  System = 'system',
  User = 'user',
  Assistant = 'assistant',
  Tool = 'tool',
}

export enum LanguageModelBackend {
  Device = 'device',
  Remote = 'remote',
}

export enum StructuredOutputConstraintMode {
  OpenAIJSONSchema = 'openai_json_schema',
  LlamaJSONSchema = 'llama_json_schema',
  LlamaGBNF = 'llama_gbnf',
  LiteRTLLGuidanceJSONSchema = 'litert_llguidance_json_schema',
  NativeToolCall = 'native_tool_call',
  PromptedJSON = 'prompted_json',
}

export enum ChatCompletionFinishReason {
  Stop = 'stop',
  Length = 'length',
  ToolCalls = 'tool_calls',
  ContentFilter = 'content_filter',
  Error = 'error',
  Other = 'other',
  Unknown = 'unknown',
}

export enum StructuredOutputDiagnosticCategory {
  JSONParse = 'json_parse',
  SchemaValidation = 'schema_validation',
  FinishReason = 'finish_reason',
  ToolCallContract = 'tool_call_contract',
  Serialization = 'serialization',
}

export enum StructuredOutputValidationCode {
  Required = 'required',
  AdditionalProperty = 'additional_property',
  Type = 'type',
  Other = 'other',
}

export enum StructuredOutputRepairStatus {
  NotAttempted = 'not_attempted',
  Failed = 'failed',
}

const toolNameSchema = z.string().min(1).max(128).regex(/^[A-Za-z0-9_.-]+$/);

export const structuredOutputValidationIssueSchema = z.strictObject({
  fieldPath: z.string().max(256).regex(/^\/(?:[A-Za-z0-9_.$~-]+(?:\/[A-Za-z0-9_.$~-]+)*)?$/),
  code: z.enum(StructuredOutputValidationCode),
});

export const structuredOutputDiagnosticSchema = z.strictObject({
  category: z.enum(StructuredOutputDiagnosticCategory),
  finishReason: z.enum(ChatCompletionFinishReason).optional(),
  toolName: toolNameSchema.optional(),
  validationIssues: z.array(structuredOutputValidationIssueSchema).max(8).optional(),
  repairStatus: z.enum(StructuredOutputRepairStatus).optional(),
}).superRefine((diagnostic, context) => {
  if (diagnostic.finishReason !== undefined && diagnostic.category !== StructuredOutputDiagnosticCategory.FinishReason) {
    context.addIssue({
      code: 'custom',
      path: ['finishReason'],
      message: 'finishReason is only valid for finish_reason diagnostics',
    });
  }
  if (diagnostic.validationIssues !== undefined && diagnostic.category !== StructuredOutputDiagnosticCategory.SchemaValidation) {
    context.addIssue({
      code: 'custom',
      path: ['validationIssues'],
      message: 'validationIssues is only valid for schema_validation diagnostics',
    });
  }
});

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
  role: z.enum(LanguageModelMessageRole),
  content: z.string().optional(),
  parts: z.array(languageModelMessagePartSchema).optional(),
});

export const chatCompletionFunctionSchema = z.looseObject({
  name: z.string().trim().min(1),
  description: z.string().optional(),
  parameters: jsonValueSchema,
});

export const chatCompletionToolSchema = z.looseObject({
  type: z.literal('function'),
  function: chatCompletionFunctionSchema,
});

export const chatCompletionToolCallFunctionSchema = z.looseObject({
  name: z.string().trim().min(1),
  arguments: z.string().refine(isJSONDocumentObject, 'arguments must be a JSON object'),
});

export const chatCompletionToolCallSchema = z.looseObject({
  id: z.string().trim().min(1),
  type: z.literal('function'),
  function: chatCompletionToolCallFunctionSchema,
});

export const chatCompletionMessageSchema = z.looseObject({
  role: z.enum(ChatCompletionMessageRole),
  content: z.string().optional(),
  toolCallId: z.string().trim().min(1).optional(),
  toolCalls: z.array(chatCompletionToolCallSchema).optional(),
});

const chatCompletionResponseMessageSchema = z.looseObject({
  role: z.literal('assistant'),
  content: z.string().optional(),
  toolCalls: z.array(chatCompletionToolCallSchema).optional(),
}).superRefine((message, context) => {
  const toolCallIDs = new Set<string>();
  for (const [toolCallIndex, toolCall] of (message.toolCalls ?? []).entries()) {
    if (toolCallIDs.has(toolCall.id)) {
      context.addIssue({
        code: 'custom',
        path: ['toolCalls', toolCallIndex, 'id'],
        message: 'tool call IDs must be unique within a response',
      });
    }
    toolCallIDs.add(toolCall.id);
  }
});

export const structuredOutputSchemaSchema = z.looseObject({
  name: toolNameSchema,
  document: jsonValueSchema,
  isStrictlyEnforced: z.literal(true),
}).superRefine((schema, context) => {
  if (hasOpenObjectSchema(schema.document)) {
    context.addIssue({
      code: 'custom',
      path: ['document'],
      message: 'structured output JSON schema objects must set additionalProperties to false',
    });
  }
});

function hasOpenObjectSchema(value: unknown): boolean {
  if (Array.isArray(value)) return value.some(hasOpenObjectSchema);
  if (!isRecord(value)) return false;
  const schemaType = value.type;
  const isObjectType = schemaType === 'object' || (Array.isArray(schemaType) && schemaType.includes('object'));
  if (isObjectType && value.additionalProperties !== false) return true;
  return Object.values(value).some(hasOpenObjectSchema);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

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
  executionMode: z.enum(ExecutionMode),
  context: requestContextSchema.optional(),
  messages: z.array(languageModelMessageSchema),
  structuredOutputSchema: structuredOutputSchemaSchema,
  generationOptions: generationOptionsSchema.optional(),
});

export const chatCompletionRequestSchema = z.looseObject({
  model: z.string().optional(),
  executionMode: z.enum(ExecutionMode),
  context: requestContextSchema.optional(),
  messages: z.array(chatCompletionMessageSchema),
  tools: z.array(chatCompletionToolSchema).optional(),
  toolChoice: jsonValueSchema.optional(),
  parallelToolCalls: z.boolean(),
  generationOptions: generationOptionsSchema.optional(),
}).superRefine((request, context) => {
  const toolNames = new Set<string>();
  for (const [toolIndex, tool] of (request.tools ?? []).entries()) {
    const toolName = tool.function.name;
    if (toolNames.has(toolName)) {
      context.addIssue({
        code: 'custom',
        path: ['tools', toolIndex, 'function', 'name'],
        message: 'tool function names must be unique within a request',
      });
    }
    toolNames.add(toolName);
  }

  const toolCallIDs = new Set<string>();
  for (const [messageIndex, message] of request.messages.entries()) {
    for (const [toolCallIndex, toolCall] of (message.toolCalls ?? []).entries()) {
      if (toolCallIDs.has(toolCall.id)) {
        context.addIssue({
          code: 'custom',
          path: ['messages', messageIndex, 'toolCalls', toolCallIndex, 'id'],
          message: 'historical tool call IDs must be unique across a request',
        });
      }
      toolCallIDs.add(toolCall.id);
    }
  }
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
  selectedBackend: z.enum(LanguageModelBackend),
  finishReason: z.literal('stop'),
  constraintMode: z.enum(StructuredOutputConstraintMode).optional(),
  usage: languageModelUsageSchema.optional(),
});

export const chatCompletionResponseSchema = z.looseObject({
  finishReason: z.enum(ChatCompletionFinishReason),
  provider: z.string().trim().min(1),
  model: z.string().trim().min(1),
  message: chatCompletionResponseMessageSchema,
  selectedBackend: z.enum(LanguageModelBackend),
  usage: languageModelUsageSchema.optional(),
  providerMetadata: jsonValueSchema.optional(),
}).superRefine((response, context) => {
  if (response.finishReason === ChatCompletionFinishReason.ToolCalls && (response.message.toolCalls === undefined || response.message.toolCalls.length === 0)) {
    context.addIssue({
      code: 'custom',
      path: ['message', 'toolCalls'],
      message: 'tool_calls finish reason requires at least one tool call',
    });
  }
});

export type StructuredResponseRequest = z.infer<typeof structuredResponseRequestSchema>;
export type StructuredResponse = z.infer<typeof structuredResponseSchema>;
export type ChatCompletionFunction = z.infer<typeof chatCompletionFunctionSchema>;
export type ChatCompletionTool = z.infer<typeof chatCompletionToolSchema>;
export type ChatCompletionToolCallFunction = z.infer<typeof chatCompletionToolCallFunctionSchema>;
export type ChatCompletionToolCall = z.infer<typeof chatCompletionToolCallSchema>;
export type ChatCompletionMessage = z.infer<typeof chatCompletionMessageSchema>;
export type ChatCompletionRequest = z.infer<typeof chatCompletionRequestSchema>;
export type ChatCompletionResponse = z.infer<typeof chatCompletionResponseSchema>;
export type StructuredOutputValidationIssue = z.infer<typeof structuredOutputValidationIssueSchema>;
export type StructuredOutputDiagnostic = z.infer<typeof structuredOutputDiagnosticSchema>;

function isJSONDocumentObject(value: string): boolean {
  try {
    const parsedValue: unknown = JSON.parse(value);
    return Boolean(parsedValue) && typeof parsedValue === 'object' && !Array.isArray(parsedValue);
  } catch {
    return false;
  }
}
