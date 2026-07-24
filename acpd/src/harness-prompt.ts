export type HarnessEvent = {
  eventID: string;
  channelID: string | undefined;
  senderHex: string | undefined;
  senderLabel: string | undefined;
  time: string | undefined;
  content: string;
};

export type HarnessPrompt = {
  scope: string | undefined;
  channelID: string | undefined;
  channelName: string | undefined;
  threadRootEventID: string | undefined;
  replyAnchorEventID: string | undefined;
  events: HarnessEvent[];
};

const EVENT_ID_PATTERN = /^Event ID: ([0-9a-f]{64})$/m;
const REPLY_ANCHOR_PATTERN = /--reply-to ([0-9a-f]{64})/;
const CHANNEL_UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const NAMED_CHANNEL_PATTERN = /^(.*) \(#([0-9a-f-]{36})\)$/;

export function parseHarnessPrompt(promptBlocks: string[]): HarnessPrompt {
  const contextBlock = promptBlocks.find((block) => block.trimStart().startsWith('[Context]'));
  const context = parseContextBlock(contextBlock ?? '');
  const events = promptBlocks.flatMap(parseEventBlocks);
  return {
    ...context,
    channelID: context.channelID ?? events.find((event) => event.channelID)?.channelID,
    events,
  };
}

function parseContextBlock(block: string): Omit<HarnessPrompt, 'events'> {
  const channel = parseChannelLine(matchLine(block, 'Channel'));
  return {
    scope: matchLine(block, 'Scope'),
    channelID: channel.channelID,
    channelName: channel.channelName,
    threadRootEventID: matchLine(block, 'Thread root'),
    replyAnchorEventID: REPLY_ANCHOR_PATTERN.exec(block)?.[1],
  };
}

function parseChannelLine(value: string | undefined): { channelID?: string; channelName?: string } {
  if (!value) return {};
  const namedMatch = NAMED_CHANNEL_PATTERN.exec(value);
  if (namedMatch?.[2]) return { channelName: namedMatch[1], channelID: namedMatch[2] };
  if (CHANNEL_UUID_PATTERN.test(value)) return { channelID: value };
  return { channelName: value };
}

function parseEventBlocks(block: string): HarnessEvent[] {
  const anchors = findEventAnchors(block);
  return anchors.map(({ eventID, startIndex }, anchorIndex) => {
    const nextStart = anchors[anchorIndex + 1]?.startIndex ?? block.length;
    return parseEventChunk(eventID, block.slice(startIndex, nextStart));
  });
}

function findEventAnchors(block: string): Array<{ eventID: string; startIndex: number }> {
  const anchors: Array<{ eventID: string; startIndex: number }> = [];
  const pattern = new RegExp(EVENT_ID_PATTERN.source, 'gm');
  let match = pattern.exec(block);
  while (match) {
    const eventID = match[1];
    if (eventID) anchors.push({ eventID, startIndex: match.index });
    match = pattern.exec(block);
  }
  return anchors;
}

function parseEventChunk(eventID: string, chunk: string): HarnessEvent {
  const sender = parseFromLine(matchLine(chunk, 'From'));
  return {
    eventID,
    channelID: parseChannelLine(matchLine(chunk, 'Channel')).channelID,
    senderHex: sender.senderHex,
    senderLabel: sender.senderLabel,
    time: matchLine(chunk, 'Time'),
    content: parseContent(chunk),
  };
}

function parseFromLine(value: string | undefined): { senderHex?: string; senderLabel?: string } {
  if (!value) return {};
  const senderHex = /hex: ([0-9a-f]{64})/.exec(value)?.[1];
  const label = value.split(' (npub: ')[0]?.trim();
  return { senderHex, senderLabel: label && !label.startsWith('npub1') ? label : undefined };
}

function parseContent(chunk: string): string {
  const contentStart = chunk.indexOf('\nContent: ');
  if (contentStart < 0) return '';
  const afterMarker = chunk.slice(contentStart + '\nContent: '.length);
  const terminator = afterMarker.search(/\nTags: \[/);
  return terminator >= 0 ? afterMarker.slice(0, terminator) : afterMarker.trimEnd();
}

function matchLine(block: string, label: string): string | undefined {
  return new RegExp(`^${label}: (.*)$`, 'm').exec(block)?.[1]?.trim();
}
