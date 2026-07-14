import { afterEach, describe, expect, test } from 'bun:test';
import { mkdtemp, rm, stat } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';

import type { SDKDConfiguration } from '../src/configuration.ts';
import { startSDKDServer, stopSDKDServer } from '../src/server.ts';

const temporaryDirectories: string[] = [];

afterEach(async () => {
  await Promise.all(temporaryDirectories.splice(0).map(path => rm(path, { force: true, recursive: true })));
});

describe('sdkd server', () => {
  test('serves HTTP through a private Unix socket', async () => {
    const temporaryDirectory = await mkdtemp(join(tmpdir(), 'blueclaw-sdkd-'));
    temporaryDirectories.push(temporaryDirectory);
    const configuration = testConfiguration(join(temporaryDirectory, 'runtime', 'sdkd.sock'));
    const server = await startSDKDServer(configuration, () => Response.json({ status: 'ok' }));

    try {
      const response = await fetch('http://sdkd/health', { unix: configuration.socketPath });
      const socketInformation = await stat(configuration.socketPath);

      expect(await response.json()).toEqual({ status: 'ok' });
      expect(socketInformation.mode & 0o777).toBe(0o600);
    } finally {
      await stopSDKDServer(server, configuration.socketPath);
    }
  });

  test('refuses to replace an active socket and removes its socket on shutdown', async () => {
    const temporaryDirectory = await mkdtemp(join(tmpdir(), 'blueclaw-sdkd-'));
    temporaryDirectories.push(temporaryDirectory);
    const configuration = testConfiguration(join(temporaryDirectory, 'runtime', 'sdkd.sock'));
    const server = await startSDKDServer(configuration, () => Response.json({ status: 'ok' }));

    try {
      await expect(startSDKDServer(configuration, () => Response.json({ status: 'duplicate' }))).rejects.toThrow(
        'sdkd socket is already active',
      );
    } finally {
      await stopSDKDServer(server, configuration.socketPath);
    }
    await expect(stat(configuration.socketPath)).rejects.toThrow();
  });
});

function testConfiguration(socketPath: string): SDKDConfiguration {
  return {
    authKey: 'installation-key',
    autoRoute: 'remote-first',
    llamaAPIKey: 'local',
    llamaStructuredOutputsEnabled: false,
    localOnly: false,
    openRouterBaseURL: 'https://openrouter.ai/api/v1',
    requestTimeoutMillisecond: 60000,
    socketPath,
  };
}
