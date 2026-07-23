import { z } from 'zod';

import { nonNegativeIntegerSchema } from './common.ts';

export enum TaskStatus {
  Planned = 'planned',
  Running = 'running',
  WaitingUserInput = 'waiting_user_input',
  WaitingApproval = 'waiting_approval',
  Blocked = 'blocked',
  Interrupted = 'interrupted',
  Completed = 'completed',
  Failed = 'failed',
  Cancelled = 'cancelled',
}

export const taskStatusSchema = z.enum(TaskStatus);

export enum TaskAttemptStatus {
  Starting = 'starting',
  Running = 'running',
  Completed = 'completed',
  Failed = 'failed',
  Cancelled = 'cancelled',
  Interrupted = 'interrupted',
}

export const taskAttemptStatusSchema = z.enum(TaskAttemptStatus);

export enum TaskScheduleExecutionMode {
  Agent = 'agent',
  Message = 'message',
}

export const taskRunSchema = z.looseObject({
  taskRunID: z.string(),
  requesterPersonID: z.string(),
  originConversationID: z.string(),
  originReplyTargetID: z.string().optional(),
  originIsThread: z.boolean().optional(),
  currentAttemptID: z.string().optional(),
  currentAgentProfileName: z.string(),
  status: taskStatusSchema,
  prompt: z.string(),
  result: z.string(),
  failureReason: z.string(),
  createdAt: z.iso.datetime({ offset: true }),
  updatedAt: z.iso.datetime({ offset: true }),
});

export const taskAttemptSchema = z.looseObject({
  taskAttemptID: z.string(),
  taskRunID: z.string(),
  runnerID: z.string(),
  status: taskAttemptStatusSchema,
  startedAt: z.iso.datetime({ offset: true }),
  finishedAt: z.iso.datetime({ offset: true }).nullable().optional(),
  failureReason: z.string().optional(),
});

export const taskEventSchema = z.looseObject({
  taskEventID: z.string(),
  taskRunID: z.string(),
  name: z.string(),
  body: z.string(),
  createdAt: z.iso.datetime({ offset: true }),
});

export const taskArtifactSchema = z.looseObject({
  taskArtifactID: z.string(),
  taskRunID: z.string(),
  name: z.string(),
  body: z.string(),
});

const taskScheduleCommonSchema = z.looseObject({
  taskScheduleID: z.string(),
  creatorPersonID: z.string(),
  name: z.string(),
  prompt: z.string(),
  executionMode: z.enum(TaskScheduleExecutionMode),
  agentProfileName: z.string(),
  platform: z.string(),
  conversationID: z.string(),
  replyTargetID: z.string(),
  timeZone: z.string(),
  maxRunCount: nonNegativeIntegerSchema.optional(),
  completedRunCount: nonNegativeIntegerSchema,
  expiresAt: z.iso.datetime({ offset: true }).nullable(),
  nextRunAt: z.iso.datetime({ offset: true }).nullable(),
  lastRunAt: z.iso.datetime({ offset: true }).nullable(),
  lastTaskRunID: z.string(),
  leaseOwner: z.string(),
  leasedUntil: z.iso.datetime({ offset: true }).nullable(),
  failureCount: nonNegativeIntegerSchema,
  lastError: z.string(),
  nextAttemptAt: z.iso.datetime({ offset: true }).nullable(),
  createdAt: z.iso.datetime({ offset: true }),
  updatedAt: z.iso.datetime({ offset: true }),
});

export const taskScheduleSchema = z.discriminatedUnion('kind', [
  taskScheduleCommonSchema.extend({
    kind: z.literal('once'),
    runAt: z.iso.datetime({ offset: true }),
    intervalSecond: nonNegativeIntegerSchema,
    cronExpression: z.string(),
  }),
  taskScheduleCommonSchema.extend({
    kind: z.literal('interval'),
    runAt: z.iso.datetime({ offset: true }).nullable(),
    intervalSecond: z.number().int().positive(),
    cronExpression: z.string(),
  }),
  taskScheduleCommonSchema.extend({
    kind: z.literal('cron'),
    runAt: z.iso.datetime({ offset: true }).nullable(),
    intervalSecond: nonNegativeIntegerSchema,
    cronExpression: z.string().min(1),
  }),
]);

export type TaskRun = z.infer<typeof taskRunSchema>;
export type TaskSchedule = z.infer<typeof taskScheduleSchema>;
