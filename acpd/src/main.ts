import { createAcpAgent } from './acp-agent.ts';
import { createBuzzCommandRunner } from './buzz-cli.ts';
import { loadConfiguration } from './configuration.ts';
import { createJSONRPCPeer, pumpStandardInputLines } from './jsonrpc.ts';
import { createOutboundHandler } from './outbound.ts';

const configuration = loadConfiguration(process.env);
const standardOutputWriter = Bun.stdout.writer();

let peerNotify: (method: string, params: unknown) => void = () => {};
const agent = createAcpAgent(configuration, (method, params) => peerNotify(method, params));
const peer = createJSONRPCPeer(
  (line) => {
    standardOutputWriter.write(line + '\n');
    standardOutputWriter.flush();
  },
  agent.requestHandlers,
  agent.notificationHandlers,
);
peerNotify = peer.notify;

const outboundHandler = createOutboundHandler(createBuzzCommandRunner(configuration), agent);

Bun.serve({
  port: configuration.listenPort,
  hostname: '127.0.0.1',
  fetch: outboundHandler,
});

await pumpStandardInputLines(peer.handleLine);
process.exit(0);
