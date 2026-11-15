package app

import (
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
)

func docsTestRequests() []savedRequest {
	return []savedRequest{{Request: request.Request{
		Name: "Create invoice", Collection: "Billing/Invoices", Method: "POST", URL: "https://api.example.test/invoices",
		Description: "Creates an invoice. **Required:** an amount.",
		Headers:     "Authorization: Bearer header-token-secret\nContent-Type: application/json\nX-Api-Key: api-key-secret\nX-Amz-Signature: signature-secret\nsessionid: session-id-secret\nauthToken: auth-token-secret",
		Body:        `{"amount":42}`,
		Auth:        request.Auth{Type: "Bearer", Token: "auth-token-secret"},
		Examples: []request.Example{{
			Name: "Created", Status: 201, Headers: "Content-Type: application/json\nSet-Cookie: session=example-cookie-secret",
			Body: `{"id":"inv_1"}`,
		}},
	}}, {Request: request.Request{
		Name: "Health", Collection: "Billing", Method: "GET", URL: "https://api.example.test/health",
	}}}
}

func TestCollectionDocsRedactSecretsAndIncludeEverything(t *testing.T) {
	entries := docEntries(docsTestRequests(), "Billing")
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	var entry docsEntry
	for _, candidate := range entries {
		if candidate.Name == "Create invoice" {
			entry = candidate
		}
	}
	if entry.Name == "" {
		t.Fatal("Create invoice missing")
	}
	if entry.Auth != "Bearer token (hidden)" {
		t.Errorf("auth = %q", entry.Auth)
	}
	if entry.Body != `{"amount":42}` {
		t.Errorf("body = %q", entry.Body)
	}
	if len(entry.Examples) != 1 || entry.Examples[0].Status != 201 || entry.Examples[0].Body != `{"id":"inv_1"}` {
		t.Errorf("examples = %+v", entry.Examples)
	}
	if len(entry.Samples) != len(docSampleLanguages) {
		t.Errorf("samples = %d, want %d", len(entry.Samples), len(docSampleLanguages))
	}

	md := docsMarkdown("Billing", "Invoices and health.", entries)
	for _, want := range []string{"# Billing", "## POST Create invoice", "### Headers", "### Body", "### Examples",
		"201 · Created", "### Code samples", "JavaScript (fetch)", "Python (requests)", "Go (net/http)", "•••• (hidden)"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q", want)
		}
	}
	for _, secret := range []string{"header-token-secret", "api-key-secret", "auth-token-secret", "signature-secret", "session-id-secret", "example-cookie-secret"} {
		if strings.Contains(md, secret) {
			t.Errorf("markdown leaks %q", secret)
		}
	}

	page, err := docsHTMLFile("Billing", "Invoices & <script>alert(1)</script>", entries)
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	for _, want := range []string{"<!doctype html>", "Create invoice", "application/json", "Code samples", "fetch("} {
		if !strings.Contains(html, want) {
			t.Errorf("html lacks %q", want)
		}
	}
	if strings.Contains(html, "<script>") || strings.Contains(html, "<link") {
		t.Error("html is not self-contained and escaped")
	}
	if !strings.Contains(html, "Invoices &amp; &lt;script&gt;") {
		t.Error("description is not escaped")
	}
	for _, secret := range []string{"header-token-secret", "api-key-secret", "auth-token-secret", "signature-secret", "session-id-secret", "example-cookie-secret"} {
		if strings.Contains(html, secret) {
			t.Errorf("html leaks %q", secret)
		}
	}
}

func TestCollectionDocsAuthSummariesHideCredentials(t *testing.T) {
	cases := []request.Auth{
		{Type: "Basic", Username: "reader", Password: "basic-password-secret"},
		{Type: "API key", Location: "header", Key: "X-Key", Value: "api-key-value-secret"},
		{Type: "OAuth 2.0", OAuth2: request.OAuth2{Grant: "client_credentials", ClientSecret: "oauth-secret"}},
		{Type: "AWS Signature", AWS: request.AWSAuth{AccessKey: "AKIA-SECRET", SecretKey: "aws-secret"}},
		{Type: "JWT", JWT: request.JWTAuth{Algorithm: "HS256", Secret: "jwt-secret"}},
	}
	for _, auth := range cases {
		summary := authSummary(auth)
		for _, secret := range []string{"basic-password-secret", "api-key-value-secret", "oauth-secret", "AKIA-SECRET", "aws-secret", "jwt-secret"} {
			if strings.Contains(summary, secret) {
				t.Errorf("%s summary leaks %q: %s", auth.Type, secret, summary)
			}
		}
	}
	if got := authSummary(request.Auth{Type: "Basic", Username: "reader", Password: "x"}); !strings.Contains(got, "reader") {
		t.Errorf("basic summary loses the username: %q", got)
	}
}

func TestCollectionDocsCodeSampleSwitchesLanguage(t *testing.T) {
	a := workspaceApp()
	a.requests = []savedRequest{{Request: request.Request{
		Name: "Create invoice", Collection: "Billing", Method: "POST", URL: "https://api.example.test/invoices",
		Body: `{"amount":42}`,
	}}}
	a.folders = []request.Folder{{Path: "Billing", Description: "Invoices."}}
	a.openDocs("Billing")
	tt := ui.NewTester(a.view, 1280, 800)
	if !tt.HasText("Code samples") || !tt.HasText("fetch(") {
		t.Fatalf("initial samples missing: %v", tt.Texts())
	}
	if err := tt.Click("Go (net/http)"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("package main") {
		t.Fatalf("switching language did not show the Go sample: %v", tt.Texts())
	}
}
