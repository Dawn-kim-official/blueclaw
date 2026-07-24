export {};

const socketPath = process.env['ACPD_SOCKET_PATH']?.trim() || '/tmp/blueclaw-acpd.sock';

const socket = await Bun.connect({
  unix: socketPath,
  socket: {
    data(_socket, chunk) {
      Bun.write(Bun.stdout, chunk);
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
  socket.write(chunk);
}
socket.end();
