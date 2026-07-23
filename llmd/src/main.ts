import { loadLLMDConfiguration } from './configuration.ts';
import { createLLMDHandler } from './handler.ts';
import { createChatCompletionGenerator, createStructuredResponseGenerator } from './provider.ts';
import { startLLMDServer, stopLLMDServer } from './server.ts';

const configuration = loadLLMDConfiguration(process.env);
const generateStructuredResponse = createStructuredResponseGenerator(configuration);
const generateChatCompletion = createChatCompletionGenerator(configuration);
const server = await startLLMDServer(configuration, createLLMDHandler({ configuration, generateStructuredResponse, generateChatCompletion }));

async function shutdown() {
  await stopLLMDServer(server, configuration.socketPath);
  process.exit(0);
}

process.once('SIGINT', shutdown);
process.once('SIGTERM', shutdown);
