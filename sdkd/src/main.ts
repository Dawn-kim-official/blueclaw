import { loadSDKDConfiguration } from './configuration.ts';
import { createSDKDHandler } from './handler.ts';
import { createStructuredResponseGenerator } from './provider.ts';
import { startSDKDServer, stopSDKDServer } from './server.ts';

const configuration = loadSDKDConfiguration(process.env);
const generateStructuredResponse = createStructuredResponseGenerator(configuration);
const server = await startSDKDServer(configuration, createSDKDHandler({ configuration, generateStructuredResponse }));

async function shutdown() {
  await stopSDKDServer(server, configuration.socketPath);
  process.exit(0);
}

process.once('SIGINT', shutdown);
process.once('SIGTERM', shutdown);
