import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { z } from 'zod';

export enum SDKDAutoRoute {
  LocalFirst = 'local-first',
  RemoteFirst = 'remote-first',
}

export enum SDKDBooleanEnvironmentValue {
  Zero = '0',
  One = '1',
  False = 'false',
  True = 'true',
}

const environmentSchema = z.object({
  BLUECLAW_SDKD_AUTH_KEY: z.string().min(1).optional(),
  BLUECLAW_SDKD_AUTH_KEY_PATH: z.string().min(1).optional(),
  BLUECLAW_SDKD_AUTO_ROUTE: z.enum(SDKDAutoRoute).default(SDKDAutoRoute.RemoteFirst),
  BLUECLAW_SDKD_LLAMA_API_KEY: z.string().default('local'),
  BLUECLAW_SDKD_LLAMA_BASE_URL: z.string().url().optional(),
  BLUECLAW_SDKD_LLAMA_MODEL: z.string().min(1).optional(),
  BLUECLAW_SDKD_LLAMA_STRUCTURED_OUTPUTS_ENABLED: z.enum(SDKDBooleanEnvironmentValue).default(SDKDBooleanEnvironmentValue.False),
  BLUECLAW_SDKD_LOCAL_ONLY: z.enum(SDKDBooleanEnvironmentValue).default(SDKDBooleanEnvironmentValue.False),
  BLUECLAW_SDKD_OPENROUTER_BASE_URL: z.string().url().default('https://openrouter.ai/api/v1'),
  BLUECLAW_SDKD_REQUEST_TIMEOUT_MILLISECOND: z.coerce.number().int().positive().default(60000),
  BLUECLAW_SDKD_SOCKET_PATH: z.string().min(1).default('/run/blueclaw-sdkd/sdkd.sock'),
  CREDENTIALS_DIRECTORY: z.string().min(1).optional(),
  OPENROUTER_API_KEY: z.string().min(1).optional(),
  OPENROUTER_API_KEY_PATH: z.string().min(1).optional(),
});

export type SDKDConfiguration = {
  authKey: string;
  autoRoute: SDKDAutoRoute;
  llamaAPIKey: string;
  llamaBaseURL?: string;
  llamaModel?: string;
  llamaStructuredOutputsEnabled: boolean;
  localOnly: boolean;
  openRouterAPIKey?: string;
  openRouterBaseURL: string;
  requestTimeoutMillisecond: number;
  socketPath: string;
};

export function loadSDKDConfiguration(environment: Record<string, string | undefined>): SDKDConfiguration {
  const parsedEnvironment = environmentSchema.parse(environment);
  return {
    authKey: loadRequiredCredential(
      parsedEnvironment.BLUECLAW_SDKD_AUTH_KEY,
      parsedEnvironment.BLUECLAW_SDKD_AUTH_KEY_PATH,
      parsedEnvironment.CREDENTIALS_DIRECTORY,
      'sdkd-auth-key',
    ),
    autoRoute: parsedEnvironment.BLUECLAW_SDKD_AUTO_ROUTE,
    llamaAPIKey: parsedEnvironment.BLUECLAW_SDKD_LLAMA_API_KEY,
    llamaBaseURL: parsedEnvironment.BLUECLAW_SDKD_LLAMA_BASE_URL,
    llamaModel: parsedEnvironment.BLUECLAW_SDKD_LLAMA_MODEL,
    llamaStructuredOutputsEnabled: isEnabledEnvironmentValue(parsedEnvironment.BLUECLAW_SDKD_LLAMA_STRUCTURED_OUTPUTS_ENABLED),
    localOnly: isEnabledEnvironmentValue(parsedEnvironment.BLUECLAW_SDKD_LOCAL_ONLY),
    openRouterAPIKey: loadOptionalCredential(
      parsedEnvironment.OPENROUTER_API_KEY,
      parsedEnvironment.OPENROUTER_API_KEY_PATH,
      parsedEnvironment.CREDENTIALS_DIRECTORY,
      'openrouter-api-key',
    ),
    openRouterBaseURL: parsedEnvironment.BLUECLAW_SDKD_OPENROUTER_BASE_URL,
    requestTimeoutMillisecond: parsedEnvironment.BLUECLAW_SDKD_REQUEST_TIMEOUT_MILLISECOND,
    socketPath: parsedEnvironment.BLUECLAW_SDKD_SOCKET_PATH,
  };
}

function isEnabledEnvironmentValue(value: SDKDBooleanEnvironmentValue): boolean {
  return value === SDKDBooleanEnvironmentValue.One || value === SDKDBooleanEnvironmentValue.True;
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
