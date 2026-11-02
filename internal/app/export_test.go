package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json/v2"
	"net/http"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/postman"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

type exportFixture struct {
	name string
	data []byte
	// Markers in each explicitly selected scope, including duplicate copies.
	markers map[string][]string
}

func exportFixtures(t *testing.T) []exportFixture {
	t.Helper()
	url := "https://person:auth-password@example.test/path-to-review?token=query-secret&token=query-second#auth-fragment"
	r := request.Request{Name: "Name to review", Collection: "Collection/Folder", Method: "POST", URL: url,
		Headers: "Authorization: header-secret\nX-Custom: header-custom\nAccept: application/json\nContent-Type: application/json",
		Body:    "body-secret", Auth: request.Auth{Type: "Bearer", Token: "auth-token"},
		Pre: `throw "notes-pre"`, Post: `throw "notes-post"`, Checks: request.Checks{Status: "201", JSON: `/id = "assertion-to-review"`},
		Examples: []request.Example{{Name: "Example to review", Status: 201, Headers: "Set-Cookie: header-cookie", Body: "body-example",
			Request: request.ExampleRequest{Method: "POST", URL: url, Headers: "X-Example: header-example", Body: "body-original"}}}}
	folders := []request.Folder{
		{Path: "Collection", Description: "notes-collection", Pre: `throw "notes-root"`, Auth: request.Auth{Type: "Basic", Username: "auth-root-user", Password: "auth-root-password"}, Variables: []request.Field{{Name: "root", Value: "variable-root"}}},
		{Path: "Collection/Folder", Description: "notes-folder", Post: `throw "notes-folder-post"`, Auth: request.Auth{Type: "Bearer", Token: "auth-folder"}, Variables: []request.Field{{Name: "folder", Value: "variable-folder", Disabled: true}}},
	}
	collection, _, err := postman.WriteCollection("Collection", []request.Request{r,
		{Name: "Upload", Collection: "Collection/Folder", Method: "POST", URL: "https://example.test/upload", BodyMode: "Binary", BodyFile: "/private/body-file-path.bin"},
	}, folders)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := []exportFixture{{"Postman collection", collection, map[string][]string{
		"headers": {"header-secret", "header-custom", "header-cookie", "header-example"},
		"query":   {"query-secret", "query-second"}, "auth": {"auth-password", "auth-fragment", "auth-token", "auth-root-user", "auth-root-password", "auth-folder"},
		"variables": {"variable-root", "variable-folder"}, "bodies": {"body-secret", "body-example", "body-original", "/private/body-file-path.bin"},
		"notes": {"notes-pre", "notes-post", "notes-collection", "notes-root", "notes-folder", "notes-folder-post", "assertion-to-review"},
	}}}
	for _, globals := range []bool{false, true} {
		data, err := postman.WriteEnvironment("Environment", map[string]string{"token": "variable-environment", "ordinary": "variable-ordinary"}, globals)
		if err != nil {
			t.Fatal(err)
		}
		name := "Postman environment"
		if globals {
			name = "Postman globals"
		}
		fixtures = append(fixtures, exportFixture{name, data, map[string][]string{"variables": {"variable-environment", "variable-ordinary"}}})
	}
	f := flow{id: 1, method: "POST", host: "example.test", path: "/path-to-review?token=query-secret&token=query-second", url: url,
		status: 201, reqBody: "body-request", resBody: "body-response",
		reqHeaders: "POST /path-to-review?token=query-secret&token=query-second HTTP/1.1\nAuthorization: header-formatted\nAccept: application/json",
		resHeaders: "HTTP/1.1 201 Created\nSet-Cookie: header-formatted-cookie\nContent-Type: application/json",
		reqHeader:  http.Header{"Authorization": {"header-request", "header-repeated"}, "Accept": {"application/json"}},
		respHeader: http.Header{"Set-Cookie": {"header-response"}, "Content-Type": {"application/json"}},
		reqTrailer: http.Header{"X-Trailer": {"header-request-trailer"}}, resTrailer: http.Header{"X-Trailer": {"header-response-trailer"}},
		note: "notes-note", reqError: "notes-request-error", resError: "notes-response-error", actions: []string{"notes-action"},
		debugKey: "notes-debug-key", debugRun: "notes-debug-run", conn: conn{log: "notes-log", error: "notes-connection-error"}}
	har := harFromFlows([]flow{f})
	har.Log.Entries[0].Response.RedirectURL = url
	// Cover encoded bodies too: stripping Text must also strip Encoding.
	har.Log.Entries[0].Request.PostData.Encoding = "base64"
	har.Log.Entries[0].Request.PostData.Text = base64.StdEncoding.EncodeToString([]byte("body-request"))
	harData, err := json.Marshal(har)
	if err != nil {
		t.Fatal(err)
	}
	fixtures = append(fixtures, exportFixture{"HAR", harData, map[string][]string{
		"headers": {"header-request", "header-repeated", "header-response"}, "query": {"query-secret", "query-second"},
		"auth": {"auth-password", "auth-fragment"}, "bodies": {base64.StdEncoding.EncodeToString([]byte("body-request")), "body-response"},
	}})
	session := sessionFrom([]flow{f}, nil)
	session.Title, session.Notes = "notes-session-title", "notes-session-notes"
	session.Flows[0].ReqBytes = []byte("binary-request")
	session.Flows[0].ResBytes = []byte("binary-response")
	session.Frames = map[int][]sessionFrameData{1: {
		{Kind: "text", Data: "body-message", EventID: "body-event-id", EventName: "body-event-name"},
		{Kind: "binary", Payload: []byte("binary-frame"), Data: "body-preview"},
	}}
	sessionData, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	fixtures = append(fixtures, exportFixture{"Session", sessionData, map[string][]string{
		"headers": {"header-formatted", "header-formatted-cookie", "header-request", "header-repeated", "header-response", "header-request-trailer", "header-response-trailer"},
		"query":   {"query-secret", "query-second"}, "auth": {"auth-password", "auth-fragment"},
		"bodies": {"body-request", "body-response", "body-message", "body-event-id", "body-event-name", "body-preview",
			base64.StdEncoding.EncodeToString([]byte("binary-request")), base64.StdEncoding.EncodeToString([]byte("binary-response")), base64.StdEncoding.EncodeToString([]byte("binary-frame"))},
		"notes": {"notes-note", "notes-request-error", "notes-response-error", "notes-action", "notes-debug-key", "notes-debug-run", "notes-log", "notes-connection-error", "notes-session-title", "notes-session-notes"},
	}})
	return fixtures
}

