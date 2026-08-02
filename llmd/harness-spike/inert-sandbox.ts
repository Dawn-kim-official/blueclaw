import type { HarnessV1NetworkSandboxSession, HarnessV1SandboxProvider } from '@ai-sdk/harness';
import { HarnessCapabilityUnsupportedError } from '@ai-sdk/harness';

type RestrictedSandboxSession = ReturnType<HarnessV1NetworkSandboxSession['restricted']>;

const inertSandboxDescription =
  'bluecollar executes every tool inside blueclaw as the requester POSIX actor; this harness session has no sandbox of its own.';

function refuse(operation: string): never {
  throw new HarnessCapabilityUnsupportedError({
    message: `${operation} is unavailable: ${inertSandboxDescription}`,
    harnessId: 'bluecollar',
  });
}

function createInertSandboxSession(sessionIdentifier: string): HarnessV1NetworkSandboxSession {
  const fileContentByPath = new Map<string, Uint8Array>();
  const textEncoder = new TextEncoder();
  const textDecoder = new TextDecoder();

  const readBinaryFile = async ({ path }: { path: string }) => fileContentByPath.get(path) ?? null;

  const restricted: RestrictedSandboxSession = {
    description: inertSandboxDescription,
    readBinaryFile,
    readFile: async options => {
      const content = await readBinaryFile(options);
      if (content === null) return null;
      return new ReadableStream<Uint8Array>({
        start(controller) {
          controller.enqueue(content);
          controller.close();
        },
      });
    },
    readTextFile: async options => {
      const content = await readBinaryFile(options);
      return content === null ? null : textDecoder.decode(content);
    },
    writeBinaryFile: async ({ path, content }) => {
      fileContentByPath.set(path, content);
    },
    writeTextFile: async ({ path, content }) => {
      fileContentByPath.set(path, textEncoder.encode(content));
    },
    writeFile: async ({ path, content }) => {
      fileContentByPath.set(path, new Uint8Array(await new Response(content).arrayBuffer()));
    },
    spawn: () => refuse('spawn'),
    run: async () => ({ exitCode: 0, stdout: '', stderr: '' }),
  };

  return {
    ...restricted,
    id: sessionIdentifier,
    defaultWorkingDirectory: '/workspace',
    ports: [],
    getPortUrl: () => refuse('getPortUrl'),
    stop: async () => {},
    restricted: () => restricted,
  };
}

export const inertSandboxProvider: HarnessV1SandboxProvider = {
  specificationVersion: 'harness-sandbox-v1',
  providerId: 'inert',
  createSession: async options => createInertSandboxSession(options?.sessionId ?? 'inert'),
};
