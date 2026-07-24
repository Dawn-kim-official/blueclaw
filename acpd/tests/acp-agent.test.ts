import { afterEach, describe, expect, test } from 'bun:test';
import { createAcpAgentCore } from '../src/acp-agent.ts';
import type { AcpdConfiguration } from '../src/configuration.ts';

const CHANNEL_UUID = '8f14e45f-ea3c-4c2d-9d4b-1a2b3c4d5e6f';
const SECOND_CHANNEL_UUID = '9f14e45f-ea3c-4c2d-9d4b-1a2b3c4d5e6f';
const EVENT_ID = 'a'.repeat(64);
const SENDER_HEX = 'c'.repeat(64);

const configuration: AcpdConfiguration = {
  blueclawEventsURL: 'http://127.0.0.1:8080/connectors/buzz/events',
  blueclawTaskCancelURL: 'http://127.0.0.1:8080/admin/api/task/cancel',
  blueclawTaskEventsURL: 'http://127.0.0.1:8080/tasks/api/events',
  buzzCommand: 'buzz',
  listenPort: 18091,
  socketPath: '/tmp/acpd-test.sock',
  maximumTurnHoldSeconds: 3300,
  accountEmailByPubkey: {},
  accountLinksPath: undefined,
};

const originalFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = originalFetch;
});

type CapturedRequest = { url: string; body: unknown };
type NotifiedUpdate = { method: string; params: { update: { sessionUpdate: string } } };

function installFetchStub(ingressResult: object): CapturedRequest[] {
  const captured: CapturedRequest[] = [];
  globalThis.fetch = (async (input: string | URL | Request, init?: RequestInit) => {
    const url = String(input);
    captured.push({ url, body: init?.body ? JSON.parse(String(init.body)) : undefined });
    return new Response(JSON.stringify(url.includes('/connectors/') ? ingressResult : {}), { status: 200 });
  }) as typeof fetch;
  return captured;
}

function promptBlocksForEvent(channelUUID: string = CHANNEL_UUID): Array<{ type: 'text'; text: string }> {
  const contextBlock = ['[Context]', 'Scope: channel', `Channel: general (#${channelUUID})`].join('\n');
  const eventBlock = [
    '[Buzz event: mention]',
    `Event ID: ${EVENT_ID}`,
    `Channel: general (#${channelUUID})`,
    'Kind: 9',
    `From: Alice Kim (npub: npub1alice, hex: ${SENDER_HEX})`,
    'Time: 2026-07-24T10:00:00+00:00',
    'Content: hello',
    'Tags: []',
  ].join('\n');
  return [
    { type: 'text', text: contextBlock },
    { type: 'text', text: eventBlock },
  ];
}

type AgentConnection = ReturnType<ReturnType<typeof createAcpAgentCore>['createConnection']>;

async function createSession(connection: AgentConnection): Promise<string> {
  const sessionResult = (await connection.requestHandlers['session/new']?.({})) as { sessionId: string };
  return sessionResult.sessionId;
}

function messageChunkCount(updates: NotifiedUpdate[]): number {
  return updates.filter(
    (update) => update.method === 'session/update' && update.params.update.sessionUpdate === 'agent_message_chunk',
  ).length;
}

async function settle(): Promise<void> {
  await Bun.sleep(10);
}

