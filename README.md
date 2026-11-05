# vscroxy

A native desktop application for capturing, inspecting and replaying HTTP(S),
built with Go and [MyGo](https://github.com/egoist/mygo). No Electron or webview;
Node.js and a JavaScript package manager are not required to build or run it.

## Build and run

Use Go 1.27.3 or later. Linux needs GTK 3 and OpenGL runtime libraries; on Debian
or Ubuntu, install `libgtk-3-0` and `libgl1`. Run commands from the repository root:

```sh
go run ./cmd/vscroxy
go build -o bin/vscroxy ./cmd/vscroxy
go vet ./...
go test ./...
```

For live reload or a packaged native application, use the pinned MyGo CLI:

```sh
go tool mygo dev
go tool mygo build
```

The proxy prefers `127.0.0.1:7890` and shows its actual listening address in the
window. HTTPS inspection requires configuring the client to trust the generated
CA. Settings and saved work live in `~/.vscroxy`.

See the [user guide](docs/guide.md) for client and device setup, filtering,
requests, scripts, streaming protocols and the limits of HTTPS inspection.
JavaScript request/response hooks run in Goja inside the Go process, not Node.js.

For collection regression runs without a window, build
`go build -o bin/vscroxy-cli ./cmd/vscroxy-cli`.
See [headless automation](docs/automation.md) for native/Postman inputs,
environment/data files, exit codes, JSON/JUnit CI reports and opt-in authenticated
control of a running desktop.
For external AI tools, build `go build -o bin/vscroxy-mcp ./cmd/vscroxy-mcp` and
use the [MCP stdio configuration](docs/automation.md#mcp-stdio-server).

### Headless load runs

```sh
vscroxy-cli load --folder Shop --env ./env.json --environment local \
  --users 4 --duration 30s --report-json ./reports/load.json ./collections
vscroxy-cli load --var base=http://127.0.0.1:8080 ./postman.json
```

Only run collections against systems you are authorized to load-test. Flags
precede the native collections root or Postman collection JSON input:

| Flag | Default / scope |
| --- | --- |
| `--folder` | All requests; select a collection/folder path including children |
| `--env` | No environment; explicitly read native `env.json` or Postman environment/globals |
| `--environment` | The environment file's active environment |
| `--var KEY=VALUE` | Repeatable environment overrides; prefer `--env` for secrets |
| `--users` | `1`, bounded to 1–64 |
| `--duration` | `30s`, bounded to 1s–10m |
| `--request-timeout` | `30s`, positive and at most 1m |
| `--max-attempts` | `100000` total across all users, bounded to 1–1000000 |
| `--bail` | `false`; stop all users after a failed attempt |
| `--report-json` | Optional private aggregate JSON destination outside all inputs |

This is a **closed workload**, not a target arrival rate: each user repeats the
same 1–200 selected HTTP(S) steps, following script branches, until duration or an
explicit `pm.execution.setNextRequest(null)` stop. Each user keeps independent
cookies, OAuth tokens and script state between loops. The CLI uses an isolated
temporary proxy and CA, never reads the desktop profile, and never persists
script writes or mutates inputs. Collection scripts, auth and requests still
make their authored network calls; no desktop rules or hooks are inherited.
There is no iteration data, individual request/response logging, JUnit or
raw-detail report mode. Reports cannot alias inputs, including symlinks or hard
links. Reports replace atomically with mode 0600 (new directories 0700).

The final summary reports completed, failed, skipped and load-interrupted
attempts, completed attempts per elapsed second, and execution latency P50/P95/P99.
Failures are a subset of completed attempts. Latency includes scripts, auth,
network and checks for completed attempts, including failures; skips and load
interruptions are excluded. JSON contains only aggregate metrics, saved request
IDs and a controlled stop reason. Response counts/bytes describe the primary
response, not redirects, OAuth or `pm.sendRequest` traffic. This measures the
whole execution, **not server-only latency**.

Exit codes: `0` normal duration or explicit stop without failures; `1` failed
attempts; `2` setup/output errors, attempt-budget exhaustion or execution errors;
`124` parent deadline; `130` cancellation (including Ctrl-C). A requested JSON
report includes partial metrics on cancellation/deadline; output errors take
precedence and return `2`.

## Copying requests and code

The capture inspector's **Copy as…**, request options, traffic row menus and
native Request menu offer cURL (Bash), cURL (Windows CMD), PowerShell 7+,
browser fetch, Node.js 18+ fetch, Swift URLSession, Kotlin OkHttp, Java 11+
java.net.http, C# HttpClient, PHP cURL, Ruby Net::HTTP and Rust reqwest.
**Generate code…** also retains axios, Python requests, Go net/http and HTTPie.
No Node, PowerShell, Swift, Kotlin, Java, .NET, PHP, Ruby or Rust runtime is
needed by vscroxy; those runtimes and libraries are only needed to execute the
copied code.

Capture actions copy the original exchange; request-tab actions copy the
active draft, including unsaved edits and inherited auth. Copy never sends a
request or runs scripts. `{{variables}}` remain literal, but captured credentials,
literal auth and any already-cached OAuth token can be included. Review before
sharing. Full retained request bodies are used, not display previews; missing,
partial or unsupported bodies fail without replacing the clipboard.

- CMD output is for **interactive cmd.exe**, not PowerShell or `.bat` files.
  It uses `curl.exe`, Chromium DevTools-style escaping, and conservative limits:
  ASCII, no `!`/delayed expansion, no binary/control-byte body, at most 8000
  command characters. Use Bash for binary bodies or PowerShell for Unicode.
- PowerShell uses native `Invoke-WebRequest`, UTF-8 body bytes and no automatic
  redirects. It refuses representations it cannot preserve, including GET/HEAD
  bodies, non-ASCII header values and POST without explicit Content-Type.
- Fetch refuses GET/HEAD bodies and an alternate Host. Browser fetch is subject
  to CORS, browser cookies and forbidden-header policies; it is not a byte-exact
  replay client. Node fetch has no browser cookie jar or browser CORS checks.
- Swift URLSession, Java 11+ java.net.http, C# HttpClient, PHP cURL and Ruby
  Net::HTTP use their standard libraries; Kotlin needs OkHttp and Rust needs
  reqwest with the blocking feature. OkHttp refuses GET/HEAD bodies.
- Binary body drafts reference their selected file in the native snippets;
  captured binary bodies without a source file still use cURL. Multipart bodies
  are exported as assembled bytes, so binary multipart parts also use cURL.
- Ruby Net::HTTP and C# HttpClient join repeated header values with `, `
  (HTTP list syntax); Cookie is joined with `; `.
- Java HttpClient, Kotlin OkHttp and C# HttpClient refuse non-ASCII header
  values, which those clients reject or replace; Swift, PHP, Ruby and Rust send
  the UTF-8 bytes.

Captured HTTP requests can also be copied as **HAR (sanitized)** or explicitly
**HAR (with sensitive data)**, including bulk selections and imported sessions.
Sanitized copies use the same default redaction as file exports and never read
body spill files. Raw copies include complete request/response bodies (base64
for binary data); missing or incomplete bodies fail the whole copy. Clipboard
HAR shares automation's limits: 1–200 HTTP exchanges, 1 MiB metadata, 4 MiB
combined bodies and 8 MiB output. Streams and tunnels are not supported.

cURL import remains Bash/POSIX-only; CMD/PowerShell import is not supported.
`--location` / `-L` and `--no-location` preserve redirect behavior through native
save/reopen and execution, including explicit `-X` methods and a 50-hop limit.
Without `-L`, imported requests stop at the original response. Declarative Checks
also retain that original-response behavior. See the guide for credential and
request-body boundaries.

## Source layout

- `cmd/vscroxy/` is the executable entry point, including proxy-guard mode.
- `cmd/vscroxy-cli/` is the UI-independent automation executable.
- `cmd/vscroxy-mcp/` exposes authenticated desktop actions over MCP stdio, with
  read-only tools by default and independent write/sensitive startup capabilities.
- `internal/app/` owns the native UI, application state and UI tests, and
  keeps the request library as one directory per collection and one file per
  request (`library.go`).
- `internal/request/` turns an authored request into traffic: variables,
  auth and form encoding, scripts, response checks and sending through the proxy.
- `internal/runner/` shares collection execution and CI reports;
  `internal/library/` shares ordered collection-file reading with the desktop.
- `internal/postman/` converts Postman Collection v2.1 and environment files
  to and from requests and collection settings.
- `internal/vault/` seals request secrets with AES-256-GCM and limits them to
  their allowed domains.
- The other `internal/` packages contain capture, system-proxy integration,
  device access, DNS and process/icon lookup.
- `third_party/go-mitmproxy/` is the locally patched capture engine. Its
  [patch notes](third_party/go-mitmproxy/VSCROXY.md) explain why it is retained.
- `mygo.json` configures development and packaging; `.agents/setup` prepares an
  Amp orb with the Go toolchain and Linux runtime libraries.

## License

[MIT](LICENSE). See [third-party notices](THIRD_PARTY_NOTICES.md) and the
[capture engine's license](third_party/go-mitmproxy/LICENSE).
