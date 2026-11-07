# Capture session fixtures

These files are hand-written from the public formats; no third-party export is
copied into the repository.

- `charles-session.chlsj`: Charles JSON Session File. Field names, the
  `host/ip` remote and client addresses, the `times`/`durations` objects and the
  `header.headers` list were confirmed against public exports such as
  `ahutsunshine/dingdong-grabber` (`charles/ios/example.chlsj`) and
  `raw34/postman-collection-generators` (`examples/charles.chlsj`). The
  `exception` field and `"status":"EXCEPTION"` follow
  `richtermb/pycharles` (`example_sessions/httpbin.chlsj`) and
  `dperfly/fiddler2jmeter` (`examples/charles/test_http2.0.chlsj`). The bodies,
  duplicate headers and gzip `decoded` flag are written for the tests. The
  WebSocket frame bytes are hand-encoded RFC 6455 frames; Charles stores such
  raw frames in the encoded bodies of `webSocket:true` transactions, as
  `cbxss/charles-mcp` reads them
  (`src/session/chlsj.rs`, `tests/websocket_decode.rs`).
- `charles-session.chlsx`: Charles XML Session File. The element and attribute
  names follow the official DTD,
  <https://www.charlesproxy.com/dtd/charles-session-1_0.dtd>, and the extra
  timing and TLS attributes were confirmed against a public export
  (`tejaswankalluri/BookMyShow-Notifier`, `info_gather/static.chlsx`). The
  bodies, duplicate headers and WebSocket frames are written for the tests.
- `charles-native.chls`: the Java Object Serialization Stream magic
  (`AC ED 00 05`) followed by arbitrary bytes. Charles uses this container for
  its native session; the fixture only checks that the importer refuses it
  without deserializing it.
- `proxyman-request_*.json`: one entry each from a Proxyman Log. The base entry
  shape (`request`/`response`, `header.entries`, `bodyData` base64, `timing`
  seconds, `summary`, `extraInfo`) was confirmed against the public sample
  `wabiloo/trace-shrink`
  (`tests/archives/hls1-proxyman.proxymanlogv2`) and Proxyman's documented
  v1-directory/v2-zip layout (Proxyman issue #1410). The values are written for
  the tests. `websocketMessageStorage.messages` and `response.error` have no
  public sample we could verify, so their shapes here are inferred from the
  importer's documented field names; they should not be treated as a format
  guarantee.
