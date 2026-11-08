// Generates internal/request/testdata/sandbox-expectations.json.
//
// Run it with the pinned npm inputs installed (build.sh does that):
//
//	cd internal/request/js/sandbox
//	node expectations.mjs > ../../testdata/sandbox-expectations.json
//
// The Go tests run the same expressions through the embedded bundles, so the
// expected values come from Node with the exact library versions Postman
// pins, not from the implementation under test.
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);

const modules = {
	cheerio: {
		prelude: `const cheerio = require("cheerio");
const html = "<html><head><title>Hello &amp; welcome</title></head><body><ul id='list'><li class='item' data-id='1'>One</li><li class='item' data-id='2'>Two</li></ul><p class='note'>A <b>bold</b> note</p></body></html>";`,
		cases: [
			["title text", `cheerio.load(html)("title").text()`],
			["selector count", `cheerio.load(html)("#list .item").length`],
			["attribute map", `cheerio.load(html)(".item").map(function () { return cheerio(this).attr("data-id"); }).get().join(",")`],
			["attribute selector", `cheerio.load(html)("li[data-id='2']").text()`],
			["descendant selector", `cheerio.load(html)("p.note b").text()`],
			["attribute value", `cheerio.load(html)("ul").attr("id")`],
			["missing selector", `cheerio.load(html)(".missing").length`],
			["html round trip", `cheerio.load("<div><span>x</span></div>").html()`],
		],
	},
	xml2js: {
		prelude: `const xml2js = require("xml2js");
const xml = "<root id='7'><item>A</item><item>B</item><empty/></root>";`,
		cases: [
			["callback result", `(() => { let out; xml2js.parseString(xml, { explicitArray: false }, (err, result) => { out = result; }); return out; })()`],
			["explicit arrays", `(() => { let out; xml2js.parseString(xml, (err, result) => { out = result; }); return out; })()`],
			["attributes", `(() => { let out; xml2js.parseString(xml, { explicitArray: false }, (err, result) => { out = result.root.$; }); return out; })()`],
			["parseStringPromise", `await xml2js.parseStringPromise(xml, { explicitArray: false })`],
			["async callback", `await new Promise((resolve) => xml2js.parseString(xml, { async: true, explicitArray: false }, (err, result) => resolve(result)))`],
			["malformed has an error", `(() => { let out = false; xml2js.parseString("<root>", { explicitArray: false }, (err) => { out = !!err; }); return out; })()`],
		],
	},
	"csv-parse/lib/sync": {
		prelude: `const parse = require("csv-parse/lib/sync");`,
		cases: [
			["columns", `parse("a,b\\n1,2\\n3,4", { columns: true })`],
			["quoted comma", `parse("a,b\\n1,\\"x,y\\"", { columns: true })`],
			["tab delimiter", `parse("a\\tb\\n1\\t2", { delimiter: "\\t", columns: true })`],
			["rows", `parse("a,b\\n1,2")`],
			["buffer input", `parse(Buffer.from("a,b\\n1,2"), { columns: true })`],
			["object name", `parse("a,b\\n1,2", { objname: "a" })`],
		],
	},
	ajv: {
		prelude: `const Ajv = require("ajv");`,
		cases: [
			["valid", `new Ajv().validate({ type: "object", required: ["a"] }, { a: 1 })`],
			["invalid", `new Ajv().validate({ type: "object", required: ["a"] }, { b: 1 })`],
			["all errors", `(() => { const ajv = new Ajv({ allErrors: true }); ajv.validate({ type: "object", required: ["a", "b"] }, { a: 1 }); return ajv.errors.map((e) => e.keyword + ":" + e.params.missingProperty).join(","); })()`],
			["compiled validator", `new Ajv().compile({ type: "array", items: { type: "integer" } })([1, 2, 3])`],
			["coerce types", `new Ajv({ coerceTypes: true }).validate({ type: "object", properties: { n: { type: "number" } } }, { n: "2" })`],
		],
	},
	tv4: {
		prelude: `const tv4 = require("tv4");`,
		cases: [
			["valid", `tv4.validate({ a: 1 }, { type: "object", required: ["a"] })`],
			["invalid", `tv4.validate({ b: 1 }, { type: "object", required: ["a"] })`],
			["error message", `(() => { tv4.validate({ b: 1 }, { type: "object", required: ["a"] }); return tv4.error && tv4.error.message; })()`],
			["multiple errors", `tv4.validateMultiple({ b: 1 }, { type: "object", required: ["a"] }).errors.length`],
			["integer minimum", `tv4.validate(5, { type: "integer", minimum: 1 })`],
		],
	},
	uuid: {
		prelude: `const uuid = require("uuid");`,
		cases: [
			["callable", `typeof uuid`],
			["v4", `typeof uuid.v4`],
			["call length", `uuid().length`],
			["v4 length", `uuid.v4().length`],
			["v4 shape", `/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(uuid.v4())`],
			["version nibble", `uuid.v4()[14]`],
			["unique", `new Set(Array.from({ length: 100 }, () => uuid.v4())).size`],
		],
	},
	buffer: {
		prelude: `const { Buffer } = require("./node_modules/buffer");`,
		goPrelude: `const { Buffer } = require("buffer");`,
		cases: [
			["base64 encode", `Buffer.from("hello").toString("base64")`],
			["base64 decode", `Buffer.from("aGVsbG8=", "base64").toString()`],
			["hex", `Buffer.from("hello").toString("hex")`],
			["length", `Buffer.from([1, 2, 3]).length`],
			["concat", `Buffer.concat([Buffer.from("a"), Buffer.from("b")]).toString()`],
			["isBuffer", `Buffer.isBuffer(Buffer.alloc(2))`],
			["toJSON", `Buffer.from("hi").toJSON()`],
			["byteLength", `Buffer.byteLength("hé")`],
			["slice", `Buffer.from("hello").slice(1, 3).toString()`],
		],
	},
	url: {
		prelude: `const url = require("./node_modules/url");`,
		goPrelude: `const url = require("url");`,
		cases: [
			["host", `url.parse("https://user:pass@example.com:8080/p/a?x=1&y=2#f").host`],
			["hostname", `url.parse("https://user:pass@example.com:8080/p/a?x=1&y=2#f").hostname`],
			["pathname", `url.parse("https://user:pass@example.com:8080/p/a?x=1&y=2#f").pathname`],
			["query", `url.parse("https://user:pass@example.com:8080/p/a?x=1&y=2#f").query`],
			["hash", `url.parse("https://user:pass@example.com:8080/p/a?x=1&y=2#f").hash`],
			["auth", `url.parse("https://user:pass@example.com:8080/p/a?x=1&y=2#f").auth`],
			["format", `url.format({ protocol: "https:", host: "example.com", pathname: "/a", query: { x: "1" } })`],
			["resolve", `url.resolve("https://example.com/a/b", "../c")`],
			["protocol", `url.parse("http://example.com").protocol`],
		],
	},
	querystring: {
		prelude: `const qs = require("./node_modules/querystring-es3");`,
		goPrelude: `const qs = require("querystring");`,
		cases: [
			["parse repeated", `qs.parse("x=1&y=2&y=3")`],
			["stringify repeated", `qs.stringify({ x: "1", y: ["2", "3"] })`],
			["plus is space", `qs.parse("a+b=c+d")["a b"]`],
		],
	},
	path: {
		prelude: `const path = require("./node_modules/path-browserify");`,
		goPrelude: `const path = require("path");`,
		cases: [
			["join", `path.join("/a", "b", "..", "c.txt")`],
			["normalize", `path.normalize("/a//b/../c")`],
			["basename", `path.basename("/a/b/c.txt")`],
			["dirname", `path.dirname("/a/b/c.txt")`],
			["extname", `path.extname("/a/b/c.txt")`],
			["relative", `path.relative("/a/b", "/a/c")`],
		],
	},
	util: {
		prelude: `const util = require("./node_modules/util");`,
		goPrelude: `const util = require("util");`,
		cases: [
			["format", `util.format("%s=%d", "x", 2)`],
			["format json", `util.format("%j", { a: 1 })`],
			["inspect", `util.inspect({ a: 1 })`],
			["isArray", `util.isArray([])`],
			["isDate", `util.isDate(new Date(0))`],
		],
	},
	events: {
		prelude: `const { EventEmitter } = require("./node_modules/events");`,
		goPrelude: `const { EventEmitter } = require("events");`,
		cases: [
			["on emit", `(() => { const e = new EventEmitter(); let out; e.on("x", (v) => { out = v; }); e.emit("x", "yes"); return out; })()`],
			["once", `(() => { const e = new EventEmitter(); let n = 0; e.once("x", () => n++); e.emit("x"); e.emit("x"); return n; })()`],
			["listener count", `(() => { const e = new EventEmitter(); e.on("x", () => {}); e.on("x", () => {}); return e.listenerCount("x"); })()`],
			["remove listener", `(() => { const e = new EventEmitter(); const fn = () => {}; e.on("x", fn); e.removeListener("x", fn); return e.listenerCount("x"); })()`],
			["emit returns", `new EventEmitter().emit("missing")`],
		],
	},
	stream: {
		prelude: `const stream = require("./node_modules/stream-browserify");`,
		goPrelude: `const stream = require("stream");`,
		cases: [
			["constructors", `[typeof stream.Readable, typeof stream.Writable, typeof stream.PassThrough].join(",")`],
			["pass through", `await new Promise((resolve) => { const p = new stream.PassThrough(); let out = ""; p.on("data", (chunk) => { out += chunk.toString(); }); p.on("end", () => resolve(out)); p.end("hello"); })`],
			["writable", `await new Promise((resolve) => { const w = new stream.Writable({ write(chunk, enc, cb) { resolve(chunk.toString()); cb(); } }); w.write("ok"); })`],
		],
	},
	assert: {
		prelude: `const assert = require("./node_modules/assert");`,
		goPrelude: `const assert = require("assert");`,
		cases: [
			["strict equal", `(() => { assert.strictEqual(1, 1); return "ok"; })()`],
			["failure name", `(() => { try { assert.strictEqual(1, 2); } catch (e) { return e.name; } })()`],
			["deep strict equal", `(() => { assert.deepStrictEqual({ a: [1] }, { a: [1] }); return "ok"; })()`],
			["ok", `(() => { assert.ok(true); return "ok"; })()`],
		],
	},
	string_decoder: {
		prelude: `const { StringDecoder } = require("./node_modules/string_decoder");`,
		goPrelude: `const { StringDecoder } = require("string_decoder");`,
		cases: [
			["split multibyte", `(() => { const d = new StringDecoder("utf8"); return d.write(Buffer.from([0xe4, 0xb8])) + d.write(Buffer.from([0xad])); })()`],
			["end flushes", `(() => { const d = new StringDecoder("utf8"); return d.write(Buffer.from("hi")) + d.end(); })()`],
			["ascii", `(() => { const d = new StringDecoder("ascii"); return d.write(Buffer.from("abc")); })()`],
		],
	},
	punycode: {
		prelude: `const punycode = require("./node_modules/punycode");`,
		goPrelude: `const punycode = require("punycode");`,
		cases: [
			["toASCII", `punycode.toASCII("münich.example")`],
			["toUnicode", `punycode.toUnicode("xn--mnich-kva.example")`],
			["encode", `punycode.encode("münich")`],
			["decode", `punycode.decode("mnich-kva")`],
		],
	},
	os: {
		prelude: `const os = require("os-browserify/browser.js");`,
		goPrelude: `const os = require("os");`,
		cases: [
			["platform", `os.platform()`],
			["EOL", `os.EOL`],
			["tmpdir", `os.tmpdir()`],
			["type", `typeof os.cpus()`],
		],
	},
	"postman-collection": {
		prelude: `const sdk = require("postman-collection");`,
		cases: [
			["collection name", `new sdk.Collection({ info: { name: "Demo" } }).name`],
			["request url", `new sdk.Request("https://example.com/a?b=1").url.toString()`],
			["request header", `new sdk.Request({ url: "https://example.com/a", method: "POST", header: [{ key: "X-Test", value: "1" }] }).headers.get("X-Test")`],
			["query value", `new sdk.Request("https://example.com/a?b=1").url.query.get("b")`],
			["variable scope", `new sdk.VariableScope({ values: [{ key: "a", value: "1" }] }).get("a")`],
			["is collection", `sdk.Collection.isCollection(new sdk.Collection({ info: { name: "Demo" } }))`],
			["version", `typeof sdk.Version`],
		],
	},
	"liquid-json": {
		prelude: `const liquid = require("liquid-json");`,
		cases: [
			["parse", `liquid.parse('{"a":1}')`],
			["stringify", `liquid.stringify({ a: 1 })`],
			["number", `liquid.parse('{"a":1.5}')`],
			["invalid", `(() => { try { liquid.parse("{oops}"); return "parsed"; } catch (e) { return "error"; } })()`],
		],
	},
	backbone: {
		prelude: `const Backbone = require("backbone");`,
		cases: [
			["model default", `new (Backbone.Model.extend({ defaults: { x: 1 } }))().get("x")`],
			["model set", `(() => { const m = new Backbone.Model(); m.set("x", 2); return m.get("x"); })()`],
			["collection", `(() => { const c = new Backbone.Collection([{ id: 1 }, { id: 2 }]); return c.length; })()`],
			["events", `(() => { const m = new Backbone.Model(); let out; m.on("change:x", () => { out = m.get("x"); }); m.set("x", 3); return out; })()`],
			["version", `Backbone.VERSION`],
		],
	},
};

const output = { comment: "Generated by internal/request/js/sandbox/expectations.mjs with the npm versions in package-lock.json.", modules: {} };
for (const [name, module] of Object.entries(modules)) {
	const source = `${module.prelude}\nreturn (async () => [${module.cases.map(([, expr]) => `JSON.stringify(${expr})`).join(", ")}])();`;
	const values = await new Function("require", source)(require);
	output.modules[name] = {
		prelude: module.prelude,
		...(module.goPrelude ? { goPrelude: module.goPrelude } : {}),
		cases: module.cases.map(([caseName, expr], i) => ({ name: caseName, expr, want: values[i] })),
	};
}
process.stdout.write(JSON.stringify(output, null, 2) + "\n");
