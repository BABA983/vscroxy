package httpfile

import (
	"testing"

	"github.com/baba983/vscroxy/internal/request"
)

func TestWriteStripsJSONCForRESTClients(t *testing.T) {
	cases := []struct {
		name, contentType, body, want string
	}{
		{"comment and trailing comma", "application/json", "{\n  // note\n  \"a\": 1,\n}", "{\n  \n  \"a\": 1\n}"},
		{"non-json body", "text/plain", "// not json, {}", "// not json, {}"},
		{"invalid jsonc", "application/json", `{"a":1,`, `{"a":1,`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := request.Request{Name: tc.name, Method: "POST", URL: "https://api.example.test/items",
				Headers: "Content-Type: " + tc.contentType, Body: tc.body}
			data, warnings, err := Write("Exported", []request.Request{r}, nil)
			if err != nil || len(warnings) != 0 {
				t.Fatalf("write: %v, %v", warnings, err)
			}
			doc, err := Parse("Exported", data)
			if err != nil || len(doc.Requests) != 1 {
				t.Fatalf("read back the .http file: %v, %+v", err, doc)
			}
			if got := doc.Requests[0].Body; got != tc.want {
				t.Fatalf("exported body = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWriteStripsJSONCInPrepareFallback(t *testing.T) {
	body := "{\n  // note\n  \"a\": 1,\n}"
	r := request.Request{Name: "Needs sign-in", Method: "POST", URL: "https://api.example.test/items",
		Headers: "Content-Type: application/json", Body: body,
		Auth: request.Auth{Type: request.AuthOAuth2}}
	data, warnings, err := Write("Exported", []request.Request{r}, nil)
	if err != nil || len(warnings) == 0 {
		t.Fatalf("write: %v, %v", warnings, err)
	}
	doc, err := Parse("Exported", data)
	if err != nil || len(doc.Requests) != 1 {
		t.Fatalf("read back the .http file: %v, %+v", err, doc)
	}
	if got, want := doc.Requests[0].Body, "{\n  \n  \"a\": 1\n}"; got != want {
		t.Fatalf("exported body = %q, want %q", got, want)
	}
}
