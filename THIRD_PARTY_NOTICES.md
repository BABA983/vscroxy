# Third-party notices

vscroxy's desktop app links the icon package, the NTLM client and the MQTT
clients below, embeds Chai, Ajv, Lodash, CryptoJS, Moment and the Postman
sandbox libraries, and bundles the Geist and Geist Mono fonts. These notices
cover them.

## GraphQL inspector

The inspector and query formatter use unmodified
`github.com/vektah/gqlparser/v2` v2.5.64 under the MIT license:

```
MIT License

Copyright (c) 2018 Adam Scarr

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## NTLM authentication

NTLM message construction uses unmodified `github.com/Azure/go-ntlmssp`
v0.1.1 under the MIT license:

```
The MIT License (MIT)

Copyright (c) 2016 Microsoft

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## JSON query engines

The read-only inspector uses unmodified `github.com/itchyny/gojq` v0.12.20,
its dependency `github.com/itchyny/timefmt-go` v0.1.9, and
`github.com/ohler55/ojg` v1.28.9. All three use the MIT license:

```
The MIT License (MIT)

gojq: Copyright (c) 2019-2026 itchyny
timefmt-go: Copyright (c) 2020-2026 itchyny
OjG: Copyright (c) 2020 Peter Ohler

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## MessagePack inspector

The inspector uses unmodified `github.com/vmihailenco/msgpack/v5` v5.4.1 and
`github.com/vmihailenco/tagparser/v2` v2.0.0. Both use the BSD 2-clause license:

```
Copyright (c) 2013 The github.com/vmihailenco/msgpack Authors.
Copyright (c) 2019 The github.com/vmihailenco/tagparser Authors.
All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

## Chromium DevTools (CMD cURL escaping)

