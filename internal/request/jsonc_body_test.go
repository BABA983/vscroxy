package request

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestJSONCBodyThroughSend exercises the public send path: the server uses a
// standard JSON decoder, so comments or a trailing comma on the wire fail.
func TestJSONCBodyThroughSend(t *testing.T) {
	var sent string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		sent = string(body)
		var value map[string]any
		if err := json.Unmarshal(body, &value); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()
	srv := testProxy(t)

	// The commas before another property stay; only the final one is trailing.
	authored := "{\n  // note\n  \"name\": \"demo\", /* trailing */\n  \"url\": \"http://x\", // marker\n  \"comment\": \"/* inside */\",\n}"
	want := "{\n  \n  \"name\": \"demo\", \n  \"url\": \"http://x\", \n  \"comment\": \"/* inside */\"\n}"

	r := Request{Method: "POST", URL: upstream.URL, Headers: "Content-Type: application/json",
		Body: authored,
		Pre:  `pm.environment.set("pre", pm.request.body.raw)`,
		Post: `pm.environment.set("post", pm.request.body.raw)`}
	out := r.Execute(t.Context(), srv, Context{}, nil, nil)
	if out.Err != nil || out.Response == nil || out.Failed() {
		t.Fatalf("JSONC send: %s: %v", out.Stage, out.Err)
	}
	if sent != want {
		t.Fatalf("sent %q, want %q", sent, want)
	}
	if r.Body != authored {
		t.Fatalf("sending changed the authored request: %q", r.Body)
	}
	if got := out.Vars["pre"]; got != authored {
		t.Errorf("pre script saw %q, want the authored JSONC", got)
	}
	if got := out.Vars["post"]; got != want {
		t.Errorf("post script saw %q, want the sent JSON %q", got, want)
	}
}

func TestNonJSONAndInvalidJSONCBodiesAreSentAsAuthored(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		_, _ = w.Write(body)
	}))
	defer upstream.Close()
	srv := testProxy(t)

	for _, tc := range []struct {
		name, body, contentType string
	}{
		{"non-json with json type", "plain // text, not json", "application/json"},
		{"invalid jsonc with json type", "{\"a\":1,", "application/json"},
		{"jsonc-looking text body", "{\"a\":1,}", "text/plain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Request{Method: "POST", URL: upstream.URL, Headers: "Content-Type: " + tc.contentType, Body: tc.body}
			out := r.Execute(t.Context(), srv, Context{}, nil, nil)
			if out.Err != nil || out.Response == nil || out.Failed() {
				t.Fatalf("send: %s: %v", out.Stage, out.Err)
			}
			if got := out.Response.ResponseBody; got != tc.body {
				t.Fatalf("sent %q, want %q", got, tc.body)
			}
		})
	}
}
