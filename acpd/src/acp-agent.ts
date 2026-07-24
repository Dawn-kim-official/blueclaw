import type { AcpdConfiguration } from './configuration.ts';
import { parseHarnessPrompt, type HarnessEvent, type HarnessPrompt } from './harness-prompt.ts';
import type { NotificationHandler, RequestHandler } from './jsonrpc.ts';
import { createTaskActivityPoller } from './task-activity.ts';

export type AcpAgentConnection = {
  requestHandlers: Record<string, RequestHandler>;
  notificationHandlers: Record<string, NotificationHandler>;
};

export type AcpAgentCore = {
  createConnection: (notify: (method: string, params: unknown) => void) => AcpAgentConnection;
  relayOutboundReply: (channelID: string, message: string, replyKind: string | undefined) => void;
  finishTurnForChannel: (channelID: string, stopReason: string) => void;
};

type ChannelTurn = {
  sessionID: string;
  taskRunID: string | undefined;
  notify: (method: string, params: unknown) => void;
  finish: ((stopReason: string) => void) | undefined;
  terminalStopReason: string | undefined;
};

type IngressResult = {
  ignored?: boolean;
  duplicate?: boolean;
  taskRunID?: string;
};

export function createAcpAgentCore(configuration: AcpdConfiguration): AcpAgentCore {
  const turnByChannel = new Map<string, ChannelTurn>();

  function relayOutboundReply(channelID: string, message: string, replyKind: string | undefined): void {
    const turn = turnByChannel.get(channelID);
    if (!turn) return;
    if (message.trim() !== '') {
      turn.notify('session/update', {
        sessionId: turn.sessionID,
        update: { sessionUpdate: 'agent_message_chunk', content: { type: 'text', text: message } },
      });
    }
    if (replyKind !== 'checkpoint') {
      concludeTurn(turn, 'end_turn');
    }
  }

  function finishTurnForChannel(channelID: string, stopReason: string): void {
    const turn = turnByChannel.get(channelID);
    if (turn) concludeTurn(turn, stopReason);
  }

  function concludeTurn(turn: ChannelTurn, stopReason: string): void {
    turn.terminalStopReason = turn.terminalStopReason ?? stopReason;
    turn.finish?.(stopReason);
  }

  function createConnection(notify: (method: string, params: unknown) => void): AcpAgentConnection {
    const channelBySessionID = new Map<string, string>();

    async function handleInitialize(): Promise<unknown> {
      return { protocolVersion: 1, agentCapabilities: { loadSession: false }, authMethods: [] };
    }

    async function handleSessionNew(): Promise<unknown> {
      const sessionID = crypto.randomUUID();
      channelBySessionID.set(sessionID, '');
      return { sessionId: sessionID };
    }

    async function handleSessionPrompt(params: unknown): Promise<unknown> {
      const { sessionID, promptBlocks } = readPromptParams(params);
      if (!channelBySessionID.has(sessionID)) throw new Error(`unknown session ${sessionID}`);

      const prompt = parseHarnessPrompt(promptBlocks);
      if (!prompt.channelID || prompt.events.length === 0) {
        return { stopReason: 'end_turn' };
      }
      channelBySessionID.set(sessionID, prompt.channelID);

      const pendingTurn: ChannelTurn = {
        sessionID,
        taskRunID: undefined,
        notify,
        finish: undefined,
        terminalStopReason: undefined,
      };
      turnByChannel.set(prompt.channelID, pendingTurn);

      let results: IngressResult[];
      try {
        results = await forwardEvents(configuration, prompt);
      } catch (error) {
        releaseTurn(turnByChannel, prompt.channelID, pendingTurn);
        throw error;
      }
      const taskRunID = results.map((result) => result.taskRunID).filter(Boolean).at(-1);
      pendingTurn.taskRunID = taskRunID;
      if (!taskRunID || pendingTurn.terminalStopReason) {
        releaseTurn(turnByChannel, prompt.channelID, pendingTurn);
        return { stopReason: pendingTurn.terminalStopReason ?? 'end_turn' };
      }

      const stopActivityPoller = createTaskActivityPoller(configuration.blueclawTaskEventsURL, taskRunID, (update) => {
        notify('session/update', { sessionId: sessionID, update });
      });
      const turnResult = await holdTurnOpen(turnByChannel, prompt.channelID, pendingTurn, configuration.maximumTurnHoldSeconds);
      stopActivityPoller();
      return turnResult;
    }

    function handleSessionCancel(params: unknown): void {
      const sessionID = readSessionID(params);
      const channelID = sessionID ? channelBySessionID.get(sessionID) : undefined;
      const turn = channelID ? turnByChannel.get(channelID) : undefined;
      if (!turn || turn.sessionID !== sessionID) return;
      if (turn.taskRunID) {
        void cancelBlueclawTask(configuration, turn.taskRunID);
      }
      concludeTurn(turn, 'cancelled');
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
    };
  }

  return { createConnection, relayOutboundReply, finishTurnForChannel };
}

function holdTurnOpen(
  turnByChannel: Map<string, ChannelTurn>,
  channelID: string,
  turn: ChannelTurn,
  maximumHoldSeconds: number,
): Promise<{ stopReason: string }> {
  return new Promise((resolve) => {
    const timeoutHandle = setTimeout(() => finish('end_turn'), maximumHoldSeconds * 1000);
    function finish(stopReason: string): void {
      clearTimeout(timeoutHandle);
      releaseTurn(turnByChannel, channelID, turn);
      resolve({ stopReason });
    }
    turn.finish = finish;
  });
}

function releaseTurn(turnByChannel: Map<string, ChannelTurn>, channelID: string, turn: ChannelTurn): void {
  if (turnByChannel.get(channelID) === turn) {
    turnByChannel.delete(channelID);
  }
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
