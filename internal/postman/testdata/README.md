# Postman Collection v3 test data

This directory holds the Postman Collection v3 fixtures and the research that
defines what v3 is. The reader lives in `internal/postman/v3.go`; the schema
types it decodes live in `internal/postman/v3_types.go`.

## Research (2026-11-08)

### What v3 is

Postman Collection schema **3.0.0** is a *directory of YAML files*, not one
JSON export. A collection root is a folder; each request is a separate
`*.request.yaml` file; examples and stream messages live under reserved
`.resources/` directories. Postman v12 uses this format for local projects and
Native Git, and the Postman CLI can create, read, lint, run and migrate it.

The current v2.1 export is still the interchange format: Postman's **Export
collection** action writes a single v2.1 JSON file. v3 is the on-disk project
format. That is why this app imports v3 but keeps exporting v2.1 (see
"Export" below).

### Sources

| Source | What it establishes |
| --- | --- |
| https://schema.postman.com/collection/json/v3.0.0/draft-07/collection.json | Official JSON Schema, retrieved 2026-11-08. HTTP 200, about 220 KB, `$id: v3-collection`, draft-07. Defines every request kind, body branch, auth shape, settings object and example shape. |
| https://learning.postman.com/docs/use/use-collections/collections-schemas | Official docs, "Latest (v12)". States the current schema is 3.0.0, a collection is multiple YAML files on disk, requests are `*.request.yaml`, assets live under `.resources/`, and collection/folder metadata lives in optional `definition.yaml`. Also states `.yml` is a parser alias, `.yaml` is required for authoring output, paths are forward-slash and case-insensitively unique, and ordering uses numeric fractional indexes. |
| https://learning.postman.com/docs/use/native-git/develop-locally/ | Native Git stores collections as v3 YAML under `postman/`. |
| https://learning.postman.com/docs/use/native-git/setup | Official lint rules FMT001–FMT209: path traversal outside the collection root is an error, symbolic links/scripts/examples paths are resolved relative to the request, filenames must be safe and case-insensitively unique, `.resources/` holds metadata rather than runnable requests, and unrecognized fields are warnings. |
| https://learning.postman.com/docs/postman-cli/postman-cli-collections | Postman CLI: `collection migrate` converts v2.1 JSON to v3 YAML; `collection new` scaffolds a v3 directory; `collection request/folder/example` edit v3 files; `collection lint` validates them; `collection get` prints v3 JSON. A request file is `*.request.yaml` or `*.request.yml`. |
| https://learning.postman.com/docs/getting-started/importing-and-exporting/exporting-data | Postman's Export collection action writes JSON (v2.1), while Native Git syncs elements as files. |
| https://community.postman.com/t/importing-collections-in-v12/88963 | Postman staff and users confirm v3 is the v12 local format and that Export still produces v2.1 JSON; v12.0.4 added importing v2.1 JSON into local mode. |
| Postman CLI v1.86.0, `postman init`, bundled skill `postman/skills/collection-schema-v3` | Field-level authoring guide shipped by the official CLI: `reference/http.md`, `reference/other_protocols.md`, `reference/environment.md`, `reference/globals.md`. It documents the exact request kinds, body branches, auth credentials, script event names, messages and example shapes. The CLI was installed from https://dl-cli.pstmn.io/install/linux64.sh on 2026-11-08. |

The schema site URL form matters: v3 is served from
`https://schema.postman.com/collection/json/v3.0.0/draft-07/collection.json`.
The older `schema.getpostman.com/json/collection/v3.0.0/...` path returns 404.

### Confirmed structure

A collection root is any directory. Its optional metadata file is
`.resources/definition.yaml`:

```yaml
$kind: collection
id: c0ffee00-0000-4000-8000-000000000001
name: Shop API
description: Markdown
variables:
  - key: baseUrl
    value: 'https://shop.example.com'
    secret: false
  - key: vaultToken
    secret: true
    source:
      provider: postman
      postman:
        type: local
        secretId: secret-73
scripts:
  - type: http:beforeRequest
    code: |
      pm.variables.set('ts', Date.now());
    language: text/javascript
auth:
  - id: auth-bearer
    name: Bearer token
    type: bearer
    credentials:
      - key: token
        value: '{{token}}'
order: 1000
```

- A folder is a subdirectory. Its `.resources/definition.yaml` has the same
  shape; `name` defaults to the directory name. Folder scripts use the
  protocol-prefixed event names (`http:beforeRequest`, `http:afterResponse`,
  `graphql:beforeQuery`, `graphql:afterResponse`, `grpc:beforeInvoke`, …).
- A request is a `*.request.yaml` file. `$kind` selects its shape:
  `http-request`, `graphql-request`, `grpc-request`, `websocket-request`,
  `socket.io-request`, `mqtt-request`, `kafka-request`, `mcp-request`,
  `llm-request` or `data-request`. `name` is optional and defaults to the
  filename stem.
