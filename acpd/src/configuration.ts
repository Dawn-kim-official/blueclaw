export type AcpdConfiguration = {
  blueclawEventsURL: string;
  blueclawTaskCancelURL: string;
  buzzCommand: string;
  listenPort: number;
  maximumTurnHoldSeconds: number;
};

export function loadConfiguration(environment: Record<string, string | undefined>): AcpdConfiguration {
  const blueclawBaseURL = trimTrailingSlash(environment['ACPD_BLUECLAW_BASE_URL'] ?? 'http://127.0.0.1:8080');
  return {
    blueclawEventsURL: `${blueclawBaseURL}/connectors/buzz/events`,
    blueclawTaskCancelURL: `${blueclawBaseURL}/admin/api/task/cancel`,
    buzzCommand: environment['ACPD_BUZZ_COMMAND']?.trim() || 'buzz',
    listenPort: parseListenPort(environment['ACPD_LISTEN_PORT']),
    maximumTurnHoldSeconds: parsePositiveInteger(environment['ACPD_MAXIMUM_TURN_HOLD_SECONDS'], 3300),
  };
}

function trimTrailingSlash(value: string): string {
  return value.endsWith('/') ? value.slice(0, -1) : value;
}

function parseListenPort(value: string | undefined): number {
  const port = Number(value ?? '18091');
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error('ACPD_LISTEN_PORT must be a valid TCP port');
  }
  return port;
}

function parsePositiveInteger(value: string | undefined, fallback: number): number {
  if (value === undefined || value.trim() === '') return fallback;
  const parsed = Number(value);
  if (!Number.isInteger(parsed) || parsed < 1) {
    throw new Error('ACPD_MAXIMUM_TURN_HOLD_SECONDS must be a positive integer');
  }
  return parsed;
}
