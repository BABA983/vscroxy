# Headless collection runs

Build `go build -o bin/vscroxy-cli ./cmd/vscroxy-cli`. This executable has no
MyGo, window, GTK or display dependency. It uses the desktop Runner's execution
engine, request encoding, scripts, checks, auth and Cookie handling.

```sh copy Copy Collection Run
bin/vscroxy-cli run --folder Shop --env ./env.json --environment Local \
  --data ./cases.csv --report-json ./reports/run.json \
  --report-junit ./reports/run.xml ./collections
```

Flags precede the single input. The input is a native **collections root**
(not a selected collection subdirectory) or a Postman v2.0/v2.1 collection file.
`--folder Shop/Orders` includes descendants and keeps ancestor auth/scripts and
variables. Like the desktop Runner, requests are grouped by collection path,
with saved order within each folder. This is not Postman's interleaved
request/folder ordering. Postman import warnings are printed, not silently
discarded.

## Variables and execution

- `--env` explicitly reads a native env.json (shared values plus Current
  overrides) or a Postman environment/globals file. `--environment` selects a
  native environment; otherwise its `active` name is used. There is no automatic
  read of `~/.vscroxy`, desktop Vault or desktop OAuth tokens.
- Repeated `--var KEY=VALUE` overrides environment values. For secrets prefer
  an explicitly selected private environment file: arguments can appear in shell
  history and process listings.
- CSV/JSON `--data` uses the same parser and `pm.iterationData` as the desktop.
  The default iteration count is the data row count, or 1 without data;
  `--iterations 1..1000` overrides it, repeating the last row when necessary.
- Environment, local, global and collection writes carry through the run, but
  are **never written back to input files**. Cookies and acquired OAuth tokens
  live only for that process. Interactive desktop login and Vault writes are
  not supported; supply CI credentials in environment variables/files instead.
- Binary paths remain authored paths, relative to the process working directory.
  GraphQL and other bodies use the same encoding and validation as Send.
- `--bail` stops at the first failed attempt; otherwise the run continues.
  `setNextRequest(null)` deliberately stops the entire run, as on the desktop.
  An unknown next request is incomplete, not a green result.
- `--request-timeout` defaults to 30s, `--timeout` to 5m for the whole execution,
  and `--max-steps` to 10000 attempts including jumps. Both timeouts must be
  positive; max steps is 1..100000. These bounds prevent endless polling scripts.

Each run starts an isolated loopback proxy with an ephemeral CA directory.
It does not change the system proxy, trust store, desktop rules or settings.
HTTPS uses an ordinary CONNECT tunnel, with certificate verification by the
request client; it does not enable HTTPS interception. Temporary runtime and
captured spill files are discarded on exit.

## Results and CI

Exit codes: **0** success, **1** failed HTTP/check/script/transport attempt,
**2** invalid input, incomplete workflow/budget, or report/output error,
**124** whole-run timeout, **130** cancellation (Ctrl+C). Expected error statuses
set in native Checks retain their desktop semantics. A Postman `pm.test` alone
does not override the Runner's default HTTP-error policy.

JSON version 1 reports each request attempt and assertion result. JUnit has one
testcase per request attempt, with assertion results in `system-out`. A cancelled
or otherwise incomplete run adds an error testcase even if no request started.
Reports are generated for execution failures/cancellation, but not setup errors.
Reports use atomic private writes and cannot overwrite any selected input or
write inside the native collections root.

By default, stdout/reports omit URLs, headers, bodies, variables and Console.
Even assertion names/errors are replaced with ordinal names, because scripts can
put secrets in them. Authored request names, IDs and collection labels remain.
`--include-details` opts in to assertion names/errors and execution error text
**which may contain credentials or response content**. Never publish detailed
reports without review. Only run trusted collections: scripts can make network
requests and Binary/Multipart bodies can read local files.

The repository CI runs `docs/examples/ci/collection.json` against a local static
fixture and uploads its JSON/JUnit reports. To use your own API, replace that
fixture server with your test service and pass its URL as `base_url`.

## Control a running desktop

