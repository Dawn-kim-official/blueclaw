import { z } from 'zod';

import { ExecutionMode, jsonValueSchema, nonNegativeIntegerSchema, resourceScopeSchema } from './common.ts';

export enum CapabilityEstimatedLatency {
  Low = 'low',
  Medium = 'medium',
  High = 'high',
  Interactive = 'interactive',
}

export const completionEvidenceDescriptorSchema = z.looseObject({
  mode: z.string().optional(),
  action: z.string().optional(),
  targetKind: z.string().optional(),
});

export const capabilityDescriptorSchema = z.looseObject({
  name: z.string(),
  description: z.string().optional(),
  version: z.string(),
  privacyClass: z.string(),
  estimatedLatency: z.enum(CapabilityEstimatedLatency),
  requiresUserPresence: z.boolean(),
  worksOffline: z.boolean(),
  inputSchema: jsonValueSchema.optional(),
  outputSchema: jsonValueSchema.optional(),
  policyResource: z.string().optional(),
  sideEffectClass: z.string().optional(),
  requiresApproval: z.boolean().optional(),
  completionEvidence: completionEvidenceDescriptorSchema.optional(),
});

export const capabilityRegistryResponseSchema = z.looseObject({
  localOnly: z.boolean(),
  routingCandidates: z.array(z.string()).nullable(),
  deviceCapabilities: z.array(capabilityDescriptorSchema).optional(),
  companionStatus: z.string().optional(),
  companionCapabilities: z.array(capabilityDescriptorSchema).optional(),
  capabilities: z.array(capabilityDescriptorSchema).optional(),
});

export const toolInvokeContextSchema = z.looseObject({
  requesterPersonID: z.string().optional(),
  requesterEmail: z.string().optional(),
  requesterName: z.string().optional(),
  requesterPlatformUserID: z.string().optional(),
  taskSource: z.string().optional(),
  isScheduledRun: z.boolean().optional(),
  isApprovalContinuation: z.boolean().optional(),
  conversationID: z.string().optional(),
  conversationType: z.string().optional(),
  channelID: z.string().optional(),
  channelName: z.string().optional(),
  replyTargetID: z.string().optional(),
  platform: z.string().optional(),
});

export const actorContextSchema = z.looseObject({
  personID: z.string().optional(),
  email: z.string().optional(),
  displayName: z.string().optional(),
  source: z.string().optional(),
  scopes: z.array(z.string()).optional(),
  isAdmin: z.boolean().optional(),
});

export const toolInvokeRequestSchema = z.looseObject({
  toolName: z.string(),
  input: jsonValueSchema,
  idempotencyKey: z.string().optional(),
  context: toolInvokeContextSchema.optional(),
  actor: actorContextSchema.optional(),
  executionMode: z.enum(ExecutionMode).optional(),
  requiresUserPresence: z.boolean().optional(),
  privacyClass: z.string().optional(),
  sessionID: z.string().optional(),
  parentJobID: z.string().optional(),
  grantID: z.string().optional(),
  resourceScope: resourceScopeSchema.optional(),
  timeoutSecond: nonNegativeIntegerSchema.optional(),
});

export const toolInvokeResponseSchema = z.looseObject({
  provider: z.string(),
  selectedBackend: z.string(),
  toolName: z.string(),
  status: z.string().optional(),
  content: z.string().optional(),
  isError: z.boolean().optional(),
  message: z.string().optional(),
  errorCode: z.string().optional(),
  failureStage: z.string().optional(),
  retryable: z.boolean().optional(),
  safeRetry: z.boolean().optional(),
  result: jsonValueSchema,
});

export type CapabilityDescriptor = z.infer<typeof capabilityDescriptorSchema>;
export type ToolInvokeRequest = z.infer<typeof toolInvokeRequestSchema>;
export type ToolInvokeResponse = z.infer<typeof toolInvokeResponseSchema>;
