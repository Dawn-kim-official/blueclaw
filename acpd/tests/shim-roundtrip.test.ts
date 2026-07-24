import { describe, expect, test } from 'bun:test';
import { createAcpAgentCore } from '../src/acp-agent.ts';
import { createJSONRPCPeer } from '../src/jsonrpc.ts';

describe('daemon socket line handling', () => {
  test('a large request split across chunks still parses', async () => {
    const connection = createAcpAgentCore({
      blueclawEventsURL: 'http://127.0.0.1:8080/connectors/buzz/events',
      blueclawTaskCancelURL: 'http://127.0.0.1:8080/admin/api/task/cancel',
      blueclawTaskEventsURL: 'http://127.0.0.1:8080/tasks/api/events',
      buzzCommand: 'buzz',
      listenPort: 18091,
      socketPath: '/tmp/acpd-test.sock',
      maximumTurnHoldSeconds: 3300,
      accountEmailByPubkey: {},
      accountLinksPath: undefined,
    }).createConnection(() => {});
    const written: string[] = [];
    const peer = createJSONRPCPeer((line) => written.push(line), connection.requestHandlers, connection.notificationHandlers);

    const bigPrompt = JSON.stringify({
      jsonrpc: '2.0',
      id: 7,
      method: 'session/prompt',
      params: { sessionId: 'missing', prompt: [{ type: 'text', text: 'x'.repeat(64 * 1024) }] },
    }) + '\n';
    let buffered = '';
    for (let offset = 0; offset < bigPrompt.length; offset += 1024) {
      buffered += bigPrompt.slice(offset, offset + 1024);
      let newlineIndex = buffered.indexOf('\n');
      while (newlineIndex >= 0) {
        peer.handleLine(buffered.slice(0, newlineIndex));
        buffered = buffered.slice(newlineIndex + 1);
        newlineIndex = buffered.indexOf('\n');
      }
    }
    await Bun.sleep(5);
    expect(written).toHaveLength(1);
    expect(written[0]).toContain('unknown session');
  });
});