Desktop control is **off by default**. Explicitly set `VSCROXY_CONTROL_FILE` to
a nonexistent file in an existing, private directory when launching the app:

```sh copy Copy Desktop Control Launch
mkdir -p "$HOME/.vscroxy"
chmod 700 "$HOME/.vscroxy"
VSCROXY_CONTROL_FILE="$HOME/.vscroxy/control.json" bin/vscroxy
```

This opens an ephemeral **127.0.0.1-only** endpoint, not the capture proxy port.
The discovery file contains its address and a cryptographically random bearer
token. Treat it as a credential: never commit, share, or pass its contents to an
AI tool. Unix files require current-user ownership and no group/other access;
Windows files have a protected current-user-only DACL from creation, verified
again when opened. Unsupported filesystem protection fails closed. Normal Quit
removes the owned file and cancels jobs; after a crash, check the old process is
gone before removing a stale file. Existing files/symlinks are never overwritten.

Clients reject symlinks, insecure discovery files, non-loopback destinations,
HTTP redirects and environment proxies. The server checks exact Host and bearer
authentication, rejects browser Origin headers, bounds commands to 64 KiB and
responses to 16 MiB, and times out command dispatch after 10s. This is **not a
security sandbox against other processes running as your user**. A token holder
can request raw data and authorize side effects. Only give it to trusted clients.

```sh copy Copy Desktop Queries
bin/vscroxy-cli control --file "$HOME/.vscroxy/control.json" status
bin/vscroxy-cli control --file "$HOME/.vscroxy/control.json" requests.list
bin/vscroxy-cli control --file "$HOME/.vscroxy/control.json" \
  --params '{"collection":"Shop"}' --confirm collection.run
bin/vscroxy-cli control --file "$HOME/.vscroxy/control.json" \
  --params '{"id":"JOB-ID"}' runs.get
```

Flags precede the action. Output is JSON, suitable for shell redirection. Exit
codes are 0 for a successful command, 1 for an application rejection, 2 for
configuration/transport/output errors, and 130 for cancellation. A successful
`*.run` command means **accepted**, not passed: poll `runs.get` until `done`, then
check `report.failed` and `report.incomplete`. Do not automatically retry a
mutation on transport timeout: its outcome may be unknown.

| Action | Parameters / result |
| --- | --- |
| `status` | Proxy, recording, active environment and counts |
| `system-proxy.get` | Poll OS-read snapshot; optional `refresh` starts a new read-only check |
| `system-proxy.set-enabled` | Required boolean `enabled`; returns `accepted:true` and snapshot, then poll `system-proxy.get` |
| `collections.list` | Runnable saved collection/folder names |
| `requests.list` | Optional `collection` (descendants included; explicit empty selects unfiled requests) |
| `requests.get` | Saved `id`; optional `details` |
| `flows.list` | Optional `limit` (default 50, max 200), exclusive `before` ID cursor; `nextBefore:0` ends paging |
| `flows.get` | Live capture `id`; optional `details` |
| `flows.export-har` | Required `ids` (1–200 unique live HTTP IDs, in export order); optional `details`; returns a HAR 1.2 document |
| `rules.list` | Index, fingerprint `key`, name, type and enabled state |
| `request.run` | Saved `id`, optional `environment`, `iterations` (1–1000), `bail`; returns job ID |
| `collection.run` | `collection`, same run options; includes descendants |
| `runs.get` | Job `id`; returns `done` and a redacted report |
| `runs.cancel` | Job `id`; cancellation cannot undo completed external effects |
| `capture.recording` | Required boolean `enabled`; forwarding remains on |
| `collection-recording.get` | Recording configuration, active/busy state, added/updated/skipped/discarded counts and controlled problem |
| `collection-recording.start` | Required `collection` and existing `environment`; optional exact `hosts` and `apps` lists (up to 32 each) |
| `collection-recording.stop` | Stop accepting traffic and discard queued/in-flight conversions; already saved requests remain |
| `rules.set-enabled` | Required `index`, current `key` and boolean `enabled`; persists through the existing rules store |
| `rules.import` | Required `document` string containing a native JSON array of 1–100 rules; append-only, all-or-nothing |

