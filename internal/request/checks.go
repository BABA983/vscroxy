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
	if r.BodyMode == "" || r.BodyMode == "Raw" {
		texts = append(texts, r.Body)
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
	case "Basic":
		texts = append(texts, r.Auth.Username, r.Auth.Password)
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
