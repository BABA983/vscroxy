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

## Connect a phone or another device

Open **Network → Connect a device** or **Settings → Proxy → Connect a device**.
Device access is **off on every launch**; consent and listeners are not persisted.

1. Put the computer and device on the same trusted network. Select the computer's
   device-facing IP (not a wildcard); the default proxy port is 7891 and the public
   certificate service uses the next port, 7892. Allow both ports in your firewall.
2. Enter the device's IP from its Wi-Fi details in the allowlist. Exact IPs are
   preferable; a CIDR admits the entire range. Empty lists and `/0` are refused.
   The same source-IP gate protects both listeners, before any proxy dial or HTTP
   parsing; forwarded headers cannot bypass it.
3. Confirm that you trust the network/devices, then enable access. Configure the
   device's manual HTTP proxy using the displayed address. No proxy authentication
   is configured; this is restricted by source IP, not user identity.
4. Open the displayed certificate URL on the device. It serves only the public
   CA certificate as DER, never a directory or signing key. Compare the CA's
   SHA-256 fingerprint under **Checks**; obtain it over a trusted network.
5. Follow the guide's **iOS** or **Android** tab. On iOS, install the downloaded
   profile and explicitly enable full trust under General → About → Certificate
   Trust Settings. On Android, install it as a CA certificate; many Android 7+
   apps need a debug Network Security Configuration that trusts user CAs.
6. Enable **Configure HTTPS inspection** for the desired hosts, then make an
   HTTPS request from the device and inspect its decrypted path/body in Capture.

**Checks** distinguishes local proxy reachability and last admitted TCP clients
at each port. A TCP connection does **not** prove that a certificate was installed,
that HTTPS decrypted, or that the remote network path is reachable. No traffic?
Check DHCP changes, the selected interface, VPN, firewall and Wi-Fi isolation.
Certificate pinning and clients that ignore the OS proxy are not bypassed. The
pinned MITM engine generates DNS SANs, not IP SANs: intercepted HTTPS using a
numeric-IP URL can fail certificate verification; use a DNS hostname when testing.

The local proxy stays at `127.0.0.1:7890`. The gateway forwards raw TCP to that
same core, keeping capture IDs, rules and pending local requests/tunnels. Closing
the guide keeps access enabled (the Network menu shows **LAN**); **Disable device
access** closes both LAN listeners and all their active connections, including
CONNECT/WebSocket tunnels, without stopping local capture. Disable before editing
the address/allowlist. Turn off the device's manual proxy and remove the CA trust
when finished debugging. Mobile application process attribution is unavailable;
the capture's Client column may identify the desktop gateway instead.

## What is here

- `main.go`: the app state, the three-pane layout, the toolbar, the traffic
  table, the detail tabs and the status bar.
- `requests.go`: independent request documents, their tabs, draft-close
  confirmation and the inline response viewer.
- `rules.go`: visual rule management and captured-response mock creation.
- `devices.go` / `internal/lan`: opt-in, source-restricted device gateway and
  public certificate onboarding without restarting the local proxy.
- `apis.go` / `openapi.go`: the APIs workspace, local specification storage,
  OpenAPI validation, collection-to-contract drafts and endpoint-to-request conversion.
- `data.go`: seeded flows for the case where the proxy cannot start, and
  formatting helpers.
- `internal/capture`: the MITM proxy and the `Flow` boundary type. The UI
  imports this package, never the engine.
- `internal/system`: the per-platform proxy and trust-store helpers behind the
  Settings dialog.
- `internal/procinfo`: which local process owns a connection, for the Client
  column.
- `internal/appicon`: the app's icon, from its `.desktop` entry on Linux.
- `mygo.json`: the app's name, identifier and version for `mygo package`.

The **Capture** workspace has filters, domains and apps on the left, the
traffic table above, and Headers / Body / Diff below it. Request and response
headers scroll independently side by side. **Requests** has its own collection
and history sidebar and a tabbed editor; switching workspaces preserves the
capture filters and all open drafts and responses. The diff pane is
MyGo's own `ui/editor` diff viewer, not Monaco, so it stays native and can be
reused on mobile later. It compares a response with the previous response for
the same method, scheme and URL.

## The table

