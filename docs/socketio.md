# Socket.IO interoperability checks

Client usage and limits are in the [guide](guide.md#interactive-socketio-requests).
The application has no Node runtime or Socket.IO library dependency. Normal Go
tests run literal-wire peers through the real capture proxy, covering WS/WSS,
custom endpoint/query/auth/cookies, binary events/ACKs (including empty and
non-UTF-8 data), malformed peers, cancellation and heartbeat expiry.

An optional integration test also runs against the official JavaScript server.
Install its pinned test dependency outside the repository:

```bash
npm install --prefix /tmp/vscroxy-socketio --no-audit --no-fund --ignore-scripts socket.io@4.8.4
```

From the repository root, run the fixture (loopback only):

```bash
NODE_PATH=/tmp/vscroxy-socketio/node_modules PORT=3187 node docs/examples/socketio-server.cjs
```

In another terminal:

```bash
VSCROXY_SOCKETIO_URL=http://127.0.0.1:3187/socket.io/ go test -race ./internal/request -run '^TestSocketIO' -count=3 -timeout=90s
```

`TestSocketIOOfficialServer` verifies `/` and `/admin` with namespace auth,
WebSocket transport/query, nested binary attachments, binary server ACKs,
client replies to server ACK requests, namespace rejection and disconnect.
Its fixture uses a short server heartbeat interval. It is skipped unless the
environment variable is set; normal tests require no external service.

For a native UI check, start capture and create a Socket.IO document with this
endpoint, namespace `/admin` and auth `{"token":"fixture"}`. Connect, emit
`echo` with `["hello", {"n":73}]` and **Request ACK**, then reply to the pending
`question` ACK. For binary round-trip, emit `bytes` with arguments
`[{"_placeholder":true,"num":0}]` and attachments `["/wBJwyg="]`.
Expect `bytes-back` and a binary ACK with the same five bytes. Disconnect and
reconnect explicitly: the new session must contain no old messages or ACK IDs.
