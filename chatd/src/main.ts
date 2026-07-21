import { Chat } from 'chat';
import { createMemoryState } from '@chat-adapter/state-memory';
import { createMattermostAdapter } from './adapters/mattermost/index.ts';
import { loadConfiguration } from './configuration.ts';
import { createBridge } from './bridge.ts';
import { createOutboundHandler } from './outbound.ts';

const configuration = loadConfiguration(process.env);
const mattermostAdapter = createMattermostAdapter({
  baseUrl: configuration.mattermostBaseURL,
  botToken: configuration.mattermostBotToken,
  callbackUrl: configuration.actionCallbackURL,
});
const chat = new Chat({
  userName: configuration.botUserName,
  state: createMemoryState(),
  concurrency: 'queue',
  adapters: {
    mattermost: mattermostAdapter,
  },
});

createBridge(chat, configuration);

await chat.initialize();

const outboundHandler = createOutboundHandler(mattermostAdapter, configuration);

Bun.serve({
  port: configuration.listenPort,
  hostname: '127.0.0.1',
  fetch: async (request) => {
    const requestUrl = new URL(request.url);
    if (requestUrl.pathname === '/webhooks/mattermost') {
      return chat.webhooks.mattermost(request);
    }
    if (requestUrl.pathname.startsWith('/v1/platform/')) {
      return outboundHandler(request);
    }
    return new Response('Not Found', { status: 404 });
  },
});
