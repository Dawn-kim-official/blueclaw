import { describe, expect, test } from 'bun:test';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import { LLMDAutoRoute, loadLLMDConfiguration } from '../src/configuration.ts';

describe('llmd configuration', () => {
  test('loads secure defaults and explicit routes', () => {
    const configuration = loadLLMDConfiguration({
      BLUECLAW_LLMD_AUTH_KEY: 'installation-key',
      BLUECLAW_LLMD_LLAMA_BASE_URL: 'http://127.0.0.1:8080/v1',
      BLUECLAW_LLMD_LLAMA_MODEL: 'local-model',
      OPENROUTER_API_KEY: 'remote-key',
    });

    expect(configuration.socketPath).toBe('/run/blueclaw-llmd/llmd.sock');
    expect(configuration.autoRoute).toBe(LLMDAutoRoute.RemoteFirst);
    expect(configuration.llamaModel).toBe('local-model');
    expect(configuration.openRouterBaseURL).toBe('https://openrouter.ai/api/v1');
    expect(configuration.localOnly).toBe(false);
  });

  test('requires an installation auth key', () => {
    expect(() => loadLLMDConfiguration({})).toThrow();
  });

  test('loads systemd credentials without exposing them in environment values', () => {
    const credentialsDirectory = mkdtempSync(join(tmpdir(), 'blueclaw-llmd-credentials-'));
    try {
      writeFileSync(join(credentialsDirectory, 'llmd-auth-key'), 'installation-key\n', { mode: 0o600 });
      writeFileSync(join(credentialsDirectory, 'openrouter-api-key'), 'OPENROUTER_API_KEY=remote-key\n', { mode: 0o600 });

      const configuration = loadLLMDConfiguration({ CREDENTIALS_DIRECTORY: credentialsDirectory });

      expect(configuration.authKey).toBe('installation-key');
      expect(configuration.openRouterAPIKey).toBe('remote-key');
    } finally {
      rmSync(credentialsDirectory, { force: true, recursive: true });
    }
  });

  test('prefers the systemd credential directory over explicit credential paths', () => {
    const credentialsDirectory = mkdtempSync(join(tmpdir(), 'blueclaw-llmd-credentials-'));
    try {
      writeFileSync(join(credentialsDirectory, 'llmd-auth-key'), 'systemd-installation-key\n', { mode: 0o600 });
      writeFileSync(join(credentialsDirectory, 'openrouter-api-key'), 'systemd-remote-key\n', { mode: 0o600 });

      const configuration = loadLLMDConfiguration({
        BLUECLAW_LLMD_AUTH_KEY_PATH: '/run/credentials/blueclaw-llmd.service/llmd-auth-key',
        CREDENTIALS_DIRECTORY: credentialsDirectory,
        OPENROUTER_API_KEY_PATH: '/run/credentials/blueclaw-llmd.service/openrouter-api-key',
      });

      expect(configuration.authKey).toBe('systemd-installation-key');
      expect(configuration.openRouterAPIKey).toBe('systemd-remote-key');
    } finally {
      rmSync(credentialsDirectory, { force: true, recursive: true });
    }
  });

  test('keeps explicit credential paths for standalone execution', () => {
    const credentialsDirectory = mkdtempSync(join(tmpdir(), 'blueclaw-llmd-credentials-'));
    try {
      const authKeyPath = join(credentialsDirectory, 'standalone-auth-key');
      const openRouterAPIKeyPath = join(credentialsDirectory, 'standalone-openrouter-key');
      writeFileSync(authKeyPath, 'standalone-installation-key\n', { mode: 0o600 });
      writeFileSync(openRouterAPIKeyPath, 'OPENROUTER_API_KEY=standalone-remote-key\n', { mode: 0o600 });

      const configuration = loadLLMDConfiguration({
        BLUECLAW_LLMD_AUTH_KEY_PATH: authKeyPath,
        OPENROUTER_API_KEY_PATH: openRouterAPIKeyPath,
      });

      expect(configuration.authKey).toBe('standalone-installation-key');
      expect(configuration.openRouterAPIKey).toBe('standalone-remote-key');
    } finally {
      rmSync(credentialsDirectory, { force: true, recursive: true });
    }
  });

  test('allows an omitted optional credential in the systemd directory', () => {
    const credentialsDirectory = mkdtempSync(join(tmpdir(), 'blueclaw-llmd-credentials-'));
    try {
      writeFileSync(join(credentialsDirectory, 'llmd-auth-key'), 'installation-key\n', { mode: 0o600 });

      const configuration = loadLLMDConfiguration({ CREDENTIALS_DIRECTORY: credentialsDirectory });

      expect(configuration.authKey).toBe('installation-key');
      expect(configuration.openRouterAPIKey).toBeUndefined();
    } finally {
      rmSync(credentialsDirectory, { force: true, recursive: true });
    }
  });
});
