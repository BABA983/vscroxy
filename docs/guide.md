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

For a stable port, open **Settings → Proxy → Port**, enter a number
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

## Setup guide

On the first launch (no `~/.vscroxy` yet) the traffic list shows a four-step
setup guide: start capturing, trust the certificate, choose what to decrypt,
and the first decrypted request, which the guide selects as it closes. Each
step completes on its own as the app sees it done. **Skip guide** closes it;
**Show setup guide** in the command palette opens it again.

## Connect a phone or another device

For a local process, **Network → Set up a local client…** provides copyable cURL,
Node.js (`https-proxy-agent`), Python (`requests`) and Go examples. They set the
proxy and trust the public CA for that process only; they do not change the OS
proxy, install a system CA or skip TLS verification. cURL uses POSIX shell syntax.
Run the example yourself, then **Diagnose…** opens Connection diagnostics for
one process to show whether the request arrived and was decrypted.

Open **Network → Connect a device** or **Settings → Devices → Connect a device**.
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
   SHA-256 fingerprint in **Diagnostics** (Phone or tablet); obtain it over a
   trusted network.
5. Follow the guide's **iOS** or **Android** tab. On iOS, install the downloaded
   profile and explicitly enable full trust under General → About → Certificate
   Trust Settings. On Android, install it as a CA certificate; many Android 7+
   apps need a debug Network Security Configuration that trusts user CAs.
6. Enable **Configure HTTPS inspection** for the desired hosts, then make an
   HTTPS request from the device and inspect its decrypted path/body in Capture.

**Diagnose…** opens Connection diagnostics for a phone or tablet: the last
admitted clients at each port, the CA fingerprint, the host's decryption rule and
the target's traffic. Evidence is session-wide, not attributed to a specific
device, and client CA trust remains explicitly unverified. A TCP connection does
**not** prove that a certificate was installed,
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
- `sessions.go` / `sessions_ui.go`: historical session tabs, immutable payloads,
  annotations, cross-session comparison and deletion.
- `export.go`: shared Postman, HAR and session export redaction options.
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
the proxy address, the two layout buttons (**Side by side** and **Stacked**),
**Commands** and **Settings**, with the rest in the **⋯** menu. It is a native
window, not a frameless one, so resizing, full screen and the system window
menu still work.

There is one sidebar, and what it has chosen is what the content area shows.
It has three groups:

- **Live**: all requests, errors, slow and pinned, then apps, domains, and one
  **Views** area holding opened views and saved filters, whose create and
  manage actions are two compact buttons under the list. Choosing one shows the
  traffic table with its inspector.
- **Collections**: a search field, **New request**, the **⋯** collection menu,
  the collection tree, and the API contracts. Choosing a request opens it in
  the request editor; choosing a contract opens it.
- **Sessions**: saved historical sessions and **Open session…**.

The collection **⋯** menu and Commands open what has no place of its own in
the sidebar: **Environments…**, **History**, **Runner…**, **New API
contract**, **Import OpenAPI…**, **Generate API from collection…**, imports,
**Vault…** and **Cookies…**. The active environment is chosen in the request
editor's header. The inspector shows the request and the response **beside each
other**, each with its own Headers / Params / Body / Raw (request) or
Headers / Body / Raw / Timing (response) tabs, so a body comparison never needs
a tab switch. Each pane has a separate heading and format menu above its tabs.
Its **⋯** menu holds the exchange-wide views — Diff, Connection
and, for WebSocket and SSE, Messages — and the layout choices. Request and
response headers scroll independently. Collections form a nested tree;
search reveals matching ancestors without changing saved expansion choices.
A saved request has a **History / Examples** column beside its editor, from
900 DIPs of content width. **History** lists the retained runs sent from that
request, newest first, matched by the request itself and never by URL; choosing
one shows its result read-only where the response goes, with **Open as draft**,
until **×** or the next Send brings back the request's own response.
**Examples** lists the saved examples and opens one in the Examples tab.
**History** opens retained attempts across the whole window, without
sending them again; **Close** returns. One request shows at a time, under a
breadcrumb header above the editor and inline response viewer. Moving between sidebar groups
preserves the capture filters, all open drafts and responses, and the session tabs. The response's **Last attempt** disclosure shows the
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
request's when the response has none), a request or response header only,
a URL query parameter, or a **Request JSON / Response JSON** scalar field.
JSON fields use case-sensitive RFC 6901 pointers such as `/data/user/id` or
`/items/0/name`; `~1` escapes `/` and `~0` escapes `~`. Query parameter names
are also case-sensitive; only header names ignore case.

Added columns sort lexically like the others, a header cell's menu filters
by its value, and the header menu removes columns or toggles built-ins,
including **Remote IP**. Choices, ordering and widths stay in `settings.json`.
At most 16 custom columns can be added. JSON columns read complete retained
bodies off-thread (including spill files), never a partial preview. Limits:
1 MiB input, depth 64, 16384 nodes and 4 KiB scalar cells. Missing fields show
`—`; malformed, incomplete, unavailable or oversized evidence shows
**Unavailable** with an explanation on hover. Objects/arrays are not scalar
cells; use the body inspector for them. Reading shows `…`, then the active
sort updates without changing the selected capture or its original bytes.

**GraphQL awareness**

**Add column… → GraphQL operation** displays the selected operation's type
and case-sensitive name, such as `query Search` or `mutation Save`.
Anonymous operations show `(anonymous)`; batches keep request order.
The column reads complete retained request bodies in the background and
sorts lexically. Display text is capped at 4 KiB; exact matching uses the
complete names. Missing or persisted-query-only evidence shows `—`;
invalid, ambiguous or unavailable evidence shows **Unavailable**, never
the names of unselected operations or a guessed name from `operationName`.

**Filters… → GraphQL operation** or `operation:Search` in search selects
an exact name, even without the column. Saved filters keep it. In
**Tools → Rules**, **GraphQL operation** adds the same condition to URL,
method, App and headers with AND. A batch matches when a verified selected
member has the exact name; a malformed member invalidates the whole batch.
The rule test accepts a **Test GraphQL body**; POST defaults to a JSON
envelope, or set `; Content-Type: application/graphql` for a raw query.
GET test requests use their URL parameters only. Live rules also decode
supported request content-encodings, bounded to 1 MiB; streamed/unavailable
or oversized request bodies cannot match an operation condition.

Supported evidence is GET `query` / `operationName`, POST JSON envelopes
(`application/json` or `application/*+json`), JSON batches and raw
`application/graphql`. Multiple operations require an exact selector;
duplicate names and non-lone anonymous operations are invalid. Limits:
1 MiB input, depth 64, 16384 JSON tokens and 16384 GraphQL tokens shared
across at most 32 batch entries. This is syntax and operation-identity
inspection, not schema validation, fragment resolution or GraphQL execution.
No schema fetch, request send or capture mutation occurs during inspection.

The protocol tabs beside the search keep one kind of traffic in view: HTTP or
HTTPS by scheme, WebSocket and SSE by kind, and JSON, images or HTML by the
response's content type (the last two live in the toolbar's **⋯**). They narrow
what the sidebar's filters and the search show, and the footer counts the
captured and shown rows. Feedback appears in full in temporary toasts rather
than staying in the footer indefinitely. The proxy indicator in the title bar
opens Proxy settings.

The search field matches **host and path (including query) only**, not the
scheme, headers or bodies; Ctrl/Cmd+F focuses it. Its mode selector defaults to
**Text**: case-insensitive literal contains, with the existing `key:value`
chips. **Wildcard** matches the whole host + path: `*` is any text (including
slashes), `?` is one Unicode character, and all other characters are literal.
For example, `*.example.com/projects*` matches that path on subdomains;
`projects` alone is not a whole-host/path wildcard. **Regex** searches anywhere
using Go/RE2 syntax, for example `/(projects|login)$`; it defaults to ignoring
case, with `(?-i)` available for case-sensitive expressions. Backreferences and
lookaround are unsupported. Both pattern modes treat the entire field as one
expression: spaces, quotes and `status:` are not chip syntax. A malformed or
over-4096-byte pattern shows an error and no results, never a fallback literal
search. Empty patterns impose no search restriction. Modes and expressions stay
with each independent view and saved filter; **Reset all** restores Text.
Editing an existing content chip in pattern mode opens Filters without
overwriting the expression. Compact windows scroll the protocol tabs to keep
search and chip controls usable.

Use **Filters…** for headers, bodies and status codes. Status accepts comma-separated
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
or `>` with a non-negative integer. **Match populated fields** defaults to
**All (AND)**; choose **Any (OR)** to match any non-empty content field.
Host, App, method, status, operation, header, body, duration, size, color and
note are independent alternatives in OR mode; empty fields are not alternatives.
Methods and status alternatives within one field always combine with OR.
This is one flat group, not a nested Boolean expression language. Search text,
sidebar, protocol/content-type and the time brush always narrow the group's
results. A filter with no populated fields shows everything in that scope.
The combination stays with each view and saved filter. **Content: OR** makes
the non-default mode visible; removing it returns to AND without clearing fields.
A removable token strip shows every active
condition (URL, source, protocol and content filters); **Reset all** clears them.
It appears only while conditions are active, keeping the normal toolbar to two rows.

Body conditions search both request and response independently, including whole
spill files beyond the 8 MiB preview. Searches are case-insensitive, chunked,
cancellable and off the UI thread (at most four readers); pending rows remain
hidden until matched. Progress and unreadable/incomplete-body warnings appear
with the views in the sidebar. Missing or incomplete bodies never silently match just their
preview. In OR mode a proven metadata match displays immediately without needing
a body read; other rows wait for full-body evidence. URL/header/status filtering
remains immediate.

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

**Create collection…** opens a six-step draft from selected captures, or from
all captures under a domain/App through its sidebar context menu:
**Scope → Endpoints → Variables → Structure → Examples → Done**. Back keeps
draft edits; Cancel/Escape discards them. Nothing is written before Create,
and creating never sends requests.

Scope excludes static resources, tunnels and network failures by default;
HTTP 4xx/5xx responses remain available. Endpoints groups identical methods
and origins using whole numeric/UUID path segments as parameters. Other slugs
stay literal. Edit `:name` templates, Split or Merge to correct the inference.
Variables creates host/path variables in a new or existing environment and
extracts Authorization, Cookie and API-key headers. The default storage is a
local **Current value**, not an exported shared value; **Vault** encrypts and
restricts the value to its captured hosts. **Leave in requests** explicitly
keeps the original header in library files. Identical Authorization across all
included requests is inherited from collection auth; other credentials remain
header references. Query/body secrets still need review before sharing.

