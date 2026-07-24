export type TaskActivityUpdate = {
  sessionUpdate: 'tool_call' | 'tool_call_update';
  toolCallId: string;
  title?: string;
  kind?: string;
  status: 'pending' | 'completed';
};

type ParsedTaskEvent = { name: string; body: string };

const TOOL_EVENT_PATTERN = /^tool\.(.+)\.(requested|result|cancelled)$/;

export function parseTaskEventStream(streamText: string): ParsedTaskEvent[] {
  const events: ParsedTaskEvent[] = [];
  for (const block of streamText.split('\n\n')) {
    const nameMatch = /^event: (.+)$/m.exec(block);
    const bodyMatch = /^data: (.*)$/m.exec(block);
    if (!nameMatch?.[1]) continue;
    events.push({ name: nameMatch[1], body: bodyMatch?.[1] ?? '' });
  }
  return events;
}

export function toolActivityUpdates(events: ParsedTaskEvent[], taskRunID: string): TaskActivityUpdate[] {
  const updates: TaskActivityUpdate[] = [];
  const requestCountByTool: Record<string, number> = {};
  const resultCountByTool: Record<string, number> = {};
  for (const event of events) {
    const toolMatch = TOOL_EVENT_PATTERN.exec(event.name);
    if (!toolMatch?.[1] || !toolMatch[2]) continue;
    const toolName = toolMatch[1];
    if (toolMatch[2] === 'requested') {
      const occurrence = (requestCountByTool[toolName] = (requestCountByTool[toolName] ?? 0) + 1);
      updates.push({
        sessionUpdate: 'tool_call',
        toolCallId: `${taskRunID}:${toolName}:${occurrence}`,
        title: toolName,
        kind: 'other',
        status: 'pending',
      });
      continue;
    }
    const occurrence = (resultCountByTool[toolName] = (resultCountByTool[toolName] ?? 0) + 1);
    updates.push({
      sessionUpdate: 'tool_call_update',
      toolCallId: `${taskRunID}:${toolName}:${occurrence}`,
      status: 'completed',
    });
  }
  return updates;
}

export function createTaskActivityPoller(
  taskEventsURL: string,
  taskRunID: string,
  emit: (update: TaskActivityUpdate) => void,
): () => void {
  let emittedUpdateCount = 0;
  let isStopped = false;
  const intervalHandle = setInterval(async () => {
    if (isStopped) return;
    try {
      const response = await fetch(`${taskEventsURL}?taskRunID=${encodeURIComponent(taskRunID)}`);
      if (!response.ok) return;
      const updates = toolActivityUpdates(parseTaskEventStream(await response.text()), taskRunID);
      for (const update of updates.slice(emittedUpdateCount)) {
        emit(update);
      }
      emittedUpdateCount = updates.length;
    } catch {
      return;
    }
  }, 2000);
  return () => {
    isStopped = true;
    clearInterval(intervalHandle);
  };
}
