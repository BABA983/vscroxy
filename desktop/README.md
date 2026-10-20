# vscroxy desktop

The desktop app, written in Go with [MyGo](https://github.com/egoist/mygo)'s
native UI: no webview, no HTML, no JavaScript in the app itself. MyGo draws
the widgets on the GPU and takes text, input methods and accessibility from
the platform.

This is the inspector shell. It runs on seeded flows so the layout, the
traffic table, the detail panes and the diff viewer can be judged before the
capture core is wired in.

## Run

Go 1.27 or later. On Linux the app needs GTK 3 at run time; nothing else.

```sh
go run .
```

`mygo dev` rebuilds and restarts on changes:

```sh
go tool mygo dev
```

## What is here

- `main.go`: the app state, the three-pane layout, the toolbar, the traffic
  table, the detail tabs and the status bar.
- `data.go`: seeded flows, their bodies and headers, and formatting helpers.
  The capture core will replace `seedFlows`.
- `mygo.json`: the app's name, identifier and version for `mygo package`.

The layout is the Proxyman one: filters and domains on the left, the request
table in the middle, and Headers / Body / Diff under it. The diff pane is
MyGo's own `ui/editor` diff viewer, not Monaco, so it stays native and can be
reused on mobile later.

## Next

- Replace `seedFlows` with the capture core's flow stream (Go, `go-mitmproxy`
  as the engine), keeping `flow` as the boundary type.
- Turn Headers / Body into a real inspector: JSON tree, hex, image and
  rendered previews, with body search.
- Add the replay and scripting path: a flow becomes a request template, then
  a collection item with pre-request and post-response scripts.
