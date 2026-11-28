# vscroxy

A native desktop application for capturing, inspecting and replaying HTTP(S),
built with Go and [MyGo](https://github.com/egoist/mygo). No Electron or webview;
Node.js and a JavaScript package manager are not required to build or run it.

## Download and install

Releases are on [GitHub](https://github.com/BABA983/vscroxy/releases). A
release carries:

- macOS: `vscroxy-<version>-arm64.dmg` and `vscroxy-<version>-amd64.dmg`. Open
  the disk image and drag vscroxy to Applications.
- Windows: `vscroxy-setup-<version>-<arch>.exe`.
- Linux: `vscroxy-<version>-linux-<arch>.tar.gz` and its
  `vscroxy-<version>-linux-<arch>-install.sh`, plus `.deb` and `.rpm` packages.
- `vscroxy-cli-<os>-<arch>` and `vscroxy-mcp-<os>-<arch>` (`.exe` on Windows):
  the headless binaries, separate from the app package.
- `SHA256SUMS`: the SHA-256 of every other release asset.

The packages are not code signed yet. Verify a download from the folder holding
the files with `sha256sum -c --ignore-missing SHA256SUMS` (on macOS,
`shasum -a 256 -c SHA256SUMS`).

**macOS (Gatekeeper).** The first open is blocked with "cannot be opened
because Apple cannot check it for malicious software". Open it once from Finder
with right-click → **Open** → **Open**, or allow it in **System Settings →
Privacy & Security → Open Anyway**. If macOS says the app is damaged, remove
the quarantine flag:

```sh
xattr -dr com.apple.quarantine /Applications/vscroxy.app
```

**Windows (SmartScreen).** The first run shows "Windows protected your PC".
Choose **More info** → **Run anyway**. A downloaded `.exe` can also be blocked
by **Properties → Unblock**.

**Linux.** The `.deb` and `.rpm` install the app, its desktop entry and the
`vscroxy` command on `PATH`. The tar.gz's install script does the same for the
current user under `~/.local`, including `~/.local/bin/vscroxy`; put it beside
its archive and run `sh vscroxy-<version>-linux-<arch>-install.sh`.

### First run and the CA

1. Start vscroxy. It listens on `127.0.0.1:7890`, or the address and port in
   the footer's proxy status hover card when that one is taken.
2. Follow the setup guide: it turns on the system proxy, installs the vscroxy
   CA and checks that the first HTTPS request is decrypted.
3. Or do it by hand: **Settings → HTTPS → Install & Trust…**, then point a
   client at the proxy. On Linux the same button installs the CA into the
   system trust store through a `pkexec` authorization prompt and refreshes it
   with `update-ca-certificates` or `update-ca-trust`; if `pkexec` is missing,
   the app names the file to copy and the command to run. Firefox keeps its own
   trust store; the guide covers it along with Java, Node.js, phones and
   simulators.

On an unsigned macOS build, macOS can refuse the CA import before it shows the
authorization prompt. Import and trust it by hand: open **Keychain Access**,
select the **System** keychain, choose **File → Import Items…** and pick
`~/.vscroxy/ca/vscroxy-ca-cert.pem` (under the data folder chosen with
`--user-data-dir` or `VSCROXY_HOME` when one is set), open **vscroxy CA**,
expand **Trust**, set **When using this certificate** to **Always Trust**, and
authenticate.

The CA private key stays on this computer. The [user guide](docs/guide.md)
covers Firefox, Java, Node.js, phones, simulators and the command line.

## Removing the vscroxy certificate

The app generates a CA named `vscroxy CA` and keeps it in `~/.vscroxy/ca`, or
in the data folder set with `--user-data-dir` / `VSCROXY_HOME`. Quit vscroxy
before removing its copies; the next launch generates a new CA that clients
must trust again. Firefox, Java and phones keep their own trust stores, which
the [user guide](docs/guide.md) explains.

**macOS.** Remove **vscroxy CA** from the **System** keychain in Keychain
Access, or run:

```sh
sudo security delete-certificate -c "vscroxy CA" /Library/Keychains/System.keychain
```

Remove it from the login keychain too if you imported it there by hand.

**Windows.** The button trusts the CA for the current user in the Root store:

```bat
certutil -user -delstore Root "vscroxy CA"
```

In an elevated Command Prompt, `certutil -delstore Root "vscroxy CA"` removes a
machine-wide copy. Or delete **vscroxy CA** under **Trusted Root Certification
Authorities → Certificates** in `certmgr.msc` (current user) or `certlm.msc`
(this computer).

**Linux.** The app installs the CA as `vscroxy.crt` through
`update-ca-certificates` (Debian, Ubuntu) or `update-ca-trust` (Fedora, RHEL,
openSUSE, Arch), then refreshes the store. Remove that file and refresh:

```sh
# Debian, Ubuntu
sudo rm -f /usr/local/share/ca-certificates/vscroxy.crt
sudo update-ca-certificates
# Fedora, RHEL, openSUSE
sudo rm -f /etc/pki/ca-trust/source/anchors/vscroxy.crt
sudo update-ca-trust
# Arch Linux
sudo rm -f /etc/ca-certificates/trust-source/anchors/vscroxy.crt
sudo update-ca-trust
```

**Every platform.** Delete the app's CA files so it cannot keep using them:

```sh
rm -rf ~/.vscroxy/ca
```

Use the `ca` folder under your data folder when it is not `~/.vscroxy`.

`vscroxy --version`, `vscroxy-cli --version` and `vscroxy-mcp --version` print
the version and commit; **Settings → General → About** shows them with the
license and third-party notices.

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

The proxy prefers `127.0.0.1:7890` and shows its actual listening address in
the footer's proxy status card. HTTPS inspection requires configuring the
client to trust the generated CA. Settings, captures and logs live in
`~/.vscroxy`, or in the folder selected
with `--user-data-dir <path>` / `--user-data-dir=<path>` or `VSCROXY_HOME` (a
flag wins over the variable; relative paths are relative to the working
directory). Settings → General →
About shows the folder in use. The CLI accepts the same flag and variable.

See the [user guide](docs/guide.md) for client and device setup, filtering,
requests, scripts, streaming protocols and the limits of HTTPS inspection.
JavaScript request/response hooks run in Goja inside the Go process, not Node.js.

For collection regression runs without a window, build
`go build -o bin/vscroxy-cli ./cmd/vscroxy-cli`.
See [headless automation](docs/automation.md) for native/Postman inputs,
environment/data files, exit codes, JSON/JUnit CI reports and opt-in authenticated
control of a running desktop.
For external AI tools, open **Settings → AI agents** for the MCP server snippet,
or build `go build -o bin/vscroxy-mcp ./cmd/vscroxy-mcp` and use the
[MCP stdio configuration](docs/automation.md#mcp-stdio-server). The app bundles
the [vscroxy skill](skills/vscroxy/SKILL.md); `vscroxy-mcp` serves it as
`vscroxy://skill`, and Settings → AI agents installs it for Claude Code, Codex
or Cursor.

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

cURL import reads Bash/POSIX, Chrome's CMD output and Chrome's PowerShell
`Invoke-WebRequest` output. `--location` / `-L` and `--no-location` preserve
redirect behavior through native save/reopen and execution, including explicit
`-X` methods; `--max-redirs` sets the hop limit (50 when unset). `--insecure` /
`-k` turns certificate verification off and `--max-time` / `-m` sets the request
timeout. PowerShell's `-MaximumRedirection`, `-SkipCertificateCheck` and
`-TimeoutSec` map to the same settings. Without `-L`, imported requests stop at
the original response. Declarative Checks also retain that original-response
behavior. See the guide for credential and request-body boundaries.

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

## Privacy

vscroxy has no accounts, no telemetry and no analytics. The only connection it
starts on its own is the update check, which can be turned off. See
[PRIVACY.md](PRIVACY.md) for every outbound connection.

## License

[MIT](LICENSE). See [third-party notices](THIRD_PARTY_NOTICES.md) and the
[capture engine's license](third_party/go-mitmproxy/LICENSE).