- Common request fields are `id`, `name`, `description`, `url`, `method`,
  `headers`, `queryParams`, `pathVariables`, `body`, `auth`, `settings`,
  `scripts`, `examples` and `order`. `examples` and `messages` are relative
  paths to `.resources/<request>.resources/examples` and
  `.resources/<request>.resources/messages`.
- HTTP body branches:
  - `none`
  - `json`, `text`, `xml`, `html`, `javascript`: `content` is a string.
  - `urlencoded`: `content` is an array of `{key, value, description?, disabled?}`.
  - `formdata`: `content` is an array of text parts (`{type: text, key, value, contentType?}`)
    or file parts (`{type: file, key, src}`).
  - `file`: `content: {src}`.
  - `graphql`: `content: {query, variables, operationName?}`; `variables` is a
    JSON string.
- Auth is either one object or an array (multiAuth with optional `rules`):
  `{type, credentials: [{key, value}]}`. `noauth` has no credentials;
  `inherit` takes the nearest ancestor's auth. Types in the schema are
  `basic`, `bearer`, `apikey`, `digest`, `oauth1`, `oauth2`, `jwt`, `awsv4`,
  `hawk`, `ntlm`, `asap`, `edgegrid`, `azure`, `sasl` and `inherit`.
- Request scripts use `beforeRequest` and `afterResponse`. A native GraphQL
  request's scripts use `beforeQuery` and `afterResponse`; the reader also
  accepts `beforeRequest` on a GraphQL request because the official migration
  writes it. Collection and folder scripts use the protocol-prefixed names
  above. A script has `code`, optional `language: text/javascript`, and
  optional `packages` and `requests` references.
- Examples are `*.example.yaml` files. An HTTP example has `$kind:
  http-example`, a `request` snapshot and a `response` with `statusCode`,
  `statusText`, `headers` and a required `body` (`text`, `json`, `xml`,
  `html`, `javascript` or `yaml`). A GraphQL example has `response.events`
  and an optional `response.http`. gRPC, WebSocket and Socket.IO examples
  have protocol-specific response shapes.
- Messages are `*.message.yaml` files: `websocket-message` has `content` and
  `contentType`; `socket.io-message` has `eventName`, `args` and
  `acknowledgement`; `mqtt-message` has `topic`, `content` and `contentType`.
- HTTP `settings` are `strictSSL`, `followRedirects`, `maxRedirects`,
  `protocolVersion`, redirect/cookie/TLS options and `disabledSystemHeaders`.
  gRPC, WebSocket and Socket.IO each have their own settings objects.
- `order` is a numeric fractional index used to sort folders, requests and
  examples. A file without `order` is sorted by name after ordered siblings.

### Differences from v2.1

| v2.1 | v3 |
| --- | --- |
| One JSON file with `info.schema` | A directory; `$kind` discriminators instead of a schema URL |
| `item` tree with folders and requests | Directories and `*.request.yaml` files |
| `event` with `listen` and `script.exec` | `scripts` with protocol-prefixed `type` and `code` |
| `variable` with `key`/`value` | `variables` with `key`/`value`/`description`/`disabled`/`secret`/`source` |
| `auth: {type, bearer: [{key, value}]}` | `auth: {type, credentials: [{key, value}]}` or an array of auth sets |
| `body.mode: raw` + `options.raw.language` | `body.type: json`/`text`/`xml`/`html`/`javascript` |
| `body.mode: file` + `file.src` | `body.type: file` + `content.src` |
| `body.mode: graphql` + `graphql` | `body.type: graphql` + `content` |
| `protocolProfileBehavior` | `settings` |
| `response` array on the request | `examples` directory with `*.example.yaml` |
| HTTP requests only | HTTP, GraphQL, gRPC, WebSocket, Socket.IO, MQTT, Kafka, MCP, LLM and data request kinds |

## Fixture provenance

`testdata/v3/shop` is **not** a copied user export. Its base files were
generated with the official Postman CLI v1.86.0 from a v2.1 fixture
(`postman collection migrate`), and native protocol/example files were
generated with `postman collection new`, `collection request add`,
`collection request update`, `collection example add` and
`collection example update`. The rest was written by hand from the official
schema and the bundled `collection-schema-v3` skill:

- collection and folder definitions with multiAuth, variables, a vault secret,
  descriptions, scripts and ordering;
- HTTP body branches (`text`, `xml`, `urlencoded`, `formdata`, `file`,
  `graphql`);
- every auth type the app supports, NTLM included;
- native `graphql-request`, `grpc-request`, `websocket-request`,
  `socket.io-request`, `mqtt-request`, and Streamable HTTP/stdio `mcp-request` files;
- WebSocket and Socket.IO message files;
- a local GraphQL SDL file and a native GraphQL example shape;
- path variables, disabled headers, request settings and saved examples.

Those hand-written parts are **schema-derived ("按 schema 推断")**, not
captured from a real Postman user's project. No public repository with a
real-world v3 collection was found for reuse; if one is added later, record
its URL, commit and license here.

The fixture is validated with the official linter:

