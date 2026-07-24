import { Chat, type Adapter } from 'chat';
import { createMemoryState } from '@chat-adapter/state-memory';
import { createBuzzAdapter } from './adapters/buzz/index.ts';
import { createMattermostAdapter } from './adapters/mattermost/index.ts';
import { loadConfiguration } from './configuration.ts';
import { createBridge } from './bridge.ts';
import { createOutboundHandler } from './outbound.ts';

const configuration = loadConfiguration(process.env);

const adapters: Record<string, Adapter> = {};
if (configuration.mattermost) {
  adapters.mattermost = createMattermostAdapter({
    baseUrl: configuration.mattermost.baseURL,
    botToken: configuration.mattermost.botToken,
    callbackUrl: configuration.mattermost.actionCallbackURL,
  });
}
if (configuration.buzz) {
  adapters.buzz = createBuzzAdapter({
    relayURL: configuration.buzz.relayURL,
    privateKeyHex: configuration.buzz.privateKeyHex,
    botDisplayName: configuration.botUserName,
    accountLinksPath: configuration.buzz.accountLinksPath,
    authTagJSON: configuration.buzz.authTagJSON,
  });
}

const chat = new Chat({
  userName: configuration.botUserName,
  state: createMemoryState(),
  concurrency: 'queue',
  adapters,
});

createBridge(chat, configuration);

await chat.initialize();

const outboundHandler = createOutboundHandler(adapters as never, configuration);

Bun.serve({
  port: configuration.listenPort,
  hostname: '127.0.0.1',
  fetch: async (request) => {
    const requestUrl = new URL(request.url);
    if (requestUrl.pathname === '/webhooks/mattermost' && adapters.mattermost) {
      return chat.webhooks.mattermost?.(request) ?? new Response('Not Found', { status: 404 });
    }
    if (requestUrl.pathname.startsWith('/v1/platform/')) {
      return outboundHandler(request);
    }
    return new Response('Not Found', { status: 404 });
  },
});