func TestExportRedactionContracts(t *testing.T) {
	for _, fixture := range exportFixtures(t) {
		t.Run(fixture.name, func(t *testing.T) {
			original := bytes.Clone(fixture.data)
			// Exercise every combination: each field is independent, not merely
			// an all-safe vs all-raw switch. This also covers all options off.
			for mask := range 64 {
				o := exportOptions{Headers: mask&1 != 0, Query: mask&2 != 0, Auth: mask&4 != 0,
					Variables: mask&8 != 0, Bodies: mask&16 != 0, ScriptsNotes: mask&32 != 0}
				out, err := redactExport(fixture.data, o)
				if err != nil {
					t.Fatalf("options %+v: %v", o, err)
				}
				if mask == 0 && !bytes.Equal(out, original) {
					t.Fatal("all-off export changed the original bytes")
				}
				selected := map[string]bool{"headers": o.Headers, "query": o.Query, "auth": o.Auth, "variables": o.Variables, "bodies": o.Bodies, "notes": o.ScriptsNotes}
				for scope, markers := range fixture.markers {
					for _, marker := range markers {
						if !bytes.Contains(original, []byte(marker)) {
							t.Fatalf("bad fixture: missing marker %q", marker)
						}
						if strings.Contains(string(out), marker) == selected[scope] {
							t.Fatalf("options %+v: scope %s marker %q selected=%t: %s", o, scope, marker, selected[scope], out)
						}
					}
				}
				if !bytes.Equal(fixture.data, original) {
					t.Fatal("redaction mutated the input bytes")
				}
				if fixture.name == "Postman collection" || fixture.name == "HAR" || fixture.name == "Session" {
					for _, unchanged := range []string{"path-to-review", "example.test", "application/json"} {
						if !bytes.Contains(out, []byte(unchanged)) {
							t.Fatalf("removed out-of-scope content %q", unchanged)
						}
					}
				}
			}
			// The raw result is a separate allocation, not a mutable alias.
			out, _ := redactExport(fixture.data, exportOptions{})
			out[0] = '!'
			if !bytes.Equal(fixture.data, original) {
				t.Fatal("unredacted result aliases original")
			}
		})
	}
}