Structure defaults to Path prefix; Host and Flat are also available. Rename
requests, edit their folders, drag them onto folders or before another request,
or use Move up/down. Examples initially selects the latest successful response
and one response per HTTP error status. Incomplete/binary/oversized responses
must be deselected. Done optionally opens an independent, unsaved **OpenAPI
draft** for review. Existing variable/Vault names with different values are
never silently replaced. If a collection write fails after dependency writes,
the wizard reports the retained environment/credentials and supports retry.

When a capture matches a saved request, its header shows **Matches: …**.
The popup offers **Open request** and **Save response as example**, without
sending. Matching uses method, scheme, host/port and complete path segments,
ignores query, and resolves the current environment. Ambiguous matches are all
listed. Saving keeps the saved request template, not expanded capture secrets,
and does not save unrelated edits in an open request tab.

Editing a capture and **Save as request** use the complete retained, decoded
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

Rules are a page of the **Tools** window: open it with **⋯ → Debugging
rules…**, **Settings → General → Manage rules…**, a request's **Tools** menu
or the Breakpoints and Scenario chips. The list shows the rules in the order
they are tried, each with its switch, type icon, name, method and match, its
scenario and how many requests it changed since launch (changing what it
matches or does starts the count again).
Drag a row, or use **⋯ → Move up/Move down**, to change the order; **⋯ →
Delete rule…** deletes it. The chosen rule is edited beside the list. **Save
rule** (Ctrl/Cmd+S) applies it to new requests only, without restarting
capture. A failed save leaves the running rules unchanged. Closing Tools
discards unsaved field edits.

**Test a request** above the list takes a URL, optionally after a method, then
an app after `@` and headers after `;`
(`POST https://api.example.com/v1/orders @ Google Chrome; X-Env: staging`; GET
and https otherwise). The rules that
would act on it are numbered in the order they act: the first matching request
breakpoint (after every matching Script rule), then the Map or Block rule that wins, then the first matching
response breakpoint for a response from upstream, matched against the URL a
Map Remote rule sent it to. The other rules fade; hovering one says why it does
not act (disabled, inactive scenario, shadowed by a rule above, URL or method
mismatch, breakpoints off). Clearing the field brings the list back. Testing
uses saved rules and the current Breakpoints switch, sends nothing and does not
read response files or run scripts.

Rules live in `~/.vscroxy/rules.json`. Enabled Map Local, Map Remote and Block
rules apply in order, first match wins. Breakpoints match independently at their
request/response phase and also require the toolbar's **Break** switch. A rule
applies when every condition it sets holds:

- **URL** (`match`, read as `matchMode` says): **Contains** (the default) is a
  literal part of the full URL. **Wildcard** (`"wildcard"`) is the whole URL,
  `*` for any text and `?` for one character; the scheme may be left out, so
  `*.example.com/v1/users/*` matches `https://api.example.com/v1/users/42`.
  **Regex** (`"regex"`) is a Go regular expression found anywhere in the URL;
  anchor it with `^` and `$`.
- **Method** (`method`): the exact method.
- **Apps** (`apps`): the client's executable name, as in the ignore lists:
  `Google Chrome*` matches every Chrome helper.
- **Headers** (`when`): request headers the rule needs, each `present`, or with
  a value that `equals`, `contains` or `matches` (a regular expression). With
  several values, one is enough; names ignore case.

A response breakpoint looks at the request as it was sent upstream, after a
Map Remote rule changed its URL. An invalid regular
expression is refused when the rule is saved. `disabled: true` turns a rule
off.

**Breakpoint templates** keep a message you edit often. At a pause, edit it and
choose **Save as template…**; at a later pause of the same phase, pick it from
the template menu next to it to fill the dialog, then continue as usual. A
request template replaces the method, headers and body; a response template
the status, headers and body. Neither changes the paused URL. Saving under an
existing name replaces that template, and the menu's **Delete** removes one.
They are kept in `~/.vscroxy/breakpoint-templates.json`. A body the dialog
cannot edit as text is kept as it is.

**Scenario** groups Map Local, Map Remote and Block rules into named cases such
as an empty account, server error or alternate upstream. Save a rule with a
scenario name, then choose it in the scenario menu above the rule list. At most
one named scenario is active. **No scenario** leaves only global
(blank-scenario) rules eligible. Global rules keep their place
in first-match order, including ahead of scenario rules; switching never changes
individual enabled switches. Breakpoints remain global and independent.

Selection lives in the same `rules.json` snapshot (`scenario` and matching
`scenarioActive` fields on every member), not a second settings store. Mixed
selection states and multiple active groups are rejected on load/save/apply.
Changes affect new requests, never an in-flight response. Captured **Applied**
evidence includes the scenario that actually won, so later switches do not
rewrite its provenance. Map Local, Map Remote, Block and Script rules can belong
to a scenario. This is not a delay/fault-injection engine.

**Tools → Scenarios** lists the scenarios with their switches (one on at a time)
and the rules in each: rename a scenario, rebuild one made from a collection's
examples, ungroup its rules, or delete it with its rules and the mock files
only they answer from. **Tools → Mocks** is the Rules page for the Map Local
rules alone; a new rule there is a Map Local rule.

```json
[
  {"type": "map_local", "match": "/missing", "target": "/tmp/mock.json"},
  {"type": "map_remote", "match": "http://example.com/api", "target": "http://127.0.0.1:8081"},
  {"type": "block", "matchMode": "wildcard", "match": "*.telemetry.example.com/*", "apps": ["Google Chrome*"]},
  {"type": "map_local", "matchMode": "regex", "match": "/v1/users/\\d+$", "target": "/tmp/user.json",
   "when": [{"name": "X-Env", "op": "equals", "value": "staging"}]},
  {"type": "break", "match": "/checkout"},
  {"type": "break_response", "match": "/v1/orders"}
]
```

- `map_local` answers from a file, without calling an upstream. Optional `status`
  and `headers` customize the response; connection headers are removed and length
  is recalculated. Files over 8 MiB fail with 502 instead of being truncated.
  The rule editor shows a text file's **Body** and **Save rule** writes edits
  back to it; a rule with a typed body and no file gets one in
  `~/.vscroxy/mocks`. Binary files and files over 8 MiB are edited outside.
  A `target` that is a folder answers with the file at the request's path inside
  it, after removing `pathFrom` (`/api/users/42.json` with `"pathFrom": "/api"`
  reads `users/42.json`); a path ending in `/` reads `index.html`, `..` cannot
  leave the folder, and a missing file leaves the request to the server, untouched
  by the rules below. The request test cannot tell whether a file exists, so it
  numbers a folder rule as acting.
- `map_remote` sends the request to the `target`'s scheme, host and port,
  keeping the path and query; the table keeps showing the URL the client asked
  for. `pathFrom`/`pathTo` rewrite a path starting with `pathFrom` to start with
  `pathTo` (`/v1/` → `/api/v1/`); a path in `target` then goes in front, and a
  query in `target` replaces the request's. `keepHost: true` sends the client's
  Host header instead of the target's, for servers that serve several sites
  by name. WebSocket handshakes follow the first matching Map Remote rule too,
  as `ws` or `wss` to match the target; Map Local, Block and HTTP breakpoints do
  not act on WebSockets. Message scripts and `break_message` run after mapping.
  Inside a CONNECT tunnel (HTTPS and most WebSockets) the
  proxy first connects to the original host to copy its certificate. When that
  host cannot be reached and an active Map Remote or Map Local rule could
  match it, the proxy answers with a certificate it generates for the host
  instead, so a host that exists only in a rule (`https://shop.dev.invalid`)
  works too. Requests on that host no rule maps then fail with 502. A host no
  such rule could match still fails the CONNECT. A regular expression counts
  for every host unless it starts with a literal one (`^https://api\.test/`).
- `block` does what its `block` field says: nothing (the default) answers
  `403` without contacting the server; `"drop"` closes the client's connection
  without an answer, as a network failure would, and the row shows it failed
  (clients may retry a GET); `"hide"` proxies the request as usual but keeps it
  out of the table, and no rule below it acts on it. With `apps`, wildcards and
  regular expressions this is also the place for a block list that the ignore
  lists, which match only hosts and apps, cannot express. Block rules do not
  act on WebSockets.
- `break` pauses the request until you decide, and `break_response` pauses
  the response (see below). `break_message` pauses complete WebSocket data
  messages, with `direction` empty for both, `"in"` for server → client, or
  `"out"` for client → server (see [message interception](#websocket-message-interception)).

**Settings → General → Reload configuration** rereads the file. A missing
file means no rules.

A captured flow's **Tools** context submenu pre-fills its URL and method
for mapping, blocking or breakpoints. **Mock this response** copies the complete
response to `~/.vscroxy/mocks/`, retaining status and response headers (except
wire framing, compressed-body metadata and stale validators). Binary and spilled
bodies are supported up to 8 MiB; missing/partial bodies and stream previews are
rejected. Review captured headers for secrets before sharing the rule. Canceling
an unsaved mock removes its file; deleting a saved rule keeps its file for reuse.

A saved request's example can answer too. **Mock** beside an example in the
Examples tab opens a Map Local rule in Tools that answers with it; it applies
once saved. **Mock “…” from examples** in a collection's or folder's menu makes
every example under it a rule of a scenario named for its path: an offline
mock of the collection, switched on in the scenario menu. A mock matches the
example's URL with the variables it sees now filled in, as a regular
expression: an unknown `{{variable}}` or `:name` segment matches one path
segment (a `{{baseUrl}}` at the start matches any scheme, host and base path),
and any query matches. Of the examples a request answers the same way, the
first answers by default and the others when the request sends
`X-Mock-Response-Name` with the example's name, as Postman's mock servers read
it. **Rebuild scenario “…” from examples** replaces the scenario's rules and
the mock files they used, in the same place in the list, keeping it on or off.

## Connection diagnostics

The app's own log is `~/.vscroxy/logs/vscroxy.log`, rotated at 5 MiB with five
files kept. It records startup and errors shown in notifications, never request
contents. Errors and warnings stay on screen until dismissed, with the raw error
under the sentence; other notifications fade after a few seconds.

**Diagnostics** under Live in the sidebar is the one place for "why don't I see
this traffic". It is also reached from **Network → Connection diagnostics**, the
command palette, **Diagnose capture…** in the empty list, **Diagnose…** in client
and device setup, and **Explain** on a failed exchange. Choose the client (This
Mac, Phone or tablet, One process) and an HTTP(S) target URL; five steps are
checked in order: **Listener**, the client's proxy (system proxy, device access
or the process itself), **Certificate**, **HTTPS decryption** and **Actual
traffic**. The first failing step opens with its details, and its fix is the
highlighted button. Certificate trust is only checked for this Mac; for a phone
or a process it is not verifiable here and Actual traffic shows the outcome.

Traffic evidence matches the target's scheme, hostname and port (default ports
are equivalent), not its path, and counts only live traffic from any client, not
imported sessions. **Inspect #N** opens a hidden record in a new view without
erasing existing filters. An absent record is not a confirmed failure; ignored
traffic is proxied without being retained. Nothing changes until a fix button is
pressed: opening or re-checking does not restore an interrupted system-proxy
session, change proxy settings, install a CA, send requests or expose a LAN
listener.

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

