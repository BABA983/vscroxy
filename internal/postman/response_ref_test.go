package postman_test

import (
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/postman"
	"github.com/baba983/vscroxy/internal/request"
)

func TestWriteCollectionConvertsResponseReferenceToPreRequestScript(t *testing.T) {
	requests := []request.Request{
		{ID: "login-id", Name: "Login", Collection: "Shop", Method: "POST", URL: "https://api.example.com/login"},
		{ID: "me-id", Name: "Me", Collection: "Shop", Method: "GET",
			URL:     `https://api.example.com/me?token={{response "Login" "/data/access_token"}}`,
			Headers: "X-Token: {{response \"Login\" \"/data/access_token\"}}\nX-Session: {{response \"Login\" \"header:X-Session\"}}",
			Auth:    request.Auth{Type: "Bearer", Token: `{{response "Login" "/data/access_token"}}`},
		},
	}
	data, warnings, err := postman.WriteCollection("Shop", requests, nil)
	if err != nil {
		t.Fatal(err)
	}
	exported := strings.ReplaceAll(string(data), `\"`, `"`)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %q; want a clean conversion", warnings)
	}
	if strings.Contains(exported, `{{response`) {
		t.Fatalf("the exported collection still holds native response references:\n%s", exported)
	}
	for _, want := range []string{"vscroxy response reference:", `pm.execution.runRequest("login-id")`, `"data","access_token"`, `res.headers.get("X-Session")`, `__vscroxy_rr_`} {
		if !strings.Contains(exported, want) {
			t.Errorf("export is missing %s", want)
		}
	}
	if n := strings.Count(exported, "vscroxy response reference:"); n != 2 {
		t.Errorf("generated %d scripts; want one JSON pointer and one header reference", n)
	}
}

func TestWriteCollectionReportsUnconvertibleResponseReference(t *testing.T) {
	requests := []request.Request{{ID: "me-id", Name: "Me", Collection: "Shop", Method: "GET",
		URL: `https://api.example.com/me?token={{response "Missing" "/token"}}`}}
	data, warnings, err := postman.WriteCollection("Shop", requests, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ReplaceAll(string(data), `\"`, `"`), `{{response`) {
		t.Fatal("an unconverted reference should remain visible in the export")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "Could not convert 1 response reference") {
		t.Fatalf("warnings = %q", warnings)
	}
}

func TestReadCollectionReportsConvertedReferenceStillAScript(t *testing.T) {
	data := []byte(`{"info":{"name":"Shop","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},"item":[{"name":"Me","event":[{"listen":"prerequest","script":{"exec":["// ` + "vscroxy response reference:" + ` Login /token"]}}],"request":{"method":"GET","url":"https://api.example.com/me"}}]}`)
	got, err := postman.ReadCollection(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "imported as a script") {
		t.Fatalf("warnings = %q", got.Warnings)
	}
}
