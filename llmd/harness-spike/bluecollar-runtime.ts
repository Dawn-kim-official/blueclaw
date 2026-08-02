import type { HarnessV1ToolSpec } from '@ai-sdk/harness';

export type BluecollarToolSpecification = {
  readonly name: string;
  readonly description?: string;
  readonly inputSchema?: HarnessV1ToolSpec['inputSchema'];
};

export type BluecollarTurnEvent =
  | { readonly kind: 'checkpoint'; readonly message: string }
  | { readonly kind: 'toolCall'; readonly toolCallID: string; readonly toolName: string; readonly input: string }
  | { readonly kind: 'final'; readonly message: string };

export type BluecollarToolOutcome = {
  readonly output: unknown;
  readonly isError?: boolean;
};

export type BluecollarTurnRequest = {
  readonly taskRunID: string;
  readonly prompt: string;
  readonly instructionPrompt?: string;
  readonly toolSpecifications: ReadonlyArray<BluecollarToolSpecification>;
  readonly emitTurnEvent: (event: BluecollarTurnEvent) => void;
  readonly invokeTool: (invocation: {
    readonly toolCallID: string;
    readonly toolName: string;
    readonly input: string;
  }) => Promise<BluecollarToolOutcome>;
};

export type BluecollarTurnResult = {
  readonly finishMessage: string;
  readonly toolNames: ReadonlyArray<string>;
};

export type BluecollarTurnRunner = (request: BluecollarTurnRequest) => Promise<BluecollarTurnResult>;

const scriptedToolInputByToolName: Record<string, string> = {
  record_effect: JSON.stringify({ summary: 'scripted effect' }),
};

function scriptedToolInput(toolName: string): string {
  return scriptedToolInputByToolName[toolName] ?? JSON.stringify({ query: 'scripted input' });
}

export const runScriptedBluecollarTurn: BluecollarTurnRunner = async request => {
  request.emitTurnEvent({ kind: 'checkpoint', message: `working on: ${request.prompt}` });

  const firstToolSpecification = request.toolSpecifications[0];
  if (firstToolSpecification === undefined) {
    const finishMessage = `answered without tools: ${request.prompt}`;
    request.emitTurnEvent({ kind: 'final', message: finishMessage });
    return { finishMessage, toolNames: [] };
  }

  const toolCallID = `${request.taskRunID}-call-1`;
  const input = scriptedToolInput(firstToolSpecification.name);
  request.emitTurnEvent({ kind: 'toolCall', toolCallID, toolName: firstToolSpecification.name, input });

  const outcome = await request.invokeTool({ toolCallID, toolName: firstToolSpecification.name, input });
  const finishMessage = `${firstToolSpecification.name} returned ${JSON.stringify(outcome.output)}`;
  request.emitTurnEvent({ kind: 'final', message: finishMessage });
  return { finishMessage, toolNames: [firstToolSpecification.name] };
};
