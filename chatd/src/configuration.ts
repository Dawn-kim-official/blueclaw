export type ChatdConfiguration = {
  botUserName: string;
  mattermostBaseURL: string;
  mattermostBotToken: string;
  actionCallbackURL: string | undefined;
  blueclawIngressURL: string;
  listenPort: number;
};

export function loadConfiguration(environment: Record<string, string | undefined>): ChatdConfiguration {
  return {
    botUserName: requireValue(environment, 'CHATD_BOT_USER_NAME'),
    mattermostBaseURL: requireValue(environment, 'CHATD_MATTERMOST_BASE_URL'),
    mattermostBotToken: requireValue(environment, 'CHATD_MATTERMOST_BOT_TOKEN'),
    actionCallbackURL: environment['CHATD_ACTION_CALLBACK_URL'],
    blueclawIngressURL: requireValue(environment, 'CHATD_BLUECLAW_INGRESS_URL'),
    listenPort: parseListenPort(environment['CHATD_LISTEN_PORT']),
  };
}

function requireValue(environment: Record<string, string | undefined>, key: string): string {
  const value = environment[key]?.trim();
  if (!value) throw new Error(`${key} is not configured`);
  return value;
}

function parseListenPort(value: string | undefined): number {
  const port = Number(value ?? '18090');
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error('CHATD_LISTEN_PORT must be a valid TCP port');
  }
  return port;
}
