// Optional local interoperability fixture, not an application dependency.
const http = require('node:http');
const { Server } = require('socket.io');
const server = http.createServer();
const io = new Server(server, { path: '/socket.io/', pingInterval: 100, pingTimeout: 1000 });
for (const name of ['/', '/admin']) {
  const ns = io.of(name);
  ns.use((socket, next) => socket.handshake.auth.token === 'fixture' ? next() : next(new Error('fixture auth rejected')));
  ns.on('connection', socket => {
    socket.emit('ready', { namespace: name, transport: socket.conn.transport.name, query: socket.handshake.query.client });
    socket.emit('question', 'answer me', (...args) => socket.emit('answer-observed', ...args));
    socket.on('echo', (...args) => {
      const ack = typeof args.at(-1) === 'function' ? args.pop() : null;
      socket.emit('reply', ...args);
      if (ack) ack(...args);
    });
    socket.on('bytes', (value, ack) => {
      if (!Buffer.isBuffer(value)) return socket.emit('fixture-error', 'not binary');
      socket.emit('bytes-back', { nested: [value] });
      if (ack) ack(Buffer.from([255, 0, 73, 195, 40]));
    });
    socket.on('shutdown', () => socket.disconnect(true));
  });
}
io.of('/deny').use((socket, next) => next(new Error('fixture denied')));
server.listen(Number(process.env.PORT || 3187), '127.0.0.1', () => console.log('Socket.IO fixture listening', server.address().port));
