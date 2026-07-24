import type { AcpAgent } from './acp-agent.ts';
import {
  addMessageReaction,
  removeMessageReaction,
  resolveUserProfile,
  sendChannelMessage,
  type BuzzCommandRunner,
} from './buzz-cli.ts';
import type { AcpdConfiguration } from './configuration.ts';

const OUTBOUND_ROUTE_PATTERN = /^\/v1\/platform\/buzz\/([^/]+)$/;

type ReplyAttachmentDocument = {
  devicePath?: string;
  filename?: string;
  contentBase64?: string;
};

export function createOutboundHandler(
  runBuzzCommand: BuzzCommandRunner,
  agent: AcpAgent,
  configuration: Pick<AcpdConfiguration, 'accountEmailByPubkey' | 'accountLinksPath'> = {
    accountEmailByPubkey: {},
    accountLinksPath: undefined,
  },
): (request: Request) => Promise<Response> {
  const capabilityHandlers: Record<string, (requestDocument: Record<string, unknown>) => Promise<object>> = {
    'reply.send': handleReplySend,
    'progress.start': async () => ({}),
    'progress.stop': async () => ({}),
    'identity.resolve': handleIdentityResolve,
    'history.fetch': handleHistoryFetch,
    'reaction.add': handleReactionAdd,
    'reaction.remove': handleReactionRemove,
    'interaction.resolve': async () => ({}),
    'attachments.import': async () => ({ inputParts: [], inputAttachments: [] }),
  };

  async function handleReplySend(requestDocument: Record<string, unknown>): Promise<object> {
    const replyTargetID = readString(requestDocument, 'replyTargetID');
    const message = readString(requestDocument, 'message');
    const replyKind = readOptionalString(requestDocument, 'replyKind');
    const { channelID, replyAnchorEventID } = decodeReplyTarget(replyTargetID);
    if (!channelID) throw new Error(`reply target ${replyTargetID} has no channel`);
    const filePaths = await materializeAttachmentFiles(readAttachments(requestDocument));
    const dispatchID = await sendChannelMessage(runBuzzCommand, channelID, message, replyAnchorEventID, filePaths);
    agent.relayOutboundReply(channelID, message, replyKind);
    return { dispatchID };
  }

  async function handleIdentityResolve(requestDocument: Record<string, unknown>): Promise<object> {
    const senderID = readString(requestDocument, 'senderID');
    if (!senderID) return {};
    const linkedEmail =
      (await readLinkedEmailFromFile(configuration.accountLinksPath, senderID)) ??
      configuration.accountEmailByPubkey[senderID.toLowerCase()];
    const profile = await resolveUserProfile(runBuzzCommand, senderID).catch(() => ({}));
    if (linkedEmail) return { ...profile, email: linkedEmail };
    return profile;
  }

  async function handleHistoryFetch(requestDocument: Record<string, unknown>): Promise<object> {
    return {
      messages: [],
      hasMoreBefore: false,
      historyCursor: readString(requestDocument, 'historyCursor'),
    };
  }

  async function handleReactionAdd(requestDocument: Record<string, unknown>): Promise<object> {
    await addMessageReaction(runBuzzCommand, readString(requestDocument, 'messageID'), readString(requestDocument, 'emojiName'));
    return {};
  }

  async function handleReactionRemove(requestDocument: Record<string, unknown>): Promise<object> {
    await removeMessageReaction(runBuzzCommand, readString(requestDocument, 'messageID'), readString(requestDocument, 'emojiName'));
    return {};
  }

  return async function handleOutboundRequest(request: Request): Promise<Response> {
    if (request.method !== 'POST') {
      return jsonResponse(405, { error: 'method not allowed' });
    }
    const routeMatch = OUTBOUND_ROUTE_PATTERN.exec(new URL(request.url).pathname);
    const handler = routeMatch?.[1] ? capabilityHandlers[routeMatch[1]] : undefined;
    if (!handler) {
      return jsonResponse(404, { error: 'not found' });
    }
    try {
      const requestDocument: unknown = await request.json();
      if (typeof requestDocument !== 'object' || requestDocument === null) {
        return jsonResponse(400, { error: 'request body must be an object' });
      }
      return jsonResponse(200, await handler(requestDocument as Record<string, unknown>));
    } catch (error) {
      return jsonResponse(502, { error: error instanceof Error ? error.message : String(error) });
    }
  };
}

async function readLinkedEmailFromFile(accountLinksPath: string | undefined, senderID: string): Promise<string | undefined> {
  if (!accountLinksPath) return undefined;
  const linksFile = Bun.file(accountLinksPath);
  if (!(await linksFile.exists())) return undefined;
  try {
    const links: unknown = await linksFile.json();
    if (typeof links !== 'object' || links === null) return undefined;
    const email = (links as Record<string, unknown>)[senderID.toLowerCase()];
    return typeof email === 'string' && email.trim() !== '' ? email.trim().toLowerCase() : undefined;
  } catch {
    return undefined;
  }
}

export function decodeReplyTarget(replyTargetID: string): { channelID: string | undefined; replyAnchorEventID: string | undefined } {
  const separatorIndex = replyTargetID.indexOf('/');
  if (separatorIndex < 0) {
    return { channelID: replyTargetID || undefined, replyAnchorEventID: undefined };
  }
  const channelID = replyTargetID.slice(0, separatorIndex);
  const replyAnchorEventID = replyTargetID.slice(separatorIndex + 1);
  return { channelID: channelID || undefined, replyAnchorEventID: replyAnchorEventID || undefined };
}

async function materializeAttachmentFiles(attachments: ReplyAttachmentDocument[]): Promise<string[]> {
  const filePaths: string[] = [];
  for (const attachment of attachments) {
    if (attachment.devicePath && (await Bun.file(attachment.devicePath).exists())) {
      filePaths.push(attachment.devicePath);
      continue;
    }
    if (attachment.contentBase64) {
      const temporaryPath = `/tmp/acpd-attachment-${crypto.randomUUID()}-${attachment.filename ?? 'attachment'}`;
      await Bun.write(temporaryPath, Buffer.from(attachment.contentBase64, 'base64'));
      filePaths.push(temporaryPath);
    }
  }
  return filePaths;
}

function readAttachments(requestDocument: Record<string, unknown>): ReplyAttachmentDocument[] {
  const attachments = requestDocument['attachments'];
  if (!Array.isArray(attachments)) return [];
  return attachments.filter((attachment): attachment is ReplyAttachmentDocument => typeof attachment === 'object' && attachment !== null);
}

function readString(requestDocument: Record<string, unknown>, fieldName: string): string {
  const value = requestDocument[fieldName];
  return typeof value === 'string' ? value : '';
}

function readOptionalString(requestDocument: Record<string, unknown>, fieldName: string): string | undefined {
  const value = requestDocument[fieldName];
  return typeof value === 'string' && value !== '' ? value : undefined;
}

function jsonResponse(statusCode: number, body: object): Response {
  return new Response(JSON.stringify(body), {
    status: statusCode,
    headers: { 'Content-Type': 'application/json' },
  });
}