All mutations require `confirm:true`; CLI `--confirm` supplies it. Rule
fingerprints reject stale targets after a concurrent edit/reorder. Each app-state
command and its response snapshot run on the desktop UI thread, not HTTP workers.
Runs execute asynchronously with isolated Cookies/OAuth tokens, the selected
desktop environment and folder inheritance. They do not touch Runner drafts or
persist script variable writes. An already unlocked Vault may resolve scoped
request secrets; remote runs never unlock it or enable script Vault writes.
Desktop proxy rules/Host mapping still apply; interactive OAuth login is not
started. Binary/Multipart paths resolve in the desktop process working directory.

At most one remote run is active, with a 5-minute whole-run deadline, 30s per
request and 1000-attempt budget including script jumps. The most recent 16 jobs
are retained **in memory only**, not desktop History. Polling/canceling requests
does not implicitly cancel the job; use `runs.cancel` or Quit. A deliberate
`setNextRequest(null)` is success; unknown targets/budget exhaustion are incomplete.

Default flow/request queries omit bodies, headers and auth, redact URL credentials,
fragments and query values, but retain authored labels, host/path and parameter
names. This is field redaction, not secret detection. Explicit `details:true`
discloses raw saved requests or live raw headers/URL and binary-safe base64 body
previews (at most 64 KiB per side). Previews never open spill files and report
truncation; they are not a full response download. Session tabs are not queried.
The API deliberately has no arbitrary filesystem action.

### Record live traffic into a collection

Collection recording is separate from the capture Record/Pause switch. It is
off at launch and never resumes automatically. Start with a nonempty collection
name (new or existing) and an **existing** environment for generated variables.

In the desktop, use Capture's More menu, Collections' options, the command
palette, or a host/App sidebar menu → **Record into collection…**. Review the
local-save notice before Start. The persistent status chip opens scope/counters
and can stop recording even when a CLI/MCP client started it. Closing the dialog
does not stop recording; its explicit Stop button does.

```sh copy Copy Collection Recording
bin/vscroxy-cli control --file "$HOME/.vscroxy/control.json" \
  --params '{"collection":"Recorded API","environment":"Local","hosts":["api.example.com"],"apps":["Browser"]}' \
  --confirm collection-recording.start
bin/vscroxy-cli control --file "$HOME/.vscroxy/control.json" collection-recording.get
bin/vscroxy-cli control --file "$HOME/.vscroxy/control.json" \
  --confirm collection-recording.stop
```

Only subsequently completed ambient HTTP traffic is eligible. Host filters are
case-insensitive exact hostnames (any port) or host:port values; App filters are
exact labels from the desktop sidebar. Entries within a list are OR; the host
and App lists intersect. Empty/omitted lists mean all. Static resources,
CONNECT, streams, network failures, editor/Runner sends and replays are excluded.
Table search, view filters and importing sessions do not affect this scope.

Endpoints group by origin, method and path template, inferring whole numeric or
UUID segments, not arbitrary slugs. New requests are organized by path prefix,
with stable namespaced host/path variables in the selected environment. Existing
variables are never overwritten; the active environment is not switched.
Authorization, Cookie, X-API-Key and API-Key headers become local **Current**
references, including in Examples; response Set-Cookie is removed. Current values
are plaintext local storage, excluded from environment exports, not Vault
encryption. Other headers, URL queries and request/response bodies may still
contain secrets: review recorded collections before sharing them.

Repeated endpoints preserve the authored request and update only the recorder's
own Example: latest success (2xx) and one per other HTTP status. Manual Examples
and unsaved request edits are retained. Ambiguous matches stop recording rather
than choose an arbitrary saved request. Deleted/moved recording targets also
stop the session. Subsequent captures never change the initial request's path
variable values or its authored payload/scripts/auth.

