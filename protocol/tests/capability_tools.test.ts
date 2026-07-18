import { describe, expect, test } from 'bun:test';

import {
  WorkspaceTaskSize,
  WorkspaceTaskStatus,
  buildCapabilityToolCatalog,
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
    expect(catalog.tools.map(tool => tool.name)).toEqual(['task.add', 'task.list', 'task.update', 'task.delete']);
    expect(catalog.tools.every(tool => tool.inputSchemaStrict && tool.outputSchemaStrict)).toBe(true);
    expect(catalog.tools.every(tool => tool.policyResource === `tool:${tool.name}`)).toBe(true);
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
    expect(taskUpdateInputSchema.safeParse({ query: '결산', content: '수정' }).success).toBe(false);
    expect(taskDeleteInputSchema.safeParse({ taskID: 'task-1', query: '결산' }).success).toBe(false);
  });
});
