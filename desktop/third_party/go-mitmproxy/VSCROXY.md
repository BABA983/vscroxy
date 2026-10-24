# Local go-mitmproxy patch

Runtime packages copied verbatim from `github.com/lqqyt2423/go-mitmproxy v1.9.4`
(`cert`, `internal/helper`, `proxy`), with the original MIT license and module
metadata. Upstream tests, examples, commands and unrelated addons are not copied.
This is the existing capture engine, not another proxy implementation.

The source patches are in `proxy/websocket.go`, `proxy/sse.go`, `proxy/flow.go`,
`proxy/attacker.go`, `proxy/proxy.go`, `proxy/entry.go` and `proxy/connection.go`.
Everything else is upstream.

## Connection diagnostics

`ConnContext` keeps a mutex-protected, bounded history of 64 observed connection
events. The normal dial and the separate transport used after URL rewrites attach
`httptrace` hooks for DNS/TCP and transport TLS. The manual MITM handshake records
upstream and client TLS separately. `Flow.Error` holds the exchange's actual
failure; a failed individual dial attempt followed by a successful fallback must
not mark the exchange failed.

`entry.go` and `attacker.go` report failures after CONNECT through
`HTTPConnectError`, including client certificate rejection and failure to read
the handshake. Previously those paths only logged to the process console.
Capture addons can now publish the failed CONNECT without inventing a target
HTTP response or replacing its already-successful CONNECT status.

The desktop's real-socket tests cover DNS/TCP failures, both TLS sides, successful
TLS metadata and connection reuse, plus a rewritten target's separate transport.

## SSE completion

`attacker.go` pairs `SSEStart` with a deferred `SSEEnd` when the request handler
returns. `sse.go` no longer emits that hook from its reader: an empty response
or a downstream write failure can end without the reader observing EOF. The
hook supplies completion time only, not a normal/error classification.
`TestSSECompletionWithoutMessagesAndWithFullQueue` covers empty streams and
completion evidence independent of the bounded message queue.

## `proxy/proxy.go` and `proxy/entry.go`, listener ownership

`Proxy.Serve(net.Listener)` accepts an already bound listener. `Start` still
binds the configured address, then delegates to `Serve`; the entry wraps and
serves that same socket and logs its actual address. The desktop capture can
therefore bind its preferred loopback port synchronously, fall back to an
OS-assigned port if unavailable, and pass ownership straight to the engine.
There is no probe/close/rebind gap, and a competing application's open port can
never be mistaken for successful startup. Regression coverage lives in
`TestStartRejectsAnOccupiedExplicitAddress`, `TestDefaultProxyFallsBackToAnOwnedPort`
and `TestCloseImmediatelyAfterStartReleasesThePort`.

## `proxy/websocket.go`, `webSocketHandler.handle`

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

## `proxy/flow.go`, `Request` and `Response`

`Request` gained `Trailer` and `Response` gained `Trailer` and `Proto`, so what
the transport learned about a message survives into the addon hooks:

- `Proto` is the HTTP version the server answered with (`HTTP/2.0`), which the
  capture shows instead of assuming HTTP/1.1.
- `Trailer` is the trailer block that follows the body. `Request.Trailers()` and
  `Response.Trailers()` read it from the `*http.Request`/`*http.Response` the
  transport filled, because net/http **replaces** `Trailer` with a fresh map when
  a message announced none and the trailers arrive: a copy taken when the flow
  was built would stay empty forever. Both are complete only once the body has
  ended, which is why the capture snapshots them at that point.

## `proxy/attacker.go`

### Edited Host headers

When building the upstream request, a `Host` header supplied by an addon sets
`http.Request.Host`; Go's transport ignores `Header["Host"]`. This makes a Host
edit at a breakpoint reach the upstream rather than silently using the URL's
host. `TestBreakpointEditsRequest` checks the received Host alongside URL,
Authorization, Cookie and duplicate-header edits.

### Clients without SNI

When a TLS client omits SNI (normal for IP-literal URLs), certificate issuance
falls back to the original CONNECT hostname, without its port. Previously the
engine signed a certificate for the empty string, so a client trusting the CA
still rejected an inspected IP request. Both eager and lazy handshakes use the
fallback; an explicit SNI name is unchanged. Regression coverage is in
`TestTunnelsUntilADomainIsDecrypted`, using a verified TLS client and a local
upstream on an OS-assigned nonstandard port.

