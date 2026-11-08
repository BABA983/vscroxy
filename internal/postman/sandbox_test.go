package postman_test

import (
	"testing"

	"github.com/baba983/vscroxy/internal/postman"
	"github.com/baba983/vscroxy/internal/request"
)

// TestImportedCollectionSandboxLibraries runs a script exactly as it arrives
// in an imported Postman collection. It uses several sandbox libraries and
// skips the send, so no network is needed; the values it sets prove the
// modules loaded and produced results.
func TestImportedCollectionSandboxLibraries(t *testing.T) {
	const collection = `{
  "info": {
    "name": "Sandbox libraries",
    "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"
  },
  "item": [{
    "name": "Parse an imported response",
    "request": {
      "method": "GET",
      "url": {"raw": "https://example.com/data", "protocol": "https", "host": ["example", "com"], "path": ["data"]}
    },
    "event": [{
      "listen": "prerequest",
      "script": {"type": "text/javascript", "exec": [
        "const cheerio = require('cheerio');",
        "const xml2js = require('xml2js');",
        "const parse = require('csv-parse/lib/sync');",
        "const uuid = require('uuid');",
        "const Ajv = require('ajv');",
        "const qs = require('querystring');",
        "const $ = cheerio.load('<title>Imported &amp; parsed</title>');",
        "let xml;",
        "xml2js.parseString('<root><item>2</item></root>', { explicitArray: false }, (err, result) => { xml = result; });",
        "const rows = parse('a,b\\n1,2', { columns: true });",
        "const ajv = new Ajv();",
        "pm.environment.set('title', $('title').text());",
        "pm.environment.set('xml', xml.root.item);",
        "pm.environment.set('csv', rows[0].b);",
        "pm.environment.set('uuid', /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(uuid.v4()) ? 'ok' : 'bad');",
        "pm.environment.set('schema', ajv.validate({ type: 'object', required: ['a'] }, { a: 1 }) ? 'valid' : 'invalid');",
        "pm.environment.set('query', qs.stringify({ x: '1', y: '2' }));",
        "pm.execution.skipRequest();"
      ]}
    }]
  }]
}`
	read, err := postman.ReadCollection([]byte(collection))
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Requests) != 1 {
		t.Fatalf("imported %d requests, want 1", len(read.Requests))
	}
	out := read.Requests[0].Execute(t.Context(), nil, request.Context{Folders: read.Folders}, nil, nil)
	if out.Err != nil {
		t.Fatalf("%s: %v", out.Stage, out.Err)
	}
	if !out.Skipped {
		t.Fatal("the imported script did not skip the request")
	}
	want := map[string]string{
		"title": "Imported & parsed", "xml": "2", "csv": "2", "uuid": "ok",
		"schema": "valid", "query": "x=1&y=2",
	}
	for name, value := range want {
		if got := out.Vars[name]; got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}
