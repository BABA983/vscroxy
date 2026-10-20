# vscroxy desktop

The desktop app, written in Go with [MyGo](https://github.com/egoist/mygo)'s
native UI: no webview, no HTML, no JavaScript in the app itself. MyGo draws
the widgets on the GPU and takes text, input methods and accessibility from
the platform.

The app captures real traffic: it runs a MITM proxy on `127.0.0.1:7890` with
[go-mitmproxy](https://github.com/lqqyt2423/go-mitmproxy) and shows every
exchange as it arrives.

## Run

Go 1.27 or later. On Linux the app needs GTK 3 at run time; nothing else.

```sh
go run .
```

Then point a client at the proxy:

```sh
curl -x http://127.0.0.1:7890 http://example.com/
```

For HTTPS, install the CA the proxy generates at
`~/.mitmproxy/mitmproxy-ca-cert.pem` and trust it, then:

```sh
curl --cacert ~/.mitmproxy/mitmproxy-ca-cert.pem -x http://127.0.0.1:7890 https://example.com/
```

`mygo dev` rebuilds and restarts on changes:

```sh
go tool mygo dev
```

## What is here

- `main.go`: the app state, the three-pane layout, the toolbar, the traffic
  table, the detail tabs and the status bar.
- `data.go`: seeded flows for the case where the proxy cannot start, and
  formatting helpers.
- `internal/capture`: the MITM proxy and the `Flow` boundary type. The UI
  imports this package, never the engine.
- `mygo.json`: the app's name, identifier and version for `mygo package`.

The layout is the Proxyman one: filters and domains on the left, the request
table in the middle, and Headers / Body / Diff under it. The diff pane is
MyGo's own `ui/editor` diff viewer, not Monaco, so it stays native and can be
reused on mobile later. It compares a response with the previous response for
the same method, scheme and URL.

## Next

- Rules and breakpoints on top of the capture hooks (map local, map remote,
  rewrite, pause).
- Turn a flow into a request template, then replay it and keep the history.
- A real body inspector: JSON tree, hex, image and rendered previews, with
  search, and bodies spilled to disk instead of held in memory.
- Pre-request and post-response scripts over the same flow model.
