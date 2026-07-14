import { describe, expect, test } from 'bun:test';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import { loadSDKDConfiguration } from '../src/configuration.ts';

describe('sdkd configuration', () => {
  test('loads secure defaults and explicit routes', () => {
    const configuration = loadSDKDConfiguration({
      BLUECLAW_SDKD_AUTH_KEY: 'installation-key',
      BLUECLAW_SDKD_LLAMA_BASE_URL: 'http://127.0.0.1:8080/v1',
      BLUECLAW_SDKD_LLAMA_MODEL: 'local-model',
      OPENROUTER_API_KEY: 'remote-key',
    });

    expect(configuration.socketPath).toBe('/run/blueclaw-sdkd/sdkd.sock');
    expect(configuration.autoRoute).toBe('remote-first');
    expect(configuration.llamaModel).toBe('local-model');
    expect(configuration.openRouterBaseURL).toBe('https://openrouter.ai/api/v1');
  });

  test('requires an installation auth key', () => {
    expect(() => loadSDKDConfiguration({})).toThrow();
  });

  test('loads systemd credentials without exposing them in environment values', () => {
    const credentialsDirectory = mkdtempSync(join(tmpdir(), 'blueclaw-sdkd-credentials-'));
    try {
      writeFileSync(join(credentialsDirectory, 'sdkd-auth-key'), 'installation-key\n', { mode: 0o600 });
      writeFileSync(join(credentialsDirectory, 'openrouter-api-key'), 'OPENROUTER_API_KEY=remote-key\n', { mode: 0o600 });

      const configuration = loadSDKDConfiguration({ CREDENTIALS_DIRECTORY: credentialsDirectory });

      expect(configuration.authKey).toBe('installation-key');
      expect(configuration.openRouterAPIKey).toBe('remote-key');
    } finally {
      rmSync(credentialsDirectory, { force: true, recursive: true });
    }
  });
});