For a transport failure, **Explain** in the detail header opens Diagnostics on
that exchange, with Actual traffic expanded: the recorded stage and raw error,
possible causes (not confirmed), and stage-specific checks.
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
Turning Breakpoints off releases all waiting pauses unchanged. Expired queued
dialogs are skipped; an expired open dialog is dismissed on the next UI refresh.

The flow's duration is the exchange's, not the time you spent at the
breakpoint.

## Scripts

Rule scripts change requests and responses with JavaScript and the same `pm`
API as request scripts. **Tools → Scripts** lists them, with the hooks each
defines and how many rules run it; the chosen one is edited beside a
**Console** of what it printed and the errors it hit as its rules ran (the last
500 lines). **Insert snippet** adds common code. **Save** (Ctrl/Cmd+S) applies
to new requests or the next WebSocket message. Each script is a file under `~/.vscroxy/tools/scripts/`
(`Name.js`) that can be edited by hand or kept in Git; **Reload configuration**
reads them again. A script that does not parse is marked in the list and the
editor: Script rules skip it; Code Mock rules return a local 502.

A rule script is an ES module: it exports its hooks and may `import` the
built-in libraries and addons. vscroxy converts it with esbuild before running
it, so newer syntax (optional chaining, `??`, object spread, class fields)
works, and errors name the line as you wrote it. Top-level `await` is not
supported; await inside the hooks.

A **Script** rule in **Tools → Rules** runs one script on the traffic it
matches (URL, method, apps, headers and scenario, as other rules). For HTTP,
every matching Script rule runs, in order, before breakpoints and the Map and
Block rules, each seeing what the ones before it changed. WebSocket message
hooks instead match the post-Map Remote handshake URL and run before message
breakpoints, as described below.

```js
import _ from "lodash";

export function onRequest() {
  pm.request.headers.upsert("Authorization", "Bearer " + process.env.API_TOKEN);
  pm.request.url.query.upsert("debug", "1");
  pm.variables.set("startedAt", Date.now());
}

export async function onResponse() {
  console.log(pm.response.code, Date.now() - Number(pm.variables.get("startedAt")), "ms");
  if (pm.response.code >= 500) {
    pm.response.code = 200;
    pm.response.setBody({ ok: true });
    pm.response.headers.upsert("Content-Type", "application/json");
  }
}
```

- `onRequest` edits `pm.request` (method, URL, headers, raw body) before it
  goes upstream; `onResponse` edits `pm.response` before the client gets it:
  assign `pm.response.code`, change `pm.response.headers` (`add`, `upsert`,
  `remove`), and `pm.response.setBody(text or object)`. Either may be `async`.
- `pm.variables` belong to one exchange, from its `onRequest` to its
  `onResponse`, across every script that runs on it. `pm.globals` is shared by
  all rules and saved; `pm.environment` reads the active environment and is
  read-only here. `process.env` reads the system's environment variables.
- `pm.sendRequest` goes straight to the server, not through the proxy, so a
  script's own rule never matches it; it waits up to 30 seconds.
- `import CryptoJS from "crypto-js"`, `import _ from "lodash"` and
  `import chai from "chai"` load the built-in libraries; `require(...)` also
  works.
- Bodies that are binary, compressed on the request side, or larger than 1 MiB
  are not handed to the script and stay as they are. Streaming and SSE
  responses skip `onResponse`.
- Each hook has two seconds of run time; one that fails or times out leaves the
  exchange as it was, is counted in the status bar and shows in the Console.

### Code mocks

**Tools → Mocks → New rule → Type: Code Mock** answers matching HTTP requests
with JavaScript instead of contacting their original server. Choose a script,
or choose **New script** for an `onMock` template, then **Edit script**. Save
both the script and the rule. The script's actions also offer **New Code Mock
rule**, and a capture's **Tools → Code Mock…** starts a rule for its URL.

```js
export function onMock() {
  const input = pm.request.body.raw ? JSON.parse(pm.request.body.raw) : {};
  const missing = pm.request.url.query.get("missing") === "1";
  pm.response.code = missing ? 404 : 201;
  pm.response.headers.upsert("Content-Type", "application/json");
  pm.response.setBody(missing ? { error: "Not found" } : {
    id: 42, name: input.name || "Ada", path: pm.request.url.getPath()
  });
}
```

- `onMock` starts with an empty 200 response. It reads a **read-only**
  `pm.request` and builds `pm.response` with the same `code`, `headers` and
  `setBody` API as response scripts. An `async` hook and local addons work.
  Only `onMock` runs for a Code Mock rule; its `onRequest`/`onResponse` exports
  do not. Normal matching Script rules still run before the response rules.
- Code Mock shares first-match order with Map Local, Map Remote and Block.
  It supports the same URL/method/app/header/scenario conditions. A local
  answer skips upstream response scripts and response breakpoints, just like
  Map Local. Disabled or inactive rules do not intercept requests.
- Missing/broken scripts, exceptions, timeouts and invalid output return a
  local **502**, never an upstream fallback. Exception details stay in the
  Scripts Console, not in the client's response. `pm.sendRequest` is
  unavailable in `onMock`, including from addons.
- Input must be complete, uncompressed UTF-8 text of at most **1 MiB**;
  oversized/streamed input returns **413**, binary/compressed input **415**.
  Generated bodies are at most **1 MiB**, headers **64 KiB**, status **200–599**.
  Connection/framing and Content-Encoding headers are removed, length is
  recalculated, and HEAD/204/205/304 responses send no body. The existing
  two-second script execution budget applies; this is not a memory sandbox.
- HTTPS needs inspection enabled for that app/host and the client to trust
  vscroxy's CA. A potentially matching Code Mock host uses a generated
  certificate **without first connecting upstream**; unmatched requests dial
  upstream on demand. Such connections negotiate HTTP/1.1. Bypassed tunnels
  and WebSockets are not code-mocked.
- Scripts live in `tools/scripts/*.js`; rules, scenarios, renames, reload and
  configuration export/import use existing storage. Environment variables
  are read-only; globals retain the existing saved-write behavior. Each
  exchange has a fresh JS runtime, not a persistent in-memory mock server.

This is vscroxy's native proxy-rule API, **not** Postman's Node.js mock-server
runtime or its `pm.mock.matchRequest`/`sendExample`, `pm.state` or datasets APIs.
For static saved responses, continue using Examples and Map Local.

### Addons

**Addons** are your own modules for request and rule scripts. **Tools →
Scripts → Addons** edits them; each is a file under `~/.vscroxy/tools/addons/`
(`name.js`). An addon is an ES module like a rule script: what it exports,
rule scripts and other addons import `from "name"`, and request scripts, which
stay Postman's CommonJS, get with `require("name")` (a default export is at
`.default`). An addon written as CommonJS, with `module.exports`, works too.
An addon can import the built-in libraries and other addons; it runs once per
script run, so state does not carry between exchanges. The list says how many
scripts import or require each. Names cannot be `chai`, `lodash` or
`crypto-js`. Configuration export includes them. `vscroxy-cli run --addons DIR`
loads them for headless runs.

```js
// addons/sign.js
import CryptoJS from "crypto-js";
export const sign = (text, key) => CryptoJS.HmacSHA256(text, key).toString();

// a rule script
import { sign } from "sign";
export function onRequest() {
  pm.request.headers.upsert("X-Signature", sign(pm.request.body.raw, process.env.SIGNING_KEY));
}

// a request script
const { sign } = require("sign");
```

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

**Decode text…** in a captured Body pane's options, or **Decode selection…**
in a text reader's context menu, opens a local scratch decoder. Sent responses
also have a Decode button. A selection takes priority over the body; input is
limited to 64 KiB. Supports standard/URL-safe Base64 (binary output is shown as
hex), JWT header/payload, URL components, query values, and explicitly selected
Unix seconds or milliseconds (shown in UTC). URL query decoding treats `+` as a
space; component decoding does not. JWT decoding **does not verify signatures,
expiry, issuer or other claims**. Copy result copies only the decoded preview;
neither decoding nor editing its scratch input modifies or sends a request.

Both captured sides have a **Cookies** tab, separate from the session cookie
jar. Request Cookie pairs and response Set-Cookie lines preserve duplicate
names. Response details include Domain, Path, Expires, Max-Age, SameSite,
Secure, HttpOnly and Partitioned; missing attributes are not inferred from the
live jar. Values are masked until explicitly revealed, and changing captures
resets reveal. Sent responses use the same parsed, masked view. Invalid cookie
headers are counted with a warning; Raw/Headers retains the original evidence.

**Query JSON…** in either captured Body pane's options or a sent response's
format menu opens a read-only JSONPath/jq query over the complete retained body,
including a spill file, not just its preview. Run/Cancel work off the UI thread;
closing or replacing the query discards old answers. Editing a query clears its
previous results. Copy results never modifies or sends the source.

- **JSONPath** uses a bounded OjG/Goessner dialect, not RFC 9535. Supports `$`,
  property/index selection, slices/unions, wildcards, one recursive descent,
  and boolean/comparison filters on singular `@` paths. Returns an array of
  matches, including `[]`. Filter arithmetic/functions/root references are
  disabled. Selections preserve large integers; filters require signed 64-bit
  integers and reject mixing floats with integers outside ±2^53 (use jq).
- **jq** uses gojq with selection, pipes, constructors, comparisons, `select`,
  `map`, `length`, `keys`, sorting/grouping, common type filters and bounded
  streams. Results are newline-separated JSON values; an empty stream is
  labeled explicitly. Integers retain arbitrary precision; decimal/exponent
  arithmetic uses float64. This is not a shell or the full jq CLI: modules,
  definitions, updates, multiplication, binary subtraction, formats,
  interpolation, reduce/foreach, environment and external input are disabled.