The table is laid out like a proxy tool's: method, status, the whole URL with
its scheme, the app and its icon, the duration and the size. A click on a
header sorts by that column. Ctrl-click adds a row to the choice and
Shift-click chooses a range, which **Session → Choose all shown** and
**Clear choice** also do without the keys; the status bar counts the rows
chosen, and **Export chosen HAR…** writes just those.

The chips above it keep one kind of traffic in view: HTTP or HTTPS by scheme,
WebSocket and SSE by kind, and JSON, images or HTML by the response's content
type. They narrow what the sidebar's filters and the search show, and the
status bar counts the flows, the bytes captured and the last message.

**New view** opens another live tab over the same captured traffic, not a new
proxy or capture session. Each tab retains its own search, sidebar/chip filter,
status/header/body conditions, sorting and selection. **Filters…** edits content
conditions; **Saved filters…** saves, reapplies, renames or deletes a named set in
`~/.vscroxy/views.json`. Saving conditions persists them; live view tabs themselves
are session-local. Applying a saved filter changes only the active view.

Body conditions search both request and response independently, including whole
spill files beyond the 8 MiB preview. Searches are case-insensitive, chunked,
cancellable and off the UI thread (at most four readers); pending rows remain
hidden until matched. Progress and unreadable/incomplete-body warnings appear
above the table. Missing or incomplete bodies never silently match just their
preview. URL/header/status filtering remains immediate.

A row's menu carries what a captured request is worth outside the table:
**Copy URL**, **Copy as cURL** (its headers, body and URL as a shell
command), **Open in browser**, **Copy response body**, **Pin to the top**,
**Edit & resend**, **Save as request** (into the saved ones, under a name of
its own, so a second save does not replace the first), and **Replay**.

## Rules

Open **Rules** in the toolbar (or **Settings → General → Manage rules**) to
create, edit, enable, delete and move rules up/down. Save applies changes to
new requests only, without restarting capture. A failed save leaves the running
rules unchanged. Closing the editor discards unsaved field edits.

Rules live in `~/.vscroxy/rules.json`. Enabled Map Local, Map Remote and Block
rules apply in order, first match wins. Breakpoints match independently at their
request/response phase and also require the toolbar's **Break** switch. `match`
is a literal substring of the full URL, not a regex/glob; optional `method`
restricts the HTTP method. `disabled: true` turns a rule off; old files without
this field remain enabled.

```json
[
  {"type": "map_local", "match": "/missing", "target": "/tmp/mock.json"},
  {"type": "map_remote", "match": "http://example.com/api", "target": "http://127.0.0.1:8081"},
  {"type": "block", "match": "/telemetry"},
  {"type": "break", "match": "/checkout"},
  {"type": "break_response", "match": "/v1/orders"}
]
```

- `map_local` answers from a file, without calling an upstream. Optional `status`
  and `headers` customize the response; connection headers are removed and length
  is recalculated. Files over 8 MiB fail with 502 instead of being truncated.
- `map_remote` sends the request to another scheme and host, keeping the path
  and query; the table keeps showing the URL the client asked for.
- `block` answers `403`.
- `break` pauses the request until you decide, and `break_response` pauses
  the response (see below).

**Settings → General → Reload configuration** rereads the file. A missing
file means no rules.

A captured flow's **Create rule** context submenu pre-fills its URL and method
for mapping, blocking or breakpoints. **Mock this response** copies the complete
response to `~/.vscroxy/mocks/`, retaining status and response headers (except
wire framing, compressed-body metadata and stale validators). Binary and spilled
bodies are supported up to 8 MiB; missing/partial bodies and stream previews are
rejected. Review captured headers for secrets before sharing the rule. Canceling
an unsaved mock removes its file; deleting a saved rule keeps its file for reuse.

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
Streaming and SSE responses skip the response hook. **Reload configuration**
rereads both the rules and the script.

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

## DNS mapping

**Network → DNS mapping…** lists names the proxy resolves itself, a line of
`name = address` each, kept in `~/.vscroxy/dns.txt`. The proxy dials the
address while the request keeps its name, its URL and its `Host` header, so a
phone or emulator pointed at this proxy reaches another machine as if the name
pointed there — the DNS spoofing a proxy needs, without editing the hosts file
or rooting the device. A name matches its subdomains and `*.example.com`
matches one level; the status bar shows the last name a rule answered.

