package request

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/capture"
	"golang.org/x/net/http/httpguts"
)

// Checks stay declarative: sharing a case never imports executable scripts.
// JSON checks address scalar fields with RFC 6901 pointers, not body snapshots.
type Checks struct {
	Status  string `json:"status,omitempty"`
	Headers string `json:"headers,omitempty"`
	JSON    string `json:"json,omitempty"`
}

type JSONCheck struct {
	pointer jsontext.Pointer
	want    jsontext.Value
}

func (r Checks) Parse() (int, http.Header, []JSONCheck, error) {
	status := 0
	if r.Status != "" {
		var err error
		status, err = strconv.Atoi(r.Status)
		if err != nil || status < 100 || status > 599 {
			return 0, nil, nil, errors.New("Expected status must be an HTTP code from 100 to 599, or empty.")
		}
	}
	headers := http.Header{}
	for i, line := range strings.Split(r.Headers, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) {
			return 0, nil, nil, fmt.Errorf("Header check line %d must be a valid Name: Value pair.", i+1)
		}
		headers.Add(name, value)
	}
	var checks []JSONCheck
	for i, line := range strings.Split(r.JSON, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		path, value, ok := strings.Cut(line, " = ")
		pointer, want := jsontext.Pointer(strings.TrimSpace(path)), jsontext.Value(strings.TrimSpace(value))
		if !ok || !pointer.IsValid() || !want.IsValid() || want.Kind() == '{' || want.Kind() == '[' {
			return 0, nil, nil, fmt.Errorf("JSON check line %d: use /field/path = a JSON string, number, boolean or null.", i+1)
		}
		checks = append(checks, JSONCheck{pointer, want})
	}
	return status, headers, checks, nil
}

func (r Checks) Evaluate(f capture.Flow) []TestResult {
	status, headers, checks, _ := r.Parse() // Validated before sending.
	var results []TestResult
	if status != 0 {
		results = append(results, TestResult{Name: fmt.Sprintf("Status equals %d (received %d)", status, f.Status), Pass: status == f.Status})
	}
	for _, name := range slices.Sorted(maps.Keys(headers)) {
		for _, want := range headers[name] {
			// Any repeated header may match; missing is not equal to empty.
			results = append(results, TestResult{Name: "Header " + name + " equals expected value", Pass: slices.Contains(f.ResponseHeader.Values(name), want)})
		}
	}
	for _, check := range checks {
		got, ok := jsonField(jsontext.Value(f.ResponseBody), check.pointer)
		complete := f.ResponseBodySize <= len(f.ResponseBody) && f.ResponseBodyError == ""
		results = append(results, TestResult{Name: "JSON " + string(check.pointer) + " equals expected value", Pass: complete && ok && equalJSONScalar(got, check.want)})
	}
	return results
}

// Script writes the checks as a Postman test script, for an export: one
// pm.test per result Evaluate reports, under the same name. Numbers compare
// as JavaScript does, so past 2^53 two IDs Evaluate tells apart can pass.
func (r Checks) Script() (string, error) {
	status, headers, checks, err := r.Parse()
	if err != nil || status == 0 && len(headers) == 0 && len(checks) == 0 {
		return "", err
	}
	quote := func(s string) string {
		b, _ := json.Marshal(s)
		return jsLiteral(b)
	}
	var b strings.Builder
	b.WriteString("// vscroxy Checks\n(function () {\n")
	if status != 0 {
		fmt.Fprintf(&b, "\tpm.test(%s + pm.response.code + \")\", function () {\n\t\tpm.expect(pm.response.code).to.equal(%d);\n\t});\n",
			quote(fmt.Sprintf("Status equals %d (received ", status)), status)
	}
	for _, name := range slices.Sorted(maps.Keys(headers)) {
		for _, want := range headers[name] {
			// Any repeated header may match; missing is not equal to empty.
			fmt.Fprintf(&b, "\tpm.test(%s, function () {\n\t\tpm.expect(pm.response.headers.all().some(function (h) { return h.key.toLowerCase() === %s && h.value === %s; }), %s).to.be.true;\n\t});\n",
				quote("Header "+name+" equals expected value"), quote(strings.ToLower(name)), quote(want), quote("a "+name+" header of "+want))
		}
	}
	if len(checks) > 0 {
		b.WriteString(`	// field walks a JSON Pointer's tokens into the response body.
	function field(tokens) {
		var value;
		try { value = JSON.parse(pm.response.text()); } catch (e) { return { found: false }; }
		for (var i = 0; i < tokens.length; i++) {
			var token = tokens[i];
			if (Array.isArray(value)) {
				if (!/^(0|[1-9][0-9]*)$/.test(token) || Number(token) >= value.length) return { found: false };
				value = value[Number(token)];
			} else if (value !== null && typeof value === "object" && Object.prototype.hasOwnProperty.call(value, token)) {
				value = value[token];
			} else {
				return { found: false };
			}
		}
		return { found: true, value: value };
	}
`)
		for _, check := range checks {
			tokens, _ := json.Marshal(slices.Collect(check.pointer.Tokens()))
			fmt.Fprintf(&b, "\tpm.test(%s, function () {\n\t\tvar got = field(%s);\n\t\tpm.expect(got.found, %s).to.be.true;\n\t\tpm.expect(got.value).to.equal(%s);\n\t});\n",
				quote("JSON "+string(check.pointer)+" equals expected value"), jsLiteral(tokens), quote(string(check.pointer)+" in the JSON body"), jsLiteral(check.want))
		}
	}
	b.WriteString("})();")
	return b.String(), nil
}

