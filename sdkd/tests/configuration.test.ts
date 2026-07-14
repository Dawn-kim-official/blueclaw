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
    expect(configuration.localOnly).toBe(false);
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

  test('prefers the systemd credential directory over explicit credential paths', () => {
    const credentialsDirectory = mkdtempSync(join(tmpdir(), 'blueclaw-sdkd-credentials-'));
    try {
      writeFileSync(join(credentialsDirectory, 'sdkd-auth-key'), 'systemd-installation-key\n', { mode: 0o600 });
      writeFileSync(join(credentialsDirectory, 'openrouter-api-key'), 'systemd-remote-key\n', { mode: 0o600 });

      const configuration = loadSDKDConfiguration({
        BLUECLAW_SDKD_AUTH_KEY_PATH: '/run/credentials/blueclaw-sdkd.service/sdkd-auth-key',
        CREDENTIALS_DIRECTORY: credentialsDirectory,
        OPENROUTER_API_KEY_PATH: '/run/credentials/blueclaw-sdkd.service/openrouter-api-key',
      });

      expect(configuration.authKey).toBe('systemd-installation-key');
      expect(configuration.openRouterAPIKey).toBe('systemd-remote-key');
    } finally {
      rmSync(credentialsDirectory, { force: true, recursive: true });
    }
  });

  test('keeps explicit credential paths for standalone execution', () => {
    const credentialsDirectory = mkdtempSync(join(tmpdir(), 'blueclaw-sdkd-credentials-'));
    try {
      const authKeyPath = join(credentialsDirectory, 'standalone-auth-key');
      const openRouterAPIKeyPath = join(credentialsDirectory, 'standalone-openrouter-key');
      writeFileSync(authKeyPath, 'standalone-installation-key\n', { mode: 0o600 });
      writeFileSync(openRouterAPIKeyPath, 'OPENROUTER_API_KEY=standalone-remote-key\n', { mode: 0o600 });

      const configuration = loadSDKDConfiguration({
        BLUECLAW_SDKD_AUTH_KEY_PATH: authKeyPath,
        OPENROUTER_API_KEY_PATH: openRouterAPIKeyPath,
      });

      expect(configuration.authKey).toBe('standalone-installation-key');
      expect(configuration.openRouterAPIKey).toBe('standalone-remote-key');
    } finally {
      rmSync(credentialsDirectory, { force: true, recursive: true });
    }
  });

  test('allows an omitted optional credential in the systemd directory', () => {
    const credentialsDirectory = mkdtempSync(join(tmpdir(), 'blueclaw-sdkd-credentials-'));
    try {
      writeFileSync(join(credentialsDirectory, 'sdkd-auth-key'), 'installation-key\n', { mode: 0o600 });

      const configuration = loadSDKDConfiguration({ CREDENTIALS_DIRECTORY: credentialsDirectory });

      expect(configuration.authKey).toBe('installation-key');
      expect(configuration.openRouterAPIKey).toBeUndefined();
    } finally {
      rmSync(credentialsDirectory, { force: true, recursive: true });
    }
  });
});
