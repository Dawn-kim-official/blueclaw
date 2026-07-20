import { Chat } from 'chat';
import { createMemoryState } from '@chat-adapter/state-memory';
import { createMattermostAdapter } from './adapters/mattermost/index.ts';
import { loadConfiguration } from './configuration.ts';
import { createBridge } from './bridge.ts';

const configuration = loadConfiguration(process.env);
const chat = new Chat({
  userName: configuration.botUserName,
  state: createMemoryState(),
  concurrency: 'queue',
  adapters: {
    mattermost: createMattermostAdapter({
      baseUrl: configuration.mattermostBaseURL,
      botToken: configuration.mattermostBotToken,
      callbackUrl: configuration.actionCallbackURL,
    }),
  },
});

createBridge(chat, configuration);

await chat.initialize();
