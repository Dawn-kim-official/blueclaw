import { z } from 'zod';

import {
  CapabilityAvailabilityState,
  CapabilityEstimatedLatency,
  CapabilityModelVisibility,
  CapabilitySideEffect,
  ResourceEffectIdentity,
  capabilityDescriptorSchema,
  resourceEffectContractSchema,
  toolInvokeResponseSchema,
} from './capability.ts';
import { jsonValueSchema } from './common.ts';

const dateDescription = 'Date in YYYY-MM-DD format.';
const resourceIDSchema = z.string()
  .min(1)
  .regex(/^\S(?:.*\S)?$/, 'Resource identity must not have leading or trailing whitespace.');
const sha256Schema = z.string().regex(/^[a-f0-9]{64}$/);

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

export enum WorkspaceTaskInitialStatus {
  Planned = WorkspaceTaskStatus.Planned,
  InProgress = WorkspaceTaskStatus.InProgress,
  Completed = WorkspaceTaskStatus.Completed,
  Paused = WorkspaceTaskStatus.Paused,
  Rejected = WorkspaceTaskStatus.Rejected,
  Cancelled = WorkspaceTaskStatus.Cancelled,
}

export enum WorkspaceTaskScope {
  Self = 'self',
  All = 'all',
}

export enum CalendarToolName {
  Add = 'calendar.add',
  List = 'calendar.list',
  Update = 'calendar.update',
  Delete = 'calendar.delete',
}

export enum DocumentToolName {
  Read = 'document.read',
}

export enum ImageToolName {
  Read = 'image.read',
}

export enum BrowserToolName {
  Open = 'browser.open',
  Snapshot = 'browser.snapshot',
  Screenshot = 'browser.screenshot',
  Click = 'browser.click',
}

export enum ArtifactToolName {
  Review = 'artifact.review',
}

export enum ArtifactKind {
  Site = 'site',
  Slides = 'slides',
  PowerPoint = 'pptx',
  Word = 'docx',
  PDF = 'pdf',
}

export enum ArtifactIssueSeverity {
  Blocking = 'blocking',
  Warning = 'warning',
  Information = 'info',
}

export enum ArtifactIssueCategory {
  TextFit = 'textFit',
  Layout = 'layout',
  VisualHierarchy = 'visualHierarchy',
  ContentDensity = 'contentDensity',
  TemplateSmell = 'templateSmell',
  Responsiveness = 'responsiveness',
  RenderFidelity = 'renderFidelity',
}

export enum SiteToolName {
  Create = 'site.create',
  Status = 'site.status',
  Preview = 'site.preview',
  Publish = 'site.publish',
  Delete = 'site.delete',
}

export enum SiteLifecycleStatus {
  Draft = 'draft',
  Publishing = 'publishing',
  Published = 'published',
  Unpublished = 'unpublished',
  Failed = 'failed',
}

export enum ResourceMutationEffect {
  Created = 'created',
  Updated = 'updated',
  Previewed = 'previewed',
  Published = 'published',
  Deleted = 'deleted',
}

export enum CalendarReminderLeadHours {
  One = 1,
  Two = 2,
  Three = 3,
  Six = 6,
  Twelve = 12,
  TwentyFour = 24,
  FortyEight = 48,
}

const calendarReminderLeadHourValues: readonly number[] = [
  CalendarReminderLeadHours.One,
  CalendarReminderLeadHours.Two,
  CalendarReminderLeadHours.Three,
  CalendarReminderLeadHours.Six,
  CalendarReminderLeadHours.Twelve,
  CalendarReminderLeadHours.TwentyFour,
  CalendarReminderLeadHours.FortyEight,
];

const workspaceTaskSizeSchema = z.enum(WorkspaceTaskSize);
const workspaceTaskStatusSchema = z.enum(WorkspaceTaskStatus);
const calendarReminderLeadHoursSchema = z.number()
  .refine(value => calendarReminderLeadHourValues.includes(value))
  .meta({ enum: calendarReminderLeadHourValues });

const taskParticipantSchema = z.strictObject({
  personID: z.string().optional(),
  displayName: z.string().optional(),
  email: z.string().optional(),
  mattermostUsername: z.string().optional(),
  mention: z.string().optional(),
});