describe('createAcpAgentCore', () => {
  test('initialize advertises protocol version 1', async () => {
    const connection = createAcpAgentCore(configuration).createConnection(() => {});
    const result = (await connection.requestHandlers['initialize']?.({})) as { protocolVersion: number };
    expect(result.protocolVersion).toBe(1);
  });

  test('prompt without events ends the turn immediately', async () => {
    const connection = createAcpAgentCore(configuration).createConnection(() => {});
    const sessionID = await createSession(connection);
    const result = await connection.requestHandlers['session/prompt']?.({
      sessionId: sessionID,
      prompt: [{ type: 'text', text: 'heartbeat text' }],
    });
    expect(result).toEqual({ stopReason: 'end_turn' });
  });

  test('accepted prompt without a task run ends the turn without holding', async () => {
    installFetchStub({ handled: true });
    const connection = createAcpAgentCore(configuration).createConnection(() => {});
    const sessionID = await createSession(connection);
    const result = await connection.requestHandlers['session/prompt']?.({ sessionId: sessionID, prompt: promptBlocksForEvent() });
    expect(result).toEqual({ stopReason: 'end_turn' });
  });

  test('launched task holds the turn until a terminal reply is relayed', async () => {
    const captured = installFetchStub({ handled: true, taskRunID: 'task-1' });
    const updates: NotifiedUpdate[] = [];
    const core = createAcpAgentCore(configuration);
    const connection = core.createConnection((method, params) => updates.push({ method, params } as NotifiedUpdate));
    const sessionID = await createSession(connection);

    const promptPromise = connection.requestHandlers['session/prompt']?.({ sessionId: sessionID, prompt: promptBlocksForEvent() });
    await settle();

    core.relayOutboundReply(CHANNEL_UUID, '진행 중입니다', 'checkpoint');
    await settle();
    core.relayOutboundReply(CHANNEL_UUID, '완료했습니다', 'success');

    expect(await promptPromise).toEqual({ stopReason: 'end_turn' });
    expect(messageChunkCount(updates)).toBe(2);
    const forwarded = captured.find((request) => request.url.includes('/connectors/buzz/events'));
    expect(forwarded?.body).toMatchObject({
      conversationID: CHANNEL_UUID,
      messageID: EVENT_ID,
      senderID: SENDER_HEX,
      replyTargetID: `${CHANNEL_UUID}/${EVENT_ID}`,
      prompt: 'hello',
    });
  });

  test('concurrent turns on separate connections stay independent', async () => {
    installFetchStub({ handled: true, taskRunID: 'task-parallel' });
    const core = createAcpAgentCore(configuration);
    const firstUpdates: NotifiedUpdate[] = [];
    const secondUpdates: NotifiedUpdate[] = [];
    const firstConnection = core.createConnection((method, params) => firstUpdates.push({ method, params } as NotifiedUpdate));
    const secondConnection = core.createConnection((method, params) => secondUpdates.push({ method, params } as NotifiedUpdate));
    const firstSession = await createSession(firstConnection);
    const secondSession = await createSession(secondConnection);

    const firstPrompt = firstConnection.requestHandlers['session/prompt']?.({
      sessionId: firstSession,
      prompt: promptBlocksForEvent(CHANNEL_UUID),
    });
    const secondPrompt = secondConnection.requestHandlers['session/prompt']?.({
      sessionId: secondSession,
      prompt: promptBlocksForEvent(SECOND_CHANNEL_UUID),
    });
    await settle();

    core.relayOutboundReply(SECOND_CHANNEL_UUID, '두번째 채널 완료', 'success');
    expect(await secondPrompt).toEqual({ stopReason: 'end_turn' });

    core.relayOutboundReply(CHANNEL_UUID, '첫번째 채널 완료', 'success');
    expect(await firstPrompt).toEqual({ stopReason: 'end_turn' });

    expect(messageChunkCount(firstUpdates)).toBe(1);
    expect(messageChunkCount(secondUpdates)).toBe(1);
  });

  test('ignored prompt ends the turn without holding', async () => {
    installFetchStub({ handled: true, ignored: true });
    const connection = createAcpAgentCore(configuration).createConnection(() => {});
    const sessionID = await createSession(connection);
    const result = await connection.requestHandlers['session/prompt']?.({ sessionId: sessionID, prompt: promptBlocksForEvent() });
    expect(result).toEqual({ stopReason: 'end_turn' });
  });

  test('session cancel resolves the held turn and cancels the task', async () => {
    const captured = installFetchStub({ handled: true, taskRunID: 'task-9' });
    const core = createAcpAgentCore(configuration);
    const connection = core.createConnection(() => {});
    const sessionID = await createSession(connection);

    const promptPromise = connection.requestHandlers['session/prompt']?.({ sessionId: sessionID, prompt: promptBlocksForEvent() });
    await settle();

    connection.notificationHandlers['session/cancel']?.({ sessionId: sessionID });
    expect(await promptPromise).toEqual({ stopReason: 'cancelled' });

    await settle();
    const cancelRequest = captured.find((request) => request.url.includes('/admin/api/task/cancel'));
    expect(cancelRequest?.body).toMatchObject({ taskRunIDs: ['task-9'] });
  });
});
