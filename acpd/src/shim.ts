export {};

const socketPath = process.env['ACPD_SOCKET_PATH']?.trim() || '/tmp/blueclaw-acpd.sock';
const standardOutputWriter = Bun.stdout.writer();

let pendingChunks: Uint8Array[] = [];

function flushPending(target: { write: (chunk: Uint8Array) => number }): void {
  while (pendingChunks.length > 0) {
    const head = pendingChunks[0];
    if (!head) return;
    const writtenByteCount = target.write(head);
    if (writtenByteCount < head.byteLength) {
      pendingChunks[0] = head.subarray(writtenByteCount);
      return;
    }
    pendingChunks.shift();
  }
}

const socket = await Bun.connect({
  unix: socketPath,
  socket: {
    data(_socket, chunk) {
      standardOutputWriter.write(chunk);
      standardOutputWriter.flush();
    },
    drain(currentSocket) {
      flushPending(currentSocket);
    },
    close() {
      process.exit(0);
    },
    error() {
      process.exit(1);
    },
  },
});

for await (const chunk of Bun.stdin.stream()) {
  pendingChunks.push(new Uint8Array(chunk));
  flushPending(socket);
}
socket.end();
