import { afterEach, describe, expect, test } from 'bun:test';
import { createAcpAgent } from '../src/acp-agent.ts';
import type { AcpdConfiguration } from '../src/configuration.ts';

const CHANNEL_UUID = '8f14e45f-ea3c-4c2d-9d4b-1a2b3c4d5e6f';
const EVENT_ID = 'a'.repeat(64);
const SENDER_HEX = 'c'.repeat(64);

const configuration: AcpdConfiguration = {
  blueclawEventsURL: 'http://127.0.0.1:8080/connectors/buzz/events',
  blueclawTaskCancelURL: 'http://127.0.0.1:8080/admin/api/task/cancel',
  buzzCommand: 'buzz',
  listenPort: 18091,
  maximumTurnHoldSeconds: 3300,
  accountEmailByPubkey: {},
};

const originalFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = originalFetch;
});

type CapturedRequest = { url: string; body: unknown };

function installFetchStub(ingressResult: object): CapturedRequest[] {
  const captured: CapturedRequest[] = [];
  globalThis.fetch = (async (input: string | URL | Request, init?: RequestInit) => {
    const url = String(input);
    captured.push({ url, body: init?.body ? JSON.parse(String(init.body)) : undefined });
    return new Response(JSON.stringify(url.includes('/connectors/') ? ingressResult : {}), { status: 200 });
  }) as typeof fetch;
  return captured;
}

function promptBlocksForEvent(): Array<{ type: 'text'; text: string }> {
  const contextBlock = ['[Context]', 'Scope: channel', `Channel: general (#${CHANNEL_UUID})`].join('\n');
  const eventBlock = [
    '[Buzz event: mention]',
    `Event ID: ${EVENT_ID}`,
    `Channel: general (#${CHANNEL_UUID})`,
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

async function createSessionWithAgent(agent: ReturnType<typeof createAcpAgent>): Promise<string> {
  const sessionResult = (await agent.requestHandlers['session/new']?.({})) as { sessionId: string };
  return sessionResult.sessionId;
}

async function settle(): Promise<void> {
  await Bun.sleep(10);
}

describe('createAcpAgent', () => {
  test('initialize advertises protocol version 1', async () => {
    const agent = createAcpAgent(configuration, () => {});
    const result = (await agent.requestHandlers['initialize']?.({})) as { protocolVersion: number };
    expect(result.protocolVersion).toBe(1);
  });

  test('prompt without events ends the turn immediately', async () => {
    const agent = createAcpAgent(configuration, () => {});
    const sessionID = await createSessionWithAgent(agent);
    const result = await agent.requestHandlers['session/prompt']?.({
      sessionId: sessionID,
      prompt: [{ type: 'text', text: 'heartbeat text' }],
    });
    expect(result).toEqual({ stopReason: 'end_turn' });
  });

  test('accepted prompt holds the turn until a terminal reply is relayed', async () => {
    const captured = installFetchStub({ handled: true, taskRunID: 'task-1' });
    const updates: unknown[] = [];
    const agent = createAcpAgent(configuration, (method, params) => updates.push({ method, params }));
    const sessionID = await createSessionWithAgent(agent);

    const promptPromise = agent.requestHandlers['session/prompt']?.({ sessionId: sessionID, prompt: promptBlocksForEvent() });
    await settle();

    agent.relayOutboundReply(CHANNEL_UUID, '진행 중입니다', 'checkpoint');
    await settle();
    agent.relayOutboundReply(CHANNEL_UUID, '완료했습니다', 'success');

    expect(await promptPromise).toEqual({ stopReason: 'end_turn' });
    expect(updates).toHaveLength(2);
    const forwarded = captured.find((request) => request.url.includes('/connectors/buzz/events'));
    expect(forwarded?.body).toMatchObject({
      conversationID: CHANNEL_UUID,
      messageID: EVENT_ID,
      senderID: SENDER_HEX,
      replyTargetID: `${CHANNEL_UUID}/${EVENT_ID}`,
      prompt: 'hello',
    });
  });

  test('ignored prompt ends the turn without holding', async () => {
    installFetchStub({ handled: true, ignored: true });
    const agent = createAcpAgent(configuration, () => {});
    const sessionID = await createSessionWithAgent(agent);
    const result = await agent.requestHandlers['session/prompt']?.({ sessionId: sessionID, prompt: promptBlocksForEvent() });
    expect(result).toEqual({ stopReason: 'end_turn' });
  });

  test('session cancel resolves the held turn and cancels the task', async () => {
    const captured = installFetchStub({ handled: true, taskRunID: 'task-9' });
    const agent = createAcpAgent(configuration, () => {});
    const sessionID = await createSessionWithAgent(agent);

    const promptPromise = agent.requestHandlers['session/prompt']?.({ sessionId: sessionID, prompt: promptBlocksForEvent() });
    await settle();

    agent.notificationHandlers['session/cancel']?.({ sessionId: sessionID });
    expect(await promptPromise).toEqual({ stopReason: 'cancelled' });

    await settle();
    const cancelRequest = captured.find((request) => request.url.includes('/admin/api/task/cancel'));
    expect(cancelRequest?.body).toMatchObject({ taskRunIDs: ['task-9'] });
  });
});
