import { Chat, type Adapter } from 'chat';
import { createMemoryState } from '@chat-adapter/state-memory';
import { createBuzzAdapter } from './adapters/buzz/index.ts';
import { createMattermostAdapter } from './adapters/mattermost/index.ts';
import { loadConfiguration } from './configuration.ts';
import { createBridge } from './bridge.ts';
import { createOutboundHandler } from './outbound.ts';
import { createMirror, type MirrorWiring } from './mirror/wire.ts';
import {
  normalizePlatformAdapter,
  type ContextCapableAdapter,
  type NormalizedPlatformAdapter,
} from './visible-context.ts';

const configuration = loadConfiguration(process.env);

// Peripheral platforms only; Buzz is the hub, not a fan-out target.
const connectedPlatforms: string[] = [];
if (configuration.mattermost) connectedPlatforms.push('mattermost');

let mirror: MirrorWiring | undefined;
if (configuration.admindBaseURL && configuration.buzz) {
  mirror = createMirror({
    admindBaseURL: configuration.admindBaseURL,
    connectedPlatforms,
    buzz: { relayURL: configuration.buzz.relayURL, authTagJSON: configuration.buzz.authTagJSON },
    mattermost:
      configuration.mattermost?.adminToken
        ? { baseURL: configuration.mattermost.baseURL, adminToken: configuration.mattermost.adminToken }
        : undefined,
    onError: (context, detail) => console.error('[mirror]', context, detail),
  });
}

const adapters: Record<string, Adapter> = {};
const normalizedAdapters: Record<string, NormalizedPlatformAdapter> = {};

function registerAdapter(platform: string, adapter: Adapter): void {
  adapters[platform] = adapter;
  normalizedAdapters[platform] = normalizePlatformAdapter(adapter as unknown as ContextCapableAdapter);
}

if (configuration.mattermost) {
  registerAdapter(
    'mattermost',
    createMattermostAdapter({
      baseUrl: configuration.mattermost.baseURL,
      botToken: configuration.mattermost.botToken,
      callbackUrl: configuration.mattermost.actionCallbackURL,
      mirror: mirror?.mattermost,
    }),
  );
}
if (configuration.buzz) {
  registerAdapter(
    'buzz',
    createBuzzAdapter({
      relayURL: configuration.buzz.relayURL,
      privateKeyHex: configuration.buzz.privateKeyHex,
      botDisplayName: configuration.botUserName,
      accountLinksPath: configuration.buzz.accountLinksPath,
      authTagJSON: configuration.buzz.authTagJSON,
      mirror: mirror?.buzz,
    }),
  );
}

const chat = new Chat({
  userName: configuration.botUserName,
  state: createMemoryState(),
  concurrency: 'queue',
  adapters,
});

createBridge(chat, configuration, normalizedAdapters);

await chat.initialize();

const outboundHandler = createOutboundHandler(adapters as never, configuration);

Bun.serve({
  port: configuration.listenPort,
  hostname: '127.0.0.1',
  fetch: async (request) => {
    const requestUrl = new URL(request.url);
    const webhookPlatform = requestUrl.pathname.startsWith('/webhooks/')
      ? requestUrl.pathname.slice('/webhooks/'.length)
      : '';
    if (webhookPlatform && adapters[webhookPlatform]) {
      const webhooks = chat.webhooks as Record<string, ((request: Request) => Response | Promise<Response>) | undefined>;
      return webhooks[webhookPlatform]?.(request) ?? new Response('Not Found', { status: 404 });
    }
    if (requestUrl.pathname.startsWith('/v1/platform/')) {
      return outboundHandler(request);
    }
    return new Response('Not Found', { status: 404 });
  },
});
