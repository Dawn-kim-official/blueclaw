import { describe, expect, test } from 'bun:test';

import {
  CalendarToolName,
  DocumentToolName,
  ImageToolName,
  WorkspaceTaskSize,
  WorkspaceTaskStatus,
  buildCapabilityToolCatalog,
  calendarAddInputSchema,
  calendarDeleteInputSchema,
  calendarListInputSchema,
  calendarUpdateInputSchema,
  documentReadInputSchema,
  documentReadResultSchema,
  imageReadInputSchema,
  imageReadResultSchema,
  taskAddInputSchema,
  taskDeleteInputSchema,
  taskListInputSchema,
  taskUpdateInputSchema,
} from '../src/capability_tools.ts';
import { ResourceEffectIdentity } from '../src/capability.ts';
import { protocolVersion } from '../src/registry.ts';

describe('canonical capability tools', () => {
  test('defines the complete canonical tool family', () => {
    const catalog = buildCapabilityToolCatalog(protocolVersion);

    expect(catalog.protocolVersion).toBe(protocolVersion);
    expect(catalog.tools.map(tool => tool.name)).toEqual([
      'task.add',
      'task.list',
      'task.update',
      'task.delete',
      CalendarToolName.Add,
      CalendarToolName.List,
      CalendarToolName.Update,
      CalendarToolName.Delete,
      DocumentToolName.Read,
      ImageToolName.Read,
    ]);
    expect(catalog.tools.every(tool => tool.inputSchemaStrict && tool.outputSchemaStrict)).toBe(true);
    expect(new Set(catalog.tools.map(tool => tool.name)).size).toBe(catalog.tools.length);
  });

  test('keeps task mutation contracts exact', () => {
    const catalog = buildCapabilityToolCatalog(protocolVersion);
    const addTool = catalog.tools.find(tool => tool.name === 'task.add');
    const updateTool = catalog.tools.find(tool => tool.name === 'task.update');
    const deleteTool = catalog.tools.find(tool => tool.name === 'task.delete');

    expect(addTool?.resultContract?.effects).toEqual([
      { objectType: 'task', effect: 'created', resultField: 'taskID', effectIdentity: ResourceEffectIdentity.ID },
    ]);
    expect(updateTool?.resultContract?.effects).toEqual([
      { objectType: 'task', effect: 'updated', resultField: 'taskID', effectIdentity: ResourceEffectIdentity.ID },
    ]);
    expect(deleteTool?.resultContract?.effects).toEqual([
      { objectType: 'task', effect: 'deleted', resultField: 'taskID', effectIdentity: ResourceEffectIdentity.ID },
    ]);
    expect(deleteTool?.requiresApproval).toBe(true);
  });

  test('keeps calendar metadata and mutation contracts explicit', () => {
    const catalog = buildCapabilityToolCatalog(protocolVersion);
    const addTool = catalog.tools.find(tool => tool.name === CalendarToolName.Add);
    const listTool = catalog.tools.find(tool => tool.name === CalendarToolName.List);
    const updateTool = catalog.tools.find(tool => tool.name === CalendarToolName.Update);
    const deleteTool = catalog.tools.find(tool => tool.name === CalendarToolName.Delete);

    expect(addTool).toMatchObject({
      namespace: 'calendar',
      privacyClass: 'workspace_calendar',
      policyResource: 'tool:calendar.add',
    });
    expect(addTool?.resultContract?.effects).toEqual([
      { objectType: 'calendar', effect: 'created', resultField: 'eventID', effectIdentity: ResourceEffectIdentity.ID },
    ]);
    expect(listTool?.resultContract?.effects).toEqual([]);
    expect(updateTool?.resultContract?.effects).toEqual([
      { objectType: 'calendar', effect: 'updated', resultField: 'eventID', effectIdentity: ResourceEffectIdentity.ID },
    ]);
    expect(deleteTool?.resultContract?.effects).toEqual([
      { objectType: 'calendar', effect: 'deleted', resultField: 'eventID', effectIdentity: ResourceEffectIdentity.ID },
    ]);
    expect(deleteTool?.requiresApproval).toBe(true);
  });

  test('validates task inputs without operation aliases', () => {
    expect(taskAddInputSchema.parse({
      title: '고객지원 분기 결산 누락 항목 확인',
      size: WorkspaceTaskSize.Small,
      status: WorkspaceTaskStatus.Planned,
      endDate: '2026-07-24',
    })).toEqual({
      title: '고객지원 분기 결산 누락 항목 확인',
      size: WorkspaceTaskSize.Small,
      status: WorkspaceTaskStatus.Planned,
      endDate: '2026-07-24',
    });
    expect(taskListInputSchema.safeParse({ query: '결산', scope: 'self' }).success).toBe(true);
    expect(taskUpdateInputSchema.safeParse({ taskID: 'task-1', title: '수정된 제목' }).success).toBe(true);
    expect(taskDeleteInputSchema.safeParse({ taskID: 'task-1' }).success).toBe(true);

    expect(taskAddInputSchema.safeParse({ content: '잘못된 별칭' }).success).toBe(false);
    expect(taskUpdateInputSchema.safeParse({ taskID: 'task-1' }).success).toBe(false);
    expect(taskUpdateInputSchema.safeParse({ query: '결산', content: '수정' }).success).toBe(false);
    expect(taskDeleteInputSchema.safeParse({ taskID: 'task-1', query: '결산' }).success).toBe(false);
  });

  test('validates calendar inputs with exact mutation identities', () => {
    expect(calendarAddInputSchema.safeParse({
      title: '고객지원 주간 점검',
      startISO: '2026-07-24T14:00:00+09:00',
      endISO: '2026-07-24T15:00:00+09:00',
      people: ['support@example.com'],
    }).success).toBe(true);
    expect(calendarUpdateInputSchema.safeParse({
      eventID: 'event-1',
      startISO: '2026-07-24T15:00:00+09:00',
    }).success).toBe(true);
    expect(calendarDeleteInputSchema.safeParse({ eventID: 'event-1' }).success).toBe(true);
    expect(calendarListInputSchema.safeParse({ limit: 2 }).success).toBe(true);

    expect(calendarUpdateInputSchema.safeParse({ eventID: 'event-1' }).success).toBe(false);
    expect(calendarAddInputSchema.safeParse({
      title: '고객지원 주간 점검',
      startISO: '2026-07-24T14:00:00+09:00',
      endISO: '2026-07-24T15:00:00+09:00',
      reminderLeadHours: 4,
    }).success).toBe(false);
    expect(calendarUpdateInputSchema.safeParse({ query: '주간 점검', title: '변경' }).success).toBe(false);
    expect(calendarDeleteInputSchema.safeParse({ query: '주간 점검' }).success).toBe(false);
    expect(calendarListInputSchema.safeParse({ limit: 0 }).success).toBe(false);
    expect(calendarListInputSchema.safeParse({ limit: 1.5 }).success).toBe(false);
  });

  test('publishes provider-portable minimum mutation property counts', () => {
    const catalog = buildCapabilityToolCatalog(protocolVersion);
    const taskUpdateTool = catalog.tools.find(tool => tool.name === 'task.update');
    const calendarAddTool = catalog.tools.find(tool => tool.name === CalendarToolName.Add);
    const calendarUpdateTool = catalog.tools.find(tool => tool.name === CalendarToolName.Update);

    expect(taskUpdateTool?.inputSchema).toMatchObject({ minProperties: 2 });
    expect(calendarAddTool?.inputSchema).toMatchObject({
      properties: {
        reminderLeadHours: {
          type: 'number',
          enum: [1, 2, 3, 6, 12, 24, 48],
        },
      },
    });
    expect(calendarUpdateTool?.inputSchema).toMatchObject({ minProperties: 2 });
  });

  test('validates document and image read inputs without material aliases', () => {
    expect(documentReadInputSchema.safeParse({
      path: '/workspace/shared/report.pdf',
      maxPages: 10,
      maxOutputBytes: 200000,
    }).success).toBe(true);
    expect(imageReadInputSchema.safeParse({ path: '/workspace/shared/logo.png' }).success).toBe(true);

    expect(documentReadInputSchema.safeParse({ materialID: 'material-1' }).success).toBe(false);
    expect(documentReadInputSchema.safeParse({ path: '/workspace/shared/report.pdf', ocrMode: 'always' }).success).toBe(false);
    expect(documentReadInputSchema.safeParse({ path: '/workspace/shared/report.pdf', maxPages: 0 }).success).toBe(false);
    expect(documentReadInputSchema.safeParse({ path: '/workspace/shared/report.pdf', maxPages: 501 }).success).toBe(false);
    expect(documentReadInputSchema.safeParse({ path: '/workspace/shared/report.pdf', maxOutputBytes: 0 }).success).toBe(false);
    expect(documentReadInputSchema.safeParse({ path: '/workspace/shared/report.pdf', maxOutputBytes: 1 }).success).toBe(false);
    expect(imageReadInputSchema.safeParse({ materialID: 'material-1' }).success).toBe(false);
    expect(imageReadInputSchema.safeParse({ path: '/workspace/shared/logo.png', materialID: 'material-1' }).success).toBe(false);
  });

  test('requires exact document and image read result contracts', () => {
    const documentResult = {
      status: 'ok',
      path: '/workspace/shared/report.pdf',
      format: 'markdown',
      content: '# Report',
      warnings: [],
      truncated: false,
      backend: 'markitdown',
      model: 'no_ocr',
    };
    const imageResult = {
      status: 'ok',
      path: '/workspace/shared/logo.png',
      attachments: [{
        devicePath: '/workspace/shared/logo.png',
        filename: 'logo.png',
        contentType: 'image/png',
        sizeBytes: 3,
        contentBase64: 'YWJj',
      }],
    };

    expect(documentReadResultSchema.safeParse(documentResult).success).toBe(true);
    expect(imageReadResultSchema.safeParse(imageResult).success).toBe(true);
    expect(documentReadResultSchema.safeParse({ ...documentResult, warnings: undefined }).success).toBe(false);
    expect(documentReadResultSchema.safeParse({ ...documentResult, format: 'text' }).success).toBe(false);
    expect(documentReadResultSchema.safeParse({ ...documentResult, extra: true }).success).toBe(false);
    expect(imageReadResultSchema.safeParse({ ...imageResult, attachments: [{ ...imageResult.attachments[0], sizeBytes: -1 }] }).success).toBe(false);
    expect(imageReadResultSchema.safeParse({ ...imageResult, attachments: [{ ...imageResult.attachments[0], devicePath: undefined }] }).success).toBe(false);
    expect(imageReadResultSchema.safeParse({ ...imageResult, extra: true }).success).toBe(false);
  });

  test('publishes mandatory read result contracts without effects', () => {
    const catalog = buildCapabilityToolCatalog(protocolVersion);
    const documentTool = catalog.tools.find(tool => tool.name === DocumentToolName.Read);
    const imageTool = catalog.tools.find(tool => tool.name === ImageToolName.Read);

    expect(documentTool?.resultContract?.effects).toEqual([]);
    expect(imageTool?.resultContract?.effects).toEqual([]);
    expect(documentTool?.resultContract?.schema.required).toEqual([
      'status', 'path', 'format', 'content', 'warnings', 'truncated',
    ]);
    expect(imageTool?.resultContract?.schema.required).toEqual(['status', 'path', 'attachments']);
    expect(documentTool?.inputSchema.properties).not.toHaveProperty('materialID');
    expect(imageTool?.inputSchema.properties).not.toHaveProperty('materialID');
  });
});
