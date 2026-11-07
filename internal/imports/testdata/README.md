# Capture session fixtures

These files are hand-written from the public formats; no third-party export is
copied into the repository.

- `charles-session.chlsj`: Charles JSON Session File. Field names, the
  `host/ip` remote address, the `times`/`durations` objects and the
  `header.headers` list were confirmed against public exports such as
  `ahutsunshine/dingdong-grabber` (`charles/ios/example.chlsj`) and
  `raw34/postman-collection-generators` (`examples/charles.chlsj`). The bodies,
  duplicate headers and failure entry are written for the tests.
- `charles-session.chlsx`: Charles XML Session File. The element and attribute
  names follow the official DTD,
  <https://www.charlesproxy.com/dtd/charles-session-1_0.dtd>, and the extra
  timing and TLS attributes were confirmed against a public export
  (`tejaswankalluri/BookMyShow-Notifier`, `info_gather/static.chlsx`). The
  bodies, duplicate headers and WebSocket entry are written for the tests.
- `charles-native.chls`: the Java Object Serialization Stream magic
  (`AC ED 00 05`) followed by arbitrary bytes. Charles uses this container for
  its native session; the fixture only checks that the importer refuses it
  without deserializing it.
- `proxyman-request_*.json`: one entry each from a Proxyman Log. The entry
  shape (`request`/`response`, `header.entries`, `bodyData` base64, `timing`
  seconds, `summary`, `extraInfo`, `websocketMessageStorage`) was confirmed
  against the public sample `wabiloo/trace-shrink`
  (`tests/archives/hls1-proxyman.proxymanlogv2`) and Proxyman's documented
  v1-directory/v2-zip layout (Proxyman issue #1410). The values are written for
  the tests.
