# Postman sandbox libraries

This directory builds the modules request scripts can `require` and rule
scripts, addons and Code Mocks can `import`. The module list comes from
Postman's own sources, not from memory:

- `postman-sandbox` 6.9.0 `lib/environment.js`, which is the browserify
  `require` map, and `lib/sandbox/postman-legacy-interface.js`, which adds the
  legacy globals.
- The "Import packages into your scripts" page in Postman's JavaScript
  reference, which lists the supported and deprecated library names.

Postman's sandbox pins these versions in its `package.json` /
`package-lock.json`: `cheerio` 0.22.0, `xml2js` 0.6.2, `csv-parse` 1.2.4,
`tv4` 1.3.0, `uuid` 3.4.0, `postman-collection` 5.3.1, `liquid-json` 0.3.1,
`backbone` 1.6.0, `assert` 2.0.0, `buffer` 6.0.3, `events` 2.1.0,
`os-browserify` 0.3.0, `path-browserify` 0.0.1, `process` 0.11.10,
`punycode` 2.3.1, `querystring-es3` 0.2.1, `stream-browserify` 2.0.2,
`string_decoder` 1.3.0, `url` 0.11.0 and `util` 0.12.3. `sax` 1.2.4 and
`underscore` 1.13.3 are pinned to Postman's transitive versions as well.
Postman's sandbox maps `uuid` to a small unversioned vendored v4 shim; we bundle
`uuid` 3.4.0 from its lock, which has the same callable `uuid()` /
`uuid.v4()` shape.

The app already embeds Ajv 6.12.6 (Postman 6.12.5, the patched release),
Chai 4.5.0 (Postman 4.4.1), Lodash 4.18.1, CryptoJS 4.2.0 (Postman 3.3.0)
and Moment 2.31.0 (Postman 2.30.1) in `internal/request/js/`.

## Rebuilding

```bash
internal/request/js/sandbox/build.sh
```

`build.sh` runs `npm ci` from `package-lock.json`, then the Go program in
`build/` uses the esbuild version in `go.mod` to bundle each entry point as a
CommonJS module. The generated files go to `internal/request/js/vendor/`;
`hashes.txt` records their SHA-256 and `sources.txt` records every resolved
npm tarball and integrity hash. Nothing is downloaded or compiled at runtime.

The Node shims are the same browserify packages Postman's browser bundle uses.
`fs` is an empty module, as in Postman's browser sandbox, and `timers` is the
script-scoped timer queue described below.

## Node tests

```bash
cd internal/request/js/sandbox
node expectations.mjs > ../../testdata/sandbox-expectations.json
```

The generator runs each expression against the same npm packages under Node;
`internal/request/sandbox_test.go` runs the same expressions through the
embedded bundles. This is how the expected values in
`testdata/sandbox-expectations.json` were produced.

## What is not included

- `sugar` is a prototype-extension bundle in Postman's sandbox, not a
  `require` name and not in Postman's documented library list. It is not
  included; global prototype extensions would be a surprising side effect.
- jQuery is not included. Backbone's optional `require('jquery')` fails and
  is caught, so `Backbone.$` is undefined; models, collections and events work.
- `fs` has no filesystem access, and there is no `net`, `http`, `https`,
  `zlib`, `child_process` or real Node `crypto` module. The sandbox has no
  Node runtime or event loop.
- `crypto.getRandomValues` is provided for `uuid`; use `crypto-js` for hashes.
- `pm.visualizer` is still unsupported.

## Timers

Postman exposes `setTimeout`, `setInterval`, `setImmediate` and their clear
functions as globals. goja has no event loop, so vscroxy implements a
script-scoped queue in `internal/request/script.go`:

- callbacks run after the script body, before `pm.test` results are collected;
- due order decides order, and equal due times use scheduling order;
- delays are virtual and never sleep, so a long delay cannot stall the UI;
- intervals repeat until cleared or the normal two-second script budget
  interrupts the script;
- a pending `await` can be settled by a timer; a promise with no timer left is
  still "awaited something that never finished";
- timers never cross scripts or runs.

This is enough for xml2js's `async: true` / `parseStringPromise`, lodash
`delay`/`debounce` and Postman scripts that use zero-delay callbacks. It is
not a real-time scheduler: `setTimeout(fn, 60000)` runs as soon as the body
finishes, not a minute later.

## Hostile input

cheerio 0.22.0 and xml2js 0.6.2 parse untrusted response bodies. They run
inside the same two-second script budget as any other script code, so a
pathological input is interrupted rather than hanging the app. sax does not
expand DTD entities or resolve external entities, so XML entity expansion
cannot multiply the input. csv-parse's sync API is used, so it never waits on
a stream.