// jsLiteral is JSON as a JavaScript literal: the same text, with the line
// separators JSON allows in strings escaped for older engines.
func jsLiteral(b []byte) string {
	return strings.NewReplacer("\u2028", `\u2028`, "\u2029", `\u2029`).Replace(string(b))
}

func jsonField(raw jsontext.Value, pointer jsontext.Pointer) (jsontext.Value, bool) {
	if !raw.IsValid() {
		return nil, false
	}
	for token := range pointer.Tokens() {
		switch raw.Kind() {
		case '{':
			var object map[string]jsontext.Value
			if json.Unmarshal(raw, &object) != nil {
				return nil, false
			}
			var ok bool
			raw, ok = object[token]
			if !ok {
				return nil, false
			}
		case '[':
			var array []jsontext.Value
			i, err := strconv.Atoi(token)
			if err != nil || strconv.Itoa(i) != token || json.Unmarshal(raw, &array) != nil || i < 0 || i >= len(array) {
				return nil, false
			}
			raw = array[i]
		default:
			return nil, false
		}
	}
	return raw, true
}

func equalJSONScalar(a, b jsontext.Value) bool {
	if a.Kind() != b.Kind() {
		return false
	}
	if a.Kind() == '0' {
		// Do not round large IDs to float64, nor confuse 42 with "42".
		for _, raw := range []jsontext.Value{a, b} {
			if len(raw) > 1024 {
				return false
			}
			if _, exp, ok := strings.Cut(strings.ToLower(string(raw)), "e"); ok {
				n, err := strconv.Atoi(exp)
				if err != nil || n < -10000 || n > 10000 {
					return false
				}
			}
		}
		x, ok := new(big.Rat).SetString(string(a))
		y, valid := new(big.Rat).SetString(string(b))
		return ok && valid && x.Cmp(y) == 0
	}
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && x == y
}

// Only names travel with a case, never the active environment's values.
func (r Request) Variables() []string {
	var names []string
	texts := []string{r.Method, r.URL, r.Headers}
	if r.Protocol == "Socket.IO" {
		texts = append(texts, r.SocketIO.Namespace, r.SocketIO.Auth, r.SocketIO.Event, r.SocketIO.Attachments)
	}
	if r.Protocol == "MQTT" {
		texts = append(texts, r.MQTT.mqttStrings()...)
	}
	if r.Protocol == "MCP" {
		texts = append(texts, r.MCP.Command, r.MCP.Args, r.MCP.WorkingDir, r.MCP.Tool, r.MCP.ToolArgs,
			r.MCP.ResourceURI, r.MCP.Prompt, r.MCP.PromptArgs)
		for _, field := range r.MCP.Env {
			if !field.Disabled {
				texts = append(texts, field.Name, field.Value)
			}
		}
	}
	if r.Protocol == "Socket.IO" || r.BodyMode == "" || r.BodyMode == "Raw" {
		texts = append(texts, r.Body)
	} else if r.BodyMode == "Binary" {
		texts = append(texts, r.BodyFile)
	} else if r.BodyMode == "GraphQL" {
		texts = append(texts, r.GraphQL.Query, r.GraphQL.Variables)
	} else {
		for _, field := range r.Form {
			if !field.Disabled {
				texts = append(texts, field.Name, field.Value)
			}
		}
	}
	switch r.Auth.Type {
	case "Bearer":
		texts = append(texts, r.Auth.Token)
	case "Basic", AuthDigest:
		texts = append(texts, r.Auth.Username, r.Auth.Password)
	case AuthAWS:
		for _, f := range r.Auth.AWS.fields() {
			texts = append(texts, *f)
		}
	case AuthJWT:
		for _, f := range r.Auth.JWT.fields() {
			texts = append(texts, *f)
		}
	case AuthOAuth1:
		for _, f := range r.Auth.OAuth1.fields() {
			texts = append(texts, *f)
		}
	case AuthHawk:
		for _, f := range r.Auth.Hawk.fields() {
			texts = append(texts, *f)
		}
	case AuthEdgeGrid:
		for _, f := range r.Auth.EdgeGrid.fields() {
			texts = append(texts, *f)
		}
	case AuthNTLM:
		for _, f := range r.Auth.NTLM.fields() {
			texts = append(texts, *f)
		}
	case "API key":
		texts = append(texts, r.Auth.Key, r.Auth.Value)
	}
	for _, text := range texts {
		for {
			_, rest, ok := strings.Cut(text, "{{")
			if !ok {
				break
			}
			name, tail, ok := strings.Cut(rest, "}}")
			if !ok {
				break
			}
			names = append(names, strings.TrimSpace(name))
			text = tail
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}
