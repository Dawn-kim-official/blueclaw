import { afterEach, describe, expect, test } from 'bun:test';
import { mkdtemp, rm, stat } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';

import { LLMDAutoRoute, type LLMDConfiguration } from '../src/configuration.ts';
import { startLLMDServer, stopLLMDServer } from '../src/server.ts';

const temporaryDirectories: string[] = [];

afterEach(async () => {
  await Promise.all(temporaryDirectories.splice(0).map(path => rm(path, { force: true, recursive: true })));
});

describe('llmd server', () => {
  test('serves HTTP through a private Unix socket', async () => {
    const temporaryDirectory = await mkdtemp(join(tmpdir(), 'blueclaw-llmd-'));
    temporaryDirectories.push(temporaryDirectory);
    const configuration = testConfiguration(join(temporaryDirectory, 'runtime', 'llmd.sock'));
    const server = await startLLMDServer(configuration, () => Response.json({ status: 'ok' }));

    try {
      const response = await fetch('http://llmd/health', { unix: configuration.socketPath });
      const socketInformation = await stat(configuration.socketPath);

      expect(await response.json()).toEqual({ status: 'ok' });
      expect(socketInformation.mode & 0o777).toBe(0o600);
    } finally {
      await stopLLMDServer(server, configuration.socketPath);
    }
  });

  test('refuses to replace an active socket and removes its socket on shutdown', async () => {
    const temporaryDirectory = await mkdtemp(join(tmpdir(), 'blueclaw-llmd-'));
    temporaryDirectories.push(temporaryDirectory);
    const configuration = testConfiguration(join(temporaryDirectory, 'runtime', 'llmd.sock'));
    const server = await startLLMDServer(configuration, () => Response.json({ status: 'ok' }));

    try {
      await expect(startLLMDServer(configuration, () => Response.json({ status: 'duplicate' }))).rejects.toThrow(
        'llmd socket is already active',
      );
    } finally {
      await stopLLMDServer(server, configuration.socketPath);
    }
    await expect(stat(configuration.socketPath)).rejects.toThrow();
  });
});

function testConfiguration(socketPath: string): LLMDConfiguration {
  return {
    authKey: 'installation-key',
    autoRoute: LLMDAutoRoute.RemoteFirst,
    llamaAPIKey: 'local',
    llamaStructuredOutputsEnabled: false,
    localOnly: false,
    openRouterBaseURL: 'https://openrouter.ai/api/v1',
    socketPath,
  };
}