The proxy resolves on the machine it runs on, so a hosts file entry already
applies to a device's traffic; this list is for the mappings kept with the
tool. Two things bypass it: traffic the app sends over QUIC (UDP 443) never
reaches the proxy, and an app that connects to an address it already has.

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

## Requests

**New request** opens a document tab in **Requests**. Method, URL and **Send**
are the main action row; **Params**, **Auth**, **Headers**, **Body** and **Scripts**
select what is being edited. Params edits the URL's query, retaining duplicate
keys. Auth supports Bearer, Basic and API keys in a header or query parameter;
explicit auth replaces that header and pre-request scripts run last.
Body supports raw text, URL-encoded fields and multipart text/file parts with
duplicate keys, disabled rows and a 16 MiB total form limit. File paths are
stored, not file contents; sending or exporting reads the selected local files.
Headers remain `Key: Value` lines, and Scripts contains separate
pre-request and post-response editors. **Send** runs through the proxy and
shows the response body (formatted and highlighted for JSON), headers,
status, duration, size and test results below the editor. The exchange also
lands in Capture. The inline response preview is limited to 8 MiB; Capture
retains the complete body.

**Cookies…** manages the in-memory cookie jar shared by Requests and Runner.
Set-Cookie, redirects, domain/path restrictions, expiry and Secure are respected.
Cookies can be inspected, added, deleted or cleared; they are not persisted and
raw capture Replay does not use this jar. Saved auth, environment variables and
request history may contain credentials: local files are private (0600), not
encrypted. Prefer `{{variables}}` and review exports before sharing. OpenAPI
draft generation explicitly warns that structured forms and auth need manual
schema/security definitions; it does not copy their secrets or file paths.

**Runner…** executes a saved collection, including its subfolders, sequentially.
Select/reorder steps, set 1–1000 iterations, and optionally stop on HTTP errors,
failed assertions or execution errors. Each step has a 30-second deadline;
scripts have a 2-second limit and cancellation interrupts both scripts and I/O.
The starting environment is snapshotted; script variables and session cookies
carry into later steps. Results show status, timing and assertions (latest 1000
rows, with cumulative totals). Closing the dialog leaves the run active;
**Cancel run** stops it. Unsaved request tabs are not run or modified.

Each tab owns its draft, editor selection, pending send and response. Concurrent
sends, even to the same URL, retain their own results and run the script that
was present when sent. Script variable updates go to the originating environment
without overwriting unrelated variables changed by another tab. Reopening a
saved request focuses its existing tab instead of replacing unsaved edits.
A dot marks unsaved changes; closing offers **Cancel**, **Discard** or
**Save & close**. A failed save leaves the draft open. Sending tabs cannot be
closed until the send finishes. Open tabs are session-local, not restored on
restart.

A captured row's **Edit & resend** creates a new draft and links back to its
source flow while that flow remains in the capture session. This is the step
from traffic to a request worth keeping; saved collections can then become
independent contracts in **APIs**.

**Save request** keeps the editor's request under its name in
`~/.vscroxy/requests.json`, replacing one of the same name in the same
collection; the sidebar groups the saved requests by collection, and the
**Session** menu exports and imports them as a file.

**History** lists what Compose sent, newest first, without repeats of the
same method and URL, in `~/.vscroxy/history.json`; clicking one fills the
editor with it again, and **Session → Clear history** empties the list.

### cURL

**Import cURL…** is available at the bottom of the Requests sidebar, in
**Session**, and in the native **File** menu. Paste one Bash/POSIX command to
open a new, unsent tab without replacing another draft or saving it automatically.
Single/double quotes, escaped newlines, Bash `$'…'` strings, inline short options
and `--option=value` are supported. Import reads literal request data only:
URL, method, repeated headers, cookies, Basic credentials, user agent, referer,
JSON/raw/form-encoded bodies and `--get` query data. Shell/environment/glob
expansion is never run. `{{variables}}` remain placeholders.

