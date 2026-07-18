import { z } from 'zod';

import {
  CapabilityAvailabilityState,
  CapabilityEstimatedLatency,
  CapabilityModelVisibility,
  CapabilitySideEffect,
  ResourceEffectIdentity,
  capabilityDescriptorSchema,
  toolInvokeResponseSchema,
} from './capability.ts';

const dateDescription = 'Date in YYYY-MM-DD format.';

export enum WorkspaceTaskSize {
  ExtraSmall = 'XS',
  Small = 'S',
  Medium = 'M',
  Large = 'L',
  ExtraLarge = 'XL',
  ExtraExtraLarge = 'XXL',
}

export enum WorkspaceTaskStatus {
  Planned = '예정',
  InProgress = '진행',
  Completed = '완료',
  Requested = '요청',
  Paused = '일시정지',
  Rejected = '기각',
  Cancelled = '중단',
}

export enum WorkspaceTaskScope {
  Self = 'self',
  All = 'all',
}

const workspaceTaskSizeSchema = z.enum(WorkspaceTaskSize);
const workspaceTaskStatusSchema = z.enum(WorkspaceTaskStatus);

const taskParticipantSchema = z.strictObject({
  personID: z.string().optional(),
  displayName: z.string().optional(),
  email: z.string().optional(),
  mattermostUsername: z.string().optional(),
  mention: z.string().optional(),
});

export const taskResultSchema = z.strictObject({
  taskID: z.string(),
  ownerID: z.string().optional(),
  ownerName: z.string().optional(),
  participantIDs: z.array(z.string()).optional(),
  participantNames: z.array(z.string()).optional(),
  participantPresentations: z.array(taskParticipantSchema).optional(),
  business: z.string().optional(),
  type: z.string().optional(),
  content: z.string().optional(),
  goal: z.string().optional(),
  size: z.string().optional(),
  status: z.string().optional(),
  startDate: z.string().optional(),
  endDate: z.string().optional(),
  weekCode: z.string().optional(),
  flag: z.number().int().optional(),
  requestReason: z.string().optional(),
  decisionReason: z.string().optional(),
  mattermostPostID: z.string().optional(),
});

export const taskAddInputSchema = z.strictObject({
  title: z.string().describe(
    "Concise task title. Preserve the user's exact title when they provide one; otherwise derive it directly from their request.",
  ),
  goal: z.string().describe('Definition of done or desired outcome. Omit when the title already states the complete outcome.').optional(),
  size: workspaceTaskSizeSchema
    .describe('Effort size using the work-size rubric. Omit when the request does not support a useful estimate.')
    .optional(),
  status: z.enum([
    WorkspaceTaskStatus.Planned,
    WorkspaceTaskStatus.InProgress,
    WorkspaceTaskStatus.Completed,
    WorkspaceTaskStatus.Paused,
    WorkspaceTaskStatus.Rejected,
    WorkspaceTaskStatus.Cancelled,
  ]).describe('Initial task status. Defaults to 예정. The runtime may change delegated tasks to 요청.').optional(),
  startDate: z.string().describe(`${dateDescription} Resolve relative dates from the current date. Omit when the user did not specify one.`).optional(),
  endDate: z.string().describe(`Due ${dateDescription.toLowerCase()} Resolve relative dates from the current date. Omit when the user did not specify one.`).optional(),
  targetPersonHint: z.string()
    .describe("Name or email of the person the task belongs to, e.g. 'Alice' or 'alice@example.com'. Leave empty to assign to the requester themselves.")
    .optional(),
  participantPersonHints: z.array(z.string())
    .describe('Names, @handles, or emails of additional participants explicitly named by the user. The owner is included automatically.')
    .optional(),
});

export const taskListInputSchema = z.strictObject({
  query: z.string()
    .describe("Free-text keyword filter matched against task titles and content, e.g. 'budget'. Do not put dates, week codes, or person names here — use the dedicated fields instead.")
    .optional(),
  targetPersonHint: z.string().describe('Name or email of a specific person whose tasks to list. Leave empty to use scope.').optional(),
  scope: z.enum(WorkspaceTaskScope)
    .describe('Whose tasks to list when targetPersonHint is empty. Defaults to self. Use all only for an explicit workspace-wide request.')
    .optional(),
  weekFrom: z.number()
    .describe('Start of the week range as an offset from this week: 0 this week, -1 last week, 1 next week. Omit both weekFrom and weekTo to list the current week; widen the range for other periods.')
    .optional(),
  weekTo: z.number().describe('End of the week range as an offset from this week. Omit both weekFrom and weekTo to list the current week.').optional(),
  status: z.string()
    .describe("Filter by task status. Accepted values: '예정', '진행', '완료', '요청', '일시정지', '기각', '중단' (or English equivalents: 'planned', 'in_progress', 'done', 'requested', 'paused', 'rejected', 'cancelled'). Leave empty to return all statuses.")
    .optional(),
  limit: z.number().describe('Maximum number of tasks to return. Defaults to 50.').optional(),
});

export const taskUpdateInputSchema = z.strictObject({
  taskID: z.string().describe('Exact ID of the task to update, copied from a task.list result.'),
  title: z.string().describe('New task title. Omit to leave the title unchanged.').optional(),
  goal: z.string().describe('Definition of done or success criterion for this task. Omit to leave unchanged.').optional(),
  status: workspaceTaskStatusSchema.describe('New task status. Omit to leave unchanged.').optional(),
  size: workspaceTaskSizeSchema.describe('Effort size estimate. Omit to leave unchanged.').optional(),
  category: z.string().describe('Business category label for the task. Omit to leave unchanged.').optional(),
  type: z.string().describe("Task type classification, e.g. 'task', 'milestone'. Omit to leave unchanged.").optional(),
  startDate: z.string().describe(`${dateDescription} Omit to leave unchanged.`).optional(),
  endDate: z.string().describe(`Due ${dateDescription.toLowerCase()} Omit to leave unchanged.`).optional(),
  flag: z.number().describe('Numeric flag bitmask for internal task classification. Omit to leave unchanged.').optional(),
  requestReason: z.string().describe('Reason for requesting this task. Omit to leave unchanged.').optional(),
  decisionReason: z.string().describe('Reason for the approval or rejection decision. Omit to leave unchanged.').optional(),
});

