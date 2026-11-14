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
- `pm.execution.runRequest` in `run` reaches any request in the collection,
  including ones outside `--folder`. `load` has no request lookup, so those calls
  fail with “not available in this run”.
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

## Built-in collection load runs

The desktop Collection Runner's **Load test…** opens a separate fixed-concurrency
load panel. Choose a saved collection/environment, 1–64 users and 1s–10m duration;
optionally stop on first failure. It uses saved library order, not unsaved editor
or Runner reorder/enable drafts. The native panel uses 30s per execution and a
100,000-primary-attempt budget. Closing the panel leaves work active; reopen it
to Stop. Quit cancels and joins the worker. Results are volatile aggregates, not
individual History/experiment entries. Ordinary native/remote collection runs
and loads are mutually exclusive.

The headless equivalent uses the same Runner execution lifecycle:

```sh copy Copy Collection Load
bin/vscroxy-cli load --folder Shop --env ./env.json --environment Local \
  --users 4 --duration 30s --report-json ./reports/load.json ./collections
```

See the [load CLI flags and exit codes](../README.md#headless-load-runs). Only
load-test systems you are authorized to exercise: collections, scripts and
authentication can make real, destructive network calls. This is a **closed
workload**, not a target RPS/arrival rate, ramp, distributed generator or
capacity benchmark. Existing Runner per-send connections, proxy forwarding,
rules/Host mapping, scripts and throttles still contribute overhead. Standalone
CLI has a fresh proxy without desktop rules; desktop runs use its current rules.

Each virtual user retains its own environment/local/global/collection variables,
Cookie jar and fresh OAuth token cache across loops. No script writes reach
environment/collection/Vault files, and script Vault access is disabled. Desktop
loads can read start-time snapshots of already-unlocked scoped Vault secrets;
interactive login/unlock is not started. An explicit `setNextRequest(null)` ends
that user's sequence; unknown targets stop the load with an execution error.
HTTP(S) only; variable/script-produced WebSocket targets are refused too.

No load exchange is added to capture lists, retained body files, collection
recording or experiments, including OAuth, redirects and `pm.sendRequest`.
Normal simultaneous capture remains enabled. Proxy buffering and Replay's
existing 8 MiB response preview for scripts/checks still apply: this is not a
zero-overhead network client or an unlimited body-processing mode.

Reports aggregate totals and per-selected-request metrics keyed by index/ID,
never runtime errors, assertion names, Console, URLs, headers, bodies or variable
values. HdrHistogram Go 1.4.0 (MIT), microseconds and three significant digits,
computes mean/P50/P90/P95/P99/max **execution** times: the entire Runner Execute,
including scripts, auth, primary HTTP and checks, for completed attempts including
failures. Skips and load interruptions are excluded. Samples outside the one-hour
histogram range increment `latencyOverflow` rather than silently clamping.
`completed` excludes skips/interruptions; `failed` is a subset of completed;
`failureRate=failed/completed`; `requestsPerSecond=completed/elapsed seconds`,
not wire requests/s. Primary response counts/bytes exclude redirects and
auxiliary calls. A per-request timeout counts as failure; normal duration
completion cancels in-flight attempts into `interrupted`, not failed latency
samples. No coordinated-omission correction or open-model latency claim is made.

## Mock server

`vscroxy-cli mock` serves a collection's saved examples over HTTP, as Postman's
mock servers do, so a front end or CI job can point at it instead of the real API:

```sh copy Copy Mock server
bin/vscroxy-cli mock --folder Shop --env ./env.json --environment Local \
  --port 8080 ./collections
```

It prints the address it listens on. `--addr` defaults to `127.0.0.1:0`, a
free loopback port; `--port` replaces the port in `--addr`. Each example answers
at the method and URL it was saved with. A `:name` or `{{name}}` path segment
matches any one segment, the scheme, host and query are ignored, and
`{{variables}}` are filled from globals, the environment and `--var`, in that
order. The route's first example answers unless the request chooses another:
`X-Mock-Response-Name: <example name>` picks an example by name, and
`X-Mock-Response-Code: <status>` picks the first example with that status; the
name wins when both are sent. `x-mock-match-request-body: true` limits the
choice to examples whose saved request body equals this request's body, after
variables are filled in, and a request with no such example gets 404. A route
that matches nothing returns 404. Ctrl-C stops
the server with exit code 130. An address other than loopback prints a warning,
because anyone who can reach it gets the examples without authentication. A
collection with no saved examples is refused. There is no MCP tool for mock
servers yet; the desktop start control is separate.

## Control a running desktop

Desktop control is **off by default**. Turn on **Settings → General → Local
automation** to run the endpoint at `~/.vscroxy/control.json`. The switch is kept
across launches, is not part of an exported configuration, and turning it off
stops the endpoint and removes the discovery file.

For a custom path or a scripted launch, explicitly set `VSCROXY_CONTROL_FILE` to
a nonexistent file in an existing, private directory when launching the app. That
variable wins over the Settings switch:

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
| `flows.list` | Optional `limit` (default 50, max 200), exclusive `before` ID cursor, and filters `host` (host or host:port, case-insensitive), `method`, `status` (100–999), `since`/`until` (RFC 3339, inclusive); `nextBefore:0` ends paging |
| `flows.get` | Live capture `id`; optional `details` |
| `flows.export-har` | Required `ids` (1–200 unique live HTTP IDs, in export order); optional `details`; returns a HAR 1.2 document |
| `rules.list` | Index, fingerprint `key`, name, type and enabled state |
| `request.run` | Saved `id`, optional `environment`, `iterations` (1–1000), `bail`; returns job ID |
| `collection.run` | `collection`, same run options; includes descendants |
| `collection.load` | Required `collection` (explicit empty = root), `users` (1–64), `durationSeconds` (1–600); optional `environment`, `requestTimeoutSeconds` (1–60, default 30), `maxAttempts` (1–1000000, default 100000), `bail`; returns job ID |
| `runs.get` | Job `id`; returns `done` and a redacted report; load jobs add `kind:"load"` and aggregate `report.total`/`report.requests` |
| `runs.cancel` | Job `id`; cancellation cannot undo completed external effects |
| `capture.recording` | Required boolean `enabled`; forwarding remains on |
| `collection-recording.get` | Recording configuration, active/busy state, added/updated/skipped/discarded counts and controlled problem |
| `collection-recording.start` | Required `collection` and existing `environment`; optional exact `hosts` and `apps` lists (up to 32 each) |
| `collection-recording.stop` | Stop accepting traffic and discard queued/in-flight conversions; already saved requests remain |
| `token-links.analyze` | Required `ids` (1–200 unique positive HTTP IDs); returns analysis `id` and `done` |
| `token-links.get` | Analysis `id`; returns `done`, masked source/path/consumer evidence, `skipped` and controlled `problem` |
| `token-links.prepare` | Analysis `id`, zero-based `index`, required `collection` (explicit `""` = root), existing `environment`, new `variable`; returns a proposal |
| `token-links.apply` | Reviewed proposal `key`; persists scripts/references and an empty private Current slot; requires confirmation |
| `rules.set-enabled` | Required `index`, current `key` and boolean `enabled`; persists through the existing rules store |
| `rules.import` | Required `document` string containing a native JSON array of 1–100 rules; append-only, all-or-nothing |
| `rules.create` | Required `rule` object with typed fields (`type`, `match`, and the optional fields of a rule); validated as a whole list before saving; returns `index` and `key` |
| `rules.update` | Required `index` and current `key`, plus a `rule` object; fails while the rule editor is open |
| `rules.delete` | Required `index` and current `key`; fails while the rule editor is open |
| `flows.clear` | Removes unpinned captured flows from the live table; pinned flows stay |
| `flows.annotate` | Required `id`; optional `pinned`, `color` (Red, Orange, Yellow, Green, Blue, Purple, or empty) and `note` (up to 4096 bytes); only supplied fields change |
| `breakpoints.list` | Paused exchanges: the one on screen (`current:true`) and queued ones, with ID, phase, method, redacted URL and status; no bodies or headers |
| `breakpoints.resolve` | Required `id` (must be the breakpoint on screen) and `outcome`: `continue` (unchanged) or `abort` |
| `ssl.get` | Whether all traffic is decrypted, the listed domains, and the count of listed applications |
| `ssl.update` | Optional `all` and `domains` (replacement list of host names, no schemes or paths); application entries are kept |
| `sessions.list` | Saved sessions: `key`, title, exchange count and creation time |
| `sessions.save` | Saves the live table as a new session file in the desktop; returns its `key` |
| `sessions.open` | Required `key` from `sessions.list`; shows that session in the desktop |

Pin, color and note are the same marks the desktop's Mark dialog sets. Session
files contain captured bodies and headers, as the desktop's own session files do.

All mutations require `confirm:true`; CLI `--confirm` supplies it. Rule
fingerprints reject stale targets after a concurrent edit/reorder. Each app-state
command and its response snapshot run on the desktop UI thread, not HTTP workers.
Runs execute asynchronously with isolated Cookies/OAuth tokens, the selected
desktop environment and folder inheritance. They do not touch Runner drafts or
persist script variable writes. An already unlocked Vault may resolve scoped
request secrets; remote runs never unlock it or enable script Vault writes.
Desktop proxy rules/Host mapping still apply; interactive OAuth login is not
started. Binary/Multipart paths resolve in the desktop process working directory.

At most one native/remote collection run or load is active. Ordinary remote runs
have a 5-minute whole-run deadline, 30s per request and 1000-attempt budget
including script jumps; loads use their separately bounded options. The most recent 16 jobs
are retained **in memory only**, not desktop History. Polling/canceling requests
does not implicitly cancel the job; use `runs.cancel` or Quit. A deliberate
`setNextRequest(null)` is success; unknown targets/budget exhaustion are incomplete.

```sh copy Copy Desktop Load
bin/vscroxy-cli control --file "$HOME/.vscroxy/control.json" \
  --params '{"collection":"Shop","users":4,"durationSeconds":30}' --confirm collection.load
bin/vscroxy-cli control --file "$HOME/.vscroxy/control.json" \
  --params '{"id":"JOB-ID"}' runs.get
bin/vscroxy-cli control --file "$HOME/.vscroxy/control.json" \
  --params '{"id":"JOB-ID"}' --confirm runs.cancel
```

Starting a load means accepted, not passed. Poll `done`; inspect
`report.reason`, `report.total.failed` and `report.total.interrupted`. Normal
duration completion has reason `duration`; `finished` means all users explicitly
ended their sequences. `failure`, `attempt-limit`, `cancelled`, `timed-out`,
`execution-error` or `invalid-input` must not be presented as successful duration
completion. Do not blindly retry a start after a transport timeout.

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

### Recognize and apply token links

Capture More → **Recognize token links…** analyzes selected captures (or the
newest 200 HTTP captures). After stopping collection recording, its status form
also offers **Review recorded token links** with the recorded collection and
environment preselected. Recording retains only successfully saved evidence in
memory: up to 200 exchanges / 4 MiB including metadata, with responses at most
256 KiB each. `collection-recording.get.evidenceIds` lists the retained IDs;
they remain analyzable after table eviction but disappear on dismissal, a new
recording session or restart. Missing evidence fails rather than being invented
from saved Examples.

Inference is conservative: complete successful JSON responses with string
`token`, `access_token`, `accessToken`, `id_token` or `idToken` fields matching an
exact subsequent Bearer Authorization value on the **same origin**. The source
must finish at least 1 ms before the estimated consumer start. Ambiguous fields
or sources are excluded; this is evidence, not proof of authentication. Cookies,
refresh flows and multi-hop dependencies are not inferred. Imported HTTP
snapshots may be analyzed. Body I/O runs off the UI thread; metadata is capped
at 64 KiB per flow, responses at 256 KiB and aggregate response reads at 4 MiB,
with a 10-second analysis deadline and bounded JSON traversal. Uninspectable
responses count as skipped; previews are never treated as complete bodies.

```sh copy Copy Token Link Workflow
bin/vscroxy-cli control --file "$HOME/.vscroxy/control.json" \
  --params '{"ids":[41,42]}' token-links.analyze
bin/vscroxy-cli control --file "$HOME/.vscroxy/control.json" \
  --params '{"id":"ANALYSIS-ID"}' token-links.get
bin/vscroxy-cli control --file "$HOME/.vscroxy/control.json" \
  --params '{"id":"ANALYSIS-ID","index":0,"collection":"Shop","environment":"Local","variable":"linked_token"}' token-links.prepare
# Inspect the generated fragments and resetBeforeSetup before applying.
bin/vscroxy-cli control --file "$HOME/.vscroxy/control.json" \
  --params '{"key":"PROPOSAL-ID"}' --confirm token-links.apply
```

Poll `get` until `done:true`, then check `problem` and the evidence. Analyze and
prepare are read-only **staging**, not idempotent queries: they replace the one
ephemeral analysis/proposal shared with the native UI. IDs expire ten minutes
after analysis starts; replacement, closing the preview, Quit and restart also
invalidate them. A successful proposal can only be applied once. Do not blindly
retry staging or apply after a transport failure; inspect current state first.
MCP exposes analyze/get/prepare by default; apply requires `--allow-write`.
None returns token values or requires `--allow-sensitive`.

Preparation requires each capture to match exactly one existing saved request
in the destination collection/subfolders, using the chosen environment. It
creates no requests. Apply preserves authored scripts: source Pre clearing is
prepended, extraction and consumer guards appended; consumers must use explicit
Bearer Auth or one explicit matching Authorization header. Other/inherited Auth,
dirty or sending drafts, changed targets/scopes, variable collisions, open target
environment editors and active/stopping recorders are refused. Review authored
scripts for deliberate overrides; this is not a script sandbox.

The proposal contains only saved request IDs, the selected environment/variable,
generated `sourcePre`, `sourcePost`, `consumerPre`, and `resetBeforeSetup`.
This native source binding invalidates the previous token **before checks and
all inherited/authored setup**, including script compilation failures. Extraction
checks status, JSON path and token syntax; generated JSON errors never include
the response parser's potentially sensitive exception. Consumers fail before
sending if the slot is missing, empty, invalid or shadowed. The slot starts
**empty**: captured credentials are never seeded. Run the source before its
consumers; subsequent iterations use freshly extracted tokens.

Current is plaintext local storage, **not Vault**; environment export excludes
it. Desktop remote runs and CLI runs keep variable changes isolated rather than
writing them back to desktop files. The empty Current slot is saved before the
request files: a failed multi-file save can leave the unused slot and partial
files, so reload before preparing again. Native bindings survive editor saves;
Postman export warns that it cannot preserve pre-setup invalidation. Exported
script fallback remains, but cannot cover inherited or compilation failures in
another runner.

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

The default catalog contains seventeen read-only tools, named like desktop actions:
`status`, `collection-recording.get`, `system-proxy.get`, `collections.list`, `requests.list`, `requests.get`, `flows.list`,
`flows.get`, `flows.export-har`, `breakpoints.list`, `ssl.get`, `sessions.list`, `rules.list`, `runs.get`, `token-links.analyze`,
`token-links.get`, and `token-links.prepare`. Analyze/prepare replace ephemeral
staging despite being read-only; they are not idempotent. Every tool has a typed, closed input
schema and an explicit read-only/destructive/open-world annotation. Results are
JSON structured content with a JSON text fallback. Application rejections set
MCP `isError`; a run job's acceptance still requires polling its final report.

Two **independent startup capabilities**, never tool parameters, widen access:

- `--allow-write` registers `request.run`, `collection.run`, `collection.load`, `runs.cancel`,
  `capture.recording`, `rules.set-enabled`, `rules.import`, `rules.create`, `rules.update`, `rules.delete`,
  `system-proxy.set-enabled`, `collection-recording.start`, `collection-recording.stop`, `token-links.apply`,
  `flows.clear`, `flows.annotate`, `breakpoints.resolve`, `ssl.update`, `sessions.save`, and `sessions.open`
  (37 tools in total with the read-only set). It explicitly authorizes those tools to supply the desktop's
  confirmation. Clearing, annotating, resolving breakpoints, changing HTTPS inspection domains and rules
  change what the desktop captures or sends; `sessions.save` writes captured bodies to a local file. Request execution can affect real
  external systems; enable it only for a trusted client and trusted collections.
  Collection recording persists captured content locally, potentially including
  sensitive query/body values; `--allow-write` authorizes that persistence without
  requiring `--allow-sensitive`. Recording results contain only configuration,
  state and counts, not raw captured values.
- `--allow-sensitive` permits an explicitly requested `details:true` on
  `requests.get` / `flows.get` / `flows.export-har`. It does not register mutations or change the
  default query response. Raw headers, query values, bodies and saved scripts
  may contain credentials or private data sent to the client's model provider.
- `--disable-tool <name>` (repeatable, for example `--disable-tool request.run`)
  removes one tool from the catalog a client sees. It only narrows access. An
  unknown name stops the server at startup.

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