- Limits: strict UTF-8 JSON with unique object names, 1 MiB input, 4096-byte
  expression, 2 MiB output, 4096 results, depth 64, 16384 nodes per value,
  1024-byte numbers, 100000 work units, and a one-second execution budget.
  Invalid, incomplete, unavailable or oversized bodies report an error rather
  than silently querying partial evidence. All errors discard partial results.

**Auto** is the default: images/HTML use Preview, explicit protobuf/gRPC uses
Protobuf, the structured types below use their named view, other binary bodies
use Hex, and text/JSON uses Pretty. Sent responses also offer Auto, Raw, Hex,
Pretty, and the structured view when applicable. A manual mode
applies to the current request side; changing requests or sides resets to Auto
instead of leaving the next response in an incompatible display mode.

**Form**, **Multipart**, **XML** and **MessagePack** inspect complete retained
bodies off-thread, cached separately per capture side or sent response.
Content-Type parameters, including multipart boundaries, are preserved. Invalid
or incomplete data shows an error; it is not silently treated as a valid prefix.
Pretty/Raw and Hex remain available. Copy view copies the interpretation only,
never changing the captured bytes or a draft.

- Form fields keep original order, repeated names, raw encodings, and the
  difference between `flag` and `flag=`. `+` means space; `%2B` means `+`.
  The table truncates long cells for readability; Copy view retains full data.
- Multipart lists ordered parts, their names, original filenames, headers and
  byte sizes. Only safe UTF-8 text fields preview (at most 4 KiB); file,
  binary and transfer-encoded payloads never preview. Transfer encodings are
  not decoded, and filenames are metadata, never filesystem paths to open.
- XML indents element-only content while preserving mixed content, CDATA and
  `xml:space="preserve"` subtrees. Prefixes, entities and attribute values
  are not rewritten. Use Raw when inter-element whitespace is significant.
  Only UTF-8 is supported; DTDs/directives and entity resolution are disabled.
- MessagePack keeps signed/unsigned integers exact. `$map` entries preserve
  order, repeated and non-string keys; `$binary` and `$extension` explicitly
  show Base64 bytes and extension tags (including timestamps). Nonfinite floats
  use a `$float` marker. This is a debugger notation, not round-trip JSON.
- Limits: 1 MiB source, 2 MiB rendered text, 256 multipart parts, nesting depth
  64 and 16384 nodes/fields. Parsing errors discard partial output. Large source
  bodies are still available through their existing Raw/Hex/export paths.

A body above 64 KiB is not held in memory: the capture core writes it to a
spill file under a temporary directory and the flow keeps the first 64 KiB
plus the file's path and the body's size. The Body tab, the diff, HAR export
and session save read the file back, and a replay sends it, so only viewing a
large body costs memory. The directory is removed when the app closes.

## Network conditions

The **Network** menu slows traffic to a preset: **3G**, **4G** or **DSL**.
Each HTTP exchange selects its conditions once before forwarding: later changes
do not affect its upload or download. Rates pace each body's wire bytes by
direction (not aggregate connection bandwidth); 0 means unlimited. Latency is
one wait before forwarding, including an empty request/response, not a wait per
body or TCP packet. Waiting can be interrupted by cancellation, body close or
proxy shutdown. This applies to HTTP, inspected HTTPS and SSE, not WebSocket
messages or opaque CONNECT tunnels. Local responses also use the download limit.
The table's buffered-response time includes an estimated paced download;
upload and latency are not counted twice. The timing waterfall remains the
measured proxy timeline and excludes downstream delivery. **Off** leaves
traffic alone. A request-disconnect failure percentage, when set, fails before
HTTP forwarding and records a connection error; it is not TCP/IP packet loss.
Clients may independently retry an idempotent request after a disconnect.

**Tools / Network → Custom presets…** saves up to 32 custom profiles with
separate download/upload bytes per second, latency in milliseconds and an
integer request failure percentage. Rates accept 0–1073741824 bytes/s, latency
0–60000 ms and failure 0–100%. Names are unique ignoring case; built-in names
are reserved. **Copy active conditions** fills the numeric fields without
changing the draft name. **Save & apply** validates and safely writes
`~/.vscroxy/network-presets.json` before applying; damaged, externally changed
or read-only files are left untouched. The standard **Reload** action reloads
this file too. Only custom profiles are saved; global conditions restart Off.
Deleting a preset requires confirmation and resets global conditions Off if
that preset is active. The shared menu lists saved profiles; the active chip's
close button also turns the global conditions Off.

**Tools / Network → Network rules…** opens the normal rule editor at a
Network conditions rule. URL, method, app, headers, GraphQL operation and
scenario matching work as for other rules. The first independently matching
rule replaces the entire global profile, including an all-zero profile that
explicitly selects Off; conditions are not merged. Map and Block winners are
selected separately, and a 100% failure disconnects before later steps. An
intermediate percentage in a rule preview means later steps assume that request
continues. Selection and the numeric conditions are snapshotted once per
exchange, so later edits or scenario changes do not affect it in flight.

Rules use the original URL before scripts and routing. A complete buffered POST
up to 1 MiB (including decompression) can match its verified GraphQL operation;
a streamed or oversized POST has no operation match, while a GET still uses its
URL query. Rules cover the same HTTP, inspected HTTPS and SSE traffic as global
conditions, not WebSocket messages or opaque CONNECT tunnels. Choosing a preset
in this editor copies its values; later preset edits do not update the rule.
Network rules are included in configuration import/export (the separate custom
preset list is not). The active-rule chip counts enabled rules in the current
scenario. Its close action saves all network rules disabled, including inactive
scenarios. **Off** does the same before clearing the global profile, and leaves
both rule state and global conditions unchanged if the rules cannot be saved.

**Network → Upstream…** sends every request through another proxy, such as
`http://127.0.0.1:8080`; **Upstream off** clears it. **Settings → Proxy → Upstream** edits the same
address in place; leave it empty and apply to connect directly. The engine tunnels to the
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

## Allow list

**Settings → Capture → Allow list** does the opposite: while **Only show
allowed traffic** is on, the table shows only flows to an allowed host (with
its subdomains, matched like the ignore list) or from an allowed app. Everything
else is still proxied, just not recorded. The lists are kept in
`~/.vscroxy/allow.txt` and `~/.vscroxy/allow-apps.txt`, and the switch is
remembered across launches. An ignored host or app stays out even when allowed.
With the switch on and both lists empty nothing is shown, and Settings says so.
While it is on, an **Allow list** chip sits above the traffic table: click it
to edit the lists, or its × to turn the allow list off.

## Memory limits

**Settings → Capture → Memory** bounds what a long capture keeps: **Keep up
to** a number of requests (1,000–50,000, default 5,000), a **Memory limit**
(128 MB–2 GB, default 512 MB) for their headers, kept bodies and messages, and
a **Disk limit** (1–20 GB, default 5 GB) for the temporary files that hold
bodies past 64 KB and long messages. Past any of them, the oldest requests
leave the table first, and their spill files are deleted from disk with them;
pinned requests always stay. A change applies at once and is remembered. **In
use** shows the current memory estimate, the disk the spill files take, and
the count. A body still streaming counts once its request is published.

## Host profiles

Host profiles are named hosts files the proxy resolves itself, as
SwitchHosts keeps them: "Local", "Staging", "Pre-release". The proxy dials the
address a profile gives while the request keeps its name, its URL, its `Host`
header and SNI, so a phone or emulator pointed at this proxy reaches another
machine as if the name pointed there, without editing the system's hosts file,
administrator rights, or rooting the device. vscroxy never writes the system's
hosts file.

**Hosts** beside the recording pill switches profiles: each has a switch and
its host count, and a profile a higher one overrides shows its conflicts.
**Combine** turns on several at once, the higher profile winning where two map
the same host; **One at a time** turns the others off as one turns on. A change
applies to new connections; **Drop open connections** closes the clients'
open ones so they connect again at once. While a profile is on, the enabled
tools strip shows a **Hosts** chip, whose × turns them all off. The command
palette lists every profile too.

**Manage profiles…** (Cmd/Ctrl+Shift+H) opens **Tools → Host profiles**: drag
profiles to reorder them, add one with **+**, and edit the selected one's
hosts. Each line is the standard `address host…` (several hosts, `#` comments,
IPv6) or `host = address`, optionally followed by `@ App, other app` to map
the hosts for those apps only. A host matches its subdomains, `.example.com`
only the subdomains, and `*.example.com` one level. A line that is not a
mapping is underlined and maps nothing; a line another profile that is on
overrides, or that overrides one, is marked with the other's address. **Test a
host** says where a host goes now, and from which profile and line. **Import
system hosts…** copies this system's hosts file into a new profile, off;
**Subscribe to URL…** adds a read-only profile fetched from a URL and
refreshed every hour.

Right-click a request and pick **Map this host…** to add its host to a profile,
for every app or the one that sent it; the profile turns on. Each profile is a
file in `~/.vscroxy/tools/hosts/`, `Staging.hosts` and so on, which you may edit
by hand or keep in Git; `profiles.json` beside them keeps their order, switches,
subscriptions and mode, and a `.hosts` file it does not list joins the end,
off. **Settings → General → Reload configuration** reads them again.

A request a profile mapped shows its **Remote IP** in the accent color, with
the profile and line in its tooltip (in a narrow list, a **Hosts** tag on its
path), and "Mapped by Staging → 10.0.3.21" above the inspector. Two things
bypass the profiles: traffic an app sends over QUIC (UDP 443) never reaches
the proxy, and an app that connects to an address it already has.

## Export and sessions

The **⋯** menu exports HAR 1.2 (HTTP flows only; HAR has no stream messages),
saves capture to **Sessions**, or opens session files without replacing live traffic.
**Export session…** exports the current historical session and its messages.

Postman collections/environments/globals, HAR and session exports share an
options dialog. All six redaction scopes start enabled: header/trailer values
(except Content-Type and Accept) and cookies; query values; structured auth,
URL credentials and fragments; variable values; bodies and stream messages;
and scripts/Checks, session titles, descriptions, notes and diagnostics.
Disable individual scopes to retain them, or choose **Export unredacted** to
preserve the original bytes. Removing scripts also removes Checks, since
Postman stores exported Checks as scripts. Keep that scope unchecked when sharing
executable tests, and review their literals yourself.

