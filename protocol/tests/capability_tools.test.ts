import { describe, expect, test } from 'bun:test';

import {
  ArtifactKind,
  ArtifactToolName,
  BrowserToolName,
  CalendarToolName,
  DocumentToolName,
  ImageToolName,
  SiteLifecycleStatus,
  SiteToolName,
  WebToolName,
  WorkspaceTaskInitialStatus,
  WorkspaceTaskSize,
  artifactReviewInputSchema,
  artifactReviewResultSchema,
  browserClickInputSchema,
  browserClickResultSchema,
  browserOpenInputSchema,
  browserOpenResultSchema,
  browserScreenshotInputSchema,
  browserScreenshotResultSchema,
  browserSnapshotInputSchema,
  browserSnapshotResultSchema,
  buildCapabilityToolCatalog,
  calendarAddInputSchema,
  calendarDeleteInputSchema,
  calendarListInputSchema,
  calendarUpdateInputSchema,
  documentReadInputSchema,
  documentReadResultSchema,
  imageReadInputSchema,
  imageReadResultSchema,
  siteCreateInputSchema,
  siteCreateResultSchema,
  siteDeleteInputSchema,
  siteDeleteResultSchema,
  sitePreviewInputSchema,
  sitePreviewResultSchema,
  sitePublishInputSchema,
  sitePublishResultSchema,
  siteStatusInputSchema,
  siteStatusResultSchema,
  taskAddInputSchema,
  taskDeleteInputSchema,
  taskListInputSchema,
  taskUpdateInputSchema,
  webSearchInputSchema,
  webSearchResultSchema,
} from '../src/capability_tools.ts';
import { CapabilitySideEffect, ResourceEffectIdentity } from '../src/capability.ts';
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
      WebToolName.Search,
      SiteToolName.Create,
      SiteToolName.Status,
      SiteToolName.Preview,
      SiteToolName.Publish,
      SiteToolName.Delete,
      DocumentToolName.Read,
      ImageToolName.Read,
      BrowserToolName.Open,
      BrowserToolName.Snapshot,
      BrowserToolName.Screenshot,
      BrowserToolName.Click,
      ArtifactToolName.Review,
    ]);
    expect(catalog.tools.every(tool => tool.inputSchemaStrict && tool.outputSchemaStrict)).toBe(true);
    expect(new Set(catalog.tools.map(tool => tool.name)).size).toBe(catalog.tools.length);
  });

  test('defines exact web search inputs and normalized results', () => {
    expect(webSearchInputSchema.safeParse({ query: 'internkim', limit: 3 }).success).toBe(true);
    expect(webSearchInputSchema.safeParse({ query: 'internkim', allowedDomains: ['internkim.example'] }).success).toBe(true);
    expect(webSearchInputSchema.safeParse({ query: 'internkim', limit: 0 }).success).toBe(false);
    expect(webSearchInputSchema.safeParse({ query: '   ' }).success).toBe(false);
    expect(webSearchInputSchema.safeParse({ query: 'internkim', unknown: true }).success).toBe(false);
    expect(webSearchInputSchema.safeParse({}).success).toBe(false);

    const result = {
      provider: 'openrouter',
      remoteLLMInvolved: true,
      compatibility: 'openrouter_server_tool_auto',
      query: 'internkim',
      answer: 'result',
      results: [{
        title: 'InternKim',
        url: 'https://internkim.example',
        snippet: 'An agent platform',
        source: 'internkim.example',
      }],
    };
    expect(webSearchResultSchema.safeParse(result).success).toBe(true);
    expect(webSearchResultSchema.safeParse({ ...result, extra: true }).success).toBe(false);
    expect(webSearchResultSchema.safeParse({ ...result, results: [{ title: 'InternKim' }] }).success).toBe(false);

    const catalog = buildCapabilityToolCatalog(protocolVersion);
    const descriptor = catalog.tools.find(tool => tool.name === WebToolName.Search);
    expect(descriptor?.resultContract?.effects).toEqual([]);
    expect(descriptor?.requiresApproval).toBeUndefined();
    expect(descriptor?.sideEffectClass).toBe(CapabilitySideEffect.Read);
  });

  test('defines exact browser inputs and successful results', () => {
    expect(browserOpenInputSchema.safeParse({ url: 'https://preview.example/site-1' }).success).toBe(true);
    expect(browserSnapshotInputSchema.safeParse({}).success).toBe(true);
    expect(browserScreenshotInputSchema.safeParse({ ttlSeconds: 300 }).success).toBe(true);
    expect(browserClickInputSchema.safeParse({ ref: '@e1' }).success).toBe(true);

    expect(browserOpenInputSchema.safeParse({ startURL: 'https://preview.example/site-1' }).success).toBe(false);
    expect(browserSnapshotInputSchema.safeParse({ interactive: true }).success).toBe(false);
    expect(browserScreenshotInputSchema.safeParse({ ttlSeconds: -1 }).success).toBe(false);
    expect(browserClickInputSchema.safeParse({}).success).toBe(false);

    expect(browserOpenResultSchema.safeParse({
      url: 'https://preview.example/site-1',
      requestedURL: 'https://preview.example/site-1',
      title: 'Quarterly support',
      snapshotText: '- button "Open report" [ref=e1]',
      interactiveRefs: ['@e1'],
      capturedAt: '2026-07-19T00:00:00Z',
    }).success).toBe(true);
    expect(browserSnapshotResultSchema.safeParse({
      url: 'https://preview.example/site-1',
      title: 'Quarterly support',
      snapshotText: '- button "Open report" [ref=e1]',
      interactiveRefs: ['@e1'],
      hasMore: false,
      capturedAt: '2026-07-19T00:00:00Z',
    }).success).toBe(true);
    expect(browserScreenshotResultSchema.safeParse({
      fileID: 'file-1',
      filename: 'site.png',
      sizeBytes: 1024,
      contentType: 'image/png',
      devicePath: '/tmp/internkim-companion-files/site.png',
      expiresAt: '2026-07-19T00:05:00Z',
      capturedAt: '2026-07-19T00:00:00Z',
    }).success).toBe(true);
    expect(browserClickResultSchema.safeParse({
      ok: true,
      action: 'click',
      target: '@e1',
      capturedAt: '2026-07-19T00:00:00Z',
    }).success).toBe(true);
    expect(browserClickResultSchema.safeParse({
      ok: true,
      action: 'fill',
      capturedAt: '2026-07-19T00:00:00Z',
    }).success).toBe(false);
  });

  test('defines exact artifact review evidence and result contracts', () => {
    const reviewInput = {
      artifactKind: ArtifactKind.Site,
      intent: 'Check the customer support landing page',
      rubric: 'Verify hierarchy, text fit, and primary interaction',
      evidence: [{
        role: 'desktopScreenshot',
        path: '/tmp/internkim-companion-files/site.png',
        mimeType: 'image/png',
        label: 'Desktop preview',
      }],
    };
    const reviewResult = {
      passed: false,
      issues: [{
        severity: 'warning',
        category: 'visualHierarchy',
        target: 'Primary action',
        message: 'The action is hard to distinguish.',
        suggestedFix: 'Increase contrast.',
      }],
      acceptedWarnings: [],
      summary: 'One visual hierarchy issue remains.',
    };

    expect(artifactReviewInputSchema.safeParse(reviewInput).success).toBe(true);
    expect(artifactReviewResultSchema.safeParse(reviewResult).success).toBe(true);
    expect(artifactReviewInputSchema.safeParse({ ...reviewInput, evidence: [] }).success).toBe(false);
    expect(artifactReviewInputSchema.safeParse({
      ...reviewInput,
      evidence: [{ ...reviewInput.evidence[0], mimeType: 'text/html' }],
    }).success).toBe(false);
    expect(artifactReviewResultSchema.safeParse({
      ...reviewResult,
      issues: [{ ...reviewResult.issues[0], category: 'performance' }],
    }).success).toBe(false);

    const catalog = buildCapabilityToolCatalog(protocolVersion);
    for (const toolName of [
      BrowserToolName.Open,
      BrowserToolName.Snapshot,
      BrowserToolName.Screenshot,
      BrowserToolName.Click,
      ArtifactToolName.Review,
    ]) {
      expect(catalog.tools.find(tool => tool.name === toolName)?.resultContract?.effects).toEqual([]);
    }
    expect(catalog.tools.find(tool => tool.name === ArtifactToolName.Review)?.resultContract?.evidenceCondition).toEqual({
      resultField: 'passed',
      equals: true,
    });
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
      status: WorkspaceTaskInitialStatus.Planned,
      endDate: '2026-07-24',
    })).toEqual({
      title: '고객지원 분기 결산 누락 항목 확인',
      size: WorkspaceTaskSize.Small,
      status: WorkspaceTaskInitialStatus.Planned,
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

  test('keeps the site workflow explicit without a duplicate edit tool', () => {
    const catalog = buildCapabilityToolCatalog(protocolVersion);
    const siteTools = catalog.tools.filter(tool => tool.namespace === 'site');

    expect(siteTools.map(tool => tool.name)).toEqual([
      SiteToolName.Create,
      SiteToolName.Status,
      SiteToolName.Preview,
      SiteToolName.Publish,
      SiteToolName.Delete,
    ]);
    expect(catalog.tools.some(tool => tool.name === 'site.edit')).toBe(false);
    expect(siteTools.every(tool => tool.resultContract !== undefined)).toBe(true);
    expect(siteTools.every(tool => tool.resultContract?.schema.additionalProperties === false)).toBe(true);
  });

  test('requires exact site identities for lifecycle mutations', () => {
    expect(siteCreateInputSchema.safeParse({
      slug: 'customer-support-quarterly',
      title: '고객지원 분기 결산',
      prompt: '고객지원팀이 분기별 문의량과 미해결 이슈를 공유하는 한 페이지 사이트를 만들어줘.',
    }).success).toBe(true);
    expect(siteStatusInputSchema.safeParse({ siteReference: 'customer-support-quarterly' }).success).toBe(true);
    expect(siteStatusInputSchema.safeParse({ siteReference: 'site-1', checkLive: true }).success).toBe(true);
    expect(sitePreviewInputSchema.safeParse({ siteID: 'site-1' }).success).toBe(true);
    expect(sitePublishInputSchema.safeParse({ siteID: 'site-1', message: 'Update quarterly totals' }).success).toBe(true);
    expect(siteDeleteInputSchema.safeParse({ siteID: 'site-1', reason: 'The campaign ended.' }).success).toBe(true);

    expect(siteStatusInputSchema.safeParse({}).success).toBe(false);
    expect(siteStatusInputSchema.safeParse({ siteReference: ' site-1 ' }).success).toBe(false);
    expect(siteStatusInputSchema.safeParse({ siteID: 'site-1' }).success).toBe(false);
    expect(siteStatusInputSchema.safeParse({ slug: 'customer-support-quarterly' }).success).toBe(false);
    expect(sitePreviewInputSchema.safeParse({ slug: 'customer-support-quarterly' }).success).toBe(false);
    expect(sitePublishInputSchema.safeParse({ slug: 'customer-support-quarterly' }).success).toBe(false);
    expect(siteDeleteInputSchema.safeParse({}).success).toBe(false);
    expect(siteDeleteInputSchema.safeParse({ siteID: 'site-1', userConfirmed: true }).success).toBe(false);
    expect(siteDeleteInputSchema.safeParse({ siteID: 'site-1', slug: 'customer-support-quarterly' }).success).toBe(false);
  });

  test('requires operation-specific site result shapes', () => {
    const createResult = {
      siteID: 'site-1',
      slug: 'customer-support-quarterly',
      title: '고객지원 분기 결산',
      status: SiteLifecycleStatus.Draft,
      sourceWorkspacePath: '/workspace/circles/staff/sites/customer-support-quarterly/draft',
      appWorkspacePath: '/workspace/circles/staff/sites/customer-support-quarterly/draft/app',
    };
    const statusResult = {
      ...createResult,
      workspaceHealth: 'healthy',
    };
    const previewResult = {
      siteID: 'site-1',
      status: SiteLifecycleStatus.Draft,
      sourceWorkspacePath: createResult.sourceWorkspacePath,
      previewID: 'preview-1',
      previewURL: 'https://preview.example/site-1',
      previewExpiresAt: '2026-07-19T12:00:00Z',
    };
    const publishResult = {
      siteID: 'site-1',
      status: SiteLifecycleStatus.Published,
      sourceWorkspacePath: createResult.sourceWorkspacePath,
      sourceSHA256: '254cc09182b94752e96474af9ba307f74dcfff4e8dfa5b0c4a76f97e634c1c28',
      publishedURL: 'https://customer-support-quarterly.example',
      currentVersionID: 'revision-1',
    };
    const deleteResult = { siteID: 'site-1', deleted: true };

    expect(siteCreateResultSchema.safeParse(createResult).success).toBe(true);
    expect(siteStatusResultSchema.safeParse(statusResult).success).toBe(true);
    expect(sitePreviewResultSchema.safeParse(previewResult).success).toBe(true);
    expect(sitePublishResultSchema.safeParse(publishResult).success).toBe(true);
    expect(siteDeleteResultSchema.safeParse(deleteResult).success).toBe(true);

    expect(siteCreateResultSchema.safeParse({ siteID: 'site-1', status: 'draft' }).success).toBe(false);
    expect(siteCreateResultSchema.safeParse({ ...createResult, appWorkspacePath: undefined }).success).toBe(false);
    expect(siteStatusResultSchema.safeParse({ ...statusResult, sourceWorkspacePath: undefined }).success).toBe(false);
    expect(sitePreviewResultSchema.safeParse({ ...previewResult, previewURL: undefined }).success).toBe(false);
    expect(sitePublishResultSchema.safeParse({ ...publishResult, publishedURL: undefined }).success).toBe(false);
    expect(sitePublishResultSchema.safeParse({ ...publishResult, sourceSHA256: undefined }).success).toBe(false);
    expect(siteDeleteResultSchema.safeParse({ siteID: 'site-1', status: 'deleted' }).success).toBe(false);
    expect(sitePublishResultSchema.safeParse({ ...publishResult, extra: true }).success).toBe(false);
  });

  test('publishes exact site effects and delete completion evidence', () => {
    const catalog = buildCapabilityToolCatalog(protocolVersion);
    const createTool = catalog.tools.find(tool => tool.name === SiteToolName.Create);
    const statusTool = catalog.tools.find(tool => tool.name === SiteToolName.Status);
    const previewTool = catalog.tools.find(tool => tool.name === SiteToolName.Preview);
    const publishTool = catalog.tools.find(tool => tool.name === SiteToolName.Publish);
    const deleteTool = catalog.tools.find(tool => tool.name === SiteToolName.Delete);

    expect(createTool?.resultContract?.effects).toEqual([
      { objectType: 'website', effect: 'created', resultField: 'siteID', effectIdentity: ResourceEffectIdentity.ID },
    ]);
    expect(statusTool?.resultContract?.effects).toEqual([]);
    expect(previewTool?.resultContract?.effects).toEqual([
      { objectType: 'website', effect: 'previewed', resultField: 'siteID', effectIdentity: ResourceEffectIdentity.ID },
    ]);
    expect(publishTool?.resultContract?.effects).toEqual([
      { objectType: 'website', effect: 'published', resultField: 'siteID', effectIdentity: ResourceEffectIdentity.ID },
      { objectType: 'website', effect: 'published', resultField: 'publishedURL', effectIdentity: ResourceEffectIdentity.URL },
    ]);
    expect(deleteTool?.resultContract?.effects).toEqual([
      { objectType: 'website', effect: 'deleted', resultField: 'siteID', effectIdentity: ResourceEffectIdentity.ID },
    ]);
    expect(deleteTool?.requiresApproval).toBe(true);
    expect(deleteTool?.completionEvidence).toEqual({
      mode: 'success',
      action: 'delete_site',
      targetKind: 'site',
    });
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
