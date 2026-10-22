# Local go-mitmproxy patch

Runtime packages copied verbatim from `github.com/lqqyt2423/go-mitmproxy v1.9.4`
(`cert`, `internal/helper`, `proxy`), with the original MIT license and module
metadata. Upstream tests, examples, commands and unrelated addons are not copied.
This is the existing capture engine, not another proxy implementation.

The only source patch is in `proxy/websocket.go`, `webSocketHandler.handle`:

- Pass `cloneHeaderWithoutWSHandshake(clientReq.Header)` to the upstream dial,
  as upstream already does for decrypted WSS. Previously every custom header,
  including Authorization and Cookie, was discarded for plain WS.
- Replace the CONNECT envelope's `f.Request` with the actual upgrade request,
  preserving the escaped path/query and headers for capture addons.

Upstream v1.9.4's `handleWSS` fix did not cover `handle`; there is no addon hook
before this plain-WS dial, and the original request is otherwise private.
Regression coverage lives in the desktop module's real-socket tests:
`TestStructuredWebSocketSendsAuthHeadersAndCookiesWithVerifiedTLS` and
`TestWebSocketKeepsThePathAndQuery` (alongside the existing WSS regression).

On upgrade, compare this file to the new upstream source, reapply the two
changes if still needed, and rerun `go test -race ./...` from `desktop/`.
Remove the `replace` and this directory once upstream preserves both headers
and capture metadata on plain WS. Do not edit the module cache.