The Windows CMD quoting in `internal/app/curl.go` is adapted from
`generateCurlCommand` / `escapeStringWin` in Chromium DevTools'
[`NetworkLogView.ts`](https://github.com/ChromeDevTools/devtools-frontend/blob/main/front_end/panels/network/NetworkLogView.ts).
It adds conservative encoding, delayed-expansion and command-length limits.

```
Copyright 2014 The Chromium Authors

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google Inc. nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

## HdrHistogram Go 1.4.0 (`github.com/HdrHistogram/hdrhistogram-go`)

Collection load execution uses the unmodified Go module for bounded latency
histograms and percentile statistics.

```
The MIT License (MIT)

Copyright (c) 2014 Coda Hale

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
```

## Geist and Geist Mono 1.9.0 (`internal/uikit/fonts/Geist*.ttf`)

Copyright 2024 The Geist Project Authors (https://github.com/vercel/geist-font).

The unmodified static TrueType fonts (Geist Regular, Medium and SemiBold;
Geist Mono Regular and Bold) come from `Geist/ttf/` and `GeistMono/ttf/` in the
official [Geist 1.9.0 release archive](https://github.com/vercel/geist-font/releases/download/v1.9.0/geist-font-v1.9.0.zip),
tag commit `72f67167dd304a842ef8899da57fa25c5cf21756`.

Licensed under the SIL Open Font License, Version 1.1. The complete upstream
copyright notice and license, which cover both families, are preserved in
[`internal/uikit/fonts/Geist-OFL.txt`](internal/uikit/fonts/Geist-OFL.txt)
and embedded in the executable with the fonts. Download provenance and SHA-256
checksums are recorded in [`internal/uikit/fonts/README.md`](internal/uikit/fonts/README.md).

## Ajv 6.12.6 (`internal/request/js/ajv.js`)

The unmodified `dist/ajv.bundle.js` browser build from the
[`ajv@6.12.6` npm archive](https://registry.npmjs.org/ajv/-/ajv-6.12.6.tgz)
is embedded for the Postman-style `have.jsonSchema` assertions. Its SHA-256 is
`b399e181c8d546bbb1658f711220fee6995ffa627aacc24654d10e8c635bdf32`.
The bundle includes fast-deep-equal, fast-json-stable-stringify,
json-schema-traverse, URI.js and Punycode.js. Their notices follow.

Ajv, fast-deep-equal, fast-json-stable-stringify, json-schema-traverse and
Punycode.js are MIT-licensed:

```
The MIT License (MIT)

Ajv: Copyright (c) 2015-2017 Evgeny Poberezkin
fast-deep-equal and json-schema-traverse: Copyright (c) 2017 Evgeny Poberezkin
fast-json-stable-stringify: Copyright (c) 2017 Evgeny Poberezkin
                          Copyright (c) 2013 James Halliday
Punycode.js: Copyright Mathias Bynens <https://mathiasbynens.be/>

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

URI.js 4.4.0 is BSD-2-Clause-licensed (also preserved in the bundle):

```
Copyright 2011 Gary Court. All rights reserved.

Redistribution and use in source and binary forms, with or without modification, are
permitted provided that the following conditions are met:

   1. Redistributions of source code must retain the above copyright notice, this list of
      conditions and the following disclaimer.

   2. Redistributions in binary form must reproduce the above copyright notice, this list
      of conditions and the following disclaimer in the documentation and/or other materials
      provided with the distribution.

THIS SOFTWARE IS PROVIDED BY GARY COURT ``AS IS'' AND ANY EXPRESS OR IMPLIED
WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND
FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL GARY COURT OR
CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR
CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON
ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING
NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF
ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

The views and conclusions contained in the software and documentation are those of the
authors and should not be interpreted as representing official policies, either expressed
or implied, of Gary Court.
```

## Lodash 4.18.1 (`internal/request/js/lodash.js`)

The unmodified `lodash.js` CommonJS build from the
[`lodash@4.18.1` npm archive](https://registry.npmjs.org/lodash/-/lodash-4.18.1.tgz)
is embedded for `require('lodash')` in request scripts. Its SHA-256 is
`f5465f55566bf544aad0a31c6135889ca1ed81eea8f53ec61c6cbe86926f07cf`.
Updating it requires replacing the pinned source and releasing the app; no
packages are downloaded at runtime.

The upstream license follows:

```
Copyright OpenJS Foundation and other contributors <https://openjsf.org/>

Based on Underscore.js, copyright Jeremy Ashkenas,
DocumentCloud and Investigative Reporters & Editors <http://underscorejs.org/>

This software consists of voluntary contributions made by many
individuals. For exact contribution history, see the revision history
available at https://github.com/lodash/lodash

The following license applies to all parts of this software except as
documented below:

====

Permission is hereby granted, free of charge, to any person obtaining
a copy of this software and associated documentation files (the
"Software"), to deal in the Software without restriction, including
without limitation the rights to use, copy, modify, merge, publish,
distribute, sublicense, and/or sell copies of the Software, and to
permit persons to whom the Software is furnished to do so, subject to
the following conditions:

The above copyright notice and this permission notice shall be
included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND
NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE
LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION
OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION
WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

====

Copyright and related rights for sample code are waived via CC0. Sample
code is defined as all source code displayed within the prose of the
documentation.

CC0: http://creativecommons.org/publicdomain/zero/1.0/

====

Files located in the node_modules and vendor directories are externally
maintained libraries used by this software which have their own
licenses; we recommend you read them, as their terms may differ from the
terms above.
```

## CryptoJS 4.2.0 (`internal/request/js/crypto-js.js`)

The unmodified `crypto-js.js` build from the
[`crypto-js@4.2.0` npm archive](https://registry.npmjs.org/crypto-js/-/crypto-js-4.2.0.tgz)
is embedded for `require('crypto-js')` in request and rule scripts. Its SHA-256 is
`ee02257ffbaf0a9b481c7039b0f3bb20c360c9674fe4be8b38ae709b2ea59bbe`.

```
The MIT License (MIT)

Copyright (c) 2009-2013 Jeff Mott
Copyright (c) 2013-2016 Evan Vosberg

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
```

## Moment 2.31.0 (`internal/request/js/moment.min.js`)

The unmodified `min/moment.min.js` browser build from the
[`moment@2.31.0` npm archive](https://registry.npmjs.org/moment/-/moment-2.31.0.tgz)
is embedded for `require('moment')` in request scripts and `import moment from
"moment"` in rule scripts, addons and Code Mocks. Postman's sandbox pins the
2.x line (2.30.1) and loads `moment/min/moment.min` without locale files; this
is the latest 2.x release. Its SHA-256 is
`c111ad2e4447c253765e679bb4a40ba91a3e23d80b4b69d603a0fd25164cb9de`.

The upstream license follows:

```
Copyright (c) OpenJS Foundation and other contributors

Permission is hereby granted, free of charge, to any person
obtaining a copy of this software and associated documentation
files (the "Software"), to deal in the Software without
restriction, including without limitation the rights to use,
copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the
Software is furnished to do so, subject to the following
conditions:

The above copyright notice and this permission notice shall be
included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES
OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND
NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT
HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY,
WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING
FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
OTHER DEALINGS IN THE SOFTWARE.
```

## Chai 4.5.0 (`internal/request/js/chai.js`)

The unmodified browser build of [Chai](https://www.chaijs.com/) from the
`chai@4.5.0` npm package is embedded as `pm.expect` for request scripts.

```
MIT License

Copyright (c) 2017 Chai.js Assertion Library

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## Postman sandbox libraries

The request-script sandbox embeds or bundles the libraries Postman's own
sandbox exposes, so imported Postman collections can `require` them unchanged.
`internal/request/js/sandbox/package-lock.json` pins every input; `sources.txt`
records the resolved tarball and integrity of each package, `hashes.txt` records
the SHA-256 of every generated bundle, and `build.sh` regenerates them with the
esbuild version in `go.mod`. The bundles live in `internal/request/js/vendor/`.

| Module | Version | Included as | License | Source tarball and SHA-256 |
| --- | --- | --- | --- | --- |
| `cheerio` | 0.22.0 | bundled | MIT | `https://registry.npmjs.org/cheerio/-/cheerio-0.22.0.tgz` `1634ec618e17aaf4dd27db65e24afadd544eb929c9572241ed4b47b3103e9d7d` |
| `xml2js` | 0.6.2 | bundled | MIT | `https://registry.npmjs.org/xml2js/-/xml2js-0.6.2.tgz` `6386154e63b28c5f4411bf2348596105d262e36600104dd9584e2b7251f56a11` |
| `csv-parse` | 1.2.4 | bundled (`csv-parse/lib/sync`) | BSD-3-Clause | `https://registry.npmjs.org/csv-parse/-/csv-parse-1.2.4.tgz` `a6a9ed37570c9217ebd56917715b9ac2ccaceb3dd4e4762b4a2ddd04aea42d19` |
| `tv4` | 1.3.0 | embedded | Public Domain / MIT | `https://registry.npmjs.org/tv4/-/tv4-1.3.0.tgz` `07dd35dfb9e8647dfad2a834d49e3c8eff91d14d3a0deaf7a3fcf4381ec670fe` |
| `uuid` | 3.4.0 | bundled | MIT | `https://registry.npmjs.org/uuid/-/uuid-3.4.0.tgz` `ba77c9306dbc34c7ae503e9eb142e284f98ea9ab609f416052c2fbbefb6df4dd` |
| `postman-collection` | 5.3.1 | bundled | Apache-2.0 | `https://registry.npmjs.org/postman-collection/-/postman-collection-5.3.1.tgz` `e486b4b4b61397bcc6aee2bbe5b0829c0bca6fb574a190d8a69d0266bc8e7275` |
| `liquid-json` | 0.3.1 | bundled (`liquid-json`, `json`) | Apache-2.0 | `https://registry.npmjs.org/liquid-json/-/liquid-json-0.3.1.tgz` `3b7f56ca72c69b593c4f2ef399851fb85b1073e270660664ba6b227980c7d0be` |
| `backbone` | 1.6.0 | bundled | MIT | `https://registry.npmjs.org/backbone/-/backbone-1.6.0.tgz` `6e0001cd0ee3ec26622497333c7ca03fb7217241f0dffbc2eea7f3ee33aa7d3c` |
| `assert` | 2.0.0 | bundled (`assert`) | MIT | `https://registry.npmjs.org/assert/-/assert-2.0.0.tgz` `5ef8ad20f664087abb7c48b0c6a2909b1c8daadc3b3a454c8688a91786bd21c7` |
| `buffer` | 6.0.3 | bundled (`buffer`) | MIT | `https://registry.npmjs.org/buffer/-/buffer-6.0.3.tgz` `555b6f49224afcf6b2c5a9aea7cd34a7f443f395b10532bc5580022d57aa51f5` |
| `events` | 2.1.0 | bundled (`events`) | MIT | `https://registry.npmjs.org/events/-/events-2.1.0.tgz` `513bd5d61e7c1562e825603eff86154b6b59a33051e3f69d30488f9ab5b88b93` |
| `os-browserify` | 0.3.0 | bundled (`os`) | MIT | `https://registry.npmjs.org/os-browserify/-/os-browserify-0.3.0.tgz` `7df96551e0d82458745fb7dea7e9c0739f5a0c06d20607592f9a0a00af60b738` |
| `path-browserify` | 0.0.1 | bundled (`path`) | MIT | `https://registry.npmjs.org/path-browserify/-/path-browserify-0.0.1.tgz` `b3e1d5374bbffb6414cbf83e4519e694d937d10534b3a80ccef5c02205e3011a` |
| `process` | 0.11.10 | bundled (`process`, `_process`) | MIT | `https://registry.npmjs.org/process/-/process-0.11.10.tgz` `7c10569b3c9cb056152ad630d40f9f4fcc321a0013c2bb8384f036aaa674e6bb` |
| `punycode` | 2.3.1 | bundled (`punycode`) | MIT | `https://registry.npmjs.org/punycode/-/punycode-2.3.1.tgz` `e4ce59f9fbac44349abab87279ab658f6b4614916bec5e088ae3be9323e193bb` |
| `querystring-es3` | 0.2.1 | bundled (`querystring`) | MIT | `https://registry.npmjs.org/querystring-es3/-/querystring-es3-0.2.1.tgz` `e5a389fe3a87b47ab022d6c1aa77688aa4fd0496e636de9f573c45db256f283e` |
| `stream-browserify` | 2.0.2 | bundled (`stream`) | MIT | `https://registry.npmjs.org/stream-browserify/-/stream-browserify-2.0.2.tgz` `1b977256eef691f7a8602397e9d88b6883baf72ed45d54391b1dd6d4ed921467` |
| `string_decoder` | 1.3.0 | bundled (`string_decoder`) | MIT | `https://registry.npmjs.org/string_decoder/-/string_decoder-1.3.0.tgz` `7d036900940345e423538371ae45b23bfaf4b454bc398c6ba604063b0597b3ad` |
| `url` | 0.11.0 | bundled (`url`) | MIT | `https://registry.npmjs.org/url/-/url-0.11.0.tgz` `bc4e2a3ad68ea88e1ad619bd475152f9ae972eaadae94912a729d867dc9356e6` |
| `util` | 0.12.3 | bundled (`util`) | MIT | `https://registry.npmjs.org/util/-/util-0.12.3.tgz` `56db6eb0a7ecb0e8f2c52e413aea65041c68dd9eb0c04d4f452d36253416cbe5` |
| `sax` | 1.2.4 | bundled (xml2js dependency) | ISC | `https://registry.npmjs.org/sax/-/sax-1.2.4.tgz` `adc442e017041cffca4d4418d06006ae5f17a9984557fa80c3a6cd8859021cf2` |
| `underscore` | 1.13.3 | bundled (backbone dependency) | MIT | `https://registry.npmjs.org/underscore/-/underscore-1.13.3.tgz` `ae3988d1f00e0cbded2804df51d0c8cc050f048193ab6af21ebdc22d81dfdffc` |

Postman's sandbox maps `uuid` to a small unversioned vendored v4 shim; this app
bundles `uuid` 3.4.0 from Postman's lock, which has the same callable
`uuid()` / `uuid.v4()` shape. `fs` is an empty module and `timers` is
vscroxy's script-scoped timer queue; both are written for this app under the
MIT license. Ajv 6.12.6, Chai 4.5.0,
Lodash 4.18.1, CryptoJS 4.2.0 and Moment 2.31.0 are covered by their own
sections above.

The bundles and their pinned build/test inputs contain the following
packages. MIT: `@faker-js/faker` 5.5.3, `ajv` 6.12.6, `assert` 2.0.0,
`assertion-error` 1.1.0, `available-typed-arrays` 1.0.7, `backbone` 1.6.0,
`base64-js` 1.5.1, `buffer` 6.0.3, `call-bind` 1.0.9,
`call-bind-apply-helpers` 1.0.2, `call-bound` 1.0.4, `chai` 4.5.0,
`charset` 1.0.1, `check-error` 1.0.3, `cheerio` 0.22.0, `core-util-is` 1.0.3,
`crypto-js` 4.2.0, `deep-eql` 4.1.4, `define-data-property` 1.1.4,
`define-properties` 1.2.1, `dom-serializer` 0.1.1, `dunder-proto` 1.0.1,
`es-define-property` 1.0.1, `es-errors` 1.3.0, `es-object-atoms` 1.1.2,
`es6-object-assign` 1.1.0, `events` 2.1.0, `fast-deep-equal` 3.1.3,
`fast-json-stable-stringify` 2.1.0, `file-type` 3.9.0, `for-each` 0.3.5,
`function-bind` 1.1.2, `generator-function` 2.0.1, `get-func-name` 2.0.2,
`get-intrinsic` 1.3.0, `get-proto` 1.0.1, `gopd` 1.2.0,
`has-property-descriptors` 1.0.2, `has-symbols` 1.1.0,
`has-tostringtag` 1.0.2, `hasown` 2.0.4, `htmlparser2` 3.10.1,
`iconv-lite` 0.6.3, `is-arguments` 1.2.0, `is-callable` 1.2.7,
`is-generator-function` 1.1.2, `is-nan` 1.3.2, `is-regex` 1.2.1,
`is-typed-array` 1.1.15, `isarray` 1.0.0, `json-schema-traverse` 0.4.1,
`lodash` 4.18.1, `lodash.assignin` 4.2.0, `lodash.bind` 4.2.1,
`lodash.defaults` 4.2.0, `lodash.filter` 4.6.0, `lodash.flatten` 4.4.0,
`lodash.foreach` 4.5.0, `lodash.map` 4.6.0, `lodash.merge` 4.6.2,
`lodash.pick` 4.4.0, `lodash.reduce` 4.6.0, `lodash.reject` 4.6.0,
`lodash.some` 4.6.0, `loupe` 2.3.7, `math-intrinsics` 1.1.0, `mime` 3.0.0,
`moment` 2.31.0, `object-is` 1.1.6, `object-keys` 1.1.1,
`os-browserify` 0.3.0, `path-browserify` 0.0.1, `pathval` 1.1.1,
`possible-typed-array-names` 1.1.0, `process` 0.11.10,
`process-nextick-args` 2.0.1, `punycode` 2.3.1, `querystring` 0.2.0,
`querystring-es3` 0.2.1, `readable-stream` 2.3.8 and 3.6.2,
`safe-buffer` 5.2.1, `safe-regex-test` 1.1.0, `safer-buffer` 2.1.2,
`set-function-length` 1.2.2, `stream-browserify` 2.0.2,
`string_decoder` 1.3.0, `type-detect` 4.1.0, `underscore` 1.13.3,
`url` 0.11.0, `util` 0.12.3, `util-deprecate` 1.0.2, `uuid` 3.4.0,
`which-typed-array` 1.1.24, `xml2js` 0.6.2, `xmlbuilder` 11.0.1.
ISC: `boolbase` 1.0.0, `inherits` 2.0.4, `sax` 1.2.4, `semver` 7.7.1.
BSD-2-Clause: `css-select` 1.2.0, `css-what` 2.1.3, `domelementtype` 1.3.1,
`domhandler` 2.4.2, `domutils` 1.5.1, `entities` 1.1.2, `nth-check` 1.0.2,
`uri-js` 4.4.1. BSD-3-Clause: `csv-parse` 1.2.4, `ieee754` 1.2.1.
Apache-2.0: `http-reasons` 0.1.0, `liquid-json` 0.3.1, `mime-format` 2.0.2,
`postman-collection` 5.3.1, `postman-url-encoder` 3.0.8. Public Domain / MIT:
`tv4` 1.3.0.

### MIT

The MIT-licensed packages above keep their authors' notices. The common text
is:

```
MIT License

Copyright (c) their respective authors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

Copyright notices include: Cheerio, Copyright (c) Matt Mueller; xml2js,
Copyright 2010-2013; uuid, Copyright (c) 2010-2016 Robert Kieffer and other
contributors; Backbone, Copyright (c) 2010-2024 Jeremy Ashkenas,
DocumentCloud; underscore, Copyright (c) 2009-2022 Jeremy Ashkenas, Julian
Gonggrijp, and DocumentCloud and Investigative Reporters & Editors; assert,
buffer, events, path-browserify, process, punycode, querystring-es3,
stream-browserify, string_decoder, url and util, Copyright Joyent, Inc. and
other Node contributors or their listed authors; os-browserify, Copyright (c)
2017 CoderPuppy; buffer, Copyright (c) Feross Aboukhadijeh and other
contributors.

### ISC

```
ISC License

Copyright (c) Isaac Z. Schlueter and Contributors

Permission to use, copy, modify, and/or distribute this software for any
purpose with or without fee is hereby granted, provided that the above
copyright notice and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF OR
IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
```

### BSD-3-Clause

```text
Software License Agreement (BSD License)
========================================

Copyright (c) 2011, SARL Adaltas.

All rights reserved.

Redistribution and use of this software in source and binary forms, with or without modification, are permitted provided that the following conditions are met:

-   Redistributions of source code must retain the above copyright notice, this list of conditions and the following disclaimer.

-   Redistributions in binary form must reproduce the above copyright notice, this list of conditions and the following disclaimer in the documentation and/or other materials provided with the distribution.

-   Neither the name of SARL Adaltas nor the names of its contributors may be used to endorse or promote products derived from this software without specific prior written permission of the SARL Adaltas.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

### BSD-2-Clause

```text
Copyright (c) Felix Böhm
All rights reserved.

Redistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met:

Redistributions of source code must retain the above copyright notice, this list of conditions and the following disclaimer.

Redistributions in binary form must reproduce the above copyright notice, this list of conditions and the following disclaimer in the documentation and/or other materials provided with the distribution.

THIS IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS,
EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

### Apache-2.0

The Apache-2.0 packages are `postman-collection` 5.3.1, `liquid-json` 0.3.1,
`http-reasons` 0.1.0, `mime-format` 2.0.2 and `postman-url-encoder` 3.0.8.

```text
Apache License
                           Version 2.0, January 2004
                        http://www.apache.org/licenses/

   TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION

   1. Definitions.

      "License" shall mean the terms and conditions for use, reproduction,
      and distribution as defined by Sections 1 through 9 of this document.

      "Licensor" shall mean the copyright owner or entity authorized by
      the copyright owner that is granting the License.

      "Legal Entity" shall mean the union of the acting entity and all
      other entities that control, are controlled by, or are under common
      control with that entity. For the purposes of this definition,
      "control" means (i) the power, direct or indirect, to cause the
      direction or management of such entity, whether by contract or
      otherwise, or (ii) ownership of fifty percent (50%) or more of the
      outstanding shares, or (iii) beneficial ownership of such entity.

      "You" (or "Your") shall mean an individual or Legal Entity
      exercising permissions granted by this License.

      "Source" form shall mean the preferred form for making modifications,
      including but not limited to software source code, documentation
      source, and configuration files.

      "Object" form shall mean any form resulting from mechanical
      transformation or translation of a Source form, including but
      not limited to compiled object code, generated documentation,
      and conversions to other media types.

      "Work" shall mean the work of authorship, whether in Source or
      Object form, made available under the License, as indicated by a
      copyright notice that is included in or attached to the work
      (an example is provided in the Appendix below).

      "Derivative Works" shall mean any work, whether in Source or Object
      form, that is based on (or derived from) the Work and for which the
      editorial revisions, annotations, elaborations, or other modifications
      represent, as a whole, an original work of authorship. For the purposes
      of this License, Derivative Works shall not include works that remain
      separable from, or merely link (or bind by name) to the interfaces of,
      the Work and Derivative Works thereof.

      "Contribution" shall mean any work of authorship, including
      the original version of the Work and any modifications or additions
      to that Work or Derivative Works thereof, that is intentionally
      submitted to Licensor for inclusion in the Work by the copyright owner
      or by an individual or Legal Entity authorized to submit on behalf of
      the copyright owner. For the purposes of this definition, "submitted"
      means any form of electronic, verbal, or written communication sent
      to the Licensor or its representatives, including but not limited to
      communication on electronic mailing lists, source code control systems,
      and issue tracking systems that are managed by, or on behalf of, the
      Licensor for the purpose of discussing and improving the Work, but
      excluding communication that is conspicuously marked or otherwise
      designated in writing by the copyright owner as "Not a Contribution."

      "Contributor" shall mean Licensor and any individual or Legal Entity
      on behalf of whom a Contribution has been received by Licensor and
      subsequently incorporated within the Work.

   2. Grant of Copyright License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      copyright license to reproduce, prepare Derivative Works of,
      publicly display, publicly perform, sublicense, and distribute the
      Work and such Derivative Works in Source or Object form.

   3. Grant of Patent License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      (except as stated in this section) patent license to make, have made,
      use, offer to sell, sell, import, and otherwise transfer the Work,
      where such license applies only to those patent claims licensable
      by such Contributor that are necessarily infringed by their
      Contribution(s) alone or by combination of their Contribution(s)
      with the Work to which such Contribution(s) was submitted. If You
      institute patent litigation against any entity (including a
      cross-claim or counterclaim in a lawsuit) alleging that the Work
      or a Contribution incorporated within the Work constitutes direct
      or contributory patent infringement, then any patent licenses
      granted to You under this License for that Work shall terminate
      as of the date such litigation is filed.

   4. Redistribution. You may reproduce and distribute copies of the
      Work or Derivative Works thereof in any medium, with or without
      modifications, and in Source or Object form, provided that You
      meet the following conditions:

      (a) You must give any other recipients of the Work or
          Derivative Works a copy of this License; and

      (b) You must cause any modified files to carry prominent notices
          stating that You changed the files; and

      (c) You must retain, in the Source form of any Derivative Works
          that You distribute, all copyright, patent, trademark, and
          attribution notices from the Source form of the Work,
          excluding those notices that do not pertain to any part of
          the Derivative Works; and

      (d) If the Work includes a "NOTICE" text file as part of its
          distribution, then any Derivative Works that You distribute must
          include a readable copy of the attribution notices contained
          within such NOTICE file, excluding those notices that do not
          pertain to any part of the Derivative Works, in at least one
          of the following places: within a NOTICE text file distributed
          as part of the Derivative Works; within the Source form or
          documentation, if provided along with the Derivative Works; or,
          within a display generated by the Derivative Works, if and
          wherever such third-party notices normally appear. The contents
          of the NOTICE file are for informational purposes only and
          do not modify the License. You may add Your own attribution
          notices within Derivative Works that You distribute, alongside
          or as an addendum to the NOTICE text from the Work, provided
          that such additional attribution notices cannot be construed
          as modifying the License.

      You may add Your own copyright statement to Your modifications and
      may provide additional or different license terms and conditions
      for use, reproduction, or distribution of Your modifications, or
      for any such Derivative Works as a whole, provided Your use,
      reproduction, and distribution of the Work otherwise complies with
      the conditions stated in this License.

   5. Submission of Contributions. Unless You explicitly state otherwise,
      any Contribution intentionally submitted for inclusion in the Work
      by You to the Licensor shall be under the terms and conditions of
      this License, without any additional terms or conditions.
      Notwithstanding the above, nothing herein shall supersede or modify
      the terms of any separate license agreement you may have executed
      with Licensor regarding such Contributions.

   6. Trademarks. This License does not grant permission to use the trade
      names, trademarks, service marks, or product names of the Licensor,
      except as required for reasonable and customary use in describing the
      origin of the Work and reproducing the content of the NOTICE file.

   7. Disclaimer of Warranty. Unless required by applicable law or
      agreed to in writing, Licensor provides the Work (and each
      Contributor provides its Contributions) on an "AS IS" BASIS,
      WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
      implied, including, without limitation, any warranties or conditions
      of TITLE, NON-INFRINGEMENT, MERCHANTABILITY, or FITNESS FOR A
      PARTICULAR PURPOSE. You are solely responsible for determining the
      appropriateness of using or redistributing the Work and assume any
      risks associated with Your exercise of permissions under this License.

   8. Limitation of Liability. In no event and under no legal theory,
      whether in tort (including negligence), contract, or otherwise,
      unless required by applicable law (such as deliberate and grossly
      negligent acts) or agreed to in writing, shall any Contributor be
      liable to You for damages, including any direct, indirect, special,
      incidental, or consequential damages of any character arising as a
      result of this License or out of the use or inability to use the
      Work (including but not limited to damages for loss of goodwill,
      work stoppage, computer failure or malfunction, or any and all
      other commercial damages or losses), even if such Contributor
      has been advised of the possibility of such damages.

   9. Accepting Warranty or Additional Liability. While redistributing
      the Work or Derivative Works thereof, You may choose to offer,
      and charge a fee for, acceptance of support, warranty, indemnity,
      or other liability obligations and/or rights consistent with this
      License. However, in accepting such obligations, You may act only
      on Your own behalf and on Your sole responsibility, not on behalf
      of any other Contributor, and only if You agree to indemnify,
      defend, and hold each Contributor harmless for any liability
      incurred by, or claims asserted against, such Contributor by reason
      of your accepting any such warranty or additional liability.

   END OF TERMS AND CONDITIONS
```

### tv4

`tv4` 1.3.0 is released into the public domain by Geraint Luff and others; its
`package.json` also records an MIT option for the JSONary-derived parts. The
source header is:

```text
Author: Geraint Luff and others
Year: 2013

This code is released into the "public domain" by its author(s).  Anybody may
use, alter and distribute the code without restriction.  The author makes no
guarantees, and takes no liability of any kind for use of this code.
```

## Onevra (`github.com/oneo-me/onevra/source`)

Only `source/icons/lucide` is imported. The module also ships components, shells
and examples, which this app does not use; their own notices are reproduced
after the icons' for completeness.

```
MIT License

Copyright (c) 2026 ONEO

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## Lucide icons (`source/icons/lucide`)

The generated Go icons are pinned to a Lucide commit in the package's
`upstream.json`.

```
ISC License

Copyright (c) 2026 Lucide Icons and Contributors

Permission to use, copy, modify, and/or distribute this software for any
purpose with or without fee is hereby granted, provided that the above
copyright notice and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
```

The following Lucide icons are derived from the Feather project:

```
airplay, alert-circle, alert-octagon, alert-triangle, aperture, arrow-down-circle, arrow-down-left, arrow-down-right, arrow-down, arrow-left-circle, arrow-left, arrow-right-circle, arrow-right, arrow-up-circle, arrow-up-left, arrow-up-right, arrow-up, at-sign, calendar, cast, check, chevron-down, chevron-left, chevron-right, chevron-up, chevrons-down, chevrons-left, chevrons-right, chevrons-up, circle, clipboard, clock, code, columns, command, compass, corner-down-left, corner-down-right, corner-left-down, corner-left-up, corner-right-down, corner-right-up, corner-up-left, corner-up-right, crosshair, database, divide-circle, divide-square, dollar-sign, download, external-link, feather, frown, hash, headphones, help-circle, info, italic, key, layout, life-buoy, link-2, link, loader, lock, log-in, log-out, maximize, meh, minimize, minimize-2, minus-circle, minus-square, minus, monitor, moon, more-horizontal, more-vertical, move, music, navigation-2, navigation, octagon, pause-circle, percent, plus-circle, plus-square, plus, power, radio, rss, search, server, share, shopping-bag, sidebar, smartphone, smile, square, table-2, tablet, target, terminal, trash-2, trash, triangle, tv, type, upload, x-circle, x-octagon, x-square, x, zoom-in, zoom-out
```

```
The MIT License (MIT) (for the icons listed above)

Copyright (c) 2013-present Cole Bemis

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## MyGo components (`source/components`)

Not imported by this app.

```
MIT License

Copyright (c) 2026 MyGo contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## MQTT clients

The MQTT document links two maintained Eclipse Paho Go clients, unmodified:
`github.com/eclipse/paho.mqtt.golang` v1.5.1 for MQTT 3.1.1 and
`github.com/eclipse/paho.golang` v0.23.0 for MQTT 5.0. Both are dual-licensed
under the Eclipse Public License 2.0 and the Eclipse Distribution License 1.0
(BSD 3-Clause); vscroxy uses them under the EDL 1.0. The MQTT 5.0 CONNECT
encoder in `internal/request/mqtt_client.go` works around Paho v0.23 encoding
will properties with the CONNECT property context; it is vscroxy code, not a
modified Paho file.

```
Eclipse Distribution License - v 1.0

Copyright (c) 2007, Eclipse Foundation, Inc. and its licensors.
All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

  * Redistributions of source code must retain the above copyright notice,
    this list of conditions and the following disclaimer.
  * Redistributions in binary form must reproduce the above copyright notice,
    this list of conditions and the following disclaimer in the documentation
    and/or other materials provided with the distribution.
  * Neither the name of the Eclipse Foundation, Inc. nor the names of its
    contributors may be used to endorse or promote products derived from this
    software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR CONTRIBUTORS BE
LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR
CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF
SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS
INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN
CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE)
ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE
POSSIBILITY OF SUCH DAMAGE.
```

The tests run against the unmodified `github.com/mochi-mqtt/server/v2`
v2.7.9 in-process broker under the MIT license:

```
MIT License

Copyright (c) 2023 Mochi-MQTT Organisation
Copyright (c) 2019, 2022, 2023 Jonathan Blake (mochi-co)

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
