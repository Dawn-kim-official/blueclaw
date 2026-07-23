import { chmod, mkdir, rm } from 'node:fs/promises';
import { dirname } from 'node:path';

import type { LLMDConfiguration } from './configuration.ts';

export async function startLLMDServer(
  configuration: LLMDConfiguration,
  handler: (request: Request) => Response | Promise<Response>,
): Promise<Bun.Server<undefined>> {
  await mkdir(dirname(configuration.socketPath), { mode: 0o700, recursive: true });
  if (await isActiveSocket(configuration.socketPath)) {
    throw new Error(`llmd socket is already active: ${configuration.socketPath}`);
  }
  await rm(configuration.socketPath, { force: true });
  const server = Bun.serve({
    fetch: handler,
    maxRequestBodySize: 8 * 1024 * 1024,
    unix: configuration.socketPath,
  });
  await chmod(configuration.socketPath, 0o600);
  return server;
}

export async function stopLLMDServer(server: Bun.Server<undefined>, socketPath: string): Promise<void> {
  await server.stop(true);
  await rm(socketPath, { force: true });
}

async function isActiveSocket(socketPath: string): Promise<boolean> {
  try {
    await fetch('http://llmd/health', {
      signal: AbortSignal.timeout(500),
      unix: socketPath,
    });
    return true;
  } catch {
    return false;
  }
}
