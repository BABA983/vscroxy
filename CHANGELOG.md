# Changelog

## 0.1.0 (draft)

The first public release. Everything below is in this version; the items under
**Preview** are first-version clients whose interfaces and limits may change.

### Capture

- HTTP/1.1 and HTTP/2 capture over an explicit proxy, with per-host and per-app
  HTTPS decryption, an ignore list, an allow list, and blocking.
- SOCKS5 and reverse proxy entries; upstream proxies with host rules, a bypass
  list and PAC scripts.
- Rules: block, map local, map remote, rewrite and scripts, with scenarios.
- Breakpoints for requests and responses, with templates.
- Network conditions (presets, custom presets, per-rule scope).
- Host profiles, including subscriptions, and remote-IP attribution.
- Sessions saved to disk; HAR, Charles and Proxyman session import; `.http`
  import and export.

### Requests and collections

- A request editor with params, headers, every body kind, auth, cookies,
  variables, scripts, checks, examples and code generation.
- Requests, sessions, contracts and runs open in tabs beside a fixed Live tab,
  with preview tabs, unsaved marks and Reopen Closed Tab. Saved requests and
  contracts reopen in their tabs after a restart.
- Collections as a directory of files, in `~/.vscroxy/collections` or in a
  project folder's `.vscroxy/collections`, with a Git panel.
- Environments with shared Initial values and local Current values; a sealed
  vault.
- A Runner with data files, iterations, history and JSON, JUnit and HTML
  reports.
- Postman v2.0/2.1 and v3, OpenAPI, Insomnia, Bruno, Hoppscotch, Swagger 2,
  cURL and `.http` import.

### Automation

- `vscroxy-cli`: run, load and mock collections; control a running desktop.
- `vscroxy-mcp`: an MCP stdio server over the desktop's local control endpoint.
- A local control endpoint, off by default.

### Preview

These first-version protocol clients are marked **Preview**. They work and are
covered by tests, but their editors and saved formats may change in a later
release:

- **gRPC** (unary calls run in scripts, checks, examples and the Runner;
  streaming calls stay in the interactive client)
- **SSE**
- **Socket.IO**
- **MQTT**
- **MCP** (the interactive MCP client)

### Data, privacy and diagnostics

- Every file the app owns carries a format version. A file from a newer version
  is refused and left untouched; older files are migrated, with a copy kept in
  `~/.vscroxy/backups/` (the last 10 per file). See `AGENTS.md` and
  `docs/architecture.md` 3.7.
- No accounts, no telemetry, no analytics. The only connection the app starts on
  its own is the update check, which asks GitHub for the latest release number
  once per launch and can be turned off. See [PRIVACY.md](PRIVACY.md).
- **Settings → General → About** shows the version, commit, license and
  third-party notices; the packages ship `LICENSE` and
  `THIRD_PARTY_NOTICES.md`.
- **Diagnostics → Export diagnostics…** saves a zip with the version, platform,
  settings safe to share, recent logs and connection checks, without request
  contents or secrets.
- `vscroxy --version`, `vscroxy-cli --version` and `vscroxy-mcp --version` print
  the version and commit; the startup log records both.

### Known limits

- The packages are unsigned. See [README.md](README.md) for opening them past
  Gatekeeper and SmartScreen.
- Breakpoint bodies over 1 MiB are read-only.
- WebSocket streams keep at most 200 messages per stream.
- Network conditions are not real packet loss.