export const taskDeleteInputSchema = z.strictObject({
  taskID: z.string().describe('Exact ID of the task to delete, copied from a task.list result.'),
});

export const taskListResultSchema = z.strictObject({
  tasks: z.array(taskResultSchema),
  count: z.number().int(),
  scope: z.string(),
  weekFrom: z.number().int().optional(),
  weekTo: z.number().int().optional(),
  statusFilter: z.string().optional(),
  ownerID: z.string().optional(),
});

export const taskDeleteResultSchema = z.strictObject({
  taskID: z.string(),
  deleted: z.literal(true),
});

type CapabilityToolDefinition = {
  name: string;
  description: string;
  version: string;
  estimatedLatency: CapabilityEstimatedLatency;
  inputSchema: z.ZodType;
  resultSchema: z.ZodType;
  sideEffect: CapabilitySideEffect;
  requiresApproval?: boolean;
  completionEvidence?: {
    mode: string;
    action: string;
    targetKind: string;
  };
  effect?: string;
};

const taskToolDefinitions: CapabilityToolDefinition[] = [
  {
    name: 'task.add',
    description: 'Create a new workspace task with typed task fields. Use this to add a todo or assignment for the requester or another team member. Do not use this to update an existing task — use task.update.',
    version: '3',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: taskAddInputSchema,
    resultSchema: taskResultSchema,
    sideEffect: CapabilitySideEffect.WorkspaceWrite,
    completionEvidence: { mode: 'success', action: 'write_task', targetKind: 'task' },
    effect: 'created',
  },
  {
    name: 'task.list',
    description: "List workspace tasks with optional filters. Use this to answer 'what tasks does X have', 'what is on my plate', or 'show incomplete items this week'. The default scope is the requester; set scope to all for the whole workspace.",
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Low,
    inputSchema: taskListInputSchema,
    resultSchema: taskListResultSchema,
    sideEffect: CapabilitySideEffect.Read,
  },
  {
    name: 'task.update',
    description: 'Update explicit fields on an existing task. taskID must be copied from a task.list result; use task.list first when the ID is unknown. At least one mutable field is required.',
    version: '3',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: taskUpdateInputSchema,
    resultSchema: taskResultSchema,
    sideEffect: CapabilitySideEffect.WorkspaceWrite,
    completionEvidence: { mode: 'success', action: 'write_task', targetKind: 'task' },
    effect: 'updated',
  },
  {
    name: 'task.delete',
    description: 'Permanently delete a task by the exact taskID from a task.list result. Use task.list first when the ID is unknown. Requires approval; this action is irreversible.',
    version: '3',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: taskDeleteInputSchema,
    resultSchema: taskDeleteResultSchema,
    sideEffect: CapabilitySideEffect.Destructive,
    requiresApproval: true,
    completionEvidence: { mode: 'success', action: 'delete_task', targetKind: 'task' },
    effect: 'deleted',
  },
];

export type CapabilityToolCatalog = {
  protocolVersion: string;
  tools: Array<z.infer<typeof capabilityDescriptorSchema>>;
};

export type TaskAddInput = z.infer<typeof taskAddInputSchema>;
export type TaskListInput = z.infer<typeof taskListInputSchema>;
export type TaskUpdateInput = z.infer<typeof taskUpdateInputSchema>;
export type TaskDeleteInput = z.infer<typeof taskDeleteInputSchema>;
export type TaskResult = z.infer<typeof taskResultSchema>;

export function buildCapabilityToolCatalog(protocolVersion: string): CapabilityToolCatalog {
  return {
    protocolVersion,
    tools: taskToolDefinitions.map(definition => buildCapabilityDescriptor(definition)),
  };
}

function buildCapabilityDescriptor(definition: CapabilityToolDefinition): z.infer<typeof capabilityDescriptorSchema> {
  const namespace = definition.name.split('.')[0];
  const resultContract = {
    schema: z.toJSONSchema(definition.resultSchema),
    effects: definition.effect ? [{
      objectType: namespace,
      effect: definition.effect,
      resultField: `${namespace}ID`,
      effectIdentity: ResourceEffectIdentity.ID,
    }] : undefined,
  };
  return capabilityDescriptorSchema.parse({
    name: definition.name,
    canonicalName: definition.name,
    namespace,
    modelName: definition.name,
    modelVisibility: CapabilityModelVisibility.Visible,
    modelVisible: true,
    description: definition.description,
    version: definition.version,
    privacyClass: `workspace_${namespace}`,
    estimatedLatency: definition.estimatedLatency,
    requiresUserPresence: false,
    worksOffline: false,
    inputSchema: z.toJSONSchema(definition.inputSchema),
    outputSchema: z.toJSONSchema(toolInvokeResponseSchema),
    inputSchemaStrict: true,
    outputSchemaStrict: true,
    resultContract,
    policyResource: `tool:${definition.name}`,
    sideEffectClass: definition.sideEffect,
    sideEffect: definition.sideEffect,
    requiresApproval: definition.requiresApproval,
    completionEvidence: definition.completionEvidence,
    availability: { state: CapabilityAvailabilityState.Available },
    idempotency: { supported: false, required: false, scope: 'operation' },
  });
}
