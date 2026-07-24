export type JSONRPCMessage = {
  jsonrpc: '2.0';
  id?: number | string;
  method?: string;
  params?: unknown;
  result?: unknown;
  error?: { code: number; message: string };
};

export type RequestHandler = (params: unknown) => Promise<unknown>;
export type NotificationHandler = (params: unknown) => void;

export type JSONRPCPeer = {
  notify: (method: string, params: unknown) => void;
  handleLine: (line: string) => void;
};

export function createJSONRPCPeer(
  writeLine: (line: string) => void,
  requestHandlers: Record<string, RequestHandler>,
  notificationHandlers: Record<string, NotificationHandler>,
): JSONRPCPeer {
  function send(message: JSONRPCMessage): void {
    writeLine(JSON.stringify(message));
  }

  function handleLine(line: string): void {
    if (line.trim() === '') return;
    const message = parseMessage(line);
    if (!message) return;
    if (message.method === undefined) return;
    if (message.id === undefined) {
      notificationHandlers[message.method]?.(message.params);
      return;
    }
    void respondToRequest(message.id, message.method, message.params);
  }

  async function respondToRequest(id: number | string, method: string, params: unknown): Promise<void> {
    const handler = requestHandlers[method];
    if (!handler) {
      send({ jsonrpc: '2.0', id, error: { code: -32601, message: `method not found: ${method}` } });
      return;
    }
    try {
      send({ jsonrpc: '2.0', id, result: await handler(params) });
    } catch (error) {
      send({ jsonrpc: '2.0', id, error: { code: -32603, message: error instanceof Error ? error.message : String(error) } });
    }
  }

  return {
    notify: (method, params) => send({ jsonrpc: '2.0', method, params }),
    handleLine,
  };
}

function parseMessage(line: string): JSONRPCMessage | null {
  try {
    const parsed: unknown = JSON.parse(line);
    if (typeof parsed !== 'object' || parsed === null) return null;
    return parsed as JSONRPCMessage;
  } catch {
    return null;
  }
}

export async function pumpStandardInputLines(handleLine: (line: string) => void): Promise<void> {
  const decoder = new TextDecoder();
  let buffered = '';
  for await (const chunk of Bun.stdin.stream()) {
    buffered += decoder.decode(chunk, { stream: true });
    let newlineIndex = buffered.indexOf('\n');
    while (newlineIndex >= 0) {
      handleLine(buffered.slice(0, newlineIndex));
      buffered = buffered.slice(newlineIndex + 1);
      newlineIndex = buffered.indexOf('\n');
    }
  }
}