Every export requires explicit acknowledgment; changing the options clears it.
Only the exported copy changes. This is field-based redaction, not secret
detection or guaranteed anonymization: review paths, hosts, names, parameter
names, Content-Type/Accept, assertion literals and every unselected scope.
The native save dialog chooses the destination. Files are replaced atomically
and privately (0600 on Unix), including over broadly readable files, but are
not encrypted. Clipboard/cURL copies do not use this export dialog.

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
Spill files are temporary and removed when capture closes, or earlier when
their request leaves the table (see Memory limits).
Sessions save retained payloads and metadata, not temporary spill paths, and
older session files still load.

Both plain WS and decrypted WSS retain the actual escaped path/query. A small
documented local patch to go-mitmproxy fixes plain WS's discarded handshake
headers and CONNECT-only capture metadata (see `third_party/go-mitmproxy/VSCROXY.md`).
Composed WS requests use structured auth, custom headers and session cookies;
WSS verifies the system/proxy CAs rather than disabling certificate checks.
The one-shot sender waits up to two seconds for five replies, capped at 1 MiB
each; cancellation closes the socket. Post-response scripts require HTTP.

### Interactive SSE sessions

**Network / Commands → SSE session…** opens a GET-only live event-stream
client. An active HTTP draft pre-fills its prepared URL and headers (including
auth and current variables), without sending its body or running its scripts.
You can also enter an absolute HTTP(S) URL and headers directly. **Connect SSE**
starts a fresh timeline through the running capture proxy, using the Requests
cookie jar and verified system/proxy CAs. HTTPS decryption follows the proxy's
host settings. It sends `Accept: text/event-stream` and `Cache-Control: no-cache`;
redirects use Go's standard sensitive-header policy.

Events appear as they arrive, without waiting for the response to end. The
decoder supports UTF-8, one initial BOM, LF/CRLF/CR line endings, multiline and
empty `data`, custom/default event types, persistent/reset IDs and comments.
An incomplete final block is discarded. Select an event to inspect its type,
ID and data; select a connection event for the final URL and response headers.
Search is case-insensitive across retained type, ID, payload and connection
details. **Copy event ID** and **Copy shown SSE data** copy exactly the retained
ID or displayed payload/details, including any truncation notice.

**Reconnect on EOF / network failure** is off by default. When explicitly
enabled before connecting, EOF or a network failure schedules another GET with
the last committed `Last-Event-ID`; an empty ID clears that header. The default
delay is 3 seconds. Numeric server `retry` values are bounded to 100 ms–5 min
to avoid busy loops; values overflowing uint64 are ignored. HTTP 204 stops even
with reconnect enabled. Other HTTP statuses, a non-`text/event-stream` content
type, or a line/event exceeding 1 MiB fail the session instead of retrying.
There is a 15-second response-header timeout, but no whole-stream timeout.

The timeline keeps the latest 200 events (including connection events), original
data byte counts and an eviction count. Each field retains at most a 64 KiB
UTF-8 preview plus an explicit truncation notice. Search/copy use these previews,
not evicted data; an evicted selection is cleared rather than copied stale.
**Latest event** follows the list without changing the inspected payload.
**Stop SSE**, Escape, clicking outside, closing the panel or quitting the app
cancels receiving and reconnect waits. A new Connect starts over; the timeline
is not saved or restored, and request scripts/Checks/Runner/History are not
part of this live client. Proxy HTTP rules still apply; Capture separately
retains traffic under its own limits and event framing.

### Interactive WebSocket sessions

**Network / Commands → WebSocket session…** opens a separate live client. If
the active request draft has a WS URL, it prepares that draft's URL, auth,
headers and body using current variables, without running scripts or sending.
Otherwise enter a `ws://` or `wss://` target and handshake headers. **Connect**
performs the handshake through the capture proxy using the Requests cookie jar
and verified TLS. **Subprotocols** offers comma-separated HTTP tokens in client
preference order; the server selects one or none, and the handshake event records
the actual selection. WS and decrypted WSS preserve negotiation through both
proxy handshakes. Duplicate/invalid tokens or an unoffered selection fail rather
than silently falling back to another protocol.

It sends no message until **Send text** / **Send binary**. Select **Text** for
UTF-8 text, **Hex** for binary bytes (whitespace allowed), or **Base64** for padded
base64 binary input. Invalid encoding sends nothing. Empty binary messages are
valid, and bytes are never converted through UTF-8. Send repeatedly on
the same connection, search retained payloads/events and filter In/Out/Events.
Select a message to read/copy text or binary hex; copying includes any truncation
notice and copies displayed text/hex, not a binary clipboard format.