Only complete UTF-8 bodies fitting the text-request/Example contract are saved,
not previews or warning messages. Spill reads are bounded to 5 MiB per side;
queued captures are limited to 1 MiB of in-memory payload/metadata each, with
32 queued captures and one conversion worker. Accepted spills stay owned until
conversion/discard even if table retention evicts their rows. Ineligible or
unavailable bodies increment `skipped`. Recorder-queue overload, upstream capture
loss or guarded-save conflicts **stop** the recorder with a controlled `problem`;
resolve the cause before explicitly starting again.
Variables save before their requests; a later library write failure can leave
unused local variables, never a request referencing unsaved credentials.

Stop is a write barrier, **not a flush**: queued/in-flight conversions are counted
as `discarded`. `busy` may remain true until an outstanding read returns; a new
session cannot start before it finishes, and its result cannot write after Stop.
Pausing capture or quitting also stops collection recording; resuming capture
does not implicitly restart it. Already saved collection assets survive Quit.
Opening the target environment editor stops recording before its draft snapshot;
close that editor before starting again. Concurrent variable changes or deletions
reject publication rather than overwriting an authored shared/Current value.

### Control the system proxy

`system-proxy.set-enabled` requires `confirm:true` (CLI `--confirm`, or MCP
startup `--allow-write`). It uses the existing desktop proxy session: enable
requires a live capture listener; disable restores the owned previous settings,
or follows the desktop's explicit Disable behavior without ownership. This may
change proxy settings used by other applications and may prompt for OS permission.

```sh copy Copy System Proxy Control
bin/vscroxy-cli control --file "$HOME/.vscroxy/control.json" \
  --params '{"enabled":true}' --confirm system-proxy.set-enabled
bin/vscroxy-cli control --file "$HOME/.vscroxy/control.json" system-proxy.get
```

Accepted is **not verified success**. Poll until `checking` and `applying` are
false, then inspect `checked`, `status` and `problem`. The status is `unknown`,
`off`, `matches`, `mismatch` or `automatic`; `matches` means the OS configuration
points at this app, not that every application obeys it. While an operation is
pending, the snapshot may describe the previous completed read. Changed capture
addresses invalidate old verdicts. Raw OS configurations, PAC URLs and helper
errors are not disclosed.

The first `system-proxy.get` starts a read-only OS check if none has completed.
Later gets poll the cached result; `refresh:true` requests a fresh check and
coalesces with any pending operation. Queries never run recovery writes. Changes
are rejected while another check/write or Quit is active. Accepted OS operations
continue independently of client cancellation; there is no remote cancellation
of a permission prompt. The existing ownership/recovery/normal-Quit restoration
path remains responsible for cleanup. No arbitrary proxy target is accepted.

### Export captured HTTP traffic

Select explicit IDs from `flows.list`; imported sessions and WebSocket/SSE
streams are not HAR exports. Missing IDs reject the entire export rather than
silently dropping traffic. Default exports use the desktop's field-redaction
policy: omit bodies/cookies, redact header and query values, and remove URL
credentials/fragments. They never read retained body files. Host/path, field
names, Content-Type/Accept and timing/size metadata remain; review before sharing.

```sh copy Copy HAR Export
bin/vscroxy-cli control --file "$HOME/.vscroxy/control.json" \
  --params '{"ids":[42,41]}' --output ./capture.har flows.export-har
```

`--output` writes successful JSON atomically and privately **on the CLI machine**,
not through a desktop filesystem action. Application/transport errors leave any
old destination untouched. Without it the JSON goes to stdout. The discovery
file cannot be an output destination, including aliases to it.

Explicit `details:true` exports original headers/URLs and complete retained
bodies (binary-safe base64 where needed). It may expose credentials or private
content. Body I/O happens off the UI thread. Each export is bounded to 1 MiB of
source metadata, 4 MiB of aggregate body bytes and 8 MiB of encoded HAR. Raw
exports fail on missing/incomplete/oversized bodies instead of substituting a
preview or display warning. Select smaller batches when limits are exceeded.

### Import or create rules

`rules.import` appends a native rules-file JSON array. A one-element array
creates one rule. The `document` parameter contains **text**, not a desktop
filename. CLI clients can read a local file and construct that parameter with jq:

