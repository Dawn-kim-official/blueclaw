import type { Chat, Message, Thread } from 'chat';
import type { ChatdConfiguration } from './configuration.ts';
import {
  buildVisibleContext,
  emptyVisibleContext,
  type NormalizedPlatformAdapter,
} from './visible-context.ts';

export function createBridge(
  chat: Chat,
  configuration: ChatdConfiguration,
  normalizedAdapters: Record<string, NormalizedPlatformAdapter>,
): void {
  chat.onDirectMessage(async (thread, message) => {
    await forwardMessage(configuration, normalizedAdapters, thread, message);
  });
  chat.onNewMention(async (thread, message) => {
    await thread.subscribe();
    await forwardMessage(configuration, normalizedAdapters, thread, message);
  });
  chat.onSubscribedMessage(async (thread, message) => {
    await forwardMessage(configuration, normalizedAdapters, thread, message);
  });
  chat.onAction(async (event) => {
    const platform = platformOfThread(event.threadId);
    if (!normalizedAdapters[platform]) return;
    await postToBlueclaw(configuration, platform, {
      conversationID: event.threadId,
      messageID: event.messageId ?? '',
      senderID: event.user.userId,
      replyTargetID: event.threadId,
      prompt: '',
      action: {
        actionID: event.actionId,
        actionValue:
          typeof event.value === 'string' ? event.value : JSON.stringify(event.value ?? null),
      },
    });
  });
}

function platformOfThread(threadID: string): string {
  return threadID.split(':')[0] ?? '';
}

async function forwardMessage(
  configuration: ChatdConfiguration,
  normalizedAdapters: Record<string, NormalizedPlatformAdapter>,
  thread: Thread,
  message: Message,
): Promise<void> {
  const platform = platformOfThread(thread.id);
  const adapter = normalizedAdapters[platform];
  if (!adapter) {
    throw new Error(`no adapter is registered for platform ${platform}`);
  }
  const scopeThreadId = adapter.historyScopeThreadId(thread.id, message.id);
  const context = await buildVisibleContext(adapter, scopeThreadId, {
    beforeMessageId: message.id,
    senderId: message.author.userId,
  }).catch(() => emptyVisibleContext(scopeThreadId));
  const addressing = adapter.addressingOf(message.raw);
  await postToBlueclaw(configuration, platform, {
    conversationID: thread.id,
    messageID: message.id,
    senderID: message.author.userId,
    replyTargetID: thread.id,
    prompt: message.text,
    context: {
      ...context,
      addressing: {
        botMentioned: addressing.botMentioned || message.isMention === true,
        otherPersonMentioned: addressing.otherPersonMentioned,
      },
    },
  });
}

async function postToBlueclaw(
  configuration: ChatdConfiguration,
  platform: string,
  event: Record<string, unknown>,
): Promise<void> {
  const response = await fetch(`${configuration.blueclawBaseURL}/connectors/${platform}/events`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(event),
  });
  if (!response.ok) {
    throw new Error(`blueclaw ${platform} ingress returned ${response.status}`);
  }
}
