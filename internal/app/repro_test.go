package app

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/postman"
	"github.com/baba983/vscroxy/internal/repro"
)

func TestBuildReproBundleRedactsAndEmbedsCollection(t *testing.T) {
	file := sessionFile{Title: "Checkout", Notes: "QA notes", Flows: []sessionFlow{{
		ID: 1, Method: "POST", Path: "/orders", URL: "https://api.example.com/orders?token=query-secret&code=oauth-code-secret",
		Status: 201, Millis: 42, Wait: 10, Download: 32, Timing: &capture.Timing{},
		ReqHeaders: "Authorization: Bearer header-secret\nCookie: session=cookie-secret\nX-Amz-Signature: signature-secret\nX-Amz-Credential: credential-secret\nsessionid: session-id-secret\nauthToken: auth-token-secret\nContent-Type: application/json",
		ReqBody:    `{"password":"body-secret","session":"session-body-secret","signature":"signature-body-secret","credential":"credential-body-secret","auth":"auth-body-secret","code":"code-value","amount":42}`,
		ResHeaders: "Set-Cookie: session=response-secret\nContent-Type: application/json",
		ResBody:    `{"token":"response-token","id":7}`,
		Note:       "created an order", Color: "red", Pinned: true, At: time.Unix(100, 0),
	}}}
	data, err := buildReproBundle(file)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, secret := range []string{
		"header-secret", "cookie-secret", "signature-secret", "credential-secret", "session-id-secret", "auth-token-secret",
		"body-secret", "session-body-secret", "signature-body-secret", "credential-body-secret", "auth-body-secret",
		"response-secret", "response-token", "query-secret", "oauth-code-secret",
	} {
		if strings.Contains(text, secret) {
			t.Errorf("bundle leaks %q: %s", secret, text)
		}
	}
	bundle, err := repro.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Title != "Checkout" || bundle.Notes != "QA notes" || len(bundle.Flows) != 1 {
		t.Fatalf("bundle = %+v", bundle)
	}
	flow := bundle.Flows[0]
	if flow.Note != "created an order" || !flow.Pinned || flow.Timing == nil || flow.Millis != 42 {
		t.Fatalf("flow = %+v", flow)
	}
	collection, ok := repro.Collection(data)
	if !ok {
		t.Fatal("no collection")
	}
	parsed, err := postman.ReadCollection(collection)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Requests) != 1 {
		t.Fatalf("requests = %+v", parsed.Requests)
	}
	request := parsed.Requests[0]
	if strings.Contains(request.Headers, "header-secret") || strings.Contains(request.URL, "query-secret") || strings.Contains(request.URL, "oauth-code-secret") {
		t.Fatalf("collection leaks: %+v", request)
	}
	if !strings.Contains(request.Headers, "••••") {
		t.Fatalf("collection headers not redacted: %+v", request)
	}
	if !strings.Contains(request.Body, `"code":"code-value"`) {
		t.Fatalf("body code was hidden: %+v", request)
	}
	u, err := url.Parse(request.URL)
	if err != nil || u.Query().Get("token") != "••••" || u.Query().Get("code") != "••••" {
		t.Fatalf("collection URL not redacted: %q", request.URL)
	}
}

func TestRedactReproURLUserinfo(t *testing.T) {
	got := redactReproURL("https://user:password@api.example.com/items?api_key=secret&code=oauth-code&page=2")
	if strings.Contains(got, "password") || strings.Contains(got, "secret") || strings.Contains(got, "oauth-code") || !strings.Contains(got, "page=2") {
		t.Fatalf("URL = %q", got)
	}
}
