export async function loadOwnTaskRuns(taskSessionID: string): Promise<Array<Record<string, unknown>>> {
  const response = await fetch(`/tasks/api/list?taskSessionID=${encodeURIComponent(taskSessionID)}`);
  return response.json();
}

export async function loadOwnTaskRun(taskSessionID: string, taskRunID: string): Promise<Record<string, unknown>> {
  const response = await fetch(`/tasks/api/detail?taskSessionID=${encodeURIComponent(taskSessionID)}&taskRunID=${encodeURIComponent(taskRunID)}`);
  return response.json();
}

export async function cancelOwnTaskRun(taskSessionID: string, taskRunID: string): Promise<Response> {
  return fetch(`/tasks/api/cancel?taskSessionID=${encodeURIComponent(taskSessionID)}&taskRunID=${encodeURIComponent(taskRunID)}`, {
    method: "POST"
  });
}