export const taskResultSchema = z.strictObject({
  taskID: resourceIDSchema,
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
  status: z.enum(WorkspaceTaskInitialStatus)
    .describe('Initial task status. Defaults to 예정. The runtime may change delegated tasks to 요청.')
    .optional(),
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

const taskUpdateObjectSchema = z.strictObject({
  taskID: resourceIDSchema.describe('Exact ID of the task to update, copied from a task.list result.'),
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

export const taskUpdateInputSchema = taskUpdateObjectSchema
  .refine(hasMutationField, 'At least one task field must be updated.')
  .meta({ minProperties: 2 });

export const taskDeleteInputSchema = z.strictObject({
  taskID: resourceIDSchema.describe('Exact ID of the task to delete, copied from a task.list result.'),
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
  taskID: resourceIDSchema,
  deleted: z.literal(true),
});

const calendarParticipantResultSchema = z.strictObject({
  personID: z.string().optional(),
  name: z.string(),
  email: z.string().optional(),
});

export const calendarEventResultSchema = z.strictObject({
  eventID: resourceIDSchema,
  title: z.string(),
  description: z.string(),
  location: z.string(),
  startISO: z.string(),
  endISO: z.string(),
  timeZone: z.string(),
  isAllDay: z.boolean(),
  color: z.string(),
  people: z.array(z.string()),
  participants: z.array(calendarParticipantResultSchema),
  reminderLeadHours: calendarReminderLeadHoursSchema,
  updatedAt: z.string(),
});

const calendarMutableFields = {
  title: z.string().describe('New event title. Omit to leave unchanged.').optional(),
  description: z.string().describe('New event notes or agenda. Use an empty string to clear them.').optional(),
  location: z.string().describe('New physical or virtual location. Use an empty string to clear it.').optional(),
  startISO: z.string().describe('New event start as ISO 8601 with timezone. Omit to leave unchanged.').optional(),
  endISO: z.string().describe('New event end as ISO 8601 with timezone. Omit to leave unchanged.').optional(),
  timeZone: z.string().describe('New IANA timezone identifier. Omit to leave unchanged.').optional(),
  isAllDay: z.boolean().describe('Whether the event is all day. Omit to leave unchanged.').optional(),
  color: z.string().describe('New provider-supported color label. Omit to leave unchanged.').optional(),
  people: z.array(z.string()).describe('Replacement attendee hints such as names, @handles, or emails.').optional(),
  includeRequester: z.boolean().describe('Whether the requester should be included as an attendee.').optional(),
  reminderLeadHours: calendarReminderLeadHoursSchema.describe('Reminder lead time in hours. Omit to leave unchanged.').optional(),
};

export const calendarAddInputSchema = z.strictObject({
  title: z.string().describe('Event title shown in the calendar.'),
  description: z.string().describe('Optional event notes or agenda visible to attendees.').optional(),
  location: z.string().describe('Optional physical or virtual location.').optional(),
  startISO: z.string().describe('Event start as ISO 8601 with timezone. Resolve relative times before calling.'),
  endISO: z.string().describe('Event end as ISO 8601 with timezone. It must be after startISO.'),
  timeZone: z.string().describe('IANA timezone identifier. Defaults to the workspace timezone.').optional(),
  isAllDay: z.boolean().describe('Set true for an all-day event.').optional(),
  color: z.string().describe('Optional provider-supported color label.').optional(),
  people: z.array(z.string()).describe('Attendee hints such as names, @handles, or emails.').optional(),
  includeRequester: z.boolean().describe('Set false when the requester is not an attendee. Defaults to true.').optional(),
  reminderLeadHours: calendarReminderLeadHoursSchema.describe('Reminder lead time in hours.').optional(),
});

export const calendarListInputSchema = z.strictObject({
  startISO: z.string().describe('Inclusive start of the time window as ISO 8601 with timezone.').optional(),
  endISO: z.string().describe('Exclusive end of the time window as ISO 8601 with timezone.').optional(),
  query: z.string().describe('Optional free-text filter matched against event titles, descriptions, and locations.').optional(),
  limit: z.number().positive().refine(Number.isInteger, 'Limit must be a whole number.').describe('Maximum number of events to return.').optional(),
});

const calendarUpdateObjectSchema = z.strictObject({
  eventID: resourceIDSchema.describe('Exact event ID copied from a calendar.list result.'),
  ...calendarMutableFields,
});

export const calendarUpdateInputSchema = calendarUpdateObjectSchema
  .refine(hasMutationField, 'At least one calendar event field must be updated.')
  .meta({ minProperties: 2 });

export const calendarDeleteInputSchema = z.strictObject({
  eventID: resourceIDSchema.describe('Exact event ID copied from a calendar.list result.'),
});

export const calendarListResultSchema = z.strictObject({
  events: z.array(calendarEventResultSchema),
});

export const calendarDeleteResultSchema = z.strictObject({
  eventID: resourceIDSchema,
  deleted: z.literal(true),
});

const siteSlugSchema = z.string()
  .min(1)
  .regex(/^[a-z0-9]+(?:-[a-z0-9]+)*$/, 'Site slug must use lowercase letters, numbers, and single hyphens.');

const siteContentSectionSchema = z.strictObject({
  title: z.string(),
  body: z.string(),
});

const siteContentSchema = z.strictObject({
  siteName: z.string(),
  tagline: z.string().optional(),
  heroActionLabel: z.string().optional(),
  heroActionHref: z.string().optional(),
  sections: z.array(siteContentSectionSchema).min(1),
});

export const siteCreateInputSchema = z.strictObject({
  slug: siteSlugSchema.describe('Unique URL-safe site identifier, such as team-dashboard.'),
  title: z.string().describe('Human-readable site name.').optional(),
  prompt: z.string().describe('Natural-language description of the site to build.').optional(),
  designBrief: z.string().describe('Visual style and layout guidance.').optional(),
  prototypeScope: z.string().describe('Scope limit for the initial implementation.').optional(),
  description: z.string().describe('Short public-facing description.').optional(),
  idea: z.string().describe('Core concept or value proposition.').optional(),
  purpose: z.string().describe('Why the site exists and who it serves.').optional(),
  audience: z.string().describe('Intended audience.').optional(),
  archetype: z.string().describe('Site archetype, such as dashboard or landing-page.').optional(),
  domainKeywords: z.array(z.string()).describe('Domain terms that should inform the site.').optional(),
  content: siteContentSchema.describe('Structured content for a basic content site.').optional(),
});

export const siteStatusInputSchema = z.strictObject({
  siteReference: resourceIDSchema.describe('Exact siteID or exact slug from the request or an earlier site result.'),
  checkLive: z.boolean().describe('Probe the published URL in addition to reading persisted status.').optional(),
});

export const sitePreviewInputSchema = z.strictObject({
  siteID: resourceIDSchema.describe('Exact site ID from site.create or site.status.'),
  previewID: resourceIDSchema.describe('Existing preview ID to refresh. Omit to create a new preview.').optional(),
});

export const sitePublishInputSchema = z.strictObject({
  siteID: resourceIDSchema.describe('Exact site ID from site.create or site.status.'),
  message: z.string().describe('Short revision message describing the edits being published.').optional(),
  previewID: resourceIDSchema.describe('Preview ID whose reviewed source should be published.').optional(),
});

export const siteDeleteInputSchema = z.strictObject({
  siteID: resourceIDSchema.describe('Exact site ID from site.status.'),
  reason: z.string().describe('Reason shown in the approval prompt.').optional(),
});

export const siteCreateResultSchema = z.strictObject({
  siteID: resourceIDSchema,
  slug: siteSlugSchema,
  title: z.string(),
  status: z.literal(SiteLifecycleStatus.Draft),
  sourceWorkspacePath: resourceIDSchema,
  appWorkspacePath: resourceIDSchema,
});

export const siteStatusResultSchema = z.strictObject({
  siteID: resourceIDSchema,
  slug: siteSlugSchema,
  title: z.string(),
  status: z.enum(SiteLifecycleStatus),
  sourceWorkspacePath: resourceIDSchema,
  appWorkspacePath: resourceIDSchema.optional(),
  publishedURL: resourceIDSchema.optional(),
  previewURL: resourceIDSchema.optional(),
  workspaceHealth: z.string().optional(),
  lastError: z.string().optional(),
  updatedAt: z.string().optional(),
  liveHTTPStatus: z.number().int().optional(),
});

export const sitePreviewResultSchema = z.strictObject({
  siteID: resourceIDSchema,
  status: z.enum(SiteLifecycleStatus),
  sourceWorkspacePath: resourceIDSchema,
  previewID: resourceIDSchema,
  previewURL: resourceIDSchema,
  previewExpiresAt: z.string(),
});

export const sitePublishResultSchema = z.strictObject({
  siteID: resourceIDSchema,
  status: z.literal(SiteLifecycleStatus.Published),
  sourceWorkspacePath: resourceIDSchema,
  sourceSHA256: sha256Schema,
  publishedURL: resourceIDSchema,
  currentVersionID: resourceIDSchema,
});

export const siteDeleteResultSchema = z.strictObject({
  siteID: resourceIDSchema,
  deleted: z.literal(true),
});

export const documentReadInputSchema = z.strictObject({
  path: resourceIDSchema.describe('Exact absolute /workspace path of the document to read.'),
  maxPages: z.number().int().min(1).max(500).describe('Maximum PDF pages to extract. Omit for the runtime default.').optional(),
  maxOutputBytes: z.number().int().min(1024).max(1000000).describe('Maximum Markdown bytes to return. Omit for the runtime default.').optional(),
});

export const documentReadResultSchema = z.strictObject({
  status: z.literal('ok'),
  path: resourceIDSchema,
  format: z.literal('markdown'),
  content: z.string(),
  warnings: z.array(z.string()),
  truncated: z.boolean(),
  backend: z.string().optional(),
  model: z.string().optional(),
});

const imageReadAttachmentSchema = z.strictObject({
  devicePath: resourceIDSchema,
  filename: resourceIDSchema,
  contentType: resourceIDSchema,
  sizeBytes: z.number().int().nonnegative(),
  contentBase64: z.string().min(1),
});

export const imageReadInputSchema = z.strictObject({
  path: resourceIDSchema.describe('Exact absolute /workspace path of the image to read.'),
});

export const imageReadResultSchema = z.strictObject({
  status: z.literal('ok'),
  path: resourceIDSchema,
  attachments: z.array(imageReadAttachmentSchema).min(1),
});

export const browserOpenInputSchema = z.strictObject({
  url: resourceIDSchema.describe('Absolute HTTP or HTTPS URL to open.'),
});

export const browserOpenResultSchema = z.strictObject({
  url: resourceIDSchema,
  requestedURL: resourceIDSchema,
  title: z.string().optional(),
  snapshotText: z.string().optional(),
  interactiveRefs: z.array(resourceIDSchema).optional(),
  capturedAt: resourceIDSchema,
});

export const browserSnapshotInputSchema = z.strictObject({});

export const browserSnapshotResultSchema = z.strictObject({
  url: resourceIDSchema.optional(),
  title: z.string().optional(),
  snapshotText: z.string(),
  interactiveRefs: z.array(resourceIDSchema),
  hasMore: z.boolean(),
  capturedAt: resourceIDSchema,
});

export const browserScreenshotInputSchema = z.strictObject({
  ttlSeconds: z.number().int().nonnegative().optional(),
});

export const browserScreenshotResultSchema = z.strictObject({
  fileID: resourceIDSchema,
  filename: resourceIDSchema,
  sizeBytes: z.number().int().nonnegative(),
  contentType: resourceIDSchema,
  devicePath: resourceIDSchema,
  expiresAt: resourceIDSchema,
  capturedAt: resourceIDSchema,
});

export const browserClickInputSchema = z.strictObject({
  target: resourceIDSchema.optional(),
  ref: resourceIDSchema.optional(),
  selector: resourceIDSchema.optional(),
}).refine(hasAnyField, 'A browser target, ref, or selector is required.')
  .meta({ minProperties: 1 });

export const browserClickResultSchema = z.strictObject({
  ok: z.literal(true),
  action: z.literal('click'),
  target: resourceIDSchema,
  capturedAt: resourceIDSchema,
});

const artifactReviewIssueSchema = z.strictObject({
  severity: z.enum(ArtifactIssueSeverity),
  category: z.enum(ArtifactIssueCategory),
  target: z.string(),
  message: z.string(),
  suggestedFix: z.string(),
});

export const artifactReviewInputSchema = z.strictObject({
  artifactKind: z.enum(ArtifactKind),
  intent: resourceIDSchema,
  rubric: resourceIDSchema,
  evidence: z.array(z.strictObject({
    role: resourceIDSchema,
    path: resourceIDSchema,
    mimeType: z.enum(['image/png', 'image/jpeg']),
    label: resourceIDSchema,
  })).min(1).max(8),
  expectedText: z.array(z.strictObject({
    target: resourceIDSchema,
    text: z.string(),
  })).optional(),
  previousIssues: z.array(artifactReviewIssueSchema).optional(),
});

export const artifactReviewResultSchema = z.strictObject({
  passed: z.boolean(),
  issues: z.array(artifactReviewIssueSchema),
  acceptedWarnings: z.array(z.string()),
  summary: z.string(),
});

function hasMutationField(document: object): boolean {
  return Object.keys(document).length > 1;
}

function hasAnyField(document: object): boolean {
  return Object.keys(document).length > 0;
}

type CapabilityResultDefinition = {
  schema: z.ZodType;
  effects: Array<z.infer<typeof resourceEffectContractSchema>>;
  evidenceCondition?: {
    resultField: string;
    equals: z.infer<typeof jsonValueSchema>;
  };
};

type CapabilityToolDefinition = {
  name: string;
  namespace: string;
  privacyClass: string;
  policyResource: string;
  description: string;
  version: string;
  estimatedLatency: CapabilityEstimatedLatency;
  inputSchema: z.ZodType;
  result: CapabilityResultDefinition;
  sideEffect: CapabilitySideEffect;
  requiresApproval?: boolean;
  requiresUserPresence?: boolean;
  completionEvidence?: {
    mode: string;
    action: string;
    targetKind: string;
  };
};

const taskToolDefinitions: CapabilityToolDefinition[] = [
  {
    name: 'task.add',
    namespace: 'task',
    privacyClass: 'workspace_task',
    policyResource: 'tool:task.add',
    description: 'Create a new workspace task with typed task fields. Use this to add a todo or assignment for the requester or another team member. Do not use this to update an existing task — use task.update.',
    version: '3',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: taskAddInputSchema,
    result: {
      schema: taskResultSchema,
      effects: [{
        objectType: 'task',
        effect: ResourceMutationEffect.Created,
        resultField: 'taskID',
        effectIdentity: ResourceEffectIdentity.ID,
      }],
    },
    sideEffect: CapabilitySideEffect.WorkspaceWrite,
    completionEvidence: { mode: 'success', action: 'write_task', targetKind: 'task' },
  },
  {
    name: 'task.list',
    namespace: 'task',
    privacyClass: 'workspace_task',
    policyResource: 'tool:task.list',
    description: "List workspace tasks with optional filters. Use this to answer 'what tasks does X have', 'what is on my plate', or 'show incomplete items this week'. The default scope is the requester; set scope to all for the whole workspace.",
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Low,
    inputSchema: taskListInputSchema,
    result: { schema: taskListResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.Read,
  },
  {
    name: 'task.update',
    namespace: 'task',
    privacyClass: 'workspace_task',
    policyResource: 'tool:task.update',
    description: 'Update explicit fields on an existing task. taskID must be copied from a task.list result; use task.list first when the ID is unknown. At least one mutable field is required.',
    version: '3',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: taskUpdateInputSchema,
    result: {
      schema: taskResultSchema,
      effects: [{
        objectType: 'task',
        effect: ResourceMutationEffect.Updated,
        resultField: 'taskID',
        effectIdentity: ResourceEffectIdentity.ID,
      }],
    },
    sideEffect: CapabilitySideEffect.WorkspaceWrite,
    completionEvidence: { mode: 'success', action: 'write_task', targetKind: 'task' },
  },
  {
    name: 'task.delete',
    namespace: 'task',
    privacyClass: 'workspace_task',
    policyResource: 'tool:task.delete',
    description: 'Permanently delete a task by the exact taskID from a task.list result. Use task.list first when the ID is unknown. Requires approval; this action is irreversible.',
    version: '3',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: taskDeleteInputSchema,
    result: {
      schema: taskDeleteResultSchema,
      effects: [{
        objectType: 'task',
        effect: ResourceMutationEffect.Deleted,
        resultField: 'taskID',
        effectIdentity: ResourceEffectIdentity.ID,
      }],
    },
    sideEffect: CapabilitySideEffect.Destructive,
    requiresApproval: true,
    completionEvidence: { mode: 'success', action: 'delete_task', targetKind: 'task' },
  },
];

const calendarToolDefinitions: CapabilityToolDefinition[] = [
  {
    name: CalendarToolName.Add,
    namespace: 'calendar',
    privacyClass: 'workspace_calendar',
    policyResource: 'tool:calendar.add',
    description: 'Create a calendar event with a concrete time range. Resolve natural-language dates and times before calling. Use calendar.update for an existing event.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: calendarAddInputSchema,
    result: {
      schema: calendarEventResultSchema,
      effects: [{
        objectType: 'calendar',
        effect: ResourceMutationEffect.Created,
        resultField: 'eventID',
        effectIdentity: ResourceEffectIdentity.ID,
      }],
    },
    sideEffect: CapabilitySideEffect.WorkspaceWrite,
    completionEvidence: { mode: 'success', action: 'write_calendar', targetKind: 'calendar' },
  },
  {
    name: CalendarToolName.List,
    namespace: 'calendar',
    privacyClass: 'workspace_calendar',
    policyResource: 'tool:calendar.list',
    description: 'List calendar events in a concrete time window, optionally filtered by title, description, or location. Resolve natural-language dates to startISO and endISO before calling.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Low,
    inputSchema: calendarListInputSchema,
    result: { schema: calendarListResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.Read,
  },
  {
    name: CalendarToolName.Update,
    namespace: 'calendar',
    privacyClass: 'workspace_calendar',
    policyResource: 'tool:calendar.update',
    description: 'Update explicit fields on a calendar event. eventID must be copied from a calendar.list result, and at least one mutable field is required.',
    version: '3',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: calendarUpdateInputSchema,
    result: {
      schema: calendarEventResultSchema,
      effects: [{
        objectType: 'calendar',
        effect: ResourceMutationEffect.Updated,
        resultField: 'eventID',
        effectIdentity: ResourceEffectIdentity.ID,
      }],
    },
    sideEffect: CapabilitySideEffect.WorkspaceWrite,
    completionEvidence: { mode: 'success', action: 'write_calendar', targetKind: 'calendar' },
  },
  {
    name: CalendarToolName.Delete,
    namespace: 'calendar',
    privacyClass: 'workspace_calendar',
    policyResource: 'tool:calendar.delete',
    description: 'Permanently delete a calendar event by the exact eventID from a calendar.list result. Requires approval; this action is irreversible.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: calendarDeleteInputSchema,
    result: {
      schema: calendarDeleteResultSchema,
      effects: [{
        objectType: 'calendar',
        effect: ResourceMutationEffect.Deleted,
        resultField: 'eventID',
        effectIdentity: ResourceEffectIdentity.ID,
      }],
    },
    sideEffect: CapabilitySideEffect.Destructive,
    requiresApproval: true,
    completionEvidence: { mode: 'success', action: 'write_calendar', targetKind: 'calendar' },
  },
];

const siteToolDefinitions: CapabilityToolDefinition[] = [
  {
    name: SiteToolName.Create,
    namespace: 'site',
    privacyClass: 'workspace_site',
    policyResource: 'tool:site.create',
    description: 'Create a site workspace and return its exact siteID and sourceWorkspacePath. Use file.edit on that path for subsequent source changes.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: siteCreateInputSchema,
    result: {
      schema: siteCreateResultSchema,
      effects: [{
        objectType: 'website',
        effect: ResourceMutationEffect.Created,
        resultField: 'siteID',
        effectIdentity: ResourceEffectIdentity.ID,
      }],
    },
    sideEffect: CapabilitySideEffect.WorkspaceWrite,
    completionEvidence: { mode: 'success', action: 'create_site', targetKind: 'site' },
  },
  {
    name: SiteToolName.Status,
    namespace: 'site',
    privacyClass: 'workspace_site',
    policyResource: 'tool:site.status',
    description: 'Read one site by exact siteID or slug and return its sourceWorkspacePath, lifecycle status, and published or preview URLs.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Low,
    inputSchema: siteStatusInputSchema,
    result: { schema: siteStatusResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.Read,
  },
  {
    name: SiteToolName.Preview,
    namespace: 'site',
    privacyClass: 'workspace_site',
    policyResource: 'tool:site.preview',
    description: 'Create a reviewable preview for the exact siteID after source changes have been made with file.edit.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.High,
    inputSchema: sitePreviewInputSchema,
    result: {
      schema: sitePreviewResultSchema,
      effects: [{
        objectType: 'website',
        effect: ResourceMutationEffect.Previewed,
        resultField: 'siteID',
        effectIdentity: ResourceEffectIdentity.ID,
      }],
    },
    sideEffect: CapabilitySideEffect.ExternalPublish,
    completionEvidence: { mode: 'success', action: 'preview_site', targetKind: 'site' },
  },
  {
    name: SiteToolName.Publish,
    namespace: 'site',
    privacyClass: 'workspace_site',
    policyResource: 'tool:site.publish',
    description: 'Publish the exact siteID after editing and optional preview verification, returning the public URL and deployed version.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.High,
    inputSchema: sitePublishInputSchema,
    result: {
      schema: sitePublishResultSchema,
      effects: [{
        objectType: 'website',
        effect: ResourceMutationEffect.Published,
        resultField: 'siteID',
        effectIdentity: ResourceEffectIdentity.ID,
      }],
    },
    sideEffect: CapabilitySideEffect.SitePublish,
    completionEvidence: { mode: 'success', action: 'publish_site', targetKind: 'site' },
  },
  {
    name: SiteToolName.Delete,
    namespace: 'site',
    privacyClass: 'workspace_site',
    policyResource: 'tool:site.delete',
    description: 'Permanently delete the exact siteID after explicit runtime approval.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: siteDeleteInputSchema,
    result: {
      schema: siteDeleteResultSchema,
      effects: [{
        objectType: 'website',
        effect: ResourceMutationEffect.Deleted,
        resultField: 'siteID',
        effectIdentity: ResourceEffectIdentity.ID,
      }],
    },
    sideEffect: CapabilitySideEffect.Destructive,
    requiresApproval: true,
    requiresUserPresence: true,
    completionEvidence: { mode: 'success', action: 'delete_site', targetKind: 'site' },
  },
];

const fileToolDefinitions: CapabilityToolDefinition[] = [
  {
    name: DocumentToolName.Read,
    namespace: 'document',
    privacyClass: 'workspace_document',
    policyResource: 'tool:document.read',
    description: 'Read a workspace document from an exact /workspace path and return Markdown content. Use image.read for image files.',
    version: '1',
    estimatedLatency: CapabilityEstimatedLatency.High,
    inputSchema: documentReadInputSchema,
    result: { schema: documentReadResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.Read,
  },
  {
    name: ImageToolName.Read,
    namespace: 'image',
    privacyClass: 'workspace_document',
    policyResource: 'tool:image.read',
    description: 'Read a workspace image from an exact /workspace path and return a base64 attachment. Use document.read for document files.',
    version: '1',
    estimatedLatency: CapabilityEstimatedLatency.Medium,
    inputSchema: imageReadInputSchema,
    result: { schema: imageReadResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.Read,
  },
];

const browserToolDefinitions: CapabilityToolDefinition[] = [
  {
    name: BrowserToolName.Open,
    namespace: 'browser',
    privacyClass: 'user_browser',
    policyResource: 'tool:browser.open',
    description: 'Open an exact HTTP or HTTPS URL in the available browser and return the resulting page identity and initial structure.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Interactive,
    inputSchema: browserOpenInputSchema,
    result: { schema: browserOpenResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.Connect,
    requiresUserPresence: true,
  },
  {
    name: BrowserToolName.Snapshot,
    namespace: 'browser',
    privacyClass: 'user_browser',
    policyResource: 'tool:browser.snapshot',
    description: 'Read the current browser page structure and return stable interactive references for inspection and control.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Interactive,
    inputSchema: browserSnapshotInputSchema,
    result: { schema: browserSnapshotResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.Read,
  },
  {
    name: BrowserToolName.Screenshot,
    namespace: 'browser',
    privacyClass: 'user_browser',
    policyResource: 'tool:browser.screenshot',
    description: 'Capture the visible browser page and upload it to a temporary workspace-visible device path for visual review.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Interactive,
    inputSchema: browserScreenshotInputSchema,
    result: { schema: browserScreenshotResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.Read,
  },
  {
    name: BrowserToolName.Click,
    namespace: 'browser',
    privacyClass: 'user_browser',
    policyResource: 'tool:browser.click',
    description: 'Click one exact target from the current browser snapshot and return the completed action.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.Interactive,
    inputSchema: browserClickInputSchema,
    result: { schema: browserClickResultSchema, effects: [] },
    sideEffect: CapabilitySideEffect.ExternalWrite,
  },
];

const artifactToolDefinitions: CapabilityToolDefinition[] = [
  {
    name: ArtifactToolName.Review,
    namespace: 'artifact',
    privacyClass: 'workspace_document',
    policyResource: 'tool:artifact.review',
    description: 'Review rendered artifact screenshots against a concrete intent and rubric, returning typed visual issues and suggested fixes.',
    version: '2',
    estimatedLatency: CapabilityEstimatedLatency.High,
    inputSchema: artifactReviewInputSchema,
    result: {
      schema: artifactReviewResultSchema,
      effects: [],
      evidenceCondition: { resultField: 'passed', equals: true },
    },
    sideEffect: CapabilitySideEffect.Read,
  },
];

const capabilityToolDefinitions = [
  ...taskToolDefinitions,
  ...calendarToolDefinitions,
  ...siteToolDefinitions,
  ...fileToolDefinitions,
  ...browserToolDefinitions,
  ...artifactToolDefinitions,
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
export type CalendarAddInput = z.infer<typeof calendarAddInputSchema>;
export type CalendarListInput = z.infer<typeof calendarListInputSchema>;
export type CalendarUpdateInput = z.infer<typeof calendarUpdateInputSchema>;
export type CalendarDeleteInput = z.infer<typeof calendarDeleteInputSchema>;
export type CalendarEventResult = z.infer<typeof calendarEventResultSchema>;
export type SiteCreateInput = z.infer<typeof siteCreateInputSchema>;
export type SiteStatusInput = z.infer<typeof siteStatusInputSchema>;
export type SitePreviewInput = z.infer<typeof sitePreviewInputSchema>;
export type SitePublishInput = z.infer<typeof sitePublishInputSchema>;
export type SiteDeleteInput = z.infer<typeof siteDeleteInputSchema>;
export type SiteCreateResult = z.infer<typeof siteCreateResultSchema>;
export type SiteStatusResult = z.infer<typeof siteStatusResultSchema>;
export type SitePreviewResult = z.infer<typeof sitePreviewResultSchema>;
export type SitePublishResult = z.infer<typeof sitePublishResultSchema>;
export type SiteDeleteResult = z.infer<typeof siteDeleteResultSchema>;
export type DocumentReadInput = z.infer<typeof documentReadInputSchema>;
export type DocumentReadResult = z.infer<typeof documentReadResultSchema>;
export type ImageReadInput = z.infer<typeof imageReadInputSchema>;
export type ImageReadResult = z.infer<typeof imageReadResultSchema>;
export type BrowserOpenInput = z.infer<typeof browserOpenInputSchema>;
export type BrowserOpenResult = z.infer<typeof browserOpenResultSchema>;
export type BrowserSnapshotInput = z.infer<typeof browserSnapshotInputSchema>;
export type BrowserSnapshotResult = z.infer<typeof browserSnapshotResultSchema>;
export type BrowserScreenshotInput = z.infer<typeof browserScreenshotInputSchema>;
export type BrowserScreenshotResult = z.infer<typeof browserScreenshotResultSchema>;
export type BrowserClickInput = z.infer<typeof browserClickInputSchema>;
export type BrowserClickResult = z.infer<typeof browserClickResultSchema>;
export type ArtifactReviewInput = z.infer<typeof artifactReviewInputSchema>;
export type ArtifactReviewResult = z.infer<typeof artifactReviewResultSchema>;

export function buildCapabilityToolCatalog(protocolVersion: string): CapabilityToolCatalog {
  return {
    protocolVersion,
    tools: capabilityToolDefinitions.map(definition => buildCapabilityDescriptor(definition)),
  };
}

function buildCapabilityDescriptor(definition: CapabilityToolDefinition): z.infer<typeof capabilityDescriptorSchema> {
  const resultContract = {
    schema: z.toJSONSchema(definition.result.schema),
    effects: definition.result.effects,
    evidenceCondition: definition.result.evidenceCondition,
  };
  return capabilityDescriptorSchema.parse({
    name: definition.name,
    canonicalName: definition.name,
    namespace: definition.namespace,
    modelName: definition.name,
    modelVisibility: CapabilityModelVisibility.Visible,
    modelVisible: true,
    description: definition.description,
    version: definition.version,
    privacyClass: definition.privacyClass,
    estimatedLatency: definition.estimatedLatency,
    requiresUserPresence: definition.requiresUserPresence ?? false,
    worksOffline: false,
    inputSchema: z.toJSONSchema(definition.inputSchema),
    outputSchema: z.toJSONSchema(toolInvokeResponseSchema),
    inputSchemaStrict: true,
    outputSchemaStrict: true,
    resultContract,
    policyResource: definition.policyResource,
    sideEffectClass: definition.sideEffect,
    sideEffect: definition.sideEffect,
    requiresApproval: definition.requiresApproval,
    completionEvidence: definition.completionEvidence,
    availability: { state: CapabilityAvailabilityState.Available },
    idempotency: { supported: false, required: false, scope: 'operation' },
  });
}
