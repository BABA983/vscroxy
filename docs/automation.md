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
