import { describe, expect, test } from 'bun:test';
import { parseHarnessPrompt } from '../src/harness-prompt.ts';

const CHANNEL_UUID = '8f14e45f-ea3c-4c2d-9d4b-1a2b3c4d5e6f';
const EVENT_ID = 'a'.repeat(64);
const SECOND_EVENT_ID = 'b'.repeat(64);
const SENDER_HEX = 'c'.repeat(64);
const ANCHOR_EVENT_ID = 'd'.repeat(64);

const contextBlock = [
  '[Context]',
  'Scope: channel',
  `Channel: general (#${CHANNEL_UUID})`,
  'Hint: Use `buzz messages get --channel <UUID>` for recent messages if needed.',
  `IMPORTANT: This is a new top-level message. For ordinary replies in this turn, use \`--reply-to ${ANCHOR_EVENT_ID}\` on \`buzz messages send\` — the triggering message is the thread root.`,
].join('\n');

const singleEventBlock = [
  '[Buzz event: mention]',
  `Event ID: ${EVENT_ID}`,
  `Channel: general (#${CHANNEL_UUID})`,
  'Kind: 9',
  `From: Alice Kim (npub: npub1alice, hex: ${SENDER_HEX})`,
  'Time: 2026-07-24T10:00:00+00:00',
  'Content: @internkim please summarize',
  'the thread',
  `Tags: [["p","${SENDER_HEX}"]]`,
].join('\n');

describe('parseHarnessPrompt', () => {
  test('parses context and a single event', () => {
    const prompt = parseHarnessPrompt([contextBlock, singleEventBlock]);

    expect(prompt.scope).toBe('channel');
    expect(prompt.channelID).toBe(CHANNEL_UUID);
    expect(prompt.channelName).toBe('general');
    expect(prompt.replyAnchorEventID).toBe(ANCHOR_EVENT_ID);
    expect(prompt.events).toHaveLength(1);
    const event = prompt.events[0];
    expect(event?.eventID).toBe(EVENT_ID);
    expect(event?.senderHex).toBe(SENDER_HEX);
    expect(event?.senderLabel).toBe('Alice Kim');
    expect(event?.content).toBe('@internkim please summarize\nthe thread');
  });

  test('parses a multi-event batch with separators', () => {
    const batchBlock = [
      '[Buzz events — 2 events]',
      '',
      '--- Event 1 (mention) ---',
      `Event ID: ${EVENT_ID}`,
      `Channel: general (#${CHANNEL_UUID})`,
      'Kind: 9',
      `From: npub1alice (hex: ${SENDER_HEX})`,
      'Time: 2026-07-24T10:00:00+00:00',
      'Content: first message',
      'Tags: []',
      '',
      '--- Event 2 (mention) ---',
      `Event ID: ${SECOND_EVENT_ID}`,
      `Channel: general (#${CHANNEL_UUID})`,
      'Kind: 9',
      `From: Alice Kim (npub: npub1alice, hex: ${SENDER_HEX})`,
      'Time: 2026-07-24T10:01:00+00:00',
      'Content: second message',
      'Tags: []',
    ].join('\n');

    const prompt = parseHarnessPrompt([contextBlock, batchBlock]);

    expect(prompt.events.map((event) => event.eventID)).toEqual([EVENT_ID, SECOND_EVENT_ID]);
    expect(prompt.events[0]?.content).toBe('first message');
    expect(prompt.events[0]?.senderLabel).toBeUndefined();
    expect(prompt.events[1]?.content).toBe('second message');
  });

  test('parses a bare channel uuid and thread scope', () => {
    const threadContext = [
      '[Context]',
      'Scope: thread',
      `Channel: ${CHANNEL_UUID}`,
      `Thread root: ${ANCHOR_EVENT_ID}`,
    ].join('\n');

    const prompt = parseHarnessPrompt([threadContext, singleEventBlock]);

    expect(prompt.scope).toBe('thread');
    expect(prompt.channelID).toBe(CHANNEL_UUID);
    expect(prompt.channelName).toBeUndefined();
    expect(prompt.threadRootEventID).toBe(ANCHOR_EVENT_ID);
  });

  test('falls back to the event channel when context is missing', () => {
    const prompt = parseHarnessPrompt([singleEventBlock]);

    expect(prompt.channelID).toBe(CHANNEL_UUID);
    expect(prompt.events).toHaveLength(1);
  });

  test('returns no events for plain heartbeat text', () => {
    const prompt = parseHarnessPrompt(['Check pending mentions and end your turn.']);

    expect(prompt.events).toHaveLength(0);
    expect(prompt.channelID).toBeUndefined();
  });
});
