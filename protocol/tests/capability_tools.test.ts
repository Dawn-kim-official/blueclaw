import { describe, expect, test } from 'bun:test';

import {
  CalendarToolName,
  WorkspaceTaskSize,
  WorkspaceTaskStatus,
  buildCapabilityToolCatalog,
  calendarAddInputSchema,
  calendarDeleteInputSchema,
  calendarListInputSchema,
  calendarUpdateInputSchema,
  taskAddInputSchema,
  taskDeleteInputSchema,
  taskListInputSchema,
  taskUpdateInputSchema,
} from '../src/capability_tools.ts';
import { ResourceEffectIdentity } from '../src/capability.ts';
import { protocolVersion } from '../src/registry.ts';

describe('canonical capability tools', () => {
  test('defines the complete task tool family', () => {
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
});
