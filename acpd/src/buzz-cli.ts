import type { AcpdConfiguration } from './configuration.ts';

export type BuzzCommandRunner = (commandArguments: string[], standardInput?: string) => Promise<string>;

export function createBuzzCommandRunner(configuration: AcpdConfiguration): BuzzCommandRunner {
  return async function runBuzzCommand(commandArguments, standardInput) {
    const subprocess = Bun.spawn([configuration.buzzCommand, ...commandArguments], {
      stdin: standardInput === undefined ? 'ignore' : new TextEncoder().encode(standardInput),
      stdout: 'pipe',
      stderr: 'pipe',
    });
    const [standardOutput, standardError, exitCode] = await Promise.all([
      new Response(subprocess.stdout).text(),
      new Response(subprocess.stderr).text(),
      subprocess.exited,
    ]);
    if (exitCode !== 0) {
      throw new Error(`buzz ${commandArguments[0] ?? ''} ${commandArguments[1] ?? ''} exited ${exitCode}: ${standardError.trim() || standardOutput.trim()}`);
    }
    return standardOutput;
  };
}

export async function sendChannelMessage(
  runBuzzCommand: BuzzCommandRunner,
  channelID: string,
  content: string,
  replyToEventID: string | undefined,
  filePaths: string[],
): Promise<string> {
  const commandArguments = ['messages', 'send', '--channel', channelID, '--content', '-'];
  if (replyToEventID) commandArguments.push('--reply-to', replyToEventID);
  for (const filePath of filePaths) commandArguments.push('--file', filePath);
  const output = await runBuzzCommand(commandArguments, content);
  return readStringField(output, 'event_id');
}

export async function resolveUserProfile(
  runBuzzCommand: BuzzCommandRunner,
  pubkeyHex: string,
): Promise<{ displayName?: string; email?: string }> {
  const output = await runBuzzCommand(['users', 'get', '--pubkey', pubkeyHex]);
  const profile = findProfileDocument(parseJSONSafely(output), pubkeyHex);
  if (!profile) return {};
  const displayName = readOptionalString(profile['display_name']) ?? readOptionalString(profile['name']);
  const email = readOptionalString(profile['nip05']) ?? readOptionalString(profile['nip05_handle']);
  return { displayName, email };
}

export async function addMessageReaction(
  runBuzzCommand: BuzzCommandRunner,
  messageID: string,
  emoji: string,
): Promise<void> {
  await runBuzzCommand(['reactions', 'add', '--event', messageID, '--emoji', emoji]);
}

export async function removeMessageReaction(
  runBuzzCommand: BuzzCommandRunner,
  messageID: string,
  emoji: string,
): Promise<void> {
  await runBuzzCommand(['reactions', 'remove', '--event', messageID, '--emoji', emoji]);
}

function readStringField(output: string, fieldName: string): string {
  const document = parseJSONSafely(output);
  if (document && typeof document === 'object' && !Array.isArray(document)) {
    const value = (document as Record<string, unknown>)[fieldName];
    if (typeof value === 'string') return value;
  }
  return '';
}

function findProfileDocument(document: unknown, pubkeyHex: string): Record<string, unknown> | undefined {
  if (Array.isArray(document)) {
    const profiles = document.filter(isRecord);
    return profiles.find((profile) => profile['pubkey'] === pubkeyHex) ?? profiles[0];
  }
  if (isRecord(document)) {
    const nested = document['users'] ?? document['profiles'];
    if (Array.isArray(nested)) return findProfileDocument(nested, pubkeyHex);
    return document;
  }
  return undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function readOptionalString(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() !== '' ? value.trim() : undefined;
}

function parseJSONSafely(value: string): unknown {
  try {
    return JSON.parse(value);
  } catch {
    return undefined;
  }
}
