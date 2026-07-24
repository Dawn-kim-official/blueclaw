import type { AcpdConfiguration } from './configuration.ts';
import { parseHarnessPrompt, type HarnessEvent, type HarnessPrompt } from './harness-prompt.ts';
import type { NotificationHandler, RequestHandler } from './jsonrpc.ts';
import { createTaskActivityPoller } from './task-activity.ts';

export type AcpAgent = {
  requestHandlers: Record<string, RequestHandler>;
  notificationHandlers: Record<string, NotificationHandler>;
  relayOutboundReply: (channelID: string, message: string, replyKind: string | undefined) => void;
  finishTurnForChannel: (channelID: string, stopReason: string) => void;
};

type AgentSession = {
  sessionID: string;
  channelID: string | undefined;
  lastTaskRunID: string | undefined;
  finishActiveTurn: ((stopReason: string) => void) | undefined;
};

type IngressResult = {
  ignored?: boolean;
  duplicate?: boolean;
  taskRunID?: string;
};

export function createAcpAgent(
  configuration: AcpdConfiguration,
  notify: (method: string, params: unknown) => void,
): AcpAgent {
  const sessions = new Map<string, AgentSession>();
  const sessionIDByChannelID = new Map<string, string>();

  function sessionForChannel(channelID: string): AgentSession | undefined {
    const sessionID = sessionIDByChannelID.get(channelID);
    return sessionID ? sessions.get(sessionID) : undefined;
  }

  async function handleInitialize(): Promise<unknown> {
    return { protocolVersion: 1, agentCapabilities: { loadSession: false }, authMethods: [] };
  }

  async function handleSessionNew(): Promise<unknown> {
    const session: AgentSession = {
      sessionID: crypto.randomUUID(),
      channelID: undefined,
      lastTaskRunID: undefined,
      finishActiveTurn: undefined,
    };
    sessions.set(session.sessionID, session);
    return { sessionId: session.sessionID };
  }

  async function handleSessionPrompt(params: unknown): Promise<unknown> {
    const { sessionID, promptBlocks } = readPromptParams(params);
    const session = sessions.get(sessionID);
    if (!session) throw new Error(`unknown session ${sessionID}`);

    const prompt = parseHarnessPrompt(promptBlocks);
    if (!prompt.channelID || prompt.events.length === 0) {
      return { stopReason: 'end_turn' };
    }
    session.channelID = prompt.channelID;
    sessionIDByChannelID.set(prompt.channelID, session.sessionID);

    const results = await forwardEvents(configuration, prompt);
    const acceptedResult = results.find((result) => !result.ignored && !result.duplicate);
    const taskRunID = results.map((result) => result.taskRunID).filter(Boolean).at(-1);
    session.lastTaskRunID = taskRunID ?? session.lastTaskRunID;
    if (!acceptedResult) {
      return { stopReason: 'end_turn' };
    }

    const stopActivityPoller = taskRunID
      ? createTaskActivityPoller(configuration.blueclawTaskEventsURL, taskRunID, (update) => {
          notify('session/update', { sessionId: session.sessionID, update });
        })
      : undefined;
    const turnResult = await holdTurnOpen(session, configuration.maximumTurnHoldSeconds);
    stopActivityPoller?.();
    return turnResult;
  }

  function handleSessionCancel(params: unknown): void {
    const sessionID = readSessionID(params);
    const session = sessionID ? sessions.get(sessionID) : undefined;
    if (!session) return;
    if (session.lastTaskRunID) {
      void cancelBlueclawTask(configuration, session.lastTaskRunID);
    }
    session.finishActiveTurn?.('cancelled');
  }

  function relayOutboundReply(channelID: string, message: string, replyKind: string | undefined): void {
    const session = sessionForChannel(channelID);
    if (!session) return;
    if (message.trim() !== '') {
      notify('session/update', {
        sessionId: session.sessionID,
        update: { sessionUpdate: 'agent_message_chunk', content: { type: 'text', text: message } },
      });
    }
    if (replyKind !== 'checkpoint') {
      session.finishActiveTurn?.('end_turn');
    }
  }

  function finishTurnForChannel(channelID: string, stopReason: string): void {
    sessionForChannel(channelID)?.finishActiveTurn?.(stopReason);
  }

  return {
    requestHandlers: {
      initialize: handleInitialize,
      'session/new': handleSessionNew,
      'session/prompt': handleSessionPrompt,
    },
    notificationHandlers: {
      'session/cancel': handleSessionCancel,
    },
    relayOutboundReply,
    finishTurnForChannel,
  };
}

function holdTurnOpen(session: AgentSession, maximumHoldSeconds: number): Promise<{ stopReason: string }> {
  return new Promise((resolve) => {
    const timeoutHandle = setTimeout(() => finish('end_turn'), maximumHoldSeconds * 1000);
    function finish(stopReason: string): void {
      clearTimeout(timeoutHandle);
      session.finishActiveTurn = undefined;
      resolve({ stopReason });
    }
    session.finishActiveTurn = finish;
  });
}

async function forwardEvents(configuration: AcpdConfiguration, prompt: HarnessPrompt): Promise<IngressResult[]> {
  const results: IngressResult[] = [];
  for (const event of prompt.events) {
    if (!event.senderHex) continue;
    results.push(await forwardEvent(configuration, prompt, event));
  }
  return results;
}

async function forwardEvent(
  configuration: AcpdConfiguration,
  prompt: HarnessPrompt,
  event: HarnessEvent,
): Promise<IngressResult> {
  const channelID = prompt.channelID ?? '';
  const replyAnchorEventID = prompt.replyAnchorEventID ?? event.eventID;
  const response = await fetch(configuration.blueclawEventsURL, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      conversationID: channelID,
      messageID: event.eventID,
      senderID: event.senderHex,
      replyTargetID: `${channelID}/${replyAnchorEventID}`,
      prompt: event.content,
    }),
  });
  if (!response.ok) {
    throw new Error(`blueclaw ingress returned ${response.status}`);
  }
  return (await response.json()) as IngressResult;
}

async function cancelBlueclawTask(configuration: AcpdConfiguration, taskRunID: string): Promise<void> {
  try {
    await fetch(configuration.blueclawTaskCancelURL, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ taskRunIDs: [taskRunID], reason: 'acp session cancelled' }),
    });
  } catch (error) {
    console.error(`task cancel failed for ${taskRunID}: ${error instanceof Error ? error.message : String(error)}`);
  }
}

function readPromptParams(params: unknown): { sessionID: string; promptBlocks: string[] } {
  if (typeof params !== 'object' || params === null) throw new Error('session/prompt params must be an object');
  const document = params as Record<string, unknown>;
  const sessionID = typeof document['sessionId'] === 'string' ? document['sessionId'] : '';
  if (!sessionID) throw new Error('session/prompt requires sessionId');
  const promptBlocks = Array.isArray(document['prompt'])
    ? document['prompt']
        .map((block) => (isTextBlock(block) ? block.text : ''))
        .filter((text) => text !== '')
    : [];
  return { sessionID, promptBlocks };
}

function isTextBlock(block: unknown): block is { type: 'text'; text: string } {
  return (
    typeof block === 'object' &&
    block !== null &&
    (block as Record<string, unknown>)['type'] === 'text' &&
    typeof (block as Record<string, unknown>)['text'] === 'string'
  );
}

function readSessionID(params: unknown): string | undefined {
  if (typeof params !== 'object' || params === null) return undefined;
  const sessionID = (params as Record<string, unknown>)['sessionId'];
  return typeof sessionID === 'string' ? sessionID : undefined;
}
