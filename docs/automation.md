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
| `collections.list` | Runnable saved collection/folder names |
| `requests.list` | Optional `collection` (descendants included; explicit empty selects unfiled requests) |
| `requests.get` | Saved `id`; optional `details` |
| `flows.list` | Optional `limit` (default 50, max 200), exclusive `before` ID cursor; `nextBefore:0` ends paging |
| `flows.get` | Live capture `id`; optional `details` |
| `rules.list` | Index, fingerprint `key`, name, type and enabled state |
| `request.run` | Saved `id`, optional `environment`, `iterations` (1–1000), `bail`; returns job ID |
| `collection.run` | `collection`, same run options; includes descendants |
| `runs.get` | Job `id`; returns `done` and a redacted report |
| `runs.cancel` | Job `id`; cancellation cannot undo completed external effects |
| `capture.recording` | Required boolean `enabled`; forwarding remains on |
| `rules.set-enabled` | Required `index`, current `key` and boolean `enabled`; persists through the existing rules store |

All five mutations require `confirm:true`; CLI `--confirm` supplies it. Rule
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
System-proxy changes, arbitrary rule import/creation, and full HAR export remain
separate roadmap work; the API deliberately has no arbitrary filesystem action.