```bash
postman collection lint internal/postman/testdata/v3/shop --reporter json
# {"diagnostics":[],"errorCount":0,"warningCount":0,"filesScanned":25,...}
```

License: the fixture content is authored for this repository and covered by
the repository's license. The Postman CLI and the schema are Postman's; they
are used as a generator/reference and are not vendored here.

## What the importer supports

- **HTTP**: method, URL, query and path parameters, headers, every body
  branch above, auth, scripts, settings, descriptions and examples.
- **GraphQL**: native `graphql-request` becomes the app's HTTP GraphQL body,
  including query, variables, operation name, headers, auth, request scripts
  (`beforeQuery`/`afterResponse`), HTTP/GraphQL examples and a local
  `schema: {source: file}` SDL file.
- **gRPC**: URL, `methodPath`, `message.content`, metadata/headers, auth and
  `strictSSL`. A schema file, method descriptor, gRPC scripts and gRPC
  examples have no equivalent and are reported.
- **WebSocket**: URL, headers, auth, `strictSSL` and the first saved text
  message as the document's message. Extra messages and WebSocket examples
  are reported.
- **Socket.IO**: URL, headers, auth, `strictSSL`, the first saved message's
  event, arguments and ACK flag. Subscription events, extra messages and
  examples are reported.
- **MQTT**: URL, `clientId`, `version`, `settings` (clean session, keep-alive,
  auto reconnect, connection timeout, `strictSSL`), topics with per-topic QoS,
  subscribe flag, subscription options and user properties, last will and its
  MQTT 5 properties, connect properties, saved messages and Basic auth.
  MQTT scripts and examples are reported; a message
  `properties.subscriptionIdentifier` is received-only and reported.
- **MCP**: transport, URL or command, environment rows, headers, auth,
  `strictSSL`, protocol version and request timeout, plus one
  `tools/call`, `resources/read` or `prompts/get` JSON-RPC message. Other
  methods, unknown transports, OAuth 2.0 auth and `sessionTimeout` are
  reported. The two non-obvious mappings come from Postman's own format:
  - The v3 schema's `v3-mcp-request` has `transport: "sse" | "stdio"`, and its
    `sse` branch is titled "MCP request over SSE". But Postman's current MCP
    request documentation
    (https://learning.postman.com/docs/use/send-requests/protocols/mcp-requests/create)
    says Postman supports **STDIO** and **streamable HTTP**, with the UI choice
    named **HTTP**. Postman therefore stores its Streamable HTTP request under
    the legacy `sse` enum value. The reader maps `sse` to Streamable HTTP, not
    to the app's legacy HTTP+SSE transport; the schema has no way to express
    legacy SSE separately.
  - Postman's docs say to "enter the server's command" for STDIO, and the v3
    field is one string, e.g. `npx -y @modelcontextprotocol/server-filesystem
    /tmp`. The reader splits that line with shell-like quoting (single and
    double quotes, backslash escapes, no variable or command expansion) into
    the app's `command` and one-argument-per-line `args`. A backslash outside
    quotes only escapes whitespace, a quote or another backslash, so Windows
    paths such as `C:\servers\mcp\index.js` survive; a leading `\\` is kept
    as a UNC path. If the line cannot be
    split (an unterminated quote, or an empty/newline argument that the
    one-argument-per-line format cannot represent), the whole string is
    imported as the program and the import report says so.
- **Kafka, LLM and data** requests are skipped with a warning; the app has no
  document for them.
- v3 environment and globals YAML files are separate artifacts outside a
  collection directory (`postman/environments/*.environment.yaml`,
  `postman/globals/*.globals.yaml`), so this reader does not see them. The
  existing Postman environment import still reads the JSON environment/globals
  export; v3 environment YAML is a separate item.
- MultiAuth imports the first auth set and reports the rest. A secret variable
  with a literal value becomes a local Current value (never written to the
  library); a vault reference is skipped and reported. Parameter/variable
  descriptions and schema types are reported when dropped.

## Export

There is no v3 export. Postman's own **Export collection** action still writes
v2.1 JSON, and v3 is a multi-file directory for local/Native Git projects, not
the sharing format. The app keeps its single-file v2.1 export (with the same
redaction options) so exports remain importable by Postman, Newman and this
app. `postman collection migrate` converts an app v2.1 export to v3 outside
the app.

## Limits

The directory reader treats the collection as untrusted input:

- at most 10,000 files or directory entries per import;
- at most 8 MiB per example, message or request YAML file;
- at most 1 MiB per collection or folder definition;
- at most 1 MiB for an imported GraphQL SDL file;
- at most 64 MiB of YAML read per import;
- at most 32 folder levels;
- symbolic links are never followed, and `.resources` must be a real
  directory;
- YAML aliases are refused rather than expanded; Postman never writes anchors;
- referenced examples/messages/SDL paths must resolve inside the collection
  root; absolute paths and `..` escapes are refused;
- only regular files are read; FIFOs and devices are skipped with a warning.
