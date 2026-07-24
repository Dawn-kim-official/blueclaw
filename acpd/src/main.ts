import { unlinkSync } from 'node:fs';
import { createAcpAgentCore } from './acp-agent.ts';
import { createBuzzCommandRunner } from './buzz-cli.ts';
import { loadConfiguration } from './configuration.ts';
import { createJSONRPCPeer, type JSONRPCPeer } from './jsonrpc.ts';
import { createOutboundHandler } from './outbound.ts';

const configuration = loadConfiguration(process.env);
const core = createAcpAgentCore(configuration);
const outboundHandler = createOutboundHandler(createBuzzCommandRunner(configuration), core, configuration);

Bun.serve({
  port: configuration.listenPort,
  hostname: '127.0.0.1',
  fetch: outboundHandler,
});

type ConnectionState = {
  peer: JSONRPCPeer;
  buffered: string;
  decoder: TextDecoder;
};

try {
  unlinkSync(configuration.socketPath);
} catch {
  void 0;
}

Bun.listen<ConnectionState>({
  unix: configuration.socketPath,
  socket: {
    open(socket) {
      const connection = core.createConnection((method, params) => {
        socket.write(JSON.stringify({ jsonrpc: '2.0', method, params }) + '\n');
      });
      socket.data = {
        peer: createJSONRPCPeer(
          (line) => socket.write(line + '\n'),
          connection.requestHandlers,
          connection.notificationHandlers,
        ),
        buffered: '',
        decoder: new TextDecoder(),
      };
    },
    data(socket, chunk) {
      const state = socket.data;
      state.buffered += state.decoder.decode(chunk, { stream: true });
      let newlineIndex = state.buffered.indexOf('\n');
      while (newlineIndex >= 0) {
        state.peer.handleLine(state.buffered.slice(0, newlineIndex));
        state.buffered = state.buffered.slice(newlineIndex + 1);
        newlineIndex = state.buffered.indexOf('\n');
      }
    },
    close() {},
    error() {},
  },
});

console.error(`acpd listening on ${configuration.socketPath} and 127.0.0.1:${configuration.listenPort}`);
