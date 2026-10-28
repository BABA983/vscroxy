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

## Source layout

- `cmd/vscroxy/` is the executable entry point, including proxy-guard mode.
- `internal/app/` owns the native UI, application state and UI tests, and
  keeps the request library as one directory per collection and one file per
  request (`library.go`).
- `internal/request/` turns an authored request into traffic: variables,
  auth and form encoding, scripts, response checks and sending through the proxy.
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
