import type { Chat, Message, Thread } from 'chat';
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
    await forwardEvent(configuration, inboundEvent('direct_message', thread, message));
  });
  chat.onNewMention(async (thread, message) => {
    await forwardEvent(configuration, inboundEvent('mention', thread, message));
  });
  chat.onAction(async (event) => {
    await forwardEvent(configuration, {
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

function inboundEvent(kind: BridgeInboundEvent['kind'], thread: Thread, message: Message): BridgeInboundEvent {
  return {
    kind,
    platform: 'mattermost',
    threadID: thread.id,
    messageID: message.id,
    senderID: message.author.userId,
    senderUserName: message.author.userName,
    text: message.text,
  };
}

async function forwardEvent(configuration: ChatdConfiguration, event: BridgeInboundEvent): Promise<void> {
  const response = await fetch(configuration.blueclawIngressURL, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(event),
  });
  if (!response.ok) {
    throw new Error(`blueclaw ingress returned ${response.status}`);
  }
}
