import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { z } from 'zod';

export enum LLMDAutoRoute {
  LocalFirst = 'local-first',
  RemoteFirst = 'remote-first',
}

export enum LLMDBooleanEnvironmentValue {
  Zero = '0',
  One = '1',
  False = 'false',
  True = 'true',
}

const environmentSchema = z.object({
  BLUECLAW_LLMD_AUTH_KEY: z.string().min(1).optional(),
  BLUECLAW_LLMD_AUTH_KEY_PATH: z.string().min(1).optional(),
  BLUECLAW_LLMD_AUTO_ROUTE: z.enum(LLMDAutoRoute).default(LLMDAutoRoute.RemoteFirst),
  BLUECLAW_LLMD_LLAMA_API_KEY: z.string().default('local'),
  BLUECLAW_LLMD_LLAMA_BASE_URL: z.string().url().optional(),
  BLUECLAW_LLMD_LLAMA_MODEL: z.string().min(1).optional(),
  BLUECLAW_LLMD_LLAMA_STRUCTURED_OUTPUTS_ENABLED: z.enum(LLMDBooleanEnvironmentValue).default(LLMDBooleanEnvironmentValue.False),
  BLUECLAW_LLMD_LOCAL_ONLY: z.enum(LLMDBooleanEnvironmentValue).default(LLMDBooleanEnvironmentValue.False),
  BLUECLAW_LLMD_OPENROUTER_BASE_URL: z.string().url().default('https://openrouter.ai/api/v1'),
  BLUECLAW_LLMD_SOCKET_PATH: z.string().min(1).default('/run/blueclaw-llmd/llmd.sock'),
  BLUECLAW_LLMD_STREAM_IDLE_TIMEOUT_MS: z.coerce.number().int().positive().optional(),
  CREDENTIALS_DIRECTORY: z.string().min(1).optional(),
  OPENROUTER_API_KEY: z.string().min(1).optional(),
  OPENROUTER_API_KEY_PATH: z.string().min(1).optional(),
});

export type LLMDConfiguration = {
  authKey: string;
  autoRoute: LLMDAutoRoute;
  llamaAPIKey: string;
  llamaBaseURL?: string;
  llamaModel?: string;
  llamaStructuredOutputsEnabled: boolean;
  localOnly: boolean;
  openRouterAPIKey?: string;
  openRouterBaseURL: string;
  socketPath: string;
  streamIdleTimeoutMs?: number;
};

export function loadLLMDConfiguration(environment: Record<string, string | undefined>): LLMDConfiguration {
  const parsedEnvironment = environmentSchema.parse(environment);
  return {
    authKey: loadRequiredCredential(
      parsedEnvironment.BLUECLAW_LLMD_AUTH_KEY,
      parsedEnvironment.BLUECLAW_LLMD_AUTH_KEY_PATH,
      parsedEnvironment.CREDENTIALS_DIRECTORY,
      'llmd-auth-key',
    ),
    autoRoute: parsedEnvironment.BLUECLAW_LLMD_AUTO_ROUTE,
    llamaAPIKey: parsedEnvironment.BLUECLAW_LLMD_LLAMA_API_KEY,
    llamaBaseURL: parsedEnvironment.BLUECLAW_LLMD_LLAMA_BASE_URL,
    llamaModel: parsedEnvironment.BLUECLAW_LLMD_LLAMA_MODEL,
    llamaStructuredOutputsEnabled: isEnabledEnvironmentValue(parsedEnvironment.BLUECLAW_LLMD_LLAMA_STRUCTURED_OUTPUTS_ENABLED),
    localOnly: isEnabledEnvironmentValue(parsedEnvironment.BLUECLAW_LLMD_LOCAL_ONLY),
    openRouterAPIKey: loadOptionalCredential(
      parsedEnvironment.OPENROUTER_API_KEY,
      parsedEnvironment.OPENROUTER_API_KEY_PATH,
      parsedEnvironment.CREDENTIALS_DIRECTORY,
      'openrouter-api-key',
    ),
    openRouterBaseURL: parsedEnvironment.BLUECLAW_LLMD_OPENROUTER_BASE_URL,
    socketPath: parsedEnvironment.BLUECLAW_LLMD_SOCKET_PATH,
    streamIdleTimeoutMs: parsedEnvironment.BLUECLAW_LLMD_STREAM_IDLE_TIMEOUT_MS,
  };
}

function isEnabledEnvironmentValue(value: LLMDBooleanEnvironmentValue): boolean {
  return value === LLMDBooleanEnvironmentValue.One || value === LLMDBooleanEnvironmentValue.True;
}

function loadRequiredCredential(
  directValue: string | undefined,
  explicitPath: string | undefined,
  credentialsDirectory: string | undefined,
  credentialName: string,
): string {
  const value = loadOptionalCredential(directValue, explicitPath, credentialsDirectory, credentialName);
  if (!value) throw new Error(`${credentialName} is required`);
  return value;
}

function loadOptionalCredential(
  directValue: string | undefined,
  explicitPath: string | undefined,
  credentialsDirectory: string | undefined,
  credentialName: string,
): string | undefined {
  const directCredential = normalizeCredential(directValue, credentialName);
  if (directCredential) return directCredential;
  const credentialPath = credentialsDirectory ? join(credentialsDirectory, credentialName) : explicitPath;
  if (!credentialPath) return undefined;
  try {
    return normalizeCredential(readFileSync(credentialPath, 'utf8'), credentialName);
  } catch (errorValue) {
    if (isFileNotFoundError(errorValue)) return undefined;
    throw errorValue;
  }
}

function isFileNotFoundError(errorValue: unknown): boolean {
  return errorValue instanceof Error && 'code' in errorValue && errorValue.code === 'ENOENT';
}

function normalizeCredential(value: string | undefined, credentialName: string): string | undefined {
  const normalizedValue = value?.trim();
  if (!normalizedValue) return undefined;
  if (credentialName === 'openrouter-api-key') {
    return normalizedValue.replace(/^OPENROUTER_API_KEY=/, '').trim() || undefined;
  }
  return normalizedValue;
}