The importer never executes curl or reads local files/stdin. It rejects pipelines,
substitutions, redirects, assignments, multiple URLs, `@file` bodies/headers,
multipart file uploads and unsupported options. Connection/security/redirect
options such as `--insecure`, `--location` and `--config` are rejected rather than
silently changing behavior. Output flags and `--compressed` are accepted but not
applied; sending uses this app's proxy/client, not curl's transfer settings.
PowerShell and Windows CMD quoting are not supported. Commands are limited to
16 MiB.

**Copy cURL** beside **Send** copies the active draft; **Copy as cURL** in a
captured row's menu copies that exchange. Variables stay unexpanded and scripts
are not run. GET bodies keep an explicit GET method; quotes are escaped, empty
headers preserved, and stale Host/Content-Length/connection headers omitted.
Captured spilled bodies are read in full rather than copying just the 64 KiB
head; missing files, binary/NUL bodies or commands over 16 MiB produce an error
and leave the clipboard untouched. This is a text-body export, not a multipart
file exporter. Credentials/cookies are included when present: review before sharing.

## APIs / OpenAPI

The third workspace holds contracts independently of the request library.
**From collection…** (also **Session → Generate OpenAPI…**) generates a new
OpenAPI 3.0.3 draft from the selected saved collection, not unsaved request tabs.
It merges equal method/path operations and retains their observed servers.
`{{id}}` in a path becomes an OpenAPI path parameter; literal IDs stay literal.
The generator omits query/header/body values, URL credentials, request titles
and scripts. Review literal paths and parameter names for sensitive data before
sharing. Authentication, parameter types, body schemas and response status codes
still need human review: a captured example is not a complete API contract.
**Review notes** explains these limits beside the draft.

**New** creates a standalone specification; **Import…** opens OpenAPI 3.0/3.1
JSON or YAML, including internal `#/` references and 3.1 schemas. External files
and URLs are not fetched. Documents are limited to 16 MiB. **OpenAPI** is a native
syntax-highlighted editor; **Validate** checks the structure with kin-openapi.
Edited source must be validated before its endpoints can be used or exported.
**Save API** validates and persists only that specification, leaving other
unsaved editors untouched. Invalid source or a failed save never replaces the
saved version. **Export…** writes the current validated source, preserving
comments, formatting and extensions rather than rebuilding the document.

**Endpoints → Open in Requests** opens an unsent draft with a link back to the
API editor. It uses the first server at operation, path or document scope,
substitutes declared server defaults, and creates `{{name}}` placeholders for
path and required query/header parameters. Without a server it uses
`{{base_url}}`; configure that environment variable before sending. JSON body
examples are used when supplied; schemas are not converted into invented
payloads. Review authentication, parameter serialization and the body before
sending; no runtime authentication or scripts are inferred from the specification.

Specifications live in `~/.vscroxy/apis.json`. Saving requests does not update
them. A generated specification tracks its source collection and shows a warning
when saved requests change; **Generate new draft** creates another specification,
never overwriting hand edits. There is no automatic/bidirectional sync, multi-file
bundling, Swagger 2 import or schema inference yet. Unsaved API editors and source
links are session-local; saved contracts are restored after restart. Cmd/Ctrl+3
opens the workspace.

## Timing

The **Timing** tab splits a flow's time into the wait for the server's first
response byte and the download after it, as a bar and as numbers. A flow that
never reached a server has no timing; the total in the table includes what the
throttle adds, so it matches what the client saw.

## System proxy and certificate

**Settings → Proxy** controls the system proxy; **Settings → Certificates**
contains the CA and TLS inspection actions. The native menu also opens
Settings with Cmd/Ctrl+,; Cmd/Ctrl+1 and Cmd/Ctrl+2 switch workspaces, and
Cmd/Ctrl+N starts a request. On Linux and Windows, Alt reveals the native
menu bar.

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

- Structured query/auth/form-data editors, cookie management, collection folders
  and a runner with per-request assertions.
- Named capture filters and independent traffic views; complete-body search
  rather than searching only the in-memory capture head.
- WebSocket/SSE message search and detail views, correct upgraded request paths,
  and spill-backed message retention instead of silent truncation.
- Descriptor-aware protobuf/gRPC inspection, trailers/status, and protocol
  compatibility verification before promising full HTTP/2/gRPC support.
