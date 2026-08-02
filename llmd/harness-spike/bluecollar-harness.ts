import {
  HarnessCapabilityUnsupportedError,
  type HarnessV1,
  type HarnessV1ContinueTurnState,
  type HarnessV1Prompt,
  type HarnessV1PromptControl,
  type HarnessV1PromptTurnOptions,
  type HarnessV1ResumeSessionState,
  type HarnessV1Session,
  type HarnessV1StreamPart,
  type HarnessV1ToolSpec,
} from '@ai-sdk/harness';
import {
  runScriptedBluecollarTurn,
  type BluecollarToolOutcome,
  type BluecollarTurnEvent,
  type BluecollarTurnRunner,
} from './bluecollar-runtime.ts';

export const bluecollarHarnessIdentifier = 'bluecollar';

type FinishStreamPart = Extract<HarnessV1StreamPart, { type: 'finish' }>;
type TurnUsage = FinishStreamPart['totalUsage'];
type TurnFinishReason = FinishStreamPart['finishReason'];
type UnifiedFinishReason = TurnFinishReason['unified'];

const emptyUsage: TurnUsage = {
  inputTokens: { total: undefined, noCache: undefined, cacheRead: undefined, cacheWrite: undefined },
  outputTokens: { total: undefined, text: undefined, reasoning: undefined },
};

function flattenPromptToText(prompt: HarnessV1Prompt): string {
  if (typeof prompt === 'string') return prompt;
  if (typeof prompt.content === 'string') return prompt.content;
  return prompt.content
    .map(part => (part.type === 'text' ? part.text : `[${part.type}]`))
    .join('\n');
}

function toBluecollarToolSpecifications(toolSpecs: ReadonlyArray<HarnessV1ToolSpec> | undefined) {
  return (toolSpecs ?? []).map(toolSpec => ({
    name: toolSpec.name,
    ...(toolSpec.description === undefined ? {} : { description: toolSpec.description }),
    ...(toolSpec.inputSchema === undefined ? {} : { inputSchema: toolSpec.inputSchema }),
  }));
}

function textStreamParts(identifier: string, text: string): ReadonlyArray<HarnessV1StreamPart> {
  return [
    { type: 'text-start', id: identifier },
    { type: 'text-delta', id: identifier, delta: text },
    { type: 'text-end', id: identifier },
  ];
}

function finishReason(unified: UnifiedFinishReason): TurnFinishReason {
  return { unified, raw: undefined };
}

function finishStepStreamPart(unified: UnifiedFinishReason): HarnessV1StreamPart {
  return { type: 'finish-step', finishReason: finishReason(unified), usage: emptyUsage };
}

function finishStreamPart(unified: UnifiedFinishReason): HarnessV1StreamPart {
  return { type: 'finish', finishReason: finishReason(unified), totalUsage: emptyUsage };
}

function turnEventStreamParts(turnEvent: BluecollarTurnEvent): ReadonlyArray<HarnessV1StreamPart> {
  if (turnEvent.kind === 'checkpoint') return textStreamParts('checkpoint', turnEvent.message);
  if (turnEvent.kind === 'final') return textStreamParts('final', turnEvent.message);
  return [
    {
      type: 'tool-call',
      toolCallId: turnEvent.toolCallID,
      toolName: turnEvent.toolName,
      input: turnEvent.input,
    },
    finishStepStreamPart('tool-calls'),
  ];
}

function lifecycleStateData(taskRunID: string) {
  return { taskRunID };
}

function resumeSessionState(taskRunID: string): HarnessV1ResumeSessionState {
  return {
    type: 'resume-session',
    harnessId: bluecollarHarnessIdentifier,
    specificationVersion: 'harness-v1',
    data: lifecycleStateData(taskRunID),
  };
}

class BluecollarPendingToolCalls {
  private readonly resolverByToolCallID = new Map<string, (outcome: BluecollarToolOutcome) => void>();

  await(toolCallID: string): Promise<BluecollarToolOutcome> {
    return new Promise(resolve => this.resolverByToolCallID.set(toolCallID, resolve));
  }

  settle(toolCallID: string, outcome: BluecollarToolOutcome): void {
    const resolve = this.resolverByToolCallID.get(toolCallID);
    if (resolve === undefined) return;
    this.resolverByToolCallID.delete(toolCallID);
    resolve(outcome);
  }
}

function startPromptTurn(
  taskRunID: string,
  runTurn: BluecollarTurnRunner,
  options: HarnessV1PromptTurnOptions,
): HarnessV1PromptControl {
  const pendingToolCalls = new BluecollarPendingToolCalls();

  options.emit({ type: 'stream-start', modelId: bluecollarHarnessIdentifier });

  const done = runTurn({
    taskRunID,
    prompt: flattenPromptToText(options.prompt),
    ...(options.instructions === undefined ? {} : { instructionPrompt: options.instructions }),
    toolSpecifications: toBluecollarToolSpecifications(options.tools),
    emitTurnEvent: turnEvent => {
      for (const streamPart of turnEventStreamParts(turnEvent)) options.emit(streamPart);
    },
    invokeTool: invocation => pendingToolCalls.await(invocation.toolCallID),
  }).then(() => {
    options.emit(finishStepStreamPart('stop'));
    options.emit(finishStreamPart('stop'));
  });

  return {
    submitToolResult: async input => {
      pendingToolCalls.settle(input.toolCallId, {
        output: input.output,
        ...(input.isError === undefined ? {} : { isError: input.isError }),
      });
    },
    done,
  };
}

function unsupported(capability: string): HarnessCapabilityUnsupportedError {
  return new HarnessCapabilityUnsupportedError({
    message: `bluecollar has no ${capability}: a task run is resumed by re-driving RunTurn from taskstate, never by attaching to a live turn.`,
    harnessId: bluecollarHarnessIdentifier,
  });
}

function createSession(taskRunID: string, isResume: boolean, runTurn: BluecollarTurnRunner): HarnessV1Session {
  return {
    sessionId: taskRunID,
    isResume,
    modelId: bluecollarHarnessIdentifier,
    doPromptTurn: async options => startPromptTurn(taskRunID, runTurn, options),
    doCompact: async () => {
      throw unsupported('manual compaction trigger');
    },
    doContinueTurn: async () => {
      throw unsupported('continuable in-flight turn');
    },
    doSuspendTurn: async (): Promise<HarnessV1ContinueTurnState> => {
      throw unsupported('suspendable in-flight turn');
    },
    doDetach: async () => resumeSessionState(taskRunID),
    doStop: async () => resumeSessionState(taskRunID),
    doDestroy: async () => {},
  };
}

export function createBluecollarHarness(
  options: { readonly runTurn?: BluecollarTurnRunner } = {},
): HarnessV1<{}> {
  const runTurn = options.runTurn ?? runScriptedBluecollarTurn;
  return {
    specificationVersion: 'harness-v1',
    harnessId: bluecollarHarnessIdentifier,
    builtinTools: {},
    supportsBuiltinToolApprovals: false,
    supportsBuiltinToolFiltering: true,
    doStart: async startOptions =>
      createSession(
        startOptions.sessionId,
        startOptions.resumeFrom !== undefined || startOptions.continueFrom !== undefined,
        runTurn,
      ),
  };
}