```sh copy Copy Rule Import
bin/vscroxy-cli control --file "$HOME/.vscroxy/control.json" \
  --params "$(jq -cn --rawfile document ./rules.json '{document:$document}')" \
  --confirm rules.import
```

The 64 KiB command limit still applies, including JSON string escaping. Each
batch is validated with the existing rules contract, including scenario-state
consistency against current rules. Unknown fields, invalid rules, an external
edit or a save failure reject the entire batch. Existing rule order and unsaved
editor drafts remain unchanged; the saved batch applies to new requests only.
The result gives `imported` and `total`; use `rules.list` for new indices/keys.

Import is **not idempotent**: a repeated accepted call appends duplicates. After
an ambiguous timeout, inspect `rules.list` rather than retrying blindly. Only
import trusted rules. Map Local targets may serve files already accessible to
the desktop process; Map Remote changes where future requests are sent, and
breakpoints may pause them. No new mock files are created by import, and no
additional filesystem/network sandbox is implied by `--allow-write`.

## MCP stdio server

Build `go build -o bin/vscroxy-mcp ./cmd/vscroxy-mcp`. This separate executable
uses the official Model Context Protocol Go SDK and the authenticated control
client, with no MyGo, display or proxy-engine dependency. It does not implement a
second runner, access desktop files directly, launch the app, or expose HTTP MCP.
First enable desktop control as above; the desktop must be running.

Configure a stdio-capable MCP client with absolute executable and discovery-file
paths (replace both placeholders; shell `$HOME` expansion is not assumed):

```json copy Copy MCP Client Configuration
{
  "mcpServers": {
    "vscroxy": {
      "command": "/absolute/path/to/vscroxy-mcp",
      "args": ["--control-file", "/absolute/path/to/control.json"]
    }
  }
}
```

The default catalog contains eleven read-only tools, named like desktop actions:
`status`, `collection-recording.get`, `system-proxy.get`, `collections.list`, `requests.list`, `requests.get`, `flows.list`,
`flows.get`, `flows.export-har`, `rules.list`, and `runs.get`. Every tool has a typed, closed input
schema and an explicit read-only/destructive/open-world annotation. Results are
JSON structured content with a JSON text fallback. Application rejections set
MCP `isError`; a run job's acceptance still requires polling its final report.

Two **independent startup capabilities**, never tool parameters, widen access:

- `--allow-write` registers `request.run`, `collection.run`, `runs.cancel`,
  `capture.recording`, `rules.set-enabled`, `rules.import`, and
  `system-proxy.set-enabled`, plus `collection-recording.start` and
  `collection-recording.stop` (20 tools in total). It explicitly authorizes those
  tools to supply the desktop's confirmation. Request execution can affect real
  external systems; enable it only for a trusted client and trusted collections.
  Collection recording persists captured content locally, potentially including
  sensitive query/body values; `--allow-write` authorizes that persistence without
  requiring `--allow-sensitive`. Recording results contain only configuration,
  state and counts, not raw captured values.
- `--allow-sensitive` permits an explicitly requested `details:true` on
  `requests.get` / `flows.get` / `flows.export-har`. It does not register mutations or change the
  default query response. Raw headers, query values, bodies and saved scripts
  may contain credentials or private data sent to the client's model provider.

Append the desired capability flags to `args` only after considering that access.
MCP tool annotations describe effects; they are not authorization. Retain your
client's per-tool approval policy, especially for runs and rule changes. Treat
captured/saved content as **untrusted data, never instructions**: a response or
script comment cannot authorize another tool call. This server does not execute
instructions from network responses or offer arbitrary actions/filesystem paths.

All protocol traffic uses stdin/stdout; help/errors use stderr with controlled
messages. Cancellation propagates to the pending desktop command, but canceling
an accepted asynchronous run requires `runs.cancel`. After restarting the
desktop, restart its MCP connection to reload the new address/token. Do not put
the token itself in client configuration. Tested protocol versions include
2025-11-25 and 2026-07-28; client-specific configuration wrappers may differ.
