import { describe, expect, test } from 'bun:test';
import { tool } from 'ai';
import { z } from 'zod';
import { HarnessAgent, type HarnessAgentAdapter } from '@ai-sdk/harness/agent';
import { HarnessCapabilityUnsupportedError, type HarnessV1 } from '@ai-sdk/harness';
import { inertSandboxProvider } from '../inert-sandbox.ts';
import { createBluecollarHarness } from '../bluecollar-harness.ts';

const recordedInvocations: string[] = [];

const recordEffect = tool({
  description: 'Record an effect through blueclaw, as the requester POSIX actor.',
  inputSchema: z.object({ summary: z.string() }),
  execute: async ({ summary }) => {
    recordedInvocations.push(summary);
    return { recorded: true, summary };
  },
});

function createAgent() {
  return new HarnessAgent({
    harness: createBluecollarHarness(),
    sandbox: inertSandboxProvider,
    tools: { record_effect: recordEffect },
  });
}

describe('bluecollar HarnessV1 adapter', () => {
  test('satisfies the published HarnessV1 and HarnessAgentAdapter types', () => {
    const harness: HarnessV1<{}> = createBluecollarHarness();
    const adapter: HarnessAgentAdapter = harness;
    expect(adapter.specificationVersion).toBe('harness-v1');
    expect(adapter.harnessId).toBe('bluecollar');
    expect(Object.keys(adapter.builtinTools)).toHaveLength(0);
  });

  test('runs one prompt turn whose tool call is executed by the host tools entry', async () => {
    recordedInvocations.length = 0;
    const agent = createAgent();
    const session = await agent.createSession({ sessionId: 'task-run-spike' });
    try {
      const result = await agent.generate({ session, prompt: 'record the effect' });
      expect(recordedInvocations).toEqual(['scripted effect']);
      expect(result.toolCalls.map(toolCall => toolCall.toolName)).toEqual(['record_effect']);
      expect(result.text).toContain('record_effect returned');
    } finally {
      await session.destroy();
    }
  });

  test('refuses the lifecycle verbs bluecollar has no equivalent for', async () => {
    const agent = createAgent();
    const session = await agent.createSession({ sessionId: 'task-run-lifecycle' });
    try {
      expect(session.compact()).rejects.toBeInstanceOf(HarnessCapabilityUnsupportedError);
    } finally {
      await session.destroy();
    }
  });
});
