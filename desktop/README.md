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
- `internal/system`: the per-platform proxy and trust-store helpers behind the
  System menu.
- `internal/procinfo`: which local process owns a connection, for the Client
  column.
- `internal/appicon`: the app's icon, from its `.desktop` entry on Linux.
- `mygo.json`: the app's name, identifier and version for `mygo package`.

The layout is the Proxyman one: filters and domains on the left, the request
table in the middle, and Headers / Body / Diff under it. The diff pane is
MyGo's own `ui/editor` diff viewer, not Monaco, so it stays native and can be
reused on mobile later. It compares a response with the previous response for
the same method, scheme and URL.

## The table

The table is laid out like a proxy tool's: method, status, the whole URL with
its scheme, the app and its icon, the duration and the size. A click on a
header sorts by that column.

The chips above it keep one kind of traffic in view: HTTP or HTTPS by scheme,
WebSocket and SSE by kind, and JSON, images or HTML by the response's content
type. They narrow what the sidebar's filters and the search show, and the
status bar counts the flows, the bytes captured and the last message.

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

## Bodies

The Body tab shows the response or the request, as **Pretty** text, as a
**JSON** tree with collapsible objects and arrays, or as a **Hex** dump with
offsets and text. The search field filters what shows: matching lines in
Pretty and Hex, matching leaves in JSON. A body that is not JSON falls back
to text.

A body above 64 KiB is not held in memory: the capture core writes it to a
spill file under a temporary directory and the flow keeps the first 64 KiB
plus the file's path and the body's size. The Body tab, the diff, HAR export
and session save read the file back, and a replay sends it, so only viewing a
large body costs memory. The directory is removed when the app closes.

## Network conditions

The **Network** menu slows traffic to a preset: **3G**, **4G** or **DSL**.
The throttle paces both directions and adds the preset's latency; a flow's
time includes the transfer it will take, so it matches what the client sees.
**Off** leaves traffic alone.

**Network → Upstream…** sends every request through another proxy, such as
`http://127.0.0.1:8080`; **Upstream off** clears it. The engine tunnels to the
upstream with CONNECT, so HTTPS targets chain through it; a plain HTTP target
depends on the upstream accepting CONNECT to port 80.

## SSL proxying

HTTPS is tunnelled untouched until a domain is decrypted: the table shows its
CONNECT as a `Tunnel` row, and the detail pane offers **Decrypt this domain**
and **Decrypt every domain**. **Network → SSL proxying…** edits the same list,
kept in `~/.vscroxy/ssl.json`; a domain matches its subdomains. Nothing is
decrypted out of the box, as a proxy that installs a CA must.

A connection already open keeps the choice it was made with: reload the page
after decrypting a domain.

## Ignored hosts

**Network → Ignored hosts…** keeps a host's flows out of the table while still
proxying them, for the traffic a browser makes that no one reads. The list is
kept in `~/.vscroxy/ignore.txt` and matched like the SSL list.

**Ignored apps…** does the same by app, with the names the **Apps** section
shows, in `~/.vscroxy/ignore-apps.txt`: a browser's background traffic stays
out of the table while another app's does not.

## Export and sessions

The **Session** menu exports a HAR 1.2 file (HTTP flows; WebSocket and SSE
messages have no place in HAR), saves the table and its frames as a JSON
session, and opens one back. The native save and open dialogs choose the
path.

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
against the original (same method, scheme and URL). A replay trusts the
system's CAs and the proxy's own, so an HTTPS flow replays without the CA
being trusted machine wide.

## Compose

**Compose** writes a request by hand: method, URL, headers as `Key: Value`
lines, and a body. **Send** runs it through the proxy, so the exchange lands in
the table with the rest of the traffic. A row's **Edit in Compose** fills the
editor with what was captured, the step from traffic to a request worth
keeping.

**Save** keeps the editor's request under its name in
`~/.vscroxy/requests.json`, replacing one of the same name in the same
collection; the sidebar groups the saved requests by collection, and the
**Session** menu exports and imports them as a file.

**History** lists what Compose sent, newest first, without repeats of the
same method and URL, in `~/.vscroxy/history.json`; clicking one fills the
editor with it again, and **Session → Clear history** empties the list.

## Timing

The **Timing** tab splits a flow's time into the wait for the server's first
response byte and the download after it, as a bar and as numbers. A flow that
never reached a server has no timing; the total in the table includes what the
throttle adds, so it matches what the client saw.

## System proxy and certificate

The **System** menu points the machine at the proxy and installs the CA, so a
client needs no manual setup:

- **Enable system proxy** sets the proxy to `127.0.0.1:7890`; **Disable system
  proxy** turns it off again. Linux writes the GNOME proxy settings with
  `gsettings`, macOS sets every enabled service with `networksetup`, Windows
  writes the per-user WinINet settings with `reg`.
- **Install certificate…** trusts `~/.mitmproxy/mitmproxy-ca-cert.pem`. Linux
  copies it to `/usr/local/share/ca-certificates` and runs
  `update-ca-certificates` through `pkexec`; macOS adds it to the System
  keychain through `osascript` with an admin prompt; Windows runs
  `certutil -addstore Root`, which needs an elevated process.
- **Reveal certificate** opens the folder that holds the CA, for clients that
  want the file itself.

Each helper runs off the UI thread because the platform may prompt for a
password, and its result lands in the status bar. A helper that is missing or
lacks permission reports the command to run instead of failing silently. The
CA exists only after the first HTTPS exchange, so install it after capturing
one request.

## Client attribution

The **Client** column names the app a connection came from, with its icon.
The icon comes from the app itself: the `.desktop` entry's icon on Linux, an
icon theme's for the app's name, or the logo a bundled app keeps beside its
executable, as Chrome does; on macOS, the PNG inside the `.icns` of the `.app`
bundle. An app with none gets a badge in its color with its initial, so every
row still reads at a glance.
When a connection opens, the proxy finds the socket that the client holds on
the other end and reads the process that owns it: `/proc/net/tcp` and the
descriptor tables on Linux, `lsof` on macOS, `netstat` and `tasklist` on
Windows. A request from another machine has no local process, so the column
stays empty.

The **Apps** section of the sidebar filters by app. Windows shows a badge
only: an app's icon lives in its executable's resources, which are not read
yet.

## Settings

What you set stays between launches: the dark theme, the sidebar's width, how
the table and the detail pane share the window, and the columns' order and
widths, in `~/.vscroxy/settings.json`. The window's position and size are
MyGo's own, kept in `window-state.json` under the app's config directory and
restored when they would still show on a connected display.

## Next

- Collections with folders, and a request history beside the saved ones.
- Pinned requests, and the request and response sizes apart.
- A per-app capture filter, so a browser's noise can be dropped by client.