func TestExportPostmanBodyModesAndReimport(t *testing.T) {
	for _, mode := range []string{"Raw", "URL encoded", "Multipart"} {
		t.Run(mode, func(t *testing.T) {
			r := request.Request{Name: "Request", Collection: "Collection", URL: "{{base_url}}/a%2Fb/{{ id }}?tag=first&tag=second",
				BodyMode: mode, Body: "body-secret", Form: []request.Field{{Name: "text", Value: "body-form"}, {Name: "upload", Value: "/body-local-file", File: true}},
				Checks: request.Checks{Status: "201"}}
			data, _, err := postman.WriteCollection("Collection", []request.Request{r}, nil)
			if err != nil {
				t.Fatal(err)
			}
			o := safeExportOptions()
			o.ScriptsNotes = false // Checks are scripts in Postman; retain on request.
			out, err := redactExport(data, o)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(out), "body-") || !strings.Contains(string(out), "pm.test") {
				t.Fatalf("body leaked or selected Checks lost: %s", out)
			}
			col, err := postman.ReadCollection(out)
			if err != nil || len(col.Requests) != 1 || col.Requests[0].URL != "{{base_url}}/a%2Fb/{{ id }}?tag=REDACTED&tag=REDACTED" {
				t.Fatalf("redacted collection cannot round-trip: %+v %v", col, err)
			}
		})
	}
}

func TestExportRejectsUnknownFormatsAndFuturePayloads(t *testing.T) {
	for _, data := range []string{
		`{"format":"vscroxy-regression","version":1,"request":{}}`,
		`{"version":2,"flows":[]}`,
		`{"version":1,"flows":[],"futurePayload":"hidden"}`,
		`{"version":1,"flows":[{"futurePayload":"hidden"}]}`,
		`{"log":{"entries":[],"futurePayload":"hidden"}}`,
		`not JSON`,
	} {
		if out, err := redactExport([]byte(data), safeExportOptions()); err == nil || out != nil {
			t.Fatalf("unsafe format accepted: %s, %s, %v", data, out, err)
		}
	}
}

func TestExportHeaderBlocksAndURLScopes(t *testing.T) {
	block := "X-Custom: header-first\r\nAccept: application/json\r\n folded-secret\r\nmalformed-secret\r\nX-Custom: header-second"
	out := redactExportHeaderBlock(block, safeExportOptions())
	if strings.Contains(out, "secret") || strings.Contains(out, "header-") || strings.Count(out, exportReplacement) != 2 || !strings.Contains(out, "Accept: application/json") {
		t.Fatalf("malformed/repeated headers leaked: %s", out)
	}
	for _, raw := range []string{"{{base_url}}/a%2Fb/{{ id }}?tag=one&tag=two&empty&bad=%zz", "https://u:p@example.test/path?token=one#fragment"} {
		out := redactExportURL(raw, exportOptions{Query: true})
		if strings.Contains(out, "=one") || strings.Contains(out, "=two") || strings.Contains(out, "%zz") {
			t.Fatalf("raw query leaked: %s", out)
		}
	}
}

func TestExportReviewOwnsCopyAndRequiresFreshConsent(t *testing.T) {
	a := &app{}
	data := []byte(`{"values":[]}`)
	warnings := []string{"An export warning"}
	a.reviewExport("Export variables", "variables.json", data, warnings)
	data[0], warnings[0] = '!', "changed"
	if a.exportUI.options != safeExportOptions() || a.exportUI.reviewed || a.exportUI.unredacted || a.exportUI.data[0] != '{' || a.exportUI.warnings[0] != "An export warning" {
		t.Fatalf("review did not isolate source/defaults: %+v", a.exportUI)
	}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(uikit.Theme(false))
		a.exportModal(c, c.Theme())
	}, 960, 600)
	if err := tt.Click("I understand the scope and will review the exported file before sharing"); err != nil {
		t.Fatal(err)
	}
	if !a.exportUI.reviewed {
		t.Fatal("review checkbox did not update")
	}
	if err := tt.Click("Export unredacted (include original values)"); err != nil {
		t.Fatal(err)
	}
	if !a.exportUI.unredacted || a.exportUI.reviewed {
		t.Fatal("switching to unredacted did not revoke consent")
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	a.reviewExport("Again", "variables.json", []byte(`{"values":[]}`), nil)
	tt.Frame()
	if a.exportUI.unredacted || a.exportUI.reviewed {
		t.Fatal("a new export reused unredacted mode or consent")
	}
}
