# vscroxy desktop

The desktop app, written in Go with [MyGo](https://github.com/egoist/mygo)'s
native UI: no webview, no HTML or JavaScript UI. MyGo draws
the widgets on the GPU and takes text, input methods and accessibility from
the platform.

The app captures real traffic: it runs a loopback MITM proxy with
[go-mitmproxy](https://github.com/lqqyt2423/go-mitmproxy) and shows every
exchange as it arrives. Icons come from the Lucide set pinned by
[onevra](https://github.com/oneo-me/onevra) (`source/icons/lucide`); its
licenses and the Feather-derived icons' are in
[THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md).

## Run

Go 1.27.3 or later. On Linux the app needs GTK 3 and OpenGL runtime libraries.
Run this from the repository root:

```sh
go run ./cmd/vscroxy
```

The proxy prefers `127.0.0.1:7890`. If that address is unavailable (for example,
Clash already owns it), it binds an OS-assigned loopback port instead. It holds
that socket throughout startup, without probing another application's listener
or closing/rebinding a candidate port. The OS chooses from its configured
ephemeral pool and respects its reserved/excluded-port configuration; the app
does not scan low ports or change those settings. The assigned port may differ
on the next launch.

For a stable port, open **Settings → Proxy → Listening port**, enter a number
from **1024 to 65535**, and click **Save for next launch**. Restart the app to
apply it; current capture connections and system-proxy settings are not changed
by saving. A fixed port never falls back silently: if it is unavailable, capture
stays stopped and reports the bind error. Leave the field blank and save to
restore automatic selection. The value is kept in `~/.vscroxy/settings.json`
(`proxyPort`, zero or absent for automatic). The panel distinguishes the current
listening address from the next-launch configuration.

**Live**, the empty traffic list and **Settings → Proxy** show the actual bound
address. Only a successfully bound proxy appears Live; failed startup leaves
system-proxy enablement disabled. The examples below use the preferred port;
replace `7890` with the displayed port if it changed. Then point a client at it:

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

For a local process, **Network → Set up a local client…** provides copyable cURL,
Node.js (`https-proxy-agent`), Python (`requests`) and Go examples. They set the
proxy and trust the public CA for that process only; they do not change the OS
proxy, install a system CA or skip TLS verification. cURL uses POSIX shell syntax.
Choose an HTTPS URL, enable inspection for its host, then run the example yourself.
The guide separates a listening/reachable proxy, the decryption rule and newly
observed decrypted traffic, with an action to inspect that capture.

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
at each port. Set the target HTTPS URL to see the current host rule and decrypted
traffic observed since opening setup. Imported captures never count as evidence.
Evidence is session-wide, not attributed to a specific phone or runtime, and
client CA trust remains explicitly unverified: a client could skip verification.
A TCP connection does **not** prove that a certificate was installed,
that HTTPS decrypted, or that the remote network path is reachable. No traffic?
Check DHCP changes, the selected interface, VPN, firewall and Wi-Fi isolation.
Certificate pinning and clients that ignore the OS proxy are not bypassed. The
pinned MITM engine generates DNS SANs, not IP SANs: intercepted HTTPS using a
numeric-IP URL can fail certificate verification; use a DNS hostname when testing.

The local proxy stays on loopback. The gateway forwards raw TCP to the core's
actual listening port, keeping capture IDs, rules and pending local requests/tunnels.
Closing the guide keeps access enabled (the proxy indicator shows **LAN**); **Disable device
access** closes both LAN listeners and all their active connections, including
CONNECT/WebSocket tunnels, without stopping local capture. Disable before editing
the address/allowlist. Turn off the device's manual proxy and remove the CA trust
when finished debugging. Mobile application process attribution is unavailable;
the capture's Client column may identify the desktop gateway instead.

## What is here

- `main.go`: the app state, the workspace content, the traffic table, the
  sidebar and the footer.
- `appearance.go`: the palette, the Lucide icons the app uses, and the shared
  dialog and tab chrome.
- `shell.go`: the window's own title bar, the workspace switch, the proxy
  indicator, the rare menus and the footer each workspace shares.
- `inspector.go`: the inspector under the traffic: one summary line, then the
  request and the response side by side, each with its own tabs, and the
  exchange-wide Diff, Connection and Messages views one click away.
- `investigation_board.go` / `investigation_board_ui.go`: the investigation
  board — named investigations of immutable exchange snapshots, notes,
  comparison, layered failure evidence and evidence export.
- `requests.go`: independent request documents, their tabs, draft-close
  confirmation and the inline response viewer.
- `rules.go`: visual rule management and captured-response mock creation.
- `devices.go` / `internal/lan`: opt-in, source-restricted device gateway and
  public certificate onboarding without restarting the local proxy.
- `apis.go` / `openapi.go`: API contracts, local specification storage,
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

The window draws its own title bar under the macOS traffic lights: the name,
the two workspaces — **Capture** and **Requests** — the proxy address,
**Commands** and **Settings**, with the rest in the **⋯** menu. It is a native
window, not a frameless one, so resizing, full screen and the system window
menu still work. The switch names how you work; what you work on lives in each
workspace's sidebar: **Investigations** in Capture, **API contracts** in
Requests.

The **Capture** sidebar groups **Traffic** (all requests, errors, slow), a
**Pinned** row of its own, apps, domains, one **Views** area holding opened
views and saved filters, whose create and manage actions are two compact
buttons under the list, and the investigations. Its traffic table sits above
the inspector. The inspector shows the request and the response **beside each
other**, each with its own Headers / Params / Body / Raw (request) or
Headers / Body / Raw / Timing (response) tabs, so a body comparison never needs
a tab switch. Each pane has a separate heading and format menu above its tabs.
Its **⋯** menu holds the exchange-wide views — Diff, Connection
and, for WebSocket and SSE, Messages — and the layout choices. Request and
response headers scroll independently. **Requests** has a resource rail for
**Collections**, **Envs**, **Runs** and **APIs**. Collections form a nested tree;
search reveals matching ancestors without changing saved expansion choices.
Runs opens retained attempts inline, without sending them again. Document tabs
stay in the content pane above the editor and inline response viewer. Switching
resources or workspaces preserves the capture filters, all open drafts and
responses, and the board. The response's **Last attempt** disclosure shows the
full sent draft URL and response time; the collapsed row still distinguishes
an edited, unsent draft from the retained result. The diff pane is MyGo's
own `ui/editor` diff viewer, not Monaco, so it stays native and can be reused on
mobile later. It compares a response with the previous response for the same
method, scheme and URL.

## The table

The table starts with the capture number and whole URL, followed by method,
app, status, duration and size. Search and protocol chips occupy separate rows;
**Rules** and **Layout** are directly accessible in the toolbar. A click on a
header sorts by that column. Ctrl-click adds a row to the choice and
Shift-click chooses a range, which **Session → Choose all shown** and
**Clear choice** also do without the keys; the status bar counts the rows
chosen, and **Export chosen HAR…** writes just those.

Right-click the table's header to hide or show the built-in columns (the URL
always stays) or to **Add column…** from a header (the response's, or the
request's when the response has none), a request or response header only, or
a URL query parameter. Added columns sort like the others, a header cell's
menu filters by its value, and the header menu removes them. The choice is
kept in `settings.json`; an older single header column becomes the first
added one.

The protocol tabs beside the search keep one kind of traffic in view: HTTP or
HTTPS by scheme, WebSocket and SSE by kind, and JSON, images or HTML by the
response's content type (the last two live in the toolbar's **⋯**). They narrow
what the sidebar's filters and the search show, and the footer counts the
captured and shown rows. Feedback appears in full in temporary toasts rather
than staying in the footer indefinitely. The proxy indicator in the title bar
opens Proxy settings.

The search field matches **host and path only**; Ctrl/Cmd+F focuses it. Use
**Filters…** for headers, bodies and status codes. Status accepts comma-separated
alternatives (for example `4xx, 5xx`) and validates malformed expressions before
applying them. A filtered-out list explains that captured traffic is hidden and
offers **Show all requests**. Hidden rows are removed from the bulk selection.
**Clear** asks for confirmation and keeps pinned exchanges.

Each capture view follows new rows while scrolled to the bottom. Scrolling up
pauses following without changing the request being inspected; scrolling back
to the bottom resumes it. The footer says **Auto-follow on** or **Auto-follow
off · 12 new ↓**; clicking it scrolls to the end and resumes following without
changing the inspected request or selection. Following the list, the proxy
running and breakpoints are three independent states and never imply each
other. Views keep their own scroll positions. With a
custom column sort, the bottom means the end of that displayed order, not
necessarily the most recently captured request.

**New view** opens another live tab over the same captured traffic, not a new
proxy or capture session. Each tab retains its own search, sidebar/chip filter,
status/header/body and triage conditions, sorting and selection. **Filters…** edits content
conditions; **Saved filters…** saves, reapplies, renames or deletes a named set in
`~/.vscroxy/views.json`. Saving conditions persists them; live view tabs themselves
are session-local. Applying a saved filter changes only the active view.
Views and saved filters live in the sidebar, with a **+** for a new view.
Saved filters appear as one-click buttons there; click an active
button again to show all requests in that view. Filters supports comma-separated
methods, duration in milliseconds, captured response-body bytes, color/marked
state and case-insensitive note text. Numeric fields accept `<`, `<=`, `=`, `>=`
or `>` with a non-negative integer. Conditions combine with AND; methods and
status alternatives combine with OR. A removable token strip shows every active
condition (URL, source, protocol and content filters); **Reset all** clears them.
It appears only while conditions are active, keeping the normal toolbar to two rows.

Body conditions search both request and response independently, including whole
spill files beyond the 8 MiB preview. Searches are case-insensitive, chunked,
cancellable and off the UI thread (at most four readers); pending rows remain
hidden until matched. Progress and unreadable/incomplete-body warnings appear
with the views in the sidebar. Missing or incomplete bodies never silently match just their
preview. URL/header/status filtering remains immediate.

Large HTTP uploads and downloads are kept from the actual streaming path,
including chunked transfers. Capture decodes gzip, deflate, Brotli and Zstandard
on disk without changing forwarded bytes. Aborted transfers, decode failures
and spill failures are marked incomplete rather than searched as empty bodies.
Body previews stay limited to 8 MiB; saved JSON sessions retain the complete
captured bodies and their incomplete-state metadata.

A row's menu starts with the request's next step: **Replay**, **Edit &
resend** and **Save as request** (into the saved ones without sending, under a
name of its own, so a second save does not replace the first). With several rows
chosen, **Save N requests to collection…** files them, in capture order and
unsent, under a new or existing collection, optionally one folder per host;
tunnels, WebSocket connections and incomplete or non-UTF-8 bodies are skipped
and listed. **Copy** holds
the URL, cURL and response body; then come **Open in browser**, **Filter
captures**, **Pin** and **Mark…**. **Tools** creates a rule from the request
(breakpoints, Mock this response, Map Remote, Block), maps its host in DNS or
changes its HTTPS inspection. Investigation and regression actions come last.

**Edit & replay** and **Save as request** use the complete retained, decoded
request payload, not the Body preview. Repeated headers and query parameters
are kept; stale content encoding/length, transfer framing and the client's
connection headers (`Proxy-Connection`, `Proxy-Authorization`, `Keep-Alive`
and any header `Connection` names) are removed before sending decoded bytes. Incomplete or missing payloads, non-UTF-8 bodies,
request trailers and encrypted tunnels are explicitly refused rather than
silently converted to a different request. The text request editor does not yet
provide arbitrary binary capture editing. A single `Authorization: Bearer …` or
`Basic …` header moves to the draft's **Auth** tab when the tab sends exactly
the same value again; any other form stays a header.

## Rules

Open **⋯ → Debugging rules…** (or **Settings → General → Manage rules**) to
create, edit, enable, delete and move rules up/down. Save applies changes to
new requests only, without restarting capture. A failed save leaves the running
rules unchanged. Closing the editor discards unsaved field edits.

Rules live in `~/.vscroxy/rules.json`. Enabled Map Local, Map Remote and Block
rules apply in order, first match wins. Breakpoints match independently at their
request/response phase and also require the toolbar's **Break** switch. `match`
is a literal substring of the full URL, not a regex/glob; optional `method`
restricts the HTTP method. `disabled: true` turns a rule off; old files without
this field remain enabled.

**Scenario** groups Map Local, Map Remote and Block rules into named cases such
as an empty account, server error or alternate upstream. Save a rule with a
scenario name, then explicitly **Activate scenario**; choosing a name alone does
not activate it. At most one named scenario is active. **Deactivate scenario**
leaves only global (blank-scenario) rules eligible. Global rules keep their place
in first-match order, including ahead of scenario rules; switching never changes
individual enabled switches. Breakpoints remain global and independent.

Selection lives in the same `rules.json` snapshot (`scenario` and matching
`scenarioActive` fields on every member), not a second settings store. Mixed
selection states and multiple active groups are rejected on load/save/apply.
Changes affect new requests, never an in-flight response. Captured **Applied**
evidence includes the scenario that actually won, so later switches do not
rewrite its provenance. **Test URL + method** previews saved rules without
sending: winner, shadowed rules, disabled/inactive groups, URL/method mismatches
and independent breakpoints. It does not simulate unsaved edits, scripts or
missing response-file errors. Mock payload files stay local; this is not a
portable scenario export or a delay/fault-injection engine.

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

A captured flow's **Tools** context submenu pre-fills its URL and method
for mapping, blocking or breakpoints. **Mock this response** copies the complete
response to `~/.vscroxy/mocks/`, retaining status and response headers (except
wire framing, compressed-body metadata and stale validators). Binary and spilled
bodies are supported up to 8 MiB; missing/partial bodies and stream previews are
rejected. Review captured headers for secrets before sharing the rule. Canceling
an unsaved mock removes its file; deleting a saved rule keeps its file for reuse.

## Connection diagnostics

**Network → Capture diagnostics…**, the command palette, or **Diagnose capture…**
in the empty list opens read-only checks for the listener, OS proxy readback,
target traffic and engine configuration. Enter an HTTP(S) URL: evidence matches
its scheme, hostname and port (default ports are equivalent), not its path.
Counts include retained live traffic from any client, not imported sessions;
the panel distinguishes decrypted requests, CONNECT records and records hidden
by the current capture view. **Inspect latest evidence** opens hidden records in
a new view without erasing existing filters. An absent record is not a confirmed
failure; ignored traffic is proxied without being retained. Current rules and
decryption settings are not proof of past execution or client CA trust. Opening
or refreshing checks does not restore an interrupted system-proxy session,
change proxy settings, install a CA, send requests or expose a LAN listener.

Select **Connection** in a captured exchange's detail to inspect and copy its
connection log: DNS lookup, TCP dial attempts, upstream/client TLS handshakes,
connection reuse, ALPN and the upstream certificate's SANs and expiry. The log
explicitly distinguishes verified certificates from verification skipped by the
debugging proxy. It is a capture-time snapshot with at most 64 connection events,
not a packet trace; a reused connection includes its setup history.

Transport failures appear as **Failed** and are included in **Errors**. A
successful CONNECT can still be followed by a failed TLS handshake: its original
status is retained, while the log identifies the failed stage and underlying
error. A client-side TLS error suggests checking that client's CA trust or
certificate pinning; it does not prove either is the cause. Logs and failure
details survive saving/reopening a session; older/imported sessions may have no
connection history. Review hostnames, addresses and certificate details before
sharing logs.

For a transport failure, **Explain** in the detail header shows the recorded
stage and raw error, possible causes (not confirmed), and stage-specific checks.
Client and upstream TLS failures have different guidance; unknown stages do not
guess a cause. **Copy diagnostics** combines the request, recorded status, error,
guidance and available connection log, even for a session with no log. The
existing **Copy connection log** action still copies only the original log.
Explanations are local guidance: opening them does not retry requests, change
certificate trust or send diagnostics to an external service. Review URLs,
addresses and raw errors before sharing the copied report.

## Breakpoints

Turn on **Breakpoints** in the capture toolbar. A request matching a `break` rule pauses
with its method, URL, headers and body. A response matching `break_response`
pauses with its status, headers and body; the response script, when there is
one, runs first. Repeat a header name on separate lines to keep multiple values
(for example, `Set-Cookie`). Invalid method/URL/status/header edits are reported
inline and cannot be applied.

- **Apply & continue** sends the edits. Body edits use decoded bytes and remove
  stale content-encoding, digest and framing headers. Unchanged bodies retain
  their original bytes and encoding, including header-only edits.
- **Continue original** discards edits in the dialog and preserves the message
  as it reached that breakpoint, including any earlier script changes.
- **Abort** answers with the selected `502`, `429` or `503` status. Escape or an
  outside click does not abort a live exchange.

Binary, undecodable and text bodies over 1 MiB are preserved read-only; their
headers can still be edited. Streamed exchanges do not offer body breakpoints.
The dialog shows the pending count; queued pauses appear in turn. The existing
two-minute pause timeout continues the exchange unchanged if no decision arrives.

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

Single header values are strings; repeated values are arrays of strings.
For example, `res.headers["Set-Cookie"] = ["session=abc; Path=/", "csrf=xyz; Path=/"]`
keeps two independent cookies. Assign a string to replace a field, an array for
multiple values, or delete the property to remove it. Empty hooks preserve
repeated fields.

Each hook runs in a fresh sandbox with a 250 ms limit and no files or
network; a hook that loops is interrupted and counted in the status bar.
Top-level execution during startup/reload has the same 250 ms limit; a failed
load returns an error instead of blocking initialization or replacing the active script.
Streaming and SSE responses skip the response hook. **Reload configuration**
rereads both the rules and the script.

## Bodies

The Body tab shows the response or the request, as **Pretty** text, as a
**JSON** tree with collapsible objects and arrays, or as a **Hex** dump with
offsets and text. Pretty is a read-only, selectable code reader with line
numbers, wrapping and JSON highlighting/formatting. **Find in text…**, or
Cmd/Ctrl+F while reading, highlights matches without hiding surrounding lines;
Enter / Shift+Enter and the arrow buttons move between matches. Esc closes
search. JSON and Hex instead filter matching paths/values or hex rows, as their
search placeholders explain. The JSON tree preserves object order and exact
number spellings, including large integer IDs. Invalid JSON falls back to text.

Each side shows its content type and preview byte count. **Copy shown body**
copies retained preview text, not pretty formatting or warning text. Binary
bodies disable text clipboard copying; HAR/session export retains their bytes. Display
limits, incomplete transfers and missing spill files are separate warnings;
they remain visible in every display mode and are still included in comparisons.
An empty response for HEAD, 204 or 304 is explained as expected, not a capture
failure. Unsupported previews offer direct text and hex alternatives.

**Auto** is the default: images/HTML use Preview, binary bodies use Hex,
explicit protobuf/gRPC uses Protobuf, and text/JSON uses Pretty. A manual mode
applies to the current request side; changing requests or sides resets to Auto
instead of leaving the next response in an incompatible display mode.

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

HTTPS is tunnelled untouched until a domain or app rule enables inspection:
the table shows its CONNECT as a `Tunnel` row. The detail pane keeps
**Set up certificate…** and **Enable for <host>** above its scrollable explanation,
including in small windows. **Settings → HTTPS → Manage domains…** edits the same list, kept in
`~/.vscroxy/ssl.json`; a domain matches its subdomains on all ports. Old rules
containing a port are also understood as hostname rules. IP clients without TLS
SNI receive a certificate for their CONNECT target, not an empty name. Nothing
is decrypted out of the box.

For a locally attributed connection, **Enable HTTPS for <app>** inspects all
hosts contacted by that exact executable on new connections. The **Apps**
sidebar context menu offers the same action; same-name executables at different
paths require an explicit choice. Names, parent applications and Helper groups
do not grant permission. Remote devices, non-loopback proxy connections and
unreadable or ambiguous process owners cannot use app rules. Domain/global rules
still apply independently. Client CA trust is still required.

**HTTPS rules** at the bottom of the sidebar manages domain and executable rules.
Removing an app rule takes effect only on Save; Cancel discards rule edits.
An enabled rule is not evidence that TLS succeeded, and old CONNECT records
never become decrypted requests retroactively.

A connection already open keeps the choice it was made with: reload the page
after enabling inspection.

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

HAR and session exports use private (0600 on Unix), atomic file replacement,
including over an existing broadly readable file. They still contain original
credentials and payloads: they are neither encrypted nor automatically redacted.

## WebSocket and SSE

A WebSocket connection appears as a `WS` row with status `101`, and an SSE
response as its request with the **Messages** tab. Search metadata and retained
payloads (including spill files), filter **All / In / Out**, and select a row
to inspect or copy its payload. Rows show direction, time, kind and size; SSE
keeps event name and ID separately. JSON is indented; binary opens as hex, and
**Text / Hex** switches views. Text layout previews at most 64 KiB; Hex and Copy
use the whole retained payload.

**Pause view** freezes an independent message window while capture continues;
new arrivals are counted. Search, direction filters and export use that frozen
window. **Resume** returns to the latest retained messages, not an unbounded
backlog. **Latest** follows the newest message without pausing capture.

The pane records start and observed end times, including empty SSE streams.
An unrecorded end is not proof that a connection is still open; imported sessions
are labelled historical. The engine does not expose WebSocket close codes or
end reasons, so the pane does not guess them. End evidence survives saving a
session. Completion totals also expose messages lost at the tail of a full queue.

**Export messages…** saves matching retained messages as JSON after a sensitive
data warning, with direction, sequence, timestamps, SSE name/ID, full retained
payloads and explicit missing/truncated-payload metadata. Binary bytes use the
session format's lossless base64 payload field when they are not valid UTF-8.
The file records query, direction, pause state and window/loss counts; it is not
an export of the entire conversation. File reads run off the UI thread.

The last 200 messages per flow are retained: at most 4 KiB each inline, with
longer payloads spilled to disk up to 256 KiB each. Larger messages explicitly
show retained/original lengths. Sequence gaps report dropped/evicted messages;
global queue losses appear in the status bar, including a loss at stream end.
Spill files are temporary and removed when capture closes; there is no total
disk quota for a long capture session yet.
Sessions save retained payloads and metadata, not temporary spill paths, and
older session files still load.

Both plain WS and decrypted WSS retain the actual escaped path/query. A small
documented local patch to go-mitmproxy fixes plain WS's discarded handshake
headers and CONNECT-only capture metadata (see `third_party/go-mitmproxy/VSCROXY.md`).
Composed WS requests use structured auth, custom headers and session cookies;
WSS verifies the system/proxy CAs rather than disabling certificate checks.
The one-shot sender waits up to two seconds for five replies, capped at 1 MiB
each; cancellation closes the socket. Post-response scripts require HTTP.

### Interactive WebSocket sessions

**Network / Commands → WebSocket session…** opens a separate live client. If
the active request draft has a WS URL, it prepares that draft's URL, auth,
headers and body using current variables, without running scripts or sending.
Otherwise enter a `ws://` or `wss://` target and handshake headers. **Connect**
performs the handshake through the capture proxy using the Requests cookie jar
and verified TLS. It sends no message until **Send text**. Send repeatedly on
the same connection, search retained payloads/events and filter In/Out/Events.
Select a message to read/copy text or binary hex; copying includes any truncation
notice and copies displayed text/hex, not a binary clipboard format.

The timeline keeps the latest 200 events, at most 64 KiB per payload, original
byte counts and an eviction count. Messages over 1 MiB end the receive stream;
sends are also limited to 1 MiB. It follows the bottom until scrolled away;
**Latest message** resumes following without changing the inspected payload.
An actual peer close includes its code/reason; an empty close frame is distinct
from TCP loss, local disconnect and handshake failure. These are client-observed
events, not inferred close codes on passive captures. Proxy-captured messages
still appear in Capture with its existing retention/export capabilities.

**Disconnect**, Escape, clicking outside or closing the panel disconnects; a
new **Connect** starts a fresh timeline. No automatic reconnect or restart
restore occurs. The timeline is session-local, not an experiment or a saved
conversation. HTTP Requests/Runner's one-shot WS sender remains available.
Interactive binary sending, subprotocol negotiation, automatic SSE reconnect
and live gRPC timelines are not provided by this panel.

## Protobuf and gRPC

**Body → Protobuf** inspects raw protobuf and native gRPC envelopes. Explicit
protobuf/gRPC content types select it automatically, even for an empty or broken
body. Without a schema it shows field numbers and wire values, not guessed field
names. **Schema…** (also **View → Protobuf schema…**) imports a `.proto` source
or a binary `FileDescriptorSet` (`.pb` / `.protoset` / `.desc`). Source imports
resolve relative to the source directory and optional include roots; standard
Google imports are bundled, and no installed `protoc` is required. Descriptor
sets must include their dependencies (`protoc --include_imports`). A successful
load replaces the local registry in `~/.vscroxy/protobuf.pb`; a failed load leaves
the previous registry intact.

**Auto** resolves `/package.Service/Method` to the request's input or response's
output message type. For raw protobuf, select its fully qualified message type.
The decoder displays names, signed values, enums, nested messages, maps, repeated
fields and unknown wire fields. gRPC bodies show every framed message, including
empty messages and per-message gzip compression. Search filters decoded messages;
select one to inspect it or **Copy decoded**. A broken later frame reports its
message number without hiding earlier valid messages. Inspection reads complete
retained spill files, not the 8 MiB Body preview, off the UI thread. Limits are
32 MiB encoded/decoded per body, 16 MiB decoded per message and 1000 messages;
text rendering previews 64 KiB, while Copy keeps the full decoded text.

**Headers** keeps trailers separate from initial headers and displays the actual
request/response HTTP versions. gRPC status prefers final `grpc-status` and
percent-decodes `grpc-message`, with initial-header fallback for trailers-only
responses. HTTP 200 alone is not success: a missing gRPC status is explicitly
unknown; a nonzero status appears in **Errors** and colors the HTTP code as a
failure without changing that code. JSON sessions preserve both trailer blocks
(including binary metadata) and protocols; HAR has no trailer representation.

The inspected TLS/HTTP/2 path forwards unary, server-streaming, client-streaming
and interactive bidirectional gRPC, including gzip, metadata, trailers-only
errors, deadlines and cancellation. The client must use the HTTP proxy and trust
its CA; enable HTTPS inspection for the target host. Messages are forwarded as
they arrive, but the capture's Body inspector receives the completed exchange,
not a live RPC timeline. This is an inspector, not a reflection-driven RPC client.
gRPC-Web, h2c and HTTP/3/QUIC are not supported inspection modes; compressed
message encodings other than gzip are reported as unsupported. The current Replay
sender uses HTTP/1.1, so native gRPC replay is not supported. Body-editing request
and response scripts/breakpoints are unavailable for streaming RPCs; Block,
Map Local and Map Remote rules still apply before forwarding.

## Replay

Select a flow and press **Replay**: the request goes out again through the
proxy, so the new exchange is captured like any other and can be diffed
against the original. A replay trusts the
system's CAs and the proxy's own, so an HTTPS flow replays without the CA
being trusted machine wide.

Ctrl/Cmd+R opens replay options for the inspected request; Ctrl/Cmd+Alt+C copies it as cURL.
**Session → Replay chosen…** snapshots the selected HTTP requests and sends them
in capture order after confirmation. Choose a per-request timeout (30 seconds by
default, 0 for no timeout), watch sent/failed counts, or stop the batch. Replaying
can mutate server data; streams and tunnels are excluded.

**Network → No Caching** is a session-only switch, also visible in the status
bar. New exchanges strip conditional cache headers, send no-cache request
headers, and remove response validators while forbidding response storage.
Each exchange snapshots the switch; changes do not rewrite historical captures.
The detail header's **Applied** line records cache bypass, scripts, edited
breakpoints and the matching Block/Map Local/Map Remote rule. JSON sessions
retain this evidence even if the active rules later change.

Replay completion reports the source and response status with **Inspect replay**,
which opens the exact result in Requests without stealing focus on arrival.
Diff labels the previous/current sides and explains when no baseline exists;
an empty previous response is still a valid baseline.

Captured replays have a **Replay of #N** link and **Compare with source** action.
The original lists its **Replays (N)**, including redirect exchanges. Links use
the sending connection, not URL matching or an extra upstream request header;
concurrent sends of the same URL keep their own sources. JSON sessions retain
these links (HAR does not); a removed source is shown as unavailable. Following
a linked capture hidden by filters opens a separate capture view instead of
erasing the original view's conditions and list position.

### Multi-request analysis

Ctrl/Cmd-click or Shift-click to choose requests. Two or more choices replace
the detail pane with a **Summary**: recorded response bytes, error/incomplete
counts, average/P50/maximum duration, and the five slowest requests with inspect
links. P50 uses nearest rank; durations are per captured exchange, not total
elapsed session time. Errors include HTTP 4xx/5xx, connection failures and
incomplete bodies. Filters remove hidden requests from the selection.

Choose exactly two rows and press **Compare two**, regardless of their URLs.
Compare request/response bodies or header blocks, **Swap sides**, and choose
**Split** or **Unified**. Left starts with the earlier capture; labels identify
both captures. Bodies use the same bounded 8 MiB preview and incomplete-body
notices as the inspector. Comparisons are read-only and keep their position
when unrelated traffic arrives. **Inspect focused** returns to one request.
The single-request Diff tab still compares with the previous same-method/URL
response.

## Requests

**New request** opens a document tab in **Requests**. The document tabs stay in
the content pane, so the sidebar runs up to the title bar and only the
workspace switch sits above them. The breadcrumb keeps a back button to the
traffic or to the capture the draft came from (**Source #N**), then the
collection and name as quiet text: clicking it renames the request and moves it
between collections, so neither needs a permanent input. The environment,
**Save** and the request's **⋯** menu — Copy as cURL, Variables, Run records,
Send history, regression import/export and environment management — sit at the
right. Method, URL and **Send** are the main action row; the method is an
editable dropdown with its own chevron, so the common verbs are a click away and
one the list lacks is still typed and kept. **Params**,
**Headers**, **Auth**, **Body**, **Scripts** and **Checks** select what is being
edited. **Params** and **Headers** are tables of an enable box, a key and a
value under **KEY** and **VALUE** titles, so order, duplicate names, empty
values and switched-off rows survive; a cell keeps a quiet border until the
pointer or the keyboard is on it. Header values whose name is a credential
(`Authorization`, `Cookie`, `X-Api-Key`, anything with `token`, `secret` or
`password`) are masked by default and show in plain text only when that row's
eye reveals them: moving the keyboard through the table never reveals one, and
a revealed cell is tinted with an accent border so the plain text is obvious.
**Bulk edit** swaps a table for the `Key: Value` block when pasting several at
once, and its text replaces the table when you switch back; switched-off rows
wait outside the block and come back with the table. Because the block writes
every value in plain text, switching to it asks first when a credential is in
the table. Disabled rows stay in the request file
(`headerList`) but never in the block that is sent, copied or exported. Params
edits the URL's query the same way, and only enabled rows reach it. Auth supports
**Inherit**, Bearer, Basic, API keys in a header or query parameter, OAuth
2.0, Digest, AWS Signature and JWT Bearer; explicit auth replaces that header
unless the pre-script explicitly sets/removes it. **Digest** sends once, and
when the answer is a 401 with a Digest challenge (MD5, SHA-256 or their -sess
forms, qop `auth` or none) sends again with the answer; both exchanges are in
Capture, and checks and scripts see the second. A challenge it cannot answer,
such as one offering only `auth-int`, fails as **Digest authentication
failed**. Copied cURL and code carry no Digest header, since there is no
challenge yet. **AWS Signature** signs with SigV4 as the last step before
sending, after the pre-request script, so headers a script adds are signed:
`host`, `content-type` and every `x-amz-*` header, with `X-Amz-Date`,
`X-Amz-Content-Sha256` and the session token when given. Region defaults to
`us-east-1`; S3 paths are signed without normalizing. **JWT Bearer** signs a
token per send with HS256/384/512 (a secret, optionally base64), RS, PS or ES
256/384/512 (a PEM private key), from a JSON payload and optional extra header
fields, and sends it after a prefix (`Bearer`) or as a query parameter
(`token`). Nothing is added to the payload, so set `exp` or `iat` yourself;
`{{$timestamp}}` works there. **Inherit** (the default for new requests) uses the nearest
folder or collection auth and says which one it is; see [Collections, variables
and the vault](#collections-variables-and-the-vault).

**OAuth 2.0** gets an access token with the authorization code grant with PKCE
(the default), the authorization code grant, client credentials or the
password grant, and sends it as `Authorization: Bearer …` (or the header
prefix you set). The implicit grant is not offered. The two authorization code
grants open the sign-in in your browser, which returns to a loopback address
this app listens on: `http://127.0.0.1` on a free port unless **Callback URL**
names a `127.0.0.1`, `localhost` or `[::1]` address; register it with the
provider. **Get new access token** in the Auth settings starts that; client
credentials and the password grant need no click, as the first send gets a
token. A token is reused until 30 seconds before it expires, then refreshed
with its refresh token, or fetched again for the grants without a sign-in.
Tokens live only in memory: after a restart, the authorization code grants need
a new sign-in. **Clear token** forgets one; do that after changing a secret or
password, which are not part of what identifies a token. Every field takes
`{{variables}}`; a `{{vault:name}}` there is unlocked for the access token URL's
host, the one that receives it. Token requests go through the proxy, so they
are in Capture like any other exchange (decrypted only when SSL proxying covers
the host). **Copy as cURL** includes the token this session holds.

Body supports a raw editor with line numbers, JSON highlighting, undo and
**Format**, plus URL-encoded fields and multipart text/file parts with duplicate
keys, disabled rows and a 16 MiB total form limit. The body's type and **Format**
share the Body tab's row instead of a toolbar of their own. File paths are
stored, not file contents; sending or exporting reads the selected local files.
**Scripts** edits the pre-request and post-response scripts in the same reader as
the body, with JavaScript highlighting, find and undo. **Send** runs through the
proxy and shows the response body (Pretty, Raw or Hex, with JSON highlighting),
headers, cookies, timing and test results below the editor. While nothing has
come back the request editor keeps most of the height, so a handful of header
rows and their row action fit without scrolling; the first response pulls the
divider back to about a third, unless the reader moved it themselves. Until something comes back the
response area is only its empty state and **Send history** waits, disabled, for a
first send. **Compare with source** replaces the response with the comparison and
offers **Back to response**; there is no second comparison tab. The quiet footer
under the editor says whether the draft on screen is **Matches last send · time**
or **Edited since last send · not yet sent**; the response always names the send
it came from and that the source capture is unchanged. The exchange also lands
in Capture. The inline response preview is limited to 8 MiB; Capture retains the
complete body.

**Cookies…** manages the in-memory cookie jar shared by Requests and Runner.
Set-Cookie, redirects, domain/path restrictions, expiry and Secure are respected.
Cookies can be inspected, added, deleted or cleared; they are not persisted and
raw capture Replay does not use this jar. Saved auth, environment variables and
request history may contain credentials: local files are private (0600), not
encrypted. Prefer `{{variables}}` and review exports before sharing. OpenAPI
draft generation explicitly warns that structured forms and auth need manual
schema/security definitions; it does not copy their secrets or file paths.

Send and Runner use the same execution order: pre-scripts (collection, then each
folder, then the request) → variable validation
(when Checks are enabled) → expansion, vault secrets and auth/form encoding →
send → checks and post-scripts in the same collection → folder → request order. Pre-scripts see authored templates (`pm.request.url`,
`pm.request.body` and headers may still contain `{{names}}`), not the
previously expanded request, and variables they set affect this send's URL,
headers, auth and form fields. Method/URL/body edits are retained; an explicit
body assignment, even an empty string, overrides structured forms, and header
changes override generated headers. Expansion happens once with the final
variables. cURL export encodes the draft without executing scripts.

### Postman scripts

Request scripts use Postman's `pm` API, so scripts from an imported collection
run as they are. Each runs as the body of an async function, so `await` works
at the top level, with two seconds of run time (time spent waiting on
`pm.sendRequest` does not count).

- **Variables**: `pm.environment`, `pm.globals`, `pm.collectionVariables` and
  `pm.variables` (`get`, `set`, `unset`, `has`, `clear`, `toObject`,
  `replaceIn`). `pm.variables.get` reads the effective value; `pm.variables.set`
  is local to the send, or to the rest of a Runner run. Environment, global and
  collection writes are saved when the send finishes. Values are stored as
  text: objects are JSON-encoded. `pm.iterationData` (`get`, `has`,
  `toObject`) reads the Runner data file's current row and is read-only; it is
  empty for a Send or a run without a data file.
- **Request**: `pm.request.method`, `pm.request.url` (`toString()`, `getHost()`,
  `getPath()`, `query.add/upsert/remove/get`), `pm.request.headers`
  (`add`, `upsert`, `remove`, `get`, `has`, `toObject`) and `pm.request.body.raw`.
- **Response**: `pm.response.code`, `status`, `responseTime`, `responseSize`,
  `headers`, `text()`, `json()` and the Postman assertions under
  `pm.response.to` (`have.status`, `have.header`, `have.body`, `have.jsonBody`,
  `be.ok`, `be.success`, `be.error`, `be.json` and the rest, with `not`).
  `have.jsonSchema` is not supported.
- **Tests**: `pm.test(name, fn)`, including async functions and `done`
  callbacks, `pm.test.skip`, and `pm.expect`, which is Chai 4's `expect`.
  The **Tests** tab shows why a test failed.
- **Flow**: `pm.sendRequest` (callback or `await`; sent through the proxy, so it
  is captured too), `pm.execution.skipRequest()` in a pre-request script, and
  `pm.execution.setNextRequest(name)` or `(null)` in the Runner. `pm.info`,
  `pm.cookies` (the cookie jar's cookies for the request URL),
  `console.log/info/warn/error`, `atob` and `btoa` are available.
- **Vault**: `await pm.vault.get(name)`, `set` and `unset` work only after
  **Let scripts use pm.vault** is turned on in **Vault…**. A read follows the
  secret's allowed domains for the request's host; a secret a script adds has
  none. Scripts can log what they read: Console output is not redacted.
- **Legacy**: `tests["name"] = bool`, `postman.setEnvironmentVariable` and the
  other `postman.*` variable calls, `responseBody`, `responseCode`,
  `responseHeaders`, and this app's earlier `req`, `res`, `vars`, `test` and
  `log` keep working.

`require` only provides `chai`. Postman's other sandbox libraries (lodash,
moment, crypto-js, cheerio and so on), `pm.visualizer` and timers are not
available.

**Console** in the response area shows phase-tagged `console.log(...)` output,
variable-write names (not their values), and setup/network/script errors,
including script filename and line when available. It remains available when
pre-script/setup fails before a response exists. Output is bounded to 200
messages / 4096 bytes per message; truncation is explicit. Scripts can still
log secrets: Console and retained records are not automatically redacted.

**Variables** edits environments as name/value rows, with add/remove, masked
values and explicit reveal. Empty values and whitespace are preserved; duplicate
or invalid names block saving. Only one environment is active. **Globals** in the
same dialog apply under every environment. Both are plaintext local storage;
secrets belong in the vault. **Request variables** beside the target origin expands
the draft's referenced names, environment source and unresolved state; pre-scripts
may supply missing values later. Retained runs show the referenced values after
pre-script and before expansion, distinguishing run input (environment snapshot
or a preceding Runner step) from pre-script writes. Post-script changes do not
rewrite this snapshot. Values stay masked until revealed.

If a file of the request library (below) or `env.json` cannot be read, its
error and path remain visible and writes to that asset are blocked, including
script variable saves and library imports. Repair the affected file or move it
aside as a backup, then choose **Reload local assets** to retry reading. This never deletes or
resets the original file automatically. Missing files are valid empty assets.
A proxy initialization failure shows the actual error and no sample traffic;
local request/environment assets remain accessible for offline editing.

### Collections, variables and the vault

A request's collection path (`Shop/Orders`) is a chain of levels: the collection
`Shop`, then the folder `Orders`. Right-click a request or a section in the
sidebar, or open the request's **⋯** menu, to edit **Collection “Shop”
settings…** or **Folder “Orders” settings…**. Each level has its own **Auth**,
pre-request and post-response **Scripts**, **Variables** and a **Description**.
The **Name** at the top of the same dialog renames that collection or folder:
its requests, folders and settings move together, and open tabs follow.

- **Auth**: a request whose Auth is **Inherit** sends the nearest level's auth
  that is not itself Inherit; if none has one, it sends no auth.
- **Scripts** run from the collection down to the request, for the pre-request
  and the post-response phase alike. Console lines name the level they came from.
- **Variables** resolve narrowest first: script writes, then the Runner data
  file's row, then run input (the active environment or a preceding Runner
  step), then the nearest folder up to the collection, then globals. Disabled
  rows are skipped. **Request variables** names the source of each value.
- **Dynamic variables** follow Postman's names and are generated for every
  occurrence on every send: `$guid`, `$randomUUID`, `$timestamp`,
  `$isoTimestamp`, `$randomInt`, `$randomBoolean`, `$randomAlphaNumeric`,
  `$randomHexColor`, `$randomIP`, `$randomUserName`, `$randomEmail` and
  `$randomPassword`, written as `{{$guid}}`. A variable with the same name
  wins. Other `$random…` names stay unresolved.

#### Import and export

**Import…** in the sidebar footer, **Session → Import…** and the command
palette read a Postman Collection v2.0 or v2.1 file, an environment or a
globals export, a HAR file, an OpenAPI 3.0 or 3.1 document (JSON or YAML), or
this app's own request list, telling them apart by their contents.

A **HAR** file becomes a collection named after the file: every HTTP(S) entry,
in order, converted as **Save as request** converts a captured row, up to 1000
entries. Tunnels, WebSocket connections and bodies the editor cannot hold are
skipped and listed. An **OpenAPI** document becomes a collection named after
its title, with a folder per operation's first tag; each request is the draft
**Open in Requests** makes, ordered by path, then method. Without a server it
uses `{{base_url}}`, and the collection gets an empty `base_url` variable to
fill in. **Create collection** in the API editor does the same for an open,
validated contract. A name already in use gets a number: “Pet Store 2”.

A Postman collection
becomes requests under its name, its folders become collection
paths, and its auth, scripts, variables and descriptions become the
collection and folder settings above. Requests without auth inherit it, as in
Postman. Item IDs are kept, so importing the same file again updates those
requests. Folder names containing `/` keep it as `∕`. An environment merges
into the environment of the same name; globals merge into globals.

Saved examples become the requests' examples, both ways. What has no
equivalent yet is listed after the import: request descriptions, binary file
bodies, auth other than Bearer, Basic, API key, OAuth 2.0, Digest, AWS
Signature and JWT Bearer (imported as no auth), disabled query parameters and
per-request settings. Digest keeps only the username and password: the rest
comes from the server's challenge. AWS Signature always signs in headers, and
a JWT algorithm vscroxy cannot sign with (such as EdDSA) becomes no auth. OAuth 2.0 keeps its grant, URLs, client, scope, client
authentication and header prefix; the implicit grant becomes no auth, a
callback that is not a loopback address (such as `oauth.pstmn.io`) is replaced
by the loopback default, and a token Postman adds to the query is sent in the
`Authorization` header. Postman's saved access tokens are never imported or
exported.
Raw bodies get the `Content-Type` Postman would send for their language.

Right-click a request or collection and choose **Export “Shop” as Postman…**,
or use the palette, to write the collection as Collection v2.1. Response
Checks have no Postman form and are left out. **Export as Postman…** in
**Variables** writes the environment or globals on screen. Exports contain auth
fields and variable values as they are; `{{vault:name}}` references stay
references.

**Vault…** (sidebar footer, request **⋯** menu or command palette) keeps secrets
as name, value and optional allowed domains. Reference one anywhere a variable
works with `{{vault:name}}`. It is unlocked only for a send whose target host is
allowed: `example.com` matches only that host, `*.example.com` only its
subdomains, and a secret without domains goes to any host. A refused or missing
secret fails the send before anything leaves. Vault values are never copied into
environments, Request variables or script variables. Scripts reach it only
through `pm.vault`, when allowed; the request that was sent, including the header that carried the
secret, is still retained in Capture and Run records.

The vault lives in `~/.vscroxy/vault.json`, sealed with AES-256-GCM. Its key is
kept in the operating system's credential store; where there is none (headless
Linux without a secret service), it falls back to `~/.vscroxy/vault.key` (0600)
and the dialog says so. A vault whose key is missing is never replaced:
**Reset vault…** deletes it only after confirmation.

**Runner…** executes a saved collection, including its subfolders, sequentially.
`pm.execution.setNextRequest("name")` jumps to the request with that name (or
ID) among the run's requests, so a request can repeat itself to poll;
`setNextRequest(null)`, or a name the run does not have, ends the run.
`pm.variables`, globals and collection variables a script sets are seen by the
requests after it.
**Data file…** picks a CSV with a header row or a JSON array of objects (at most
1000 rows; JSON values that are not strings become JSON text). Each iteration
uses the next row as variables, readable as `{{column}}` and through
`pm.iterationData`; choosing a file sets the iterations to its row count, and
iterations past the last row reuse it, as in Postman. A row's values are never
written to the environment. **Remove** goes back to running without one.
Select/reorder steps, set 1–1000 iterations, and optionally stop on HTTP errors,
failed assertions or execution errors. Each step has a 30-second deadline;
scripts have a 2-second limit and cancellation interrupts both scripts and I/O.
The starting environment is snapshotted; script variables and session cookies
carry into later steps. Results show status, timing and assertions (latest 1000
rows, with cumulative totals). Closing the dialog leaves the run active;
**Cancel run** stops it. Unsaved request tabs are not run or modified.
Each delivered attempt also appears in **Run records**, including failures and
in-flight cancellation; **Inspect retained run** opens its immutable snapshot
and Console. Runner results are not the deduplicated recent-request list.

Each tab owns its draft, editor selection, pending send and response. Concurrent
sends, even to the same URL, retain their own results and run the script that
was present when sent. Script variable updates go to the originating environment
without overwriting unrelated variables changed by another tab. Reopening a
saved request focuses its existing tab instead of replacing unsaved edits.
A dot marks unsaved changes; closing offers **Cancel**, **Discard** or
**Save & close**. A failed save leaves the draft open. Sending tabs cannot be
closed until the send finishes. Open drafts are restored from the private
experiment checkpoint described below; editor selection and layout are session-local.

A captured row's **Edit & replay** opens an unsent draft at once, with the
complete decoded UTF-8 payload and duplicate headers; encoding/framing headers
are removed. Missing/incomplete content, non-UTF-8, trailers and CONNECT/tunnels
block conversion with a message. Opening a draft never sends and never edits
the original capture. Source identity and bounded source evidence accompany the
saved request across restart.

**Save request** keeps the editor's request in the request library with
a stable ID: renaming or moving it updates the same asset. Unrelated same-name
assets are not silently overwritten. **Duplicate as new request** creates a new
identity. Legacy assets gain deterministic IDs in memory without a load-time
rewrite; the next explicit save persists them. Imports update matching IDs and
rename unrelated name collisions. The sidebar groups requests by collection; the
**Session** menu exports and imports them as a file. Right-click a request for
**Delete request…**, or a collection or folder for **Delete “Shop/Orders”…**,
which removes it with every request, folder and setting under it after a
confirmation. A deleted request's open tab closes, unless it holds unsaved
edits: then it stays open as a new, unsaved request.

**Save as example** beside a response keeps it with the request, as Postman's
examples are: the status, the response headers (without the encoding and
framing headers, since the body is kept decoded), the body, and the request
that got it as authored, `{{variables}}` unexpanded, so no secret is written
with it. Text bodies up to 5 MiB can be kept. Examples are part of the draft:
a saved request with no other unsaved edits is saved at once, otherwise the
next **Save** keeps them. The **Examples** tab lists them, renames and deletes
them, and shows each one's body, headers and request. They live in the
request's file and travel through Postman import and export; sends and Run
records leave them out.

#### The request library on disk

The library is a directory tree in `~/.vscroxy/collections/`, so a collection
can be kept in Git or edited by hand:

```text
collections/
  folder.json                    order of the requests outside a collection
  Shop/                          the collection “Shop”
    folder.json                  its settings and the order of its requests
    List products.request.json   one request: its ID, name and everything else
    Orders/                      the folder “Shop/Orders”
      folder.json
      Get order.request.json
```

A directory's name is the collection or folder name; a request file's name
follows the request's name, which the file holds. Characters a file system
forbids (`/ \ : * ? " < > |`), `%`, a leading dot, trailing dots and spaces
and Windows device names are written as `%XX`. Requests whose names differ only
in letter case get a numbered file name; collections and folders that differ
only in letter case are refused, because they would share a directory on macOS
and Windows. Files are indented JSON, private to you (0600). Other files and
names starting with a dot (such as `.git`) are not the library's and are left
alone.

The app reads the tree at start and with **Reload collections from disk** in
the command palette. A request file added by hand is shown after the requests
its `folder.json` lists, and gets a stable ID from its path until it is saved.
Saving writes only the files that change. When a file was changed outside the
app since it was read (by `git pull`, for example), the save is refused before
anything is written, and the file is never overwritten or deleted: reload, then
save again. Reloading shows the new contents in open tabs without unsaved
changes; a tab with unsaved changes keeps them, and saving it replaces the file.

The first start of this version moves `requests.json` and `collections.json`
into the tree and keeps them as `requests.json.migrated` and
`collections.json.migrated`. If either cannot be read, nothing is moved and
saving stays blocked until the file is repaired.

**Runs → All retained runs…** opens the retained records for choosing an individual
immutable attempt. **Send history** scopes the same view to a draft (or its source),
and **Debug history** scopes it to capture provenance. **Recent requests** beneath
these records retains the older `~/.vscroxy/history.json` meaning: sent requests
deduplicated by method + URL, not an execution audit log. Opening one creates an
unsent copy. **Session → Clear history** clears only this recent-request list.

### Regression cases

Right-click an HTTP capture and choose **Save as regression…**. This opens an
unsent Requests draft linked to that capture, with its status as the first
assertion. Review/edit the request, use `{{base_url}}` and other variables for
targets/credentials, then **Save request**. Capture conversion uses the same full
decoded request contract as **Edit & replay**, not a shell/cURL round-trip or UI
body preview. Its body has no cURL size/control-character restriction; incomplete,
missing or non-UTF-8 content, trailers and tunnels are refused explicitly.

**Checks** supports an optional exact status, exact response header values
(one `Name: Value` per line; any repeated value can match), and JSON scalar fields
using RFC 6901 pointers, for example `/items/0/id = 73` or `/ok = true`.
Use `~1` for `/` and `~0` for `~` in object keys. JSON strings must be quoted;
missing is not null, numbers are not strings, and large integer IDs are not
rounded to float64. Numeric comparisons support up to 1024 characters and
exponents from -10000 to 10000. JSON checks fail for invalid/truncated responses;
the inline response limit is 8 MiB. All checks must pass.

**Send** and **Runner…** execute these same checks and show pass/fail results.
An explicitly expected 4xx/5xx is not a Runner failure. Requests with Checks stop
at redirects to assert the original response; ordinary requests still follow
redirects. Missing required variables stop the case before sending. Rerunning is
not a dry run: POST/PUT/DELETE and other requests can change server data, and the
existing Requests/Runner session cookie jar still applies locally.

**Case → Share regression…** makes a separate, editable preview. It replaces
the origin with `{{base_url}}`, query values with `{{query_N}}` (supply URL-encoded
values), non-content-negotiation header values with placeholders and the entire
request body with `{{request_body}}`. It drops Host/wire-framing/proxy headers,
URL userinfo/fragments, scripts, local environment values, session cookie-jar
contents and responses. Explicit Cookie/Authorization headers become variables.
The name/collection become generic. Paths, parameter names, Accept/Content-Type
and assertion literals still need manual review; automatic replacement is **not**
a guarantee of anonymization. Confirm review to copy/export; changing the preview
requires review again. This affects only the shared copy, not the saved original.

**Case / Session → Import regression…** accepts a versioned `vscroxy-regression`
JSON file (up to 1 MiB), shows a review gate, then opens an **unsent** draft.
No scripts, local form files or unknown schema members/versions are accepted.
Supply your own Variables and verify the target before sending or saving.
Portable exports currently support raw HTTP text bodies, not file-backed forms,
WebSocket/SSE conversations, diagnostic archives or CLI/CI execution. Ordinary
**Export requests**, HAR/session exports and cURL copies are **not** sanitized by
this feature and may still contain credentials. Case files are written privately
(0600 on Unix), atomically, but are not encrypted.

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

**Generate code…** (request **⋯** menu or command palette) shows the draft as
cURL, JavaScript `fetch`, Node.js axios, Python requests, Go `net/http` or
HTTPie, with its auth and encoded body, and copies it. Like cURL, it leaves
`{{variables}}` as written and runs no script. Each language is written to send
the same method, URL, headers and body bytes; libraries still add their own
defaults (`User-Agent`, `Accept`, `Accept-Encoding`), and `fetch` and requests
join repeated headers into one. What a language cannot send exactly is refused
with a reason instead: binary bodies (use cURL), `fetch` with a body on GET or
a `Host` other than the URL's, and HTTPie with values it would alter.

**Copy cURL** beside **Send** copies the active draft; **Copy as cURL** in a
captured row's menu copies that exchange. Variables stay unexpanded and scripts
are not run. Commands target Bash/POSIX shells, not PowerShell or CMD. GET bodies
keep an explicit GET method; HEAD uses curl's head mode. Quotes, duplicate/empty
headers and an explicit Host are preserved; wire framing and connection-only
headers are removed. Captured decoded bodies omit stale content-encoding/digest
headers, while drafts retain their authored encodings. Curl's default Accept,
User-Agent and Content-Type are suppressed when absent, and a captured
Accept-Encoding also enables response decompression. TLS verification stays on;
the command ignores `.curlrc` but still uses curl's normal proxy environment.

Captured spill files are read in full. Small text bodies use `--data-raw` (a
leading `@` stays literal). Binary/NUL bodies and text whose shell-quoted form
exceeds 32 KiB use a base64 here-document piped into `curl --data-binary @-`;
paste the **whole** multiline command into a shell with `base64 -d`, or save it
as a script and run `sh script.sh`. This avoids OS argument-length limits and
does not reference capture-machine temporary files. This generated pipeline
cannot be re-imported by the intentionally restricted cURL importer.

Missing/incomplete bodies, request trailers, CONNECT/WebSocket rows, oversized
header/URL arguments, HEAD with a body, and commands over 16 MiB produce an
error without replacing the clipboard. Multipart drafts read their selected
files and export the assembled bytes, including binary parts. Credentials and
cookies are included: review before sharing. Expired credentials, body-sensitive
signatures and application-specific client certificates may still require manual
changes; copying a request cannot guarantee a remote server will authorize it.

The exporter remains local rather than using `moul/http2curl`: that library
inlines bodies with `-d`, cannot represent NUL/large arguments, joins repeated
headers, and unconditionally disables HTTPS verification. Replay tests execute
the copied command with real curl against a local server, including a compressed
upload captured through the actual proxy, rather than relying on import/export
round trips alone.

## APIs / OpenAPI

Contracts live under **APIs** in the Requests resource rail, independently of
the collection they were generated from. The sidebar offers **New contract**,
OpenAPI import and **From collection…**. Switching back to **Collections**
returns to the request editor without discarding its drafts.

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
opens **Requests** on its contracts; Cmd/Ctrl+4 opens the investigations.

## Timing

The **Timing** tab splits a flow's time into the wait for the server's first
response byte and the download after it, as a bar and as numbers. A flow that
never reached a server has no timing; the total in the table includes what the
throttle adds, so it matches what the client saw.

## System proxy and certificate

**Settings → Proxy** controls the system proxy; **Settings → HTTPS**
shows the local CA, system trust and domain decryption rules. The native menu also opens
Settings with Cmd/Ctrl+,; Cmd/Ctrl+1 and Cmd/Ctrl+2 switch workspaces, and
Cmd/Ctrl+N starts a request. On Linux and Windows, Alt reveals the native
menu bar.

- **Enable system proxy** sets the proxy to the actual address displayed above;
  it first saves the previous settings and starts a separate recovery process.
  **Disable system proxy** restores this session's saved settings; without an
  owned session it explicitly turns the system proxy off. Startup never enables
  a proxy automatically, but retries an interrupted, recorded recovery.
  Linux writes the GNOME proxy settings with
  `gsettings`, macOS sets every enabled service with `networksetup`, Windows
  reads/writes the current user's default LAN settings through WinINet and
  notifies applications of changes. macOS/Windows retain PAC/discovery and bypass
  settings; automatic configuration is reported as a conflict. Linux temporarily
  selects manual mode and loopback bypasses, then restores the previous mode and
  bypass list. Windows services using WinHTTP need separate configuration.
- Normal quit (including closing the window) waits for a pending proxy write,
  then restores the settings this instance replaced before closing capture.
  Each protocol is checked first: replacement proxies installed by another app
  are left alone, including a mixed HTTP/HTTPS configuration.
  If cleanup fails, quit is cancelled and capture stays running while you repair
  the settings and retry.
- The recovery process owns both the enable operation and rollback. Its pipe
  closes when the main process dies (including SIGKILL), so it restores without
  waiting for the app to reopen, even if the owner died during enable. It ignores
  a broken reply pipe rather than exiting before rollback. A file lock prevents
  another running instance from restoring or replacing the active session.
  The private `~/.vscroxy/proxy-recovery.json` journal is saved before OS writes
  and removed only after restoration is checked; failed recovery retains it.
  Errors go to `proxy-recovery.json.log`, with several automatic retry attempts.
  Startup and **Check again** retry an orphaned record, never an unowned proxy.
- This is a same-user helper, not an installed privileged service or login item.
  It cannot guarantee immediate recovery if the helper is also killed, the whole
  process tree is terminated, or OS permissions deny restoration. In those cases
  reopen the app to retry; if necessary remove only its stale loopback proxy in
  OS network settings. macOS refuses automatic takeover of authenticated proxies:
  `networksetup` cannot safely read back and restore their credentials. Proxy
  passwords are not requested or written to the journal.
- **System proxy** reads back the OS configuration on opening the Proxy page
  and after an operation; **Check again** refreshes it. The page keeps the
  HTTP/HTTPS targets, working state, read/write errors, mismatches and automatic
  proxy configuration conflicts visible instead of treating a successful helper
  exit as proof. Controls are disabled while an operation is pending. This checks
  OS settings, not whether every application honors them; clients with their own
  proxy settings, bypass lists, or separate trust stores still need configuration.
- **Install & Trust…** trusts `~/.mitmproxy/mitmproxy-ca-cert.pem`. Linux
  detects `update-ca-certificates` (Debian/Ubuntu) or `update-ca-trust`
  (Fedora/Arch), installs the public CA in the matching anchor directory,
  and refreshes trust through one `pkexec` authorization. Missing helpers
  or unsupported distributions get a manual-import explanation. macOS checks
  the exact public certificate in the System keychain first, skipping import
  if it is already there. Otherwise it elevates only `security add-certificates`, using a
  temporary copy of the validated public certificate, never the private key.
  After confirming the import, the normal, non-elevated GUI app requests
  trust through the native Security framework. Trust is never configured
  from the elevated shell. Approve the macOS authorization prompts (import
  and trust may prompt separately); the entire app does not need `sudo`.
  Windows runs `certutil -user -addstore Root` to trust the CA for the current
  user, without running the capture app as administrator. Other Windows
  accounts and services need their own trust setup. All platforms validate
  the root and import a temporary public-only snapshot, never a private key.
- **Show certificate** opens the folder that holds the CA, for clients that
  want the file itself.
- **Check again** re-reads the certificate and checks the current system trust
  store without changing it. The page also shows the CA name, validity dates
  and SHA-256 fingerprint. macOS uses `security verify-cert`, Windows uses the
  native system verifier, and Linux requires `openssl` for a fresh check.
- **Manage domains…** opens the SSL proxying editor. Trusting the CA and
  enabling domain decryption are separate steps; neither enables the other.

Each helper runs off the UI thread because the platform may prompt for a
password. Installation is followed by a new trust check; failures remain
visible on the HTTPS page, including after **Check again** until trust is
verified or installation is retried. Cancelling authorization is shown
separately from an installation failure; an already trusted CA cannot be
installed again from this page. macOS installation requires a graphical
login session; unsigned or invalidly signed builds may be denied before
an authorization prompt appears. If automatic installation fails on macOS, use
**Show certificate** to locate the public CA, import it in Keychain Access,
and set **Trust → When using this certificate → Always Trust**, then check
again. Never import or share `mitmproxy-ca.pem`, which contains the private key.
A missing helper or insufficient permission is reported rather than treated
as proof that a certificate is uninstalled.
System trust only describes this machine: remote devices, separate application
trust stores and certificate pinning may require client-side configuration.

An undecrypted **CONNECT** detail explains that it is an encrypted tunnel, not
one HTTPS request. HTTPS encrypts request/response headers (including Cookie and
Set-Cookie) as well as bodies; trusting the CA alone does not expose them.
The detail reads in three layers and scrolls as one at a short window.
**What happened** is the title of the recorded failure stage — client TLS
handshake, upstream TLS handshake, tunnel setup, DNS, TCP or HTTP — with one line
saying what that stage means and what it does not prove, then the raw error,
copyable. **Next steps** lead with **View connection record** when a failure was
recorded: the evidence is read before anything is reconfigured, and enabling
inspection for the host stays a secondary configuration action. A tunnel that
did not fail still leads with enabling inspection for its host. **Known configuration** lists the
domain rule, the app rule and this system's trust as secondary lines, with the
reminder that today's rules never reveal a tunnel recorded earlier. Widening
inspection to everything an executable opens is a quiet link below, not a fix
for the failure on screen. After changing HTTPS setup, establish a new
connection and retry the request; existing tunnels and their CONNECT records stay
encrypted.

## Client attribution

The **Client** column names the app a connection came from, with its icon.
The icon comes from the app itself: the `.desktop` entry's icon on Linux, an
icon theme's for the app's name, or the logo a bundled app keeps beside its
executable, as Chrome does; on macOS, the PNG inside the `.icns` of the `.app`
bundle that encloses the process, so a helper process such as Chrome's shows
Chrome's icon. An app with none gets a badge in its color with its initial, so
every row still reads at a glance.
When a connection opens, the proxy finds the socket that the client holds on
the other end and reads the process that owns it: `/proc/net/tcp` and the
descriptor tables on Linux, `lsof` on macOS, `netstat` and `tasklist` on
Windows (with the executable path read through the Windows process API).
Both client and proxy socket endpoints must match a unique local owner.
A request from another machine has no local process, so the column
stays empty.

The **Apps** section of the sidebar filters by app. Windows shows a badge
only: an app's icon lives in its executable's resources, which are not read
yet.

## Settings

What you set stays between launches: the dark theme, the sidebar's width, how
the table and the detail pane share the window, the inspector layout and sidebar
visibility, and the columns' order and widths, in `~/.vscroxy/settings.json`.
The window's position and size are
MyGo's own, kept in `window-state.json` under the app's config directory and
restored when they would still show on a connected display.

### Desktop workbench

The default is **Bottom**: requests above, inspector below, even on wide windows.
Choose **⋯ → View → Inspector: Right (wide windows)** to use a side inspector from
1000 DIPs of content width, or **Adaptive** to move it right from 1160 DIPs.
Even Right yields at small widths; both orientations keep
their own divider positions. In the side layout, the table prioritizes URL,
method, status and duration, and header blocks stack rather than squeezing values
into two narrow columns. Client and size remain visible in the inspector.

**Focus** (Cmd/Ctrl+Shift+Enter) temporarily gives the inspector the full content
area. **Back to list** restores the layout and inspected request. Cmd/Ctrl+B
toggles the sidebar. **View → Reset desktop layout** restores the defaults.
Selecting multiple requests reveals **Replay**, **Export HAR** and **Clear
selection** directly above the table; replay settings snapshot the entire choice.

**Commands** (Cmd/Ctrl+K) searches capture actions, layouts, setup and settings.
Use arrows and Enter to choose, Escape to dismiss and restore the previous focus.
With an empty search the list holds only what can run now; a search also lists
unavailable matches last, each explaining why. Commands show their shortcut and
current target.
Cmd/Ctrl+F searches URLs, Cmd/Ctrl+R opens replay settings for the selection,
and Cmd/Ctrl+Alt+C copies the inspected request (or current draft) as cURL.
Toolbar buttons, row menus, native menus, palette and shortcuts share request
availability and target rules. A multi-selection replays the whole choice;
single-request edit/cURL actions are disabled rather than choosing an arbitrary
row. A choice containing tunnels or live streams cannot be replayed as HTTP.

### Keyboard debugging

`Cmd/Ctrl` means Command on macOS, Control on Windows/Linux. `Ctrl+Tab` uses
Control on every platform. These are vscroxy's bindings, not a claim that
Proxyman or Postman use identical Windows/Linux defaults.

| Action | Shortcut and scope |
| --- | --- |
| Search traffic | Cmd/Ctrl+F in Capture; Enter or ↓ moves to the list |
| Navigate / inspect / return | ↑/↓ in the list; Enter focuses the inspector; Esc returns to the list |
| Focus primary input | Cmd/Ctrl+L: request list in Capture, URL in Requests |
| Edit captured request | Cmd/Ctrl+Alt+Enter; opens an unsent draft and focuses its URL |
| Replay options | Cmd/Ctrl+R in Capture; never sends without the replay dialog's Send action |
| Send current draft | Cmd/Ctrl+Enter in Requests; unavailable while sending or capture is stopped |
| Save current draft | Cmd/Ctrl+S |
| Compare exact last send with source | Cmd/Ctrl+Shift+D after sending a draft opened from Capture |
| Back to original traffic view | Cmd/Ctrl+[; preserves the draft and original capture conditions |
| Cycle request tabs | Ctrl+Tab / Ctrl+Shift+Tab |
| Close current request tab | Cmd/Ctrl+W; asks about unsaved changes; unavailable while sending |
| Close window | Cmd/Ctrl+Shift+W (separate from closing a request) |

Plain Enter in the URL input never sends. Dialogs own their shortcuts: sending,
opening drafts and workspace commands cannot act through a modal. Local response
editors keep their own Find commands. Cmd/Ctrl+1–4 still select workspaces and
their sections, not individual drafts.

The design takes **Edit and Repeat** from [Proxyman](https://docs.proxyman.com/advanced-features/edit-and-repeat)
and the explicit **Send** chord documented by [Postman](https://blog.postman.com/boost-your-productivity-in-postman-with-these-4-keyboard-shortcuts/).
Unlike [Proxyman's Repeat](https://docs.proxyman.com/advanced-features/repeat),
Cmd/Ctrl+Enter in Capture does not immediately replay traffic. Cmd/Ctrl+K retains
our existing command palette; it is not Postman's resource search. Some official
references are macOS-only or historical, so we deliberately define one consistent
vscroxy mapping instead of silently inheriting conflicting shortcuts.

### Continuous debugging

**Edit & replay** opens a named request tab in **Requests**. The document tabs
stay in the content pane, so a draft and its response remain one click away
while traffic continues arriving. Unsaved tabs still require save or
discard before closing. Open request drafts, their active tab and saved/unsaved
baseline are checkpointed privately for restart; they are not automatically
added to the saved request library and never send on restore.

**Back to traffic** returns to the draft's original capture view without clearing
search, protocol, header/body or triage conditions, changing its chosen request,
or resuming a paused list. **Capture → All requests** returns to the currently
active live view.
If the original view was closed, Back to traffic returns to the current view
and explains the fallback instead of recreating it.
Header, text-body, JSON and hex reading positions survive leaving Capture;
choosing a different request resets those readers.

After sending, **Compare with source** uses the captured source and this
document's last send, not the next matching URL or the current unsent edits. Each
document keeps its own comparison part, side order, split/unified layout and
reader state. Comparison fills the work area and **Back to response** returns to
the response tab that was open; a subsequent send leaves the comparison and
shows the new response.

The last-send side represents the initial client request and final HTTP response.
Request headers include client-side processing, but proxy rules can still change
the forwarded exchange; followed redirects can return a response from another
URL. Capture remains the source for individual wire exchanges. Previews are
bounded to 8 MiB and explicitly mark incomplete text. Sending is a real network
operation and is not undone by returning to Capture or closing a tab.

### Run records (experiment snapshots)

**Run records…** in Requests or Commands opens the same retained experiment store
as the sidebar's **Runs**. It retains independent snapshots of Send and Runner
attempts, including failures: authored draft, environment name, referenced-variable
snapshot, Console, prepared request/final response, checks, summary and notes.
Only referenced variables are retained, not the entire environment. Repeated
method/URL attempts remain separate. Choose
a baseline or **Use as left**, then compare any other attempt using the existing
body/header diff. **Ignore JSON keys** removes comma-separated top-level response
keys only in the comparison; invalid or incomplete JSON is never normalized.
Original snapshots stay unchanged. **Open as draft** restores the original
editable request and recorded result without sending or running scripts.

`~/.vscroxy/experiments.json` is an atomic, private (0600), unencrypted checkpoint.
It may contain credentials and bodies. It keeps at most 100 attempts / 32 MiB,
evicting oldest non-baseline attempts, and at most 256 KiB per request/response
payload with explicit truncation/missing-body warnings. Binary payloads survive
restart losslessly and never depend on temporary capture spill files. Open drafts
retain their full authored content within the same 32 MiB budget; an oversized
checkpoint reports an error rather than silently discarding draft data. Corrupt
or unsupported archives are left untouched and reported read-only. Notes and
baseline persist; comparison layout and ignored-key choices are session-local.
**Clear experiments…** asks before removing records; it leaves open drafts,
saved requests and captured traffic alone. This is a debugging notebook, not a
complete wire archive: redirect hops remain in Capture and cookies/variables may
have changed since a run.

### Request debug history

**Debug history** on a captured exchange or its editable draft opens the existing
workbench scoped to that capture's linked open drafts and retained experiments.
Links use stable identities, not method/URL or reusable capture IDs. The immutable
source snapshot, draft identity and run links survive restart; reopening the
original saved session restores **Inspect source capture**. If the source is no
longer in Capture, that button is disabled and the retained snapshot remains
available. **Origin draft** returns to the exact originating tab without replacing
unsent edits; **Open as draft** creates a new editable branch without sending.

**Compare source** compares the original capture snapshot against the experiment's
client request/final response. **Recorded interventions** shows actual captured
scenario/rule actions for the selected wire exchange, never inferred from today's
configuration. Each HTTP(S) Send or Runner attempt has a local correlation identity, including
across redirect hops; it is not sent as an HTTP header. Up to 16 separate wire
exchanges are retained per run. Legacy runs and missing wire evidence are labeled
explicitly and do not imply that no rule ran. Interactive WebSocket sends are not
linked into this HTTP(S) history.

Source/wire snapshots share the experiment checkpoint's 100-run / 32 MiB budget
and 256 KiB-per-body limit, with explicit incomplete-body diagnostics. They may
contain credentials, private URLs and rule details; this is local, unencrypted
debugging history, not sanitized evidence for sharing.

### Investigations

The **Investigations** section of the Capture sidebar (Ctrl/Cmd+4) is where
evidence outlives the traffic list. Each investigation has a name, one line of
notes that expands when clicked, and a list of exchanges. **Add to investigation…**
in a capture's inspector or row menu snapshots that exchange — bodies, headers,
connection evidence and notes as they are now — into the investigation on
screen, starting one when the board is empty. Later live traffic and edits never
change a snapshot; the same flow can be inspected and added again as a separate
exchange. Investigations live in `~/.vscroxy/investigations.json` (private,
0600), up to 20 investigations of 64 exchanges and 32 MiB; adding past a limit
reports it instead of dropping evidence.

Reading one exchange shows the request and the response beside each other when
the pane is wide enough; below that a **Request / Response** switch shows one at
a time, with the same Headers and Body reading as Capture.
Credentials are masked until **Reveal credentials**; the export review shows the
raw bytes, not the masked reading. A failed exchange layers its evidence:
the recorded stage and raw error first, then **Possible causes · not confirmed**
and **What to try** — never a single inferred diagnosis. A successful replay is
one exchange in the list, not proof that the original problem is gone.

Choose two exchanges and **Compare two** to read their headers or bodies side by
side. **Export evidence…** turns the chosen exchanges into one bundle (one source
per exchange, so labels stay attached) and opens the review below; nothing is
written before that confirmation. **⋯ → Delete investigation…** asks first.

### Historical sessions

**Historical sessions…** in the Capture sidebar's Investigations section, or
**Commands → Historical sessions & evidence…**, opens historical session or evidence JSON files without replacing
Capture, changing rules, running scripts or sending requests. Choose sources in the two panes, select exchanges across
files and **Compare selected two** using the existing header/body comparison.
Identical request IDs in different files remain independent. Search is a literal,
case-insensitive match on URL, numeric status, exchange/source notes and errors;
it does not scan headers, bodies or messages. Selections survive filtering and
source changes, including hidden matches; **Clear selection** removes them.

Add an evidence title and notes, then **Review selected evidence…**. The editable
JSON contains only the selected exchanges and their associated messages, with
source labels, binary payloads, trailers and incomplete-capture diagnostics.
**This is raw evidence, not automatically sanitized:** credentials, cookies,
URL secrets, bodies, base64 payloads and private notes may remain. Review all
fields and manually remove secrets before sharing. Export requires explicit
confirmation; any subsequent edit clears that confirmation. The atomic, private
(0600) export preserves exactly the reviewed JSON and can be reopened here.

Opening files is bounded to 8 sources / 48 MiB of canonical JSON in memory,
16 MiB per file, 2,000 exchanges and 10,000 messages per source. Evidence export
is limited to 64 exchanges / 4 MiB. Oversized inputs/selections report an error
rather than silently dropping evidence. Files are read off the UI thread and
never autosaved or reopened on launch; **Forget opened files** releases the
reader's sources without deleting the originals. Imported evidence remains
historical data, not verified current network or certificate state.

## Next

- Structured query/auth/form-data editors, cookie management, collection folders
  and a runner with per-request assertions.
- Named capture filters and independent traffic views; complete-body search
  rather than searching only the in-memory capture head.
- WebSocket/SSE message search and detail views, correct upgraded request paths,
  and spill-backed message retention instead of silent truncation.
