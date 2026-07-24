import type { Chat, Message, Thread } from 'chat';
import { BUZZ_ADAPTER_NAME } from './adapters/buzz/types.ts';
import type { ChatdConfiguration } from './configuration.ts';

export type BridgeInboundEvent = {
  kind: 'direct_message' | 'mention' | 'channel_message' | 'action';
  platform: 'mattermost';
  threadID: string;
  messageID: string;
  senderID: string;
  senderUserName: string;
  text: string;
  actionID?: string;
  actionValue?: string;
};

export function createBridge(chat: Chat, configuration: ChatdConfiguration): void {
  chat.onDirectMessage(async (thread, message) => {
    await forwardMessage(configuration, 'direct_message', thread, message);
  });
  chat.onNewMention(async (thread, message) => {
    await thread.subscribe();
    await forwardMessage(configuration, 'mention', thread, message);
  });
  chat.onSubscribedMessage(async (thread, message) => {
    await forwardMessage(configuration, message.isMention ? 'mention' : 'channel_message', thread, message);
  });
  chat.onAction(async (event) => {
    await forwardLegacyEvent(configuration, {
      kind: 'action',
      platform: 'mattermost',
      threadID: event.threadId,
      messageID: event.messageId ?? '',
      senderID: event.user.userId,
      senderUserName: event.user.userName,
      text: '',
      actionID: event.actionId,
      actionValue: typeof event.value === 'string' ? event.value : JSON.stringify(event.value ?? null),
    });
  });
}

function platformOfThread(threadID: string): string {
  return threadID.split(':')[0] ?? '';
}

async function forwardMessage(
  configuration: ChatdConfiguration,
  kind: BridgeInboundEvent['kind'],
  thread: Thread,
  message: Message,
): Promise<void> {
  const platform = platformOfThread(thread.id);
  if (platform === BUZZ_ADAPTER_NAME) {
    await forwardNormalizedEvent(configuration, platform, thread, message);
    return;
  }
  await forwardLegacyEvent(configuration, {
    kind,
    platform: 'mattermost',
    threadID: thread.id,
    messageID: message.id,
    senderID: message.author.userId,
    senderUserName: message.author.userName,
    text: message.text,
  });
}

async function forwardNormalizedEvent(
  configuration: ChatdConfiguration,
  platform: string,
  thread: Thread,
  message: Message,
): Promise<void> {
  const response = await fetch(`${configuration.blueclawBaseURL}/connectors/${platform}/events`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      conversationID: thread.id,
      messageID: message.id,
      senderID: message.author.userId,
      replyTargetID: thread.id,
      prompt: message.text,
      context: { messages: [], hasMoreBefore: true, historyCursor: thread.id },
    }),
  });
  if (!response.ok) {
    throw new Error(`blueclaw ${platform} ingress returned ${response.status}`);
  }
}

async function forwardLegacyEvent(configuration: ChatdConfiguration, event: BridgeInboundEvent): Promise<void> {
  if (!configuration.blueclawIngressURL) return;
  const response = await fetch(configuration.blueclawIngressURL, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(event),
  });
  if (!response.ok) {
    throw new Error(`blueclaw ingress returned ${response.status}`);
  }
}
