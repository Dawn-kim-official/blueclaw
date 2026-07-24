import { describe, expect, test } from 'bun:test';
import { parseTaskEventStream, toolActivityUpdates } from '../src/task-activity.ts';

const STREAM = [
  'event: task.created\ndata: {"prompt":"hello"}',
  'event: tool.calendar.list.requested\ndata: {"input":{}}',
  'event: tool.calendar.list.result\ndata: {"count":3}',
  'event: tool.calendar.list.requested\ndata: {"input":{}}',
].join('\n\n');

describe('task activity mapping', () => {
  test('parses sse blocks into named events', () => {
    const events = parseTaskEventStream(STREAM);
    expect(events.map((event) => event.name)).toEqual([
      'task.created',
      'tool.calendar.list.requested',
      'tool.calendar.list.result',
      'tool.calendar.list.requested',
    ]);
  });

  test('maps tool events to acp tool call updates with stable ids', () => {
    const updates = toolActivityUpdates(parseTaskEventStream(STREAM), 'run-1');
    expect(updates).toEqual([
      { sessionUpdate: 'tool_call', toolCallId: 'run-1:calendar.list:1', title: 'calendar.list', kind: 'other', status: 'pending' },
      { sessionUpdate: 'tool_call_update', toolCallId: 'run-1:calendar.list:1', status: 'completed' },
      { sessionUpdate: 'tool_call', toolCallId: 'run-1:calendar.list:2', title: 'calendar.list', kind: 'other', status: 'pending' },
    ]);
  });
});
