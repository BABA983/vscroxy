# Privacy

vscroxy is a local tool. It has no accounts, no telemetry and no analytics: the
code sends nothing about what you capture or send, and there is no crash
reporting service. What you capture, save and send stays on this computer
unless you send it somewhere.

## Connections the app makes on its own

These are the only network connections vscroxy starts without you sending a
request. They are in the source and in the packages:

- **Update check**, on by default: once per launch, `GET
  https://api.github.com/repos/BABA983/vscroxy/releases/latest` with the header
  `User-Agent: vscroxy-update-check` and no query, cookie, token or identifier.
  It reads the release tag and page URL and shows a notice when a newer version
  exists. It never downloads or installs anything. Turn it off in **Settings →
  General → Updates**.
- **PAC file**, only when an upstream PAC URL is configured: `GET` that URL at
  launch and when you press Retry. The URL is yours; the script runs locally in
  a sandbox (`internal/upstream`).
- **Hosts subscriptions**, only for a profile you subscribed: `GET` its URL when
  it is older than an hour or you refresh it. It connects directly, not through
  the proxy (`internal/hosts`).

Nothing else. MyGo, the UI library, has download support for web content, which
this app does not use; its fonts and icons are bundled.

## Connections you start

- Sending a request, running a collection or replaying traffic connects to the
  request's URL, through the upstream proxy, PAC result or SOCKS5 entry you
  configured, if any.
- Scripts you run can send requests with `pm.sendRequest`; those go where the
  script points them.
- The protocol clients connect where you point them: WebSocket, SSE, Socket.IO,
  gRPC (including server reflection), MQTT and MCP servers.
- OAuth 2.0 and OAuth 1.0 ask the authorization and token endpoints in the
  request's auth settings. The authorization page opens in your browser; the
  implicit grant and the browser flow listen on a loopback redirect.
- The local automation endpoint listens on loopback only. `vscroxy-cli` and
  `vscroxy-mcp` connect to it on this computer.
  An agent you connect through `vscroxy-mcp` or `vscroxy-cli` can read
  captured text bodies (`flows.get` with `body: "text"`, up to 64 KiB each)
  without `--allow-sensitive`. Header and query values are redacted, but body
  contents are passed as they are, and the agent may send them to its model
  provider. Raw headers, bodies and scripts still need `--allow-sensitive`.
- Importing a file is local. OpenAPI external references are not fetched, and
  neither are files a request refers to.
- **Report issue** (Help menu or command palette) opens a GitHub page in your
  browser with a new-issue form filled in with the app version and commit, OS
  and version, architecture, MyGo version, locale and theme, and the names of
  enabled features. It carries no request contents, hosts, URLs, home paths,
  setting values beyond that short list, or vault data. Nothing is posted until
  you review the form and submit it yourself; you can close the page without
  posting.

## What is stored

Everything under `~/.vscroxy` and, when a project folder is set,
`<project>/.vscroxy` stays on this computer: settings, rules, collections,
environments, sessions, logs, the vault, and backups. Captured traffic is never
uploaded anywhere.

The log at `~/.vscroxy/logs/vscroxy.log` holds the app's own messages, never
request contents. Secrets are sealed with the vault key; the CA private key and
the vault key are private files (mode 0600). Persistent cookies are off by
default; when you turn them on they are kept in a private file, outside
collections and exports.

The diagnostics bundle is written only where you choose to save it. It holds the
version and platform, settings safe to share, the recent log and the connection
checks, and no request contents or secrets; the tests check that.

## Third parties

The only third party the app contacts without a URL you gave it is GitHub, for
the update check. Building and releasing the app uses GitHub too, which is not a
connection the app makes. The [third-party notices](THIRD_PARTY_NOTICES.md) list every
module and bundled library the packages carry.
