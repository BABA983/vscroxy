// The Postman-compatible script API. Go supplies `host` (variables, the
// request, the response, sends, the vault and test results) and Chai; this
// file shapes them into pm.*, console and the legacy globals Postman still
// runs: tests[...], postman.*, responseBody, responseCode and the like.
(function (global, host, chai) {
	"use strict";

	function text(v) {
		if (v === undefined || v === null) return "";
		if (typeof v === "string") return v;
		if (typeof v === "object") {
			try { return JSON.stringify(v); } catch (e) { return String(v); }
		}
		return String(v);
	}

	function format(values) {
		var parts = [];
		for (var i = 0; i < values.length; i++) {
			var v = values[i];
			parts.push(typeof v === "string" ? v : v instanceof Error ? String(v) : text(v));
		}
		return parts.join(" ");
	}

	function errorText(e) {
		if (e && typeof e === "object" && e.message !== undefined) {
			return (e.name && e.name !== "AssertionError" ? e.name + ": " : "") + e.message;
		}
		return text(e);
	}

	// A variable scope. Only writable scopes get set, unset and clear.
	function scope(name, writable) {
		var s = {
			has: function (key) { return host.get(name, text(key)) !== undefined; },
			get: function (key) { return host.get(name, text(key)); },
			toObject: function () { return host.object(name); },
			replaceIn: function (template) { return host.replaceIn(text(template)); },
		};
		s.toJSON = s.toObject;
		if (writable) {
			s.set = function (key, value) { host.set(name, text(key), text(value)); };
			s.unset = function (key) { host.unset(name, text(key)); };
			s.clear = function () { host.clear(name); };
		}
		return s;
	}

	// A read-only view of [key, value] pairs with Postman's PropertyList calls.
	function pairList(read) {
		function all() {
			var pairs = read(), out = [];
			for (var i = 0; i < pairs.length; i++) out.push({ key: pairs[i][0], value: pairs[i][1] });
			return out;
		}
		function find(key) {
			var wanted = text(key).toLowerCase(), list = all();
			for (var i = 0; i < list.length; i++) if (list[i].key.toLowerCase() === wanted) return list[i];
		}
		return {
			all: all,
			get: function (key) { var h = find(key); return h ? h.value : undefined; },
			one: function (key) { return find(key); },
			has: function (key, value) {
				var h = find(key);
				return !!h && (arguments.length < 2 || h.value === text(value));
			},
			count: function () { return all().length; },
			each: function (fn) { all().forEach(fn); },
			map: function (fn) { return all().map(fn); },
			filter: function (fn) { return all().filter(fn); },
			toObject: function () {
				var out = {}, list = all();
				for (var i = 0; i < list.length; i++) out[list[i].key] = list[i].value;
				return out;
			},
			toString: function () {
				return all().map(function (h) { return h.key + ": " + h.value; }).join("\n");
			},
		};
	}

	// header accepts {key, value}, "Key: value" or (key, value).
	function header(h, value) {
		if (typeof h === "string" && value !== undefined) return { key: h, value: text(value) };
		if (typeof h === "string") {
			var at = h.indexOf(":");
			return at < 0 ? { key: h.trim(), value: "" } : { key: h.slice(0, at).trim(), value: h.slice(at + 1).trim() };
		}
		return { key: text(h && h.key), value: text(h && h.value) };
	}

	function headerList(read, add, upsert, remove) {
		var list = pairList(read);
		if (add) {
			list.add = function (h, v) { h = header(h, v); add(h.key, h.value); };
			list.upsert = function (h, v) { h = header(h, v); upsert(h.key, h.value); };
			list.remove = function (key) { remove(typeof key === "object" ? text(key.key) : text(key)); };
			list.clear = function () { list.all().forEach(function (h) { remove(h.key); }); };
		}
		return list;
	}

	// Query parameters are kept as written: Postman neither decodes nor encodes.
	function splitURL(raw) {
		var hash = "", query = null, at = raw.indexOf("#");
		if (at >= 0) { hash = raw.slice(at); raw = raw.slice(0, at); }
		at = raw.indexOf("?");
		if (at >= 0) { query = raw.slice(at + 1); raw = raw.slice(0, at); }
		var params = [];
		if (query) {
			query.split("&").forEach(function (part) {
				if (part === "") return;
				var eq = part.indexOf("=");
				params.push(eq < 0 ? [part, null] : [part.slice(0, eq), part.slice(eq + 1)]);
			});
		}
		return { base: raw, params: params, hash: hash };
	}

	function joinURL(u) {
		var q = u.params.map(function (p) { return p[1] === null ? p[0] : p[0] + "=" + p[1]; }).join("&");
		return u.base + (u.params.length ? "?" + q : "") + u.hash;
	}

	function makeURL(get, set) {
		function edit(fn) { var u = splitURL(get()); fn(u); set(joinURL(u)); }
		function authority() {
			var base = splitURL(get()).base, at = base.indexOf("://");
			var rest = at >= 0 ? base.slice(at + 3) : base, slash = rest.indexOf("/");
			return { host: slash < 0 ? rest : rest.slice(0, slash), path: slash < 0 ? "/" : rest.slice(slash), protocol: at >= 0 ? base.slice(0, at) : "" };
		}
		var query = pairList(function () {
			return splitURL(get()).params.map(function (p) { return [p[0], p[1] === null ? "" : p[1]]; });
		});
		query.add = function (p, v) { p = typeof p === "string" && v === undefined ? splitURL("?" + p).params[0] || [p, null] : [text(p.key !== undefined ? p.key : p), text(p.value !== undefined ? p.value : v)]; edit(function (u) { u.params.push(p); }); };
		query.upsert = function (p, v) {
			var key = text(p.key !== undefined ? p.key : p), value = text(p.value !== undefined ? p.value : v);
			edit(function (u) {
				for (var i = 0; i < u.params.length; i++) if (u.params[i][0] === key) { u.params[i][1] = value; return; }
				u.params.push([key, value]);
			});
		};
		query.remove = function (key) {
			key = text(typeof key === "object" ? key.key : key);
			edit(function (u) { u.params = u.params.filter(function (p) { return p[0] !== key; }); });
		};
		query.clear = function () { edit(function (u) { u.params = []; }); };
		return {
			query: query,
			toString: function () { return get(); },
			toJSON: function () { return get(); },
			update: function (raw) { set(text(raw)); },
			getHost: function () { var h = authority().host, at = h.lastIndexOf("@"); h = at >= 0 ? h.slice(at + 1) : h; var colon = h.lastIndexOf(":"); return colon >= 0 && h.indexOf("]") < colon ? h.slice(0, colon) : h; },
			getPath: function () { return authority().path; },
			getQueryString: function () { return joinURL({ base: "", params: splitURL(get()).params, hash: "" }).slice(1); },
			getRemote: function () { return authority().host; },
			get protocol() { return authority().protocol; },
			addQueryParams: function (params) { [].concat(params).forEach(function (p) { query.add(p); }); },
			removeQueryParams: function (keys) { [].concat(keys).forEach(function (k) { query.remove(k); }); },
		};
	}

	function makeRequest(r) {
		var url = makeURL(function () { return r.get("url"); }, function (v) { r.set("url", v); });
		var headers = headerList(r.headers, r.add, r.upsert, r.remove);
		function body() {
			return {
				mode: "raw",
				get raw() { return r.get("body"); },
				set raw(v) { r.set("body", text(v)); },
				toString: function () { return r.get("body"); },
				update: function (v) { r.set("body", typeof v === "object" && v !== null && "raw" in v ? text(v.raw) : text(v)); },
				isEmpty: function () { return r.get("body") === ""; },
			};
		}
		var request = {
			get method() { return r.get("method"); },
			set method(v) { r.set("method", text(v)); },
			get url() { return url; },
			set url(v) { r.set("url", typeof v === "object" && v !== null && v.raw !== undefined ? text(v.raw) : text(String(v))); },
			headers: headers,
			get body() { return body(); },
			set body(v) { body().update(v); },
			addHeader: headers.add || function () {},
			removeHeader: headers.remove || function () {},
			upsertHeader: headers.upsert || function () {},
			addQueryParams: url.addQueryParams,
			removeQueryParams: url.removeQueryParams,
			toJSON: function () { return { method: r.get("method"), url: r.get("url"), header: headers.all(), body: { mode: "raw", raw: r.get("body") } }; },
		};
		return request;
	}

	function parseJSON(body) {
		try { return JSON.parse(body); } catch (e) { throw new SyntaxError("The response body is not JSON: " + e.message); }
	}

	// jsonPath reads "a.b[0].c" from a parsed body; found is false when absent.
	function jsonPath(value, path) {
		var parts = String(path).replace(/\[(\d+)\]/g, ".$1").split(".").filter(Boolean);
		for (var i = 0; i < parts.length; i++) {
			if (value === null || typeof value !== "object" || !(parts[i] in value)) return { found: false };
			value = value[parts[i]];
		}
		return { found: true, value: value };
	}

	// assertions are pm.response.to.*: the Postman Chai plugin's response checks.
	function assertions(res, negate) {
		var a = {};
		["to", "be", "have", "a", "an", "and", "is", "with", "that", "which", "has"].forEach(function (word) {
			Object.defineProperty(a, word, { get: function () { return a; } });
		});
		Object.defineProperty(a, "not", { get: function () { return assertions(res, !negate); } });
		function check(pass, message) {
			if (pass === negate) throw new chai.AssertionError((negate ? "expected response not " : "expected response ") + message);
		}
		function between(lo, hi, label) {
			return function () { check(res.code >= lo && res.code <= hi, "to be " + label + " but got status " + res.code); return a; };
		}
		function exactly(code, label) {
			return function () { check(res.code === code, "to be " + label + " (" + code + ") but got status " + res.code); return a; };
		}
		var getters = {
			ok: exactly(200, "OK"), accepted: exactly(202, "accepted"), badRequest: exactly(400, "a bad request"),
			unauthorized: exactly(401, "unauthorized"), unauthorised: exactly(401, "unauthorised"),
			forbidden: exactly(403, "forbidden"), notFound: exactly(404, "not found"), rateLimited: exactly(429, "rate limited"),
			info: between(100, 199, "informational"), success: between(200, 299, "successful"),
			redirection: between(300, 399, "a redirection"), clientError: between(400, 499, "a client error"),
			serverError: between(500, 599, "a server error"), error: between(400, 599, "an error"),
			json: function () {
				var parsed = true;
				try { JSON.parse(res.text()); } catch (e) { parsed = false; }
				check(parsed, "to have a JSON body");
				return a;
			},
			withBody: function () { check(res.text() !== "", "to have a body"); return a; },
		};
		Object.keys(getters).forEach(function (name) { Object.defineProperty(a, name, { get: getters[name] }); });
		a.status = function (want) {
			if (typeof want === "number") check(res.code === want, "to have status code " + want + " but got " + res.code);
			else check(res.status === text(want), "to have status reason " + text(want) + " but got " + res.status);
			return a;
		};
		a.header = function (key, value) {
			if (arguments.length < 2) check(res.headers.has(key), "to have header " + key);
			else check(res.headers.get(key) === text(value), "to have header " + key + ": " + text(value) + " but got " + text(res.headers.get(key)));
			return a;
		};
		a.body = function (want) {
			var body = res.text();
			if (arguments.length === 0) check(body !== "", "to have a body");
			else if (want instanceof RegExp) check(want.test(body), "body to match " + want);
			else check(body === text(want), "body to equal " + text(want));
			return a;
		};
		a.jsonBody = function (path, value) {
			var body;
			try { body = JSON.parse(res.text()); } catch (e) { check(false, "to have a JSON body"); return a; }
			if (arguments.length === 0) { check(true, "to have a JSON body"); return a; }
			if (typeof path === "object") { check(chai.util.eql(body, path), "JSON body to deeply equal " + text(path)); return a; }
			var got = jsonPath(body, path);
			if (arguments.length < 2) check(got.found, "JSON body to have " + path);
			else check(got.found && chai.util.eql(got.value, value), "JSON body " + path + " to equal " + text(value) + " but got " + text(got.value));
			return a;
		};
		a.responseTime = { below: function (ms) { check(res.responseTime < ms, "time to be below " + ms + " ms but took " + res.responseTime); return a; } };
		a.jsonSchema = function () { throw new Error("pm.response.to.have.jsonSchema is not supported; check fields with pm.expect"); };
		return a;
	}

	function makeResponse(r) {
		var res = {
			code: r.code,
			status: r.status,
			responseTime: r.time,
			responseSize: r.size,
			headers: headerList(function () { return r.headers; }),
			text: function () { return r.body; },
			json: function () { return parseJSON(r.body); },
			reason: function () { return r.status; },
			size: function () { return { body: r.body.length, total: r.size }; },
		};
		res.stream = r.body;
		Object.defineProperty(res, "to", { get: function () { return assertions(res, false); } });
		return res;
	}

	// sendDefinition turns pm.sendRequest's argument into what host.send takes.
	function sendDefinition(def) {
		if (typeof def === "string") return { method: "GET", url: def, header: [], mode: "Raw", body: "", form: [] };
		var url = def.url;
		if (url && typeof url === "object") url = url.raw !== undefined ? url.raw : String(url);
		var headers = [], h = def.header !== undefined ? def.header : def.headers;
		if (typeof h === "string") h = h.split("\n");
		if (Array.isArray(h)) {
			h.forEach(function (one) { if (one && !one.disabled) { one = header(one); if (one.key) headers.push([one.key, one.value]); } });
		} else if (h && typeof h === "object") {
			Object.keys(h).forEach(function (k) { headers.push([k, text(h[k])]); });
		}
		var out = { method: text(def.method || "GET"), url: text(url), header: headers, mode: "Raw", body: "", form: [] };
		var body = def.body;
		if (typeof body === "string") out.body = body;
		else if (body && typeof body === "object") {
			var fields = function (list, files) {
				return (list || []).filter(function (f) { return !f.disabled; }).map(function (f) {
					var file = files && f.type === "file";
					return { name: text(f.key), value: text(file ? f.src : f.value), file: !!file };
				});
			};
			if (body.mode === "urlencoded") { out.mode = "URL encoded"; out.form = fields(body.urlencoded, false); }
			else if (body.mode === "formdata") { out.mode = "Multipart"; out.form = fields(body.formdata, true); }
			else if (body.mode === "graphql") {
				var gql = body.graphql || {};
				out.body = JSON.stringify({ query: gql.query, variables: typeof gql.variables === "string" ? JSON.parse(gql.variables || "{}") : gql.variables || {} });
				if (!headers.some(function (one) { return one[0].toLowerCase() === "content-type"; })) headers.push(["Content-Type", "application/json"]);
			} else out.body = text(body.raw);
		}
		return out;
	}

	var results = [];
	function record(name, fn) {
		var entry = { name: text(name), pass: false, error: "", skipped: false, pending: true };
		results.push(entry);
		function settle(err) {
			if (!entry.pending) return;
			entry.pending = false;
			entry.pass = !err;
			entry.error = err ? errorText(err) : "";
		}
		try {
			if (typeof fn !== "function") settle(new TypeError("pm.test needs a function"));
			else if (fn.length > 0) fn(function (err) { settle(err); });
			else {
				var out = fn();
				if (out && typeof out.then === "function") out.then(function () { settle(); }, settle);
				else settle();
			}
		} catch (e) {
			settle(e);
		}
	}

	var location = Array.prototype.slice.call(host.info.location);
	var pm = {
		info: {
			eventName: host.info.eventName,
			iteration: host.info.iteration,
			iterationCount: host.info.iterationCount,
			requestName: host.info.requestName,
			requestId: host.info.requestId,
		},
		environment: scope("environment", true),
		globals: scope("globals", true),
		collectionVariables: scope("collectionVariables", true),
		variables: scope("variables", true),
		iterationData: scope("iterationData", false),
		expect: chai.expect,
		test: function (name, fn) { record(name, fn); return pm; },
		sendRequest: function (def, callback) {
			var res, failure = null;
			try {
				var d = sendDefinition(def);
				res = makeResponse(host.send(d.method, d.url, d.header, d.mode, d.body, d.form));
			} catch (e) { failure = e; }
			if (typeof callback === "function") callback(failure, res);
			return failure ? Promise.reject(failure) : Promise.resolve(res);
		},
		execution: {
			location: location,
			skipRequest: function () { host.skip(); },
			setNextRequest: function (name) { host.next(name === null || name === undefined ? null : text(name)); },
		},
		cookies: (function () {
			var list = pairList(host.cookies);
			return { has: function (n) { return list.has(n); }, get: function (n) { return list.get(n); }, toObject: list.toObject };
		})(),
		vault: {
			get: function (name) { try { return Promise.resolve(host.vault("get", text(name), "")); } catch (e) { return Promise.reject(e); } },
			set: function (name, value) { try { host.vault("set", text(name), text(value)); return Promise.resolve(); } catch (e) { return Promise.reject(e); } },
			unset: function (name) { try { host.vault("unset", text(name), ""); return Promise.resolve(); } catch (e) { return Promise.reject(e); } },
		},
		visualizer: { set: function () { host.log("warning", "pm.visualizer is not supported; the template was ignored"); }, clear: function () {} },
		require: function (name) { return global.require(name); },
	};
	pm.environment.name = host.info.environment;
	pm.test.skip = function (name) { results.push({ name: text(name), pass: true, skipped: true, error: "", pending: false }); return pm; };
	pm.test.index = function () { return results.length; };
	pm.execution.location.current = location[location.length - 1];
	pm.expect.fail = chai.assert.fail;
	if (host.request) pm.request = makeRequest(host.request);
	if (host.response) pm.response = makeResponse(host.response);
	global.pm = pm;

	global.console = {
		log: function () { host.log("log", format(arguments)); },
		info: function () { host.log("log", format(arguments)); },
		debug: function () { host.log("log", format(arguments)); },
		warn: function () { host.log("warning", format(arguments)); },
		error: function () { host.log("error", format(arguments)); },
		clear: function () {},
	};
	global.require = function (name) {
		if (name === "chai") return chai;
		throw new Error("require(\"" + name + "\") is not available in vscroxy scripts");
	};
	global.atob = host.atob;
	global.btoa = host.btoa;

	// Legacy Postman globals and vscroxy's original vars/test/log API.
	global.tests = {};
	global.postman = {
		setEnvironmentVariable: pm.environment.set,
		getEnvironmentVariable: pm.environment.get,
		clearEnvironmentVariable: pm.environment.unset,
		setGlobalVariable: pm.globals.set,
		getGlobalVariable: pm.globals.get,
		clearGlobalVariable: pm.globals.unset,
		setNextRequest: pm.execution.setNextRequest,
	};
	global.vars = { get: function (k) { return host.get("variables", text(k)) || ""; }, set: function (k, v) { host.set("legacy", text(k), text(v)); } };
	global.test = function (name, pass) { results.push({ name: text(name), pass: !!pass, error: "", skipped: false, pending: false }); };
	global.log = function () { host.log("log", format(arguments)); };
	if (host.response) {
		global.responseBody = host.response.body;
		global.responseCode = { code: host.response.code, name: host.response.status, detail: host.response.status };
		global.responseTime = host.response.time;
		global.responseHeaders = pm.response.headers.toObject();
	}

	// finish reports every test: pm.test, test() and tests[...] alike.
	return function finish() {
		Object.keys(global.tests).forEach(function (name) {
			results.push({ name: name, pass: !!global.tests[name], error: "", skipped: false, pending: false });
		});
		return results.map(function (r) {
			return r.pending ? { name: r.name, pass: false, error: "The test did not finish", skipped: false } : r;
		});
	};
});