Both the live session payload and Capture's WebSocket **Messages** detail offer
an explicit **Protobuf** view. It decodes one complete raw protobuf message,
never guesses a gRPC envelope or a schema from the bytes. **Auto** shows field
numbers; import a schema with **Schema…** and choose its message type for named
fields. The schema registry is shared with the HTTP body inspector; type and
selection in the live panel are independent. **Copy decoded** copies the full
decoded text; Text/Hex and their raw copy actions remain available. Truncated
payloads, missing spill files and malformed protobuf show an error, not a
partial message presented as complete. Inspection is off-thread and limited to
16 MiB per message (the live timeline's smaller retention limit still applies).

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
Automatic SSE reconnect and live gRPC timelines are not provided by this panel.

### WebSocket message interception

**Tools → Rules → WebSocket message breakpoint** (`break_message`) pauses
complete text or binary messages while the toolbar's **Breakpoints** switch is
on. Choose Both, Client → server (`out`), or Server → client (`in`). It works
for proxied WS and inspected/decrypted WSS, including the interactive client;
opaque tunnels are not inspected. Handshakes, ping/pong and close frames do not
pause. Match conditions use the handshake method, headers, app and URL after
Map Remote. The first matching breakpoint for each direction wins.

- Edit UTF-8 text directly, or binary bytes as canonical padded standard
  Base64. **Apply & continue** preserves the opcode and sends the edited bytes.
  Invalid Base64/UTF-8 or edits over 1 MiB cannot be applied. Larger incoming
  messages are read-only in the dialog, not silently truncated for editing.
- **Continue original** preserves the message as it reached the breakpoint,
  including successful script edits. **Drop message** suppresses only that
  message; it does not close the connection. HTTP fields, abort status and
  HTTP breakpoint templates are not offered here.
- Either direction can pause without preventing the other from pausing.
  The two-minute timeout and turning Breakpoints off continue unchanged.
  Connection cancellation and proxy shutdown release pending waits; stale
  decisions are rejected. Reading has bounded lookahead, not an unbounded
  queue: a close behind more queued data may not be observed until the pause
  resumes or times out.

A **Script** rule can export `onMessage(message)`, including `async`. Matching
hooks run in rule order before the message breakpoint. `pm.request` is read-only
handshake metadata; HTTP `onRequest`/`onResponse` do not run for WebSockets.

```js
export function onMessage(message) {
  if (message.direction === "out" && message.type === "text") {
    if (message.text() === "heartbeat") { message.drop(); return; }
    message.setText(message.text().replace("production", "staging"));
  }
  // message.base64() reads any payload losslessly.
  // message.setBase64("AP6AAQ==") changes it to binary bytes 00 fe 80 01.
}
```

`type` (`text`/`binary`) and `direction` (`in`/`out`) are read-only properties.
`text()` reads text only; `base64()` reads either kind. `setText(string)` changes
the payload and opcode to text; `setBase64(string)` to binary, using strict
canonical padded standard Base64. `drop()` stops this message's remaining
script chain and breakpoint. Empty payloads are valid. There is no `pm.message`
or global `message` alias. `pm.info.eventName` is `message`.

Incoming messages over 1 MiB skip scripts; setters enforce the same byte limit.
A failed/invalid/timed-out hook preserves the message and connection-local
variables as they were before that rule, logs its error to the Scripts Console,
and lets later rules run. Successful earlier rules remain applied. The existing
CPU budget and direct `pm.sendRequest` path apply, with connection cancellation.
`pm.variables` last for one connection and are shared across both directions;
script chains are serialized, but a paused dialog does not hold that lock.
Every hook has a fresh JS runtime: use variables, not module closures, for state.
Console output, `pm.globals` writes and external requests are not rolled back
on script failure.

Capture records the post-interception payload and labels suppressed messages
`dropped text`/`dropped binary`; these are not extra wire messages or queue-loss
gaps. Stream completion follows the end of both directions' message callbacks.
The live client's sent timeline still records its original send intent; its
received timeline shows what actually reached that client.

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

### Interactive Socket.IO requests

Create a **New request** and choose **Socket.IO** in the protocol selector.
The address bar is the Engine.IO HTTP(S) or WS(S) endpoint, for example
`https://api.example.com/socket.io/?tenant=demo`; an empty/root path defaults
to `/socket.io/`. A custom endpoint path is kept exactly. Under **Connection**,
set a separate namespace (default `/`) and an optional JSON auth object.
Namespace auth is distinct from the handshake's **Headers** / **HTTP auth**.
Variables and host-restricted Vault references work in these fields and event
arguments. The session snapshots its original endpoint and variable scope;
switching environments cannot redirect it or change later message expansion.

**Connect** opens a fresh session through the running capture proxy, sharing the
Requests cookie jar and verifying system/proxy CAs. It does not emit the event
draft automatically. Under **Event**, enter an event name and a JSON **argument
array**, then **Emit event**. An empty editor means `[]`. **Request ACK** assigns
a connection-local ID and shows incoming ACKs in the timeline. When the server
asks for an ACK, select its numeric ID and **Reply ACK**, using the same argument
array and attachments; each request can be answered only once.

**Attachments** is a JSON array of canonical padded base64 strings, for example
`["AP9J", ""]`. Each corresponds to a placeholder in the arguments, such as
`[{"payload":{"_placeholder":true,"num":0}}, {"_placeholder":true,"num":1}]`.
Indices start at zero; all attachments must be referenced. Empty base64 means
an empty binary attachment, not no attachment. Incoming binary events/ACKs show
their JSON placeholders and exact attachment bytes encoded as base64, including
non-UTF-8 payloads. JSON numbers are not decoded through floating point.

Search matches retained event names and payload previews, case-insensitively.
**Copy shown message** copies the selected preview, including any truncation
notice. The timeline retains the latest 200 connection/message events, byte
counts and an eviction count, with a 64 KiB UTF-8 preview per field. Wire packets
and their attachments together are limited to 1 MiB (or a smaller server limit
when sending), with at most 32 attachments and 128 unanswered ACKs per direction.
Reconnect to discard pending ACKs. The opening/namespace handshake has a
10-second deadline; established reads follow server heartbeat settings, which
ordinary events do not extend. Writes have a 5-second deadline.

**Disconnect**, closing/reloading the document, or quitting cancels the session,
including a pending handshake. Draft configuration is saved with the request;
the live connection, ACK state and timeline are not saved or restored. Supports
Socket.IO v3/v4 servers with the default parser, Engine.IO 4 and WebSocket-only
transport. No polling fallback, automatic reconnect, retry, offline queue,
Socket.IO v2 or alternate parsers. HTTP scripts/Checks/token bindings (including
inherited scripts) must be removed, not silently skipped. HTTP Runner, code
generation, History and Examples are not this client's execution API; Postman
export skips non-HTTP documents with a warning. See [interoperability checks](socketio.md).

### Interactive gRPC requests

Create a **New request** and change its protocol from **HTTP** to **gRPC**.
This is a normal request document, not a separate dialog. Enter an explicit
`https://host:port` endpoint (TLS) or `http://host:port` (plaintext HTTP/2),
without a method path or query. Under **Schema**, import a `.proto` or compiled
descriptor set with its dependencies, or choose **Reflect services**. Reflection
uses the document's metadata/auth, prefers v1 and falls back to v1alpha only if
v1 is unimplemented. Discovery and source compilation are cancellable; a failed
load keeps the previous schema. Compiled descriptors and the selected method
are saved with this document, independent of the capture inspector's registry.

Select a service/method and enter its protobuf JSON under **Message**. **Send**
starts unary, server-streaming, client-streaming or bidirectional calls according
to the descriptor. For client/bidirectional streams, the first message is sent
immediately; **Send message** sends the current editor contents again, and
**Finish sending** half-closes the send side without cancelling received replies.
Bidirectional replies appear before finishing. **Cancel send** cancels the RPC;
it cannot undo work already performed by the server. The default deadline is
30 seconds, selectable up to one hour. Closing/reloading the document or quitting
cancels its discovery and calls. No automatic reconnect or replay occurs.

**Messages** retains the latest 200 input/output events with original byte counts,
an eviction count and at most 64 KiB of UTF-8 preview each. **Copy shown message**
copies that preview including any truncation notice. JSON and wire messages are
limited to 1 MiB. **Metadata** separates initial metadata and final trailers;
`-bin` values are entered/displayed as padded base64. **Status** shows the actual
gRPC code, message and structured error details, including trailers-only errors.

Metadata and JSON support the existing variables and host-restricted Vault.
Auth supports inherited/explicit None, Basic, Bearer and header API keys.
HTTP scripts (including inherited scripts), Checks and token bindings must be
removed before calling: they are not silently skipped. Runner/CLI HTTP execution
and HTTP code generation reject gRPC documents; Postman v2.1 export skips them
with a warning. Calls/timelines are session-local, not HTTP History or Examples.

TLS verifies certificates using system roots and the running proxy's CA.
HTTPS uses capture while the proxy is running; enable HTTPS inspection for the
target host to inspect it. Without capture it connects directly with verified
TLS. Plaintext h2c always connects directly and is explicitly marked **not
captured**, because the capture engine does not support h2c. This client does not
add custom CA/mTLS settings or gRPC-Web support.

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

**Network → No Caching** (or **Settings → Capture → Caching**) is a
session-only switch, also visible in the status bar. New exchanges strip conditional cache headers, send no-cache request
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

**New request** opens a request in the content area, one at a time. Other open
requests wait in the sidebar: new ones under **Unsaved** at the top of
COLLECTIONS, saved ones with unsaved changes marked by a dot in their
collection. A saved request left unchanged and unsent closes when another is
shown, since it reopens from its collection as it was. The header is a
breadcrumb of the collection and name: clicking it renames the request and moves it
between collections, so neither needs a permanent input. The environment,
**Save** and the request's **⋯** menu — Copy as cURL, Variables, History
and environment management — sit at the
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
2.0, OAuth 1.0, Digest, AWS Signature, JWT Bearer, Hawk and Akamai EdgeGrid;
explicit auth replaces that header unless the pre-script explicitly sets or
removes it. **Digest** sends once, and
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
`{{$timestamp}}` works there. **OAuth 1.0** signs with HMAC-SHA1,
HMAC-SHA256, PLAINTEXT or RSA-SHA1/256/512, from a consumer key and secret, an
optional token and secret, and a PEM private key for RSA. **Add authorization
to** sends the parameters in `Authorization`, the query, or a form body;
**Automatic** follows Postman and uses a URL-encoded POST/PUT body, the query
otherwise. **Body hash** adds `oauth_body_hash` for a body that is not
form-encoded. Timestamp, nonce, version, realm, callback and verifier are
optional; an empty timestamp and nonce are generated per send, and version
defaults to 1.0. Every field takes `{{variables}}` and `{{vault:name}}`, and a
vault secret is unlocked for the request's host. **Hawk** signs with SHA-256
(the default) or SHA-1 from an auth ID and key. **Payload hash** covers
`Content-Type` and the body; **ext**, **App** and **Delegation** add the
protocol's application data and Oz IDs. Timestamp and nonce are generated per
send when empty. Postman's Hawk **User** is kept for round-trips but is not
part of the MAC. **Akamai EdgeGrid** signs with the access token, client token
and client secret. **Base URL** overrides the host in the signature (a scheme
and path are ignored), and **Headers to sign** is a comma-separated, ordered
list of header names; POST bodies are hashed automatically. Timestamp and
nonce are generated per send when empty. Copied cURL and code carry the signed
header, query or body as they carry AWS and JWT signatures, so an OAuth 1.0
signature is only valid for the timestamp and nonce it was made with.
**NTLM** is not offered: its Type 1/2/3 challenge needs the same connection
for every step, while each send opens a new connection (the replay transport
disables keep-alives and the proxy pins an upstream connection to its
downstream one). Postman collections that use NTLM import as no auth with a
warning naming this; Postman's `oauth1`, `hawk` and `edgegrid` auth objects
round-trip, with an OAuth 1.0 header/body switch that imports as Automatic
when Postman sets `addParamsToHeader` false. **Inherit** (the default for new
requests) uses the nearest folder or collection auth and says which one it is;
see [Collections, variables and the vault](#collections-variables-and-the-vault).

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
stored, not file contents; sending or generating code reads the selected local files.

**Binary** sends a single file as the entire body, without multipart wrapping.
Choose **Body → Binary → Choose file…**, or enter a local path such as
`{{upload_path}}`. Paths support variables (including pre-request script and
Runner data values); relative paths are relative to the app's working directory,
not the collection file. Files must be regular files up to 16 MiB; empty files
are allowed. Each send reads the current bytes without text conversion or
variable substitution inside the file. Missing, unreadable or oversized files
fail before sending, never falling back to an inactive raw body. Content-Type
defaults to `application/octet-stream`; set it in Headers to override it.
A pre-request script that explicitly updates `pm.request.body.raw` or calls
`pm.request.body.update(...)` replaces the file with that raw body.
Saving a request or exporting it to Postman keeps only the file reference,
not the file bytes; the receiving machine must have the file or select another.

**GraphQL** provides separate **Query** and **Variables** editors under
**Body → GraphQL**, with GraphQL/JSON highlighting, find and undo. Enter one
query or mutation; multi-operation documents that need `operationName` still
use a Raw JSON body. **Variables** is a JSON object, or blank to send `{}`;
**Format** indents its JSON without expanding placeholders. On **Query**,
**Format** uses the GraphQL parser/formatter, preserving parsed semantics
and producing stable output. It never expands templates or edits Variables;
invalid syntax or a format that cannot preserve semantics leaves the query
unchanged and reports an error. Limits are 1 MiB source, depth 64 and 16384
tokens. In particular, variable-definition directives are rejected because
the upstream formatter would omit them. Block strings may become equivalent
quoted strings; comment placement follows the upstream formatter. Both fields support
`{{variables}}`, dynamic values, Vault references and pre-request script/Runner
values. Expansion is textual, as with Raw JSON: escape JSON string values when
needed. Sending validates the expanded variables as an object and encodes
`{"query": "…", "variables": {…}}`, defaulting Content-Type to `application/json`
unless you set it. An empty query or invalid variables fail before sending.
The HTTP method stays the one selected; use POST for the JSON envelope.
Saved requests, restored drafts and Postman collections keep query and variables
separately, including unexpanded templates; switching body types keeps inactive
edits but never sends them. Code/cURL export uses the same JSON encoding without
expanding secrets (variables must already be valid JSON for code export).
A script's explicit raw-body assignment or `pm.request.body.update(...)`
replaces GraphQL encoding, as it does forms. Schema introspection, completions
and an authored operationName selector are not included yet.

**Scripts** edits the pre-request and post-response scripts in the same reader as
the body, with JavaScript highlighting, find and undo. **Send** runs through the
proxy and shows the response body (Pretty, Raw or Hex, with JSON highlighting),
headers, cookies, timing and test results below the editor. While nothing has
come back the request editor keeps most of the height, so a handful of header
rows and their row action fit without scrolling; the first response pulls the
divider back to about a third, unless the reader moved it themselves. Until something comes back the
response area is only its empty state and **History** waits, disabled, for a
first send. **Compare with source** replaces the response with the comparison and
offers **Back to response**; there is no second comparison tab. The quiet footer
under the editor says whether the draft on screen is **Matches last send · time**
or **Edited since last send · not yet sent**; the response always names the send
it came from and that the source capture is unchanged. The exchange also lands
in Capture. The inline response preview is limited to 8 MiB; Capture retains the
complete body.

**Save body…** (the save icon beside a Requests response, or **Body → … →
Save body…** in Capture) saves the retained, decoded body bytes without Pretty
formatting, Hex encoding, headers or redaction. Binary and empty bodies are
supported. A large captured body is streamed from its retained file, not from
the truncated display preview. An incomplete body, missing retained file or
size mismatch fails without replacing an existing destination. If a Requests
response exceeds its 8 MiB retained preview, save the complete body from
Capture instead. Canceling the native save dialog writes nothing. The selected
response is fixed when saving starts, even if another request is sent meanwhile.
Saved bodies may contain credentials; review them before sharing.

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
  `have.jsonSchema`, `be.ok`, `be.success`, `be.error`, `be.json` and the rest,
  with `not`).
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
- **Postman's legacy globals**: `tests["name"] = bool`,
  `postman.setEnvironmentVariable` and the other `postman.*` variable calls,
  `responseBody`, `responseCode` and `responseHeaders`.

**JSON Schema**: `pm.response.to.have.jsonSchema(schema, options)` and
`pm.expect(data).to.have.jsonSchema(schema, options)` use the embedded Ajv
6.12.6 validator, with Draft-07 by default. Like Postman, the schema argument
must be an object, all validation errors are collected by default, and the
optional second argument accepts Ajv 6 options such as `allErrors`, `format`,
`coerceTypes` and `useDefaults`. Each assertion has its own validator, so options
and registered schemas do not leak to another assertion. Both forms support
`.not`; an invalid schema or non-JSON response is still an error, not a successful
negated check. Local `$ref` references work; other schemas can be supplied with
`options.schemas`, but are never downloaded from the network or read from disk.
Async schemas (`$async`) are not supported. Validation runs within the script's
normal time budget; scripts that do not use schema assertions do not load Ajv.

**Script libraries**: `require('chai')`, `require('lodash')` and
`require('crypto-js')` (CryptoJS 4.2.0, random numbers from the system) are available
(also through `pm.require`). Lodash 4.18.1 is embedded in the app: no Node.js,
npm installation or network download is needed. Use `const _ = require('lodash')`;
there is no global `_` or legacy Lodash 3 alias. It loads on first use and is
cached within that script, but each pre-request or post-response script gets
fresh exports, including on subsequent runs. Library execution uses the normal
script time budget. Updating the embedded library requires an app update.

```javascript
const _ = require('lodash');
pm.test('IDs are unique', () => {
  const ids = _.map(pm.response.json().items, 'id');
  pm.expect(_.uniq(ids)).to.have.lengthOf(ids.length);
});
```

**Insert snippet** above the editor adds common code. Besides addons (see Scripts above), only these exact module names are supported, not npm packages, file paths,
URLs or Lodash submodules such as `lodash/fp`. Other Postman sandbox libraries
(moment, cheerio and so on), `pm.visualizer` and timers are not
available. Lodash functions that need timers (`delay`, `defer`, `debounce`,
`throttle`) are therefore not supported.

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
- **Current values** in Variables override shared environment values locally.
  They are masked by default, saved locally in plaintext and omitted from
  Postman environment exports. Use Vault when encryption is required. Script
  updates to an existing Current value stay local too.
- **Dynamic variables** follow Postman's names and are generated for every
  occurrence on every send, written as `{{$guid}}`. In scripts, use
  `pm.variables.replaceIn('{{$randomFirstName}}')`. Names are case-sensitive;
  a defined variable with the same name wins, even if its value is empty.
  Unknown names stay unresolved. The 32 supported names are:
  - **Common**: `$guid`, `$randomUUID`, `$timestamp`, `$isoTimestamp`,
    `$randomInt` (0–1000), `$randomBoolean`, `$randomAlphaNumeric`,
    `$randomColor`, `$randomHexColor`.
  - **Network and accounts**: `$randomIP`, `$randomIPV6`, `$randomMACAddress`,
    `$randomProtocol`, `$randomSemver`, `$randomUserName`, `$randomEmail`,
    `$randomExampleEmail`, `$randomPassword`, `$randomDomainName`,
    `$randomDomainSuffix`, `$randomDomainWord`, `$randomUrl`.
  - **People and location**: `$randomFirstName`, `$randomLastName`,
    `$randomFullName`, `$randomPhoneNumber` (ten digits, `###-###-####`),
    `$randomCountryCode`, `$randomLatitude` (−90 to 90), `$randomLongitude`
    (−180 to 180). Coordinates have four decimal places.
  - **Other test data**: `$randomPrice` (0.00–1000.00, two decimal places),
    `$randomWeekday`, `$randomMonth` (English names).

These use small built-in sample vocabularies, not the full Faker dataset or its
locale coverage. Generated fields are independent: a full name is not tied to
separate first/last-name variables. Generated domain names, URLs and email
addresses use reserved `example.com`, `example.net` or `example.org` domains;
`$randomDomainSuffix` chooses `com`, `net` or `org`. Generation does not access the
network, and does not guarantee that phone numbers or IP addresses are reserved
or reachable. Passwords and the other random values are test data, not production
credentials.

#### Import and export

**Import…** in the sidebar footer, **Session → Import…** and the command
palette read a Postman Collection v2.0 or v2.1 file, an environment or a
globals export, a HAR file or an OpenAPI 3.0 or 3.1 document (JSON or YAML),
telling them apart by their contents.

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

Saved examples become the requests' examples, both ways. Postman binary file
bodies retain `file.src` as a local file reference, without reading the file
during import or export. What has no equivalent yet is listed after the import:
request descriptions, auth other than Bearer, Basic, API key, OAuth 2.0, Digest, AWS
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
Checks have no Postman form, so they are written as `pm.test` code after the
request's own post-response script, one test per check under the same name,
reporting the same pass or fail. Numbers compare as JavaScript numbers there, so
two integers beyond 2^53 that Checks tells apart may compare equal. Invalid
Checks are left out with a warning. Importing the file back gives a script, not
Checks. **Export as Postman…** in
**Variables** writes the environment or globals on screen. The common export
options control auth, variables and scripts; the default script removal also
removes generated Checks. Vault references are never resolved to secrets for export.

**Vault…** (sidebar footer, request **⋯** menu or command palette) keeps secrets
as name, value and optional allowed domains. Reference one anywhere a variable
works with `{{vault:name}}`. It is unlocked only for a send whose target host is
allowed: `example.com` matches only that host, `*.example.com` only its
subdomains, and a secret without domains goes to any host. A refused or missing
secret fails the send before anything leaves. Vault values are never copied into
environments, Request variables or script variables. Scripts reach it only
through `pm.vault`, when allowed; the request that was sent, including the header that carried the
secret, is still retained in Capture and History.

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
Each delivered attempt also appears in **History**, including failures and
in-flight cancellation; **Inspect retained run** opens its immutable snapshot
and Console.

Each open request owns its draft, editor selection, pending send and response. Concurrent
sends, even to the same URL, retain their own results and run the script that
was present when sent. Script variable updates go to the originating environment
without overwriting unrelated variables changed by another request. Reopening a
saved request shows its open draft instead of replacing unsaved edits.
A dot marks unsaved changes; closing offers **Cancel**, **Discard** or
**Save & close**. A failed save leaves the draft open. A request that is sending cannot be
closed (**×** in its header) until the send finishes. Open drafts are restored from the private
experiment checkpoint described below; editor selection and layout are session-local.

A captured HTTP request is itself a request document: choosing its row shows
**Captured · time · process**, an editable method, URL and **Send**, the editor's
tabs led by **Original** (the capture's own request readers) and, beside them,
the captured response until the document is sent. The document holds the
complete decoded UTF-8 payload and duplicate headers; encoding/framing headers
are removed. Missing/incomplete content, non-UTF-8, trailers and CONNECT/tunnels
stay read only and say why; a body kept on disk is read only after **Edit
request**. The first edit or send keeps a draft with the capture, marked
**Edited** in its header and by a dot in its row; the capture itself never
changes and nothing sends without **Send**. **Discard edits** in the inspector's
**⋯** menu returns to the capture as it was. **Save to collection** asks for a
name and collection, then saves it to the library. Edits stay with the capture
for the session and go when it is cleared. Source identity and bounded source evidence accompany the
saved request across restart.

**Save request** keeps the editor's request in the request library with
a stable ID: renaming or moving it updates the same asset. Unrelated same-name
assets are not silently overwritten. **Duplicate as new request** creates a new
identity. Imports update matching IDs and rename unrelated name collisions. The
sidebar groups requests by collection; share a collection with its Postman
export. Right-click a request for
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

**History** in the collection **⋯** menu opens retained attempts. The request's
**History / Examples** column and **⋯ → History** scope the same store by request
identity or capture provenance, never by URL. Status and source filters narrow
the results; **Run details…** opens notes, baseline and comparison actions.

### Regression cases

Right-click an HTTP capture and choose **Save as regression…**. This opens an
unsent Requests draft linked to that capture, with its status as the first
assertion. Review/edit the request, use `{{base_url}}` and other variables for
targets/credentials, then **Save request**. Capture conversion uses the same full
decoded request contract as editing a capture, not a shell/cURL round-trip or UI
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

Regression cases are ordinary collection requests with Checks. Share them with
Postman export and the common redaction options above; import through the normal
Postman importer. The separate `vscroxy-regression` sharing/import format is retired.

### cURL

**Import cURL…** is available at the bottom of the Requests sidebar, in
**Session**, and in the native **File** menu. Paste one Bash/POSIX command to
open a new, unsent tab without replacing another draft or saving it automatically.
Single/double quotes, escaped newlines, Bash `$'…'` strings, inline short options
and `--option=value` are supported. Import reads literal request data only:
URL, method, repeated headers, cookies, Basic credentials, user agent, referer,
JSON/raw/form-encoded bodies and `--get` query data. Shell/environment/glob
expansion is never run. `{{variables}}` remain placeholders.

`--location` / `-L` follows up to 50 HTTP(S) redirects; `--no-location`
disables it again (last option wins). Without these flags, imported requests do
not follow redirects, matching curl rather than the workspace's default.
This policy survives saving and reopening a native request and applies to Send
and Runner. With 301/302/303, an inferred POST becomes GET and loses its body;
an explicit `--request` / `-X` retains the chosen method, but still loses the body.
307/308 retain the method and body. Explicit body headers survive; curl's implicit
form Content-Type disappears with the body. No automatic Referer is added.
Authorization is not forwarded to another scheme/host/port, and literal cookies
are not forwarded to another hostname; stripped credentials stay stripped on
later hops. Workspace cookie-jar rules still apply. Custom credential headers
are ordinary headers and can cross origins, as in curl: review redirect targets.
Adding declarative Checks still stops at the original response, as for other
workspace requests. Copy/code generation represents the initial wire request,
not this saved transfer policy.

The importer never executes curl or reads local files/stdin. It rejects pipelines,
substitutions, shell redirections, assignments, multiple URLs, `@file` bodies/headers,
multipart file uploads and unsupported options. Connection/security/redirect
options such as `--insecure`, `--location-trusted`, `--max-redirs`, `--post302`
and `--config` are rejected rather than silently changing behavior. Output flags
and `--compressed` are accepted but not
applied; sending uses this app's proxy/client, not curl's transfer settings.
PowerShell and Windows CMD quoting are not supported. Commands are limited to
16 MiB.

**Generate code…** (request **⋯** menu or command palette) shows the draft as
cURL, JavaScript `fetch`, Node.js `fetch`/axios, PowerShell, Python requests,
Go `net/http`, HTTPie, Swift `URLSession`, Kotlin OkHttp, Java `java.net.http`,
C# `HttpClient`, PHP cURL, Ruby `Net::HTTP` or Rust `reqwest`, with its auth and
encoded body, and copies it. The **Copy as…** menu offers cURL, PowerShell,
fetch and the seven native-language snippets; **Generate code…** also has
axios, Python requests, Go and HTTPie. Captured requests use the original
exchange. Like cURL, it leaves `{{variables}}` as written and runs no script.
Each language is written to send the same method, URL, headers and body bytes;
libraries still add their own defaults (`User-Agent`, `Accept`,
`Accept-Encoding`), `fetch` and requests join repeated headers into one, while
Ruby `Net::HTTP` and C# `HttpClient` join repeated values with `, `. What a
language cannot send exactly is refused with a reason instead: a binary
body with no source file (use cURL), `fetch` with a body on GET or a `Host`
other than the URL's, HTTPie with values it would alter, OkHttp with a
GET/HEAD body, and Java, Kotlin and C# with a non-ASCII header value (their
HTTP clients reject or replace it). Kotlin needs OkHttp and Rust needs reqwest
with the blocking feature; the other snippets use standard libraries. A
Binary body draft reads its selected file in every native snippet instead of
inlining the bytes; the generated code reads the saved path as written, so
prefer an absolute path or run the code from the same directory. Multipart
bodies are exported as the assembled bytes, so a multipart body with non-UTF-8
file content still uses cURL. Generating code adds no runtime dependency to
vscroxy.

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
files and export the assembled bytes, including binary parts. Binary drafts
export the selected file's bytes in cURL, not a path reference; native snippets
reference the selected file instead. Captured non-UTF-8 bodies without a source
file use cURL, and the text-only snippets report an error instead of changing
the bytes. Credentials and
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

Contracts are listed in the sidebar's **Collections** group, independently of
the collection they were generated from. The collection **⋯** menu and Commands
offer **New API contract**, **Import OpenAPI…** and **Generate API from
collection…**. Choosing a request returns to the request editor without
discarding its drafts.

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
links are session-local; saved contracts are restored after restart. **API
contracts** in Commands opens the first one.

## Timing

The captured response's **Timing** tab shows a request-local waterfall on one
shared axis. **Requests → response → Timing** shows the same trace for that
exact send, with **Inspect timing in Capture** linking to its final wire hop.
Use **Focus inspector** or scroll the Timing pane when space is limited.

The trace begins at proxy request headers and ends when capture publishes its
snapshot. Observed phases are **Proxy** (client upload, rules and buffering
before upstream activity), **Connection** acquisition, **DNS**, individual
**TCP** attempts, **TLS**, **Send**, **Wait** to the first response byte, and
**Receive** until publication. Bars use actual start offsets, not a cumulative
sum: acquisition can contain DNS/TCP/TLS, and duplex send/receive can overlap.
Never add overlapping durations to infer a total.

Durations display sub-millisecond precision; hover a bar for exact offset,
duration, endpoint detail and status. Zero/subpixel observations use a hairline,
not a fabricated minimum duration. **Failed** marks a failed phase; **Open**
means unfinished at capture, including live streaming bodies. Reused upstream
connections and the bounded trace's truncation are indicated explicitly.
Unobserved phases are omitted, not assumed to take zero time. Local responses
can have only a Proxy phase, and failed dials can still have a trace.

This is **proxy-observed**, not end-to-end client latency. Eager MITM connection
setup before request headers belongs to the connection log, not this request's
DNS/TCP/TLS. Downstream delivery and estimated throttle delay are not separately
measured. SSE and other streaming snapshots stop at capture publication; they
are not a final whole-stream duration.

Session exports and HAR's private **`_vscroxyTiming`** extension preserve exact
nanosecond offsets, overlap and state. HAR's standard wait/receive fields keep
the available aggregate split; standard HAR fields alone do not reconstruct a
request-local trace. Captures without a trace show **Aggregate timing only**
when wait/download are available, otherwise **No timing for this flow**.

## System proxy and certificate

**Settings → Proxy** controls the system proxy; **Settings → HTTPS**
shows the local CA, system trust and domain decryption rules. The native menu also opens
Settings with Cmd/Ctrl+,; Cmd/Ctrl+1–3 go to the sidebar's groups, and
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

**Settings → General → Back up and share** exports one JSON file with the
settings, ignore and allow lists, HTTPS decryption, rules with the response
files they answer from (up to 8 MiB each; folders stay paths), rule scripts, addons,
Host profiles and breakpoint templates. Secrets, variables and this computer's
key log path are never included. **Import…** shows what a file holds and
replaces only the parts chosen; imported mock files go into
`~/.vscroxy/mocks/`, scripts of the same name are replaced.

What you set stays between launches: the dark theme, the sidebar's width, how
the table and the detail pane share the window, the inspector layout and sidebar
visibility, and the columns' order and widths, in `~/.vscroxy/settings.json`.
The window's position and size are
MyGo's own, kept in `window-state.json` under the app's config directory and
restored when they would still show on a connected display.

### Desktop workbench

The default is **Side by side**: requests on the left, the inspector on the
right, from 1000 DIPs of content width; a narrower window stacks them without
changing the choice. **Stacked** puts the inspector under the requests at any
width. Choose either with the two buttons in the title bar or **⋯ → View →
Layout**. Each keeps its own divider position. In the side layout, the table prioritizes URL,
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
| Cycle open requests | Ctrl+Tab / Ctrl+Shift+Tab |
| Close current request | Cmd/Ctrl+W; asks about unsaved changes; unavailable while sending |
| Close window | Cmd/Ctrl+Shift+W (separate from closing a request) |
| Manage Host profiles | Cmd/Ctrl+Shift+H |

Plain Enter in the URL input never sends. Dialogs own their shortcuts: sending,
opening drafts and workspace commands cannot act through a modal. Local response
editors keep their own Find commands. Cmd/Ctrl+1, 2 and 3 go to the sidebar's
Live, Collections and Sessions groups and show what each last showed: the
traffic, the open draft, the historical session. They never pick an individual
draft.

The design takes **Edit and Repeat** from [Proxyman](https://docs.proxyman.com/advanced-features/edit-and-repeat)
and the explicit **Send** chord documented by [Postman](https://blog.postman.com/boost-your-productivity-in-postman-with-these-4-keyboard-shortcuts/).
Unlike [Proxyman's Repeat](https://docs.proxyman.com/advanced-features/repeat),
Cmd/Ctrl+Enter in Capture does not immediately replay traffic. Cmd/Ctrl+K retains
our existing command palette; it is not Postman's resource search. Some official
references are macOS-only or historical, so we deliberately define one consistent
vscroxy mapping instead of silently inheriting conflicting shortcuts.

### Continuous debugging

A capture is edited and sent where it is, in its row's inspector, so the list,
its search, protocol, header/body and triage conditions and a paused list stay
as they were while traffic continues arriving. Escape returns to the list and
Enter reopens the same document, with its edits, response and comparison. In
Live, Cmd/Ctrl+Enter sends only while the focus is in the inspector, so the
shortcut never sends the row under the list's focus by surprise.
Open requests from the library still require save or discard before
closing; open requests, the one shown and their saved/unsaved baseline are
checkpointed privately for restart and never send on restore.
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

### History (retained attempts)

**History…** in Commands opens the same retained store as
**History** in the collection **⋯** menu. It retains independent snapshots of Send and Runner
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
**Clear History…** asks before removing records; it leaves open drafts,
saved requests and captured traffic alone. This is a debugging notebook, not a
complete wire archive: redirect hops remain in Capture and cookies/variables may
have changed since a run.

### Capture-linked History

**History** on a captured exchange or its editable draft opens the same
workbench scoped to that capture's linked open drafts and retained experiments.
Links use stable identities, not method/URL or reusable capture IDs. The immutable
source snapshot, draft identity and run links survive restart. If the source is no
longer in Capture, that button is disabled and the retained snapshot remains
available. **Origin draft** returns to the exact originating tab without replacing
unsent edits; **Open as draft** creates a new editable branch without sending.

**Compare source** compares the original capture snapshot against the experiment's
client request/final response. **Recorded interventions** shows actual captured
scenario/rule actions for the selected wire exchange, never inferred from today's
configuration. Each HTTP(S) Send or Runner attempt has a local correlation identity, including
across redirect hops; it is not sent as an HTTP header. Up to 16 separate wire
exchanges are retained per run. Missing wire evidence is labeled explicitly and
does not imply that no rule ran. Interactive WebSocket sends are not
linked into this HTTP(S) history.

Source/wire snapshots share the experiment checkpoint's 100-run / 32 MiB budget
and 256 KiB-per-body limit, with explicit incomplete-body diagnostics. They may
contain credentials, private URLs and rule details; this is local, unencrypted
debugging history, not sanitized evidence for sharing.

### Sessions

The sidebar's **Sessions** group (Ctrl/Cmd+3) replaces Investigations and the
separate historical reader. **Save evidence to Sessions** in a capture's inspector,
row menu or Commands snapshots that exchange, including retained stream messages.
**Save capture to Sessions** keeps the entire live capture. Later capture changes
never change these snapshots.

**Open session…** accepts multiple session files, up to 48 MiB each. It stores private copies under `~/.vscroxy/sessions/`;
the imported originals are never overwritten. Saved sessions stay in the sidebar
after restart; startup reads only their titles, and a session's payloads load
when it opens. Open sessions have separate tabs, marked **Historical · read-only
payloads**. Closing a tab keeps its saved file and releases its payloads.
**Delete…** in the session bar, or right-clicking it in the sidebar, removes the
saved file. Opening, annotating and comparing
never replace live traffic, change rules, run scripts or send requests.

Choose an exchange and use **Pin**, a color, exchange notes or session notes;
the title and every annotation save as they change. Writes are ordered in the
background and flushed on quit; a failed write shows in the session bar. Headers (including
trailers), bodies, messages and connection diagnostics are read-only; sensitive
header values are masked until **Reveal credentials**. Search matches URL, status,
notes and errors literally, case-insensitively; it does not search payloads.

Choose **Use as left**, switch exchange or session tab, then **Compare with left**
to reuse the header/body Diff reader. Numeric IDs reused by different sessions do
not link exchanges. **Export session…** uses the common redaction review above.
Local sessions remain private, unencrypted historical data, not verified current
network or certificate state.

## Next

- Structured query/auth/form-data editors, cookie management, collection folders
  and a runner with per-request assertions.
- Named capture filters and independent traffic views; complete-body search
  rather than searching only the in-memory capture head.
- WebSocket/SSE message search and detail views, correct upgraded request paths,
  and spill-backed message retention instead of silent truncation.
