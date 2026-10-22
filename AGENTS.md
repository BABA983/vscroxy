# vscroxy

A VS Code fork whose real work has moved to `desktop/`: a native Go desktop app
for capturing and replaying HTTP(S), drawn with [MyGo](https://github.com/egoist/mygo).
There is no web view in it, and no Electron.

## Building and testing

Go 1.27 lives at `/usr/local/go`. `.agents/setup` installs it, the GTK libraries a
window needs, and the modules.

```bash
cd desktop
go build ./...
go vet ./...
go test ./...
```

The app runs with `go run .` in `desktop/`, or a binary built there. It reads its
settings from `~/.vscroxy`. By default it prefers 127.0.0.1:7890, falling back to
an OS-assigned loopback port shown in the UI. Settings → Proxy can save a fixed
port (1024–65535) for the next launch; a fixed port never falls back on conflict.

## MyGo

The UI library's source, its `docs/` (the markdown the website is built from) and
its `examples/` ship inside the module, pinned to the version in
`desktop/go.mod`. `.agents/setup` links them at `~/.cache/mygo`.

Read `~/.cache/mygo/docs/ui/` before reaching for a widget, and
`~/.cache/mygo/AGENTS.md` for how the library expects to be used. `go doc` works
too, but the docs say what the widgets are for.

To find them without the link:

```bash
cd desktop && go list -m -f '{{.Dir}}' github.com/egoist/mygo
```
