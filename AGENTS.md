# vscroxy

A native Go desktop app for capturing and replaying HTTP(S), drawn with
[MyGo](https://github.com/egoist/mygo). There is no web view or Electron.
The Go module and MyGo configuration live at the repository root.

`cmd/vscroxy/` is the thin executable entry, including `--proxy-guard` mode.
`internal/app/` owns the UI and application state. `internal/request/` owns an
authored request and how it becomes traffic (variables, encoding, scripts,
checks, sending); it never imports MyGo. `internal/imports/` reads third-party
exports (Insomnia, Bruno, Hoppscotch, Swagger 2, and Chrome's CMD/PowerShell
cURL) into that model; it also never imports MyGo. `internal/uikit/` is the design system: tokens, the Light/Dark themes, bundled
fonts and shared components; it depends only on MyGo, and screens use its roles
and components instead of colors and sizes. `internal/vault/` seals secrets and
decides which hosts may receive them. `internal/upstream/` chooses the default
upstream, first-match host rules, bypass list and PAC result for a request, and
seals upstream passwords; it never imports MyGo. `internal/postman/` converts
Postman collections and environments. Request scripts are Postman's `pm` API, built in
`internal/request/js/pm.js` on an embedded, unmodified Chai 4.5.0. Keep the other `internal/`
packages' existing responsibility boundaries; `third_party/go-mitmproxy/` is a
locally patched dependency with its own license and patch notes.

The app is a prototype in rapid development: change file formats and APIs
directly. Do not write migrations, readers for old formats, or deprecation
shims, and delete such code when you touch it.

User feedback goes through `notify` (`internal/app/notify.go`): `done`, `inform`,
`warn`, or `fail(what, err)` for errors, which also go to the log at
`~/.vscroxy/logs/vscroxy.log` (`internal/applog`). Never log request contents.

## Building and testing

Go 1.27 lives at `/usr/local/go`. `.agents/setup` installs it, the GTK libraries a
window needs, and the modules.

```bash
go build ./...
go vet ./...
go test ./...
```

The app runs with `go run ./cmd/vscroxy` from the repository root, or a built
binary. It reads its settings from `~/.vscroxy`. By default it prefers
127.0.0.1:7890, falling back to an OS-assigned loopback port shown in the UI.
Settings → Proxy can save a fixed port (1024–65535) for the next launch; a fixed
port never falls back on conflict.

## MyGo

The UI library's source, its `docs/` (the markdown the website is built from) and
its `examples/` ship inside the module, pinned to the version in `go.mod`, in
Go's module cache (`go env GOMODCACHE`, usually `~/go/pkg/mod`). `~/.cache/mygo`
is MyGo's runtime cache for fonts and native libraries and must stay writable.

Find the module directory, then read its `docs/ui/` before reaching for a widget
and its `AGENTS.md` for how the library expects to be used. `go doc` works too,
but the docs say what the widgets are for.

```bash
go list -m -f '{{.Dir}}' github.com/egoist/mygo
```
