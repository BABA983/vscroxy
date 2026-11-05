# Local go-mitmproxy patch

Runtime packages copied verbatim from `github.com/lqqyt2423/go-mitmproxy v1.9.4`
(`cert`, `internal/helper`, `proxy`), with the original MIT license and module
metadata. Upstream tests, examples, commands and unrelated addons are not copied.
This is the existing capture engine, not another proxy implementation.

The source patches are in `proxy/websocket.go`, `proxy/addon.go`, `proxy/sse.go`, `proxy/flow.go`,
`proxy/attacker.go`, `proxy/proxy.go`, `proxy/entry.go` and `proxy/connection.go`;
`proxy/socks.go` is new. Everything else is upstream.

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

## SOCKS5-negotiated connections (`proxy/socks.go`, `proxy/entry.go`, `proxy/connection.go`)

`Proxy.ServeSOCKSConn(conn, target, done)` serves one connection whose
destination a SOCKS5 CONNECT agreed, before any HTTP byte arrived. The SOCKS
protocol itself stays in the desktop module; this seam only needs the agreed
target. Closing `done` (the capture server's shutdown signal) cancels the
initial upstream dial or TLS handshake, as the HTTP front end's request context
does for a CONNECT.

- A TLS client gets the HTTP CONNECT policy: `shouldIntercept` decides between
  the eager or lazy MITM path and a byte-for-byte tunnel. The eager path keeps
  its upstream certificate lookup, ALPN and HTTP/2 support; the lazy path and
  `SetDialFallbackRule` behave as they do after CONNECT. A bypassed tunnel is
  still shown through `Requestheaders` with a synthetic CONNECT request.
- A plaintext client is handed to the ordinary HTTP front end. The target is in
  `ConnContext.TransparentTarget`; `entry.ServeHTTP` fills an origin-form
  request's missing scheme and host from it, and pins even an absolute-form
  request to it. One SOCKS request therefore opens exactly one target, and the
  usual rules, scripts, breakpoints, capture and upstream proxy apply.
- `wrapListener.Accept` returns an already-wrapped `*wrapClientConn` unchanged:
  `ServeSOCKSConn` registers that client once, before the HTTP server parses a
  request, so one socket keeps one identity and one `ClientConnected` event.
- The plaintext path uses a small channel listener served by the same
  `entry.server`. Closing the proxy closes it with the other listeners.

Real-socket coverage lives in `internal/capture/socks_test.go`: IPv4, IPv6 and
domain greeting/request parsing, BIND and unsupported address types, RFC 1929
success and failure, plaintext HTTP capture and rules, and HTTPS both decrypted
and bypassed.

## Forward proxy connections that switch targets

A client may keep one connection to the proxy and send requests for different
hosts and ports over it. `attack` used the `ServerConn` dialled for the first
request for every later request; that connection's transport returned its one
socket for any address, so a new target's request went to the previous server,
and the previous response could arrive as an unsolicited response on an idle
channel. A request whose target differs from `ServerConn.Address` now retires
that upstream (`ServerConn.retire` in `connection.go`) and dials the new target
through the request's `dialFn`. Retiring closes the upstream socket without
ending the client's connection, which is still waiting for this response.
Intercepted CONNECT tunnels keep one target for the whole connection and are
unchanged. `TestForwardProxyFollowsTargetOnOneConnection` in `internal/capture`
covers a live switch, a switch back, an unreachable new target and the request
after it, both directly and through an upstream proxy.

## SSE completion

`attacker.go` pairs `SSEStart` with a deferred `SSEEnd` when the request handler
returns. `sse.go` no longer emits that hook from its reader: an empty response
or a downstream write failure can end without the reader observing EOF. The
hook supplies completion time only, not a normal/error classification.
`TestSSECompletionWithoutMessagesAndWithFullQueue` covers empty streams and
completion evidence independent of the bounded message queue.

`sse.go` recognizes LF, CRLF and lone CR line endings incrementally, including
CRLF split across reads. Only a blank line dispatches a data-bearing block;
EOF or a read failure discards an unfinished block. An empty `data` field still
dispatches, empty data lines retain their newlines, and field parsing removes
only one optional ASCII space after the colon rather than trimming values.
The wrapper continues forwarding the original bytes unchanged. Capture keeps
empty event payloads instead of substituting raw field text.
`TestSSEEventFraming` and `TestSSEFramingAcrossReads` exercise Capture's
`SSEMessage` path over real sockets, checking payloads, counts, incremental
delivery and byte-for-byte forwarding.

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

## WebSocket targets (Map Remote)

- `handle` runs `Requestheaders` on the upgrade request (URL `ws://…`) and dials
  the server `f.Request.URL` then names. `httpsDialLazyAttack` passes no server
  connection, so the dial waits for addons; a pre-dialed one (`UpstreamCert`)
  is replaced when the URL changed, swapping only the socket inside
  `wrapServerConn`, whose `Close` would end the client's tunnel too.
- `attacker.ServeHTTP` shows addons a decrypted upgrade as `wss://…` (`ws://`
  for a plain proxied request) and hands its flow to `handleWSS`, which dials
  `f.Request.URL` and uses that same flow for the messages instead of a new
  one.
- `cloneHeaderWithoutWSHandshake` keeps a `Host` header: the server never
  leaves one in the header map, so it is there only when an addon put it there
  to keep the client's host. Gorilla sends it as the request's Host.
- The upstream dialer of `handle` gets `TLSClientConfig`, for a `wss` target.

`TestMapRemoteRewritesPathHostAndWebSockets` in `internal/capture` covers both.

## Unreachable CONNECT targets (`proxy.go`, `entry.go`)

`Proxy.SetDialFallbackRule` decides, for an intercepted CONNECT whose server
`httpsDialFirstAttack` cannot reach, whether to continue as
`httpsDialLazyAttack` does: a certificate generated for the CONNECT's host and
an upstream dialed per request, after addons may have changed its URL. Without
the rule, or when it says no, the CONNECT fails with 502 as upstream does. The
capture answers yes when an active Map Remote rule could match the host.
`TestMapRemoteReachesUnresolvableHTTPSHosts` covers it.
Regression coverage lives in the desktop module's real-socket tests:
`TestStructuredWebSocketSendsAuthHeadersAndCookiesWithVerifiedTLS` and
`TestWebSocketKeepsThePathAndQuery` (alongside the existing WSS regression).

## WebSocket subprotocols

Both WS and decrypted WSS upstream dialers offer the client's subprotocols via
`Dialer.Subprotocols` instead of losing them with regenerated handshake headers.
The downstream upgrader selects only the protocol actually selected upstream,
never its own independent preference. An upstream selection not offered by the
client fails the handshake. `TestWebSocketSubprotocolAndBinaryThroughWSAndWSS`
in `internal/request` covers both transports, server selection/no selection,
auth and binary payloads with invalid UTF-8.

## WebSocket message interception and lifetime

`Addon.WebSocketMessage(context.Context, *Flow, *WebSocketMessage)` receives the
current complete message explicitly. Forwarding consumes its edited Type,
Content and Drop flag. Capture and LogAddon no longer guess the message from
the last entry of a concurrently appended history. History appends stay locked.

Each direction has a reader and a processor joined at connection end. An
unbuffered handoff allows one message of lookahead while a hook pauses, then
applies backpressure. Reading a close/error or closing the wrapped client
cancels both hooks. A close behind queued data may wait until processing
resumes. Shutdown gives pending writes a one-second grace period, forcibly
closes sockets if they are still blocked, joins processors, closes sockets to
release readers, joins them, then emits WebSocketEnd. The hook must honor its
context; arbitrary third-party addons
that ignore cancellation cannot be forcibly interrupted.

Desktop real-socket tests `TestWebSocketMessageBreakpoints` (WS and decrypted
WSS), `TestWebSocketDuplexPausesRelease` and
`TestWebSocketSavedScriptDuplexLocalsAndMessageDecisions` cover byte-exact edits,
isolated drops, duplex identity/locals, and release on peer/proxy close or
breakpoints off. These tests also run with the race detector.

## Local code mocks (`proxy.go`, `entry.go`, `attacker.go`)

`SetLazyInterceptRule` selects intercepted CONNECT hosts whose upstream dial
must wait until after request addons. Unlike dial fallback, it never contacts
the original host to obtain its certificate. The existing SSL interception
policy still decides whether to decrypt; bypassed tunnels are unchanged.
Capture selects hosts potentially covered by an active Code Mock rule. A
nonmatching decrypted request still dials its normal upstream on demand.

`attack` also honors a response set by `StreamRequestModifier`: large uploads
skip `Request`, and a Code Mock must reject an unavailable body locally rather
than accidentally forwarding it. No other addon response semantics change.
`TestCodeMockAnswersWithoutUpstreamConnection` checks actual HTTP/HTTPS
upstream connection counts; `TestCodeMockRefusesUnreadableInputWithoutForwarding`
covers both script and engine body limits and streamed gRPC input.

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

### A body that fails is not ended as whole

When copying a response body to the client fails, because the upstream reset its
stream or the client went away, `reply` panics with `http.ErrAbortHandler`, which
the addon-panic `recover` in `attack` re-panics. Upstream only logged the error
and returned. net/http then ended an HTTP/2 stream with END_STREAM, or finished
an HTTP/1.1 chunked body, so the client read a short body as complete. For gRPC,
a server's deadline reset (`RST_STREAM CANCEL`) reached the client as "server
closed the stream without sending trailers". The abort resets the stream, or
closes the connection, as `httputil.ReverseProxy` does. The HTTP/2 server can
reset a stream only with `INTERNAL_ERROR`, so the upstream's code is not kept.
`TestStreamedResponseCutByUpstream` covers this.

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

## Per-send upstream certificate verification

`proxy/proxy.go`, `proxy/connection.go`, `proxy/entry.go` and
`proxy/attacker.go` honor `HeaderVerifyUpstream` (`X-Vscroxy-Verify-TLS: 1`) on a
CONNECT. `entry.handleConnect` records it on the `ConnContext` and removes the
header before the flow is created, so it is neither captured nor forwarded;
`attacker.serverTlsHandshake` then stops skipping verification for that
connection. The header can only make the inspection proxy stricter, never
weaker: capture traffic keeps `SslInsecure`.

An IP target sends no SNI. `attacker.serverTlsHandshake` verifies against the
CONNECT host when `ClientHelloInfo.ServerName` is empty, so a certificate's IP
SAN is checked instead of failing with "either ServerName or
InsecureSkipVerify must be specified".

A Map Remote rule or script can rewrite an intercepted request to another host.
That path has no pre-dialed socket, so `attacker.attack` uses
`attacker.verifiedClient` when `VerifyUpstream` is set: a separate transport
that verifies certificates and disables keep-alives, never the inspection
client's insecure pool. A failed check reaches the client as a 502 and is
recorded on the flow. If the configured upstream is itself an `https://` proxy,
this path also verifies that proxy's own certificate, which is stricter than
the inspection client's usual acceptance. `TestRequestCertificateVerificationAfterMapRemote`
reproduces the rewritten-target check.

`internal/capture.ReplayWithPolicy` sends the header for an authored request
whose TLS setting verifies the certificate, and sets `InsecureSkipVerify` on the
replay client for the tunneled leg when verification is off.
`internal/request.TestRequestCertificateVerification` covers a self-signed
server with TLS tunneled and intercepted, verification on and off;
`TestRequestCertificateVerificationUsesTheIPAddress` covers the IP-SAN success
case.

WebSocket upstream dials are not covered: `proxy/websocket.go` still builds its
`tls.Client` from `Options.SslInsecure` and does not read `VerifyUpstream`, so a
WS/WSS send accepts any upstream certificate regardless of the request's TLS
setting.

## Maintaining this patch

### Deliver a closing response before canceling downstream

An upstream `Connection: close` response closes its transport as its body ends.
Do not then close the plain HTTP client's read half: net/http treats that as
request cancellation, so a buffered response still being paced downstream is
aborted. As already intended for TLS, leave the client's lifetime to net/http
after the response has been delivered. The close-after-response flag is atomic
because the upstream transport closes on its own goroutine. The app's
`TestWithoutRecordingPreservesRulesScriptsAndThrottles` exercises a scripted
response sent through the real proxy with both directional limits enabled.

On upgrade, compare these files to the new upstream source, reapply the changes
if still needed, and rerun `go test -race ./...` from the repository root. Remove the
`replace` and this directory once upstream preserves WS headers and capture
metadata, trailers, and streams gRPC. Do not edit the module cache.