### gRPC is a stream, not a message

`attack` sets `f.Stream = true` for `application/grpc` and `application/grpc+*`
(`isGRPC`). Upstream read the whole request body before dialling, which
deadlocks every client-streaming and bidirectional call: the client sends no end
of body until the server has answered, and the server has nothing to answer
until the proxy forwards. The streamed path also keeps the response from being
buffered, so a server stream reaches the client as it arrives.

`application/grpc-web*` is deliberately **not** matched: a browser sends gRPC-Web
as an ordinary request whose body is complete, and the buffered path shows it.

Consequences of the streamed path, which the desktop module documents as limits:

- `Addon.Request` and `Addon.Response` are not called for a gRPC flow. The
  desktop capture applies its rules from `Addon.Requestheaders` instead, so a
  block, a local answer or a mapped target still applies to a call. A response
  script and a breakpoint cannot: they edit a body, and the body is already
  moving. The capture keeps both bodies through
  `StreamRequestModifier`/`StreamResponseModifier`.
- The captured bodies are the gRPC frame stream as it went on the wire. Message
  compression (`grpc-encoding: gzip`) is left in the frames and named by the
  header; nothing is decompressed.

### Trailers on the wire

`reply` writes `response.Trailers()` after the body, as `http.TrailerPrefix`
keys, so a response's trailer block becomes an HTTP/1.1 chunked trailer block or
an HTTP/2 trailers frame; upstream v1.9.4 wrote only the headers and the body and
dropped the block. For an HTTP/1.1 client the names are also announced in a
`Trailer` header before the headers are written, since that is the only way
net/http chunks such a response instead of giving it a length and losing the
block. `Content-Length` is dropped when there are trailers.

A gRPC server may answer with its status in the header frame itself, with no
body: a client reads that frame as the trailers. `moveGRPCStatusToTrailer` moves
those `grpc-*` fields to the trailer block for such a response, so the call keeps
its shape and the status arrives as a status.

Request trailers are forwarded too: `proxyReq.Trailer` is set from the client's
block, with `ContentLength = -1` so an HTTP/1.1 upstream sends a chunked body and
writes them.

### Streamed responses flush their headers

`reply` flushes right after `WriteHeader` when the response is streamed. Upstream
only flushed after each body chunk, and net/http buffers the header frame until
then: a gRPC server stream that sends its first message late, or a bidirectional
call that sends nothing until the client continues, waited on headers that were
never sent.

### HTTP/2 server configuration

`serveConn` passed the live `http.Server` (`a.server`, the HTTP/1.1 side, which
is being served) as `http2.ServeConnOpts.BaseConfig`. On Go 1.27 the HTTP/2
server copies that config once per connection and runs net/http's setup on the
copy, while `http.Server.Serve` fills the same fields in at startup — two
goroutines reading and writing the same fields, which `-race` reports for any
HTTP/2 client. The patch adds `a.h2Base`, an `http.Server` that is only ever read
as a config and never served, and passes that instead.

## Limits

- Cleartext HTTP/2 (h2c) is not supported: the proxy's front end is HTTP/1.1
  unless a CONNECT tunnel negotiates `h2` through TLS. A gRPC client that needs
  h2c cannot be captured.
- A gRPC call needs an HTTP/2 upstream. The upstream protocol is the one the
  client negotiated, passed through the MITM handshake, so a gRPC server that
  only speaks HTTP/1.1 fails in the client's own ALPN check, not silently here.
- A gRPC flow cannot be replayed: `ReplayWithCookies` speaks HTTP/1.1 to the
  proxy, which a gRPC server will not answer. The replayed result does keep the
  response's trailers and protocol.

## Maintaining this patch

On upgrade, compare these files to the new upstream source, reapply the changes
if still needed, and rerun `go test -race ./...` from `desktop/`. Remove the
`replace` and this directory once upstream preserves WS headers and capture
metadata, trailers, and streams gRPC. Do not edit the module cache.
