import { describe, expect, test } from 'bun:test';
import type { AcpAgent } from '../src/acp-agent.ts';
import { createOutboundHandler, decodeReplyTarget } from '../src/outbound.ts';

const CHANNEL_UUID = '8f14e45f-ea3c-4c2d-9d4b-1a2b3c4d5e6f';
const ANCHOR_EVENT_ID = 'd'.repeat(64);
const SENDER_HEX = 'c'.repeat(64);

type RecordedCommand = { commandArguments: string[]; standardInput: string | undefined };

function createAgentStub(): { agent: AcpAgent; relayed: Array<{ channelID: string; message: string; replyKind: string | undefined }> } {
  const relayed: Array<{ channelID: string; message: string; replyKind: string | undefined }> = [];
  return {
    agent: {
      requestHandlers: {},
      notificationHandlers: {},
      relayOutboundReply: (channelID, message, replyKind) => relayed.push({ channelID, message, replyKind }),
    },
    relayed,
  };
}

function createRunnerStub(output: string): { runner: (commandArguments: string[], standardInput?: string) => Promise<string>; recorded: RecordedCommand[] } {
  const recorded: RecordedCommand[] = [];
  return {
    runner: async (commandArguments, standardInput) => {
      recorded.push({ commandArguments, standardInput });
      return output;
    },
    recorded,
  };
}

function postRequest(capabilityName: string, body: object): Request {
  return new Request(`http://127.0.0.1:18091/v1/platform/buzz/${capabilityName}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
}

describe('decodeReplyTarget', () => {
  test('splits channel and anchor', () => {
    expect(decodeReplyTarget(`${CHANNEL_UUID}/${ANCHOR_EVENT_ID}`)).toEqual({
      channelID: CHANNEL_UUID,
      replyAnchorEventID: ANCHOR_EVENT_ID,
    });
  });

  test('handles a bare channel id', () => {
    expect(decodeReplyTarget(CHANNEL_UUID)).toEqual({ channelID: CHANNEL_UUID, replyAnchorEventID: undefined });
  });
});

describe('createOutboundHandler', () => {
  test('reply.send runs the buzz CLI and relays the reply', async () => {
    const { runner, recorded } = createRunnerStub(JSON.stringify({ event_id: 'e'.repeat(64), accepted: true, message: '' }));
    const { agent, relayed } = createAgentStub();
    const handler = createOutboundHandler(runner, agent);

    const response = await handler(
      postRequest('reply.send', {
        replyTargetID: `${CHANNEL_UUID}/${ANCHOR_EVENT_ID}`,
        message: '요약 결과입니다',
        replyKind: 'success',
      }),
    );

    expect(response.status).toBe(200);
    expect(await response.json()).toEqual({ dispatchID: 'e'.repeat(64) });
    expect(recorded[0]?.commandArguments).toEqual([
      'messages',
      'send',
      '--channel',
      CHANNEL_UUID,
      '--content',
      '-',
      '--reply-to',
      ANCHOR_EVENT_ID,
    ]);
    expect(recorded[0]?.standardInput).toBe('요약 결과입니다');
    expect(relayed).toEqual([{ channelID: CHANNEL_UUID, message: '요약 결과입니다', replyKind: 'success' }]);
  });

  test('identity.resolve maps a user profile', async () => {
    const { runner } = createRunnerStub(JSON.stringify([{ pubkey: SENDER_HEX, display_name: 'Alice Kim', nip05: 'alice@dawn.example' }]));
    const { agent } = createAgentStub();
    const handler = createOutboundHandler(runner, agent);

    const response = await handler(postRequest('identity.resolve', { senderID: SENDER_HEX }));

    expect(await response.json()).toEqual({ displayName: 'Alice Kim', email: 'alice@dawn.example' });
  });

  test('reaction.add targets the message event', async () => {
    const { runner, recorded } = createRunnerStub('{}');
    const { agent } = createAgentStub();
    const handler = createOutboundHandler(runner, agent);

    const response = await handler(postRequest('reaction.add', { messageID: ANCHOR_EVENT_ID, emojiName: '👍' }));

    expect(response.status).toBe(200);
    expect(recorded[0]?.commandArguments).toEqual(['reactions', 'add', '--event', ANCHOR_EVENT_ID, '--emoji', '👍']);
  });

  test('unknown capability returns 404', async () => {
    const { runner } = createRunnerStub('{}');
    const { agent } = createAgentStub();
    const handler = createOutboundHandler(runner, agent);

    const response = await handler(postRequest('nope', {}));

    expect(response.status).toBe(404);
  });

  test('failing CLI surfaces a 502', async () => {
    const { agent } = createAgentStub();
    const handler = createOutboundHandler(async () => {
      throw new Error('buzz messages send exited 2: network');
    }, agent);

    const response = await handler(postRequest('reply.send', { replyTargetID: `${CHANNEL_UUID}/${ANCHOR_EVENT_ID}`, message: 'x' }));

    expect(response.status).toBe(502);
  });
});
