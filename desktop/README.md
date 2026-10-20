# vscroxy desktop

The desktop app, written in Go with [MyGo](https://github.com/egoist/mygo)'s
native UI: no webview, no HTML, no JavaScript in the app itself. MyGo draws
the widgets on the GPU and takes text, input methods and accessibility from
the platform.

The app captures real traffic: it runs a MITM proxy on `127.0.0.1:7890` with
[go-mitmproxy](https://github.com/lqqyt2423/go-mitmproxy) and shows every
exchange as it arrives.

## Run

Go 1.27 or later. On Linux the app needs GTK 3 at run time; nothing else.

```sh
go run .
```

Then point a client at the proxy:

```sh
curl -x http://127.0.0.1:7890 http://example.com/
```

For HTTPS, install the CA the proxy generates at
`~/.mitmproxy/mitmproxy-ca-cert.pem` and trust it, then:

```sh
curl --cacert ~/.mitmproxy/mitmproxy-ca-cert.pem -x http://127.0.0.1:7890 https://example.com/
```

`mygo dev` rebuilds and restarts on changes:

```sh
go tool mygo dev
```

## What is here

- `main.go`: the app state, the three-pane layout, the toolbar, the traffic
  table, the detail tabs and the status bar.
- `data.go`: seeded flows for the case where the proxy cannot start, and
  formatting helpers.
- `internal/capture`: the MITM proxy and the `Flow` boundary type. The UI
  imports this package, never the engine.
- `mygo.json`: the app's name, identifier and version for `mygo package`.

The layout is the Proxyman one: filters and domains on the left, the request
table in the middle, and Headers / Body / Diff under it. The diff pane is
MyGo's own `ui/editor` diff viewer, not Monaco, so it stays native and can be
reused on mobile later. It compares a response with the previous response for
the same method, scheme and URL.

## Rules

Rules live in `~/.vscroxy/rules.json` and apply to new requests in order,
first match wins. `match` is a substring of the full URL.

```json
[
  {"type": "map_local", "match": "/missing", "target": "/tmp/mock.json"},
  {"type": "map_remote", "match": "http://example.com/api", "target": "http://127.0.0.1:8081"},
  {"type": "block", "match": "/telemetry"},
  {"type": "break", "match": "/checkout"},
  {"type": "break_response", "match": "/v1/orders"}
]
```

- `map_local` answers from a file, without calling an upstream.
- `map_remote` sends the request to another scheme and host, keeping the path
  and query; the table keeps showing the URL the client asked for.
- `block` answers `403`.
- `break` pauses the request until you decide, and `break_response` pauses
  the response (see below).

The toolbar's **Reload rules** button rereads the file. A missing file means
no rules.

## Breakpoints

Turn on **Break** in the toolbar. A request matching a `break` rule pauses
with its method, URL and body; edit them and press **Continue**, or
**Abort** to answer `502`. A response matching `break_response` pauses with
its status and body; the response script, when there is one, runs first.

The flow's duration is the exchange's, not the time you spent at the
breakpoint.

## Scripts

`~/.vscroxy/script.js` may define `onRequest(req)` and `onResponse(res)`.
`req` has `method`, `url`, `headers` and `body`; `res` has `status`, `headers`
and `body`. Edits are written back before the request goes upstream and before
the response reaches the client.

```js
function onRequest(req) {
  if (req.url.includes("/v1/old")) req.url = req.url.replace("/v1/old", "/v1/new");
  req.headers["X-Debug"] = "1";
}

function onResponse(res) {
  if (res.status === 404) {
    res.status = 200;
    res.body = JSON.stringify({ ok: true });
    res.headers["Content-Type"] = "application/json";
  }
}
```

Each hook runs in a fresh sandbox with a 250 ms limit and no files or
network; a hook that loops is interrupted and counted in the status bar.
Streaming and SSE responses skip the response hook. **Reload config** rereads
both the rules and the script.

## WebSocket and SSE

A WebSocket connection appears as a `WS` row with status `101`, and an SSE
response as its request with the `Messages` tab. Frames stream in as they
arrive: the arrow is the direction, then the kind (`text`, `binary`, `event`,
`tick`, …), then the payload. Frames are kept per flow, the last 200 of them,
each clipped at 4 KB.

The WebSocket row shows the host of the tunnel and `/` for the path: the
engine hands the hook the CONNECT flow, not the upgraded request.

## Replay

Select a flow and press **Replay**: the request goes out again through the
proxy, so the new exchange is captured like any other and can be diffed
against the original (same method, scheme and URL).

## Next

- A real body inspector: JSON tree, hex, image and rendered previews, with
  search, and bodies spilled to disk instead of held in memory.
- Session export: HAR and a saved session that reopens with its flows.
- Network conditions, an upstream proxy, and the system proxy and
  certificate helpers.
