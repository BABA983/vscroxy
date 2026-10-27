# vscroxy

A native Go desktop app for capturing and replaying HTTP(S), drawn with
[MyGo](https://github.com/egoist/mygo). There is no web view or Electron.
The Go module and MyGo configuration live at the repository root.

`cmd/vscroxy/` is the thin executable entry, including `--proxy-guard` mode.
`internal/app/` owns the UI and application state. `internal/request/` owns an
authored request and how it becomes traffic (variables, encoding, scripts,
checks, sending); it never imports MyGo. Keep the other `internal/`
packages' existing responsibility boundaries; `third_party/go-mitmproxy/` is a
locally patched dependency with its own license and patch notes.

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
its `examples/` ship inside the module, pinned to the version in
`go.mod`. `.agents/setup` links them at `~/.cache/mygo`.

Read `~/.cache/mygo/docs/ui/` before reaching for a widget, and
`~/.cache/mygo/AGENTS.md` for how the library expects to be used. `go doc` works
too, but the docs say what the widgets are for.

To find them without the link:

```bash
go list -m -f '{{.Dir}}' github.com/egoist/mygo
```
