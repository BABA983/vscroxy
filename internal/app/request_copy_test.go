package app

import (
	"encoding/json/v2"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
)

func TestRequestCopyCapturedMenu(t *testing.T) {
	const body = `{"retained":"full-body-only"}`
	path := filepath.Join(t.TempDir(), "request.body")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ label, prefix string }{
		{"cURL (Bash)", "curl -q"}, {"cURL (CMD)", "curl.exe -q"},
		{"PowerShell", "# PowerShell 7+"}, {"fetch (browser)", "const response = await fetch("},
		{"fetch (Node.js)", "// Node.js 18+"},
		{"Swift", "// Swift 5+"}, {"Kotlin", "// Kotlin with OkHttp"},
		{"Java", "// Java 11+"}, {"C#", "// C# with .NET 6+"},
		{"PHP", "<?php"}, {"Ruby", `require "net/http"`}, {"Rust", "// Cargo.toml: reqwest"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			a := workspaceApp()
			a.flows[0].method = "POST"
			a.flows[0].reqHeader = http.Header{"Content-Type": {"application/json"}, "X-Original": {"captured"}}
			a.flows[0].reqFile, a.flows[0].reqBody, a.flows[0].reqSize = path, "preview-only", len(body)
			tt := ui.NewTester(a.view, 960, 600)
			if err := tt.Click("Copy as…"); err != nil {
				t.Fatal(err)
			}
			if err := chooseMenu(tt, tc.label); err != nil {
				t.Fatal(err)
			}
			got := tt.Clipboard()
			if !strings.HasPrefix(got, tc.prefix) || !strings.Contains(got, "full-body-only") || !strings.Contains(got, "X-Original") || strings.Contains(got, "preview-only") {
				t.Fatalf("incorrect captured export: %q", got)
			}
			// A failed export must not replace the previous clipboard contents.
			a.flows[0].reqFile = path + ".missing"
			a.liveFor = 0
			tt.Frame()
			if err := tt.Click("Copy as…"); err != nil {
				t.Fatal(err)
			}
			if err := chooseMenu(tt, tc.label); err != nil {
				t.Fatal(err)
			}
			if tt.Clipboard() != got {
				t.Fatal("failed export replaced clipboard")
			}
		})
	}
}

func TestRequestCopyUsesActiveDraftWithoutExecution(t *testing.T) {
	a := workspaceApp()
	openDraft(a, a.flows[0])
	d := a.requestDocument
	d.composeMethod, d.composeURL = "POST", "https://edited.example.test/{{path}}"
	d.composeHeaders, d.composeBody = "Content-Type: application/json", `{"token":"{{token}}"}`
	d.headerRows, d.headerFor = parseHeaderRows(d.composeHeaders), d.composeHeaders
	d.composePre = `throw new Error("must not run")`
	tt := ui.NewTester(a.view, 1280, 800)
	if err := tt.Click("Request options"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Copy as…"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("PowerShell"); err != nil {
		t.Fatal(err)
	}
	if got := tt.Clipboard(); !strings.Contains(got, "edited.example.test/{{path}}") || !strings.Contains(got, "{{token}}") || strings.Contains(got, "must not run") {
		t.Fatalf("did not export the unexpanded draft: %q", got)
	}
	if a.flows[0].url != "https://api.example.com/projects" || a.requests[0].URL != "https://api.example.com/projects" || a.composeWaiting {
		t.Fatal("copy changed originals or sent a request")
	}
}

func TestRequestCopyHAR(t *testing.T) {
	const body = "\x00\xff\x01binary"
	path := filepath.Join(t.TempDir(), "response.body")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	a := workspaceApp()
	flows := []flow{{id: 7, method: "POST", url: "https://user:password@example.test/a?secret=query-value", reqHeader: http.Header{"Authorization": {"Bearer synthetic-secret"}}, reqBody: "request-secret", respHeader: http.Header{"Set-Cookie": {"session=synthetic-cookie"}}, resFile: path, resBody: "preview", resSize: len(body), status: 201}, a.flows[0]}
	// The same menu supports imported/session flows, not just live IDs.
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Button(c, "Copy").PopoverMenuButton(func(m *ui.Menu) { actionMenu(m, copyAsMenu(a.captureCopyActions(c, flows)).items) })
	}, 960, 600)
	for _, raw := range []bool{false, true} {
		if err := tt.Click("Copy"); err != nil {
			t.Fatal(err)
		}
		label := "HAR (sanitized)"
		if raw {
			label = "HAR (with sensitive data)"
		}
		if err := chooseMenu(tt, label); err != nil {
			t.Fatal(err)
		}
		got := tt.Clipboard()
		var doc harFile
		if err := json.Unmarshal([]byte(got), &doc); err != nil || len(doc.Log.Entries) != 2 {
			t.Fatalf("invalid clipboard HAR: %v", err)
		}
		if !raw {
			for _, secret := range []string{"password", "query-value", "synthetic-secret", "synthetic-cookie", "request-secret", "preview"} {
				if strings.Contains(got, secret) {
					t.Fatalf("sanitized HAR contains %s", secret)
				}
			}
		} else {
			f, ok := harFlow(doc.Log.Entries[0])
			if !ok || f.body(true) != body || f.reqBody != "request-secret" || f.reqHeader.Get("Authorization") != "Bearer synthetic-secret" {
				t.Fatal("raw HAR did not preserve complete binary response and credentials")
			}
		}
	}
	previous := tt.Clipboard()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Copy"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "HAR (with sensitive data)"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != previous || flows[0].reqHeader.Get("Authorization") != "Bearer synthetic-secret" {
		t.Fatal("failed HAR copy changed clipboard or original")
	}
}

func TestRequestCopyCodeDialog(t *testing.T) {
	for _, language := range []string{"cURL (CMD)", "PowerShell", "Node.js (fetch)", "JavaScript (fetch)",
		"Swift (URLSession)", "Kotlin (OkHttp)", "Java (java.net.http)", "C# (HttpClient)",
		"PHP (cURL)", "Ruby (Net::HTTP)", "Rust (reqwest)"} {
		t.Run(language, func(t *testing.T) {
			a := workspaceApp()
			f := a.flows[0]
			f.method, f.reqBody = "POST", `{"message":"你好"}`
			f.reqHeader = http.Header{"Content-Type": {"application/json"}}
			openDraft(a, f)
			a.code.language = language
			a.openCode()
			tt := ui.NewTester(a.view, 960, 600)
			if language == "cURL (CMD)" {
				if !tt.HasText("CMD export requires ASCII without delayed-expansion markers and at most 8000 characters; use Bash or PowerShell instead") {
					t.Fatal("missing controlled CMD error")
				}
				_ = tt.Click("Copy")
				if tt.Clipboard() != "" {
					t.Fatal("unsupported format was copied")
				}
			} else {
				if err := tt.Click("Copy"); err != nil {
					t.Fatal(err)
				}
				if tt.Clipboard() == "" {
					t.Fatal("code dialog did not copy code")
				}
			}
			if err := tt.Click("Close"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if tt.HasText("Code language") {
				t.Fatal("code modal did not close")
			}
		})
	}
}

func TestGeneratedCodeCarriesAuthAndEncodedBodies(t *testing.T) {
	a := workspaceApp()
	openDraft(a, a.flows[0])
	d := a.requestDocument
	d.composeMethod, d.composeURL = "POST", "https://api.example.com/items?tag=a&tag=b"
	d.auth = request.Auth{Type: "Bearer", Token: "token-19"}
	for _, tc := range []struct {
		name, mode, fragment string
		fields               []request.Field
	}{
		{"raw", "Raw", "hello", nil},
		{"urlencoded", "URL encoded", "name=%E4%BD%A0%E5%A5%BD+%26+more", []request.Field{{Name: "name", Value: "你好 & more"}}},
		{"multipart", "Multipart", "name=", []request.Field{{Name: "name", Value: "hello"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d.bodyMode, d.formFields = tc.mode, tc.fields
			d.composeBody = ""
			if tc.mode == "Raw" {
				d.composeBody = `{"name":"hello"}`
			}
			d.composeHeaders, d.headerRows, d.headerFor = "", nil, ""
			for _, language := range []string{"Swift (URLSession)", "Kotlin (OkHttp)", "Java (java.net.http)", "C# (HttpClient)", "PHP (cURL)", "Ruby (Net::HTTP)", "Rust (reqwest)"} {
				a.code.language = language
				code, err := a.codeText(d)
				if err != nil {
					t.Fatalf("%s %s: %v", tc.name, language, err)
				}
				if !strings.Contains(code, "Authorization") || !strings.Contains(code, "Bearer token-19") {
					t.Fatalf("%s %s lost expanded auth:\n%s", tc.name, language, code)
				}
				if !strings.Contains(code, "tag=a&tag=b") || !strings.Contains(code, tc.fragment) {
					t.Fatalf("%s %s lost query or body:\n%s", tc.name, language, code)
				}
				if tc.mode == "URL encoded" && !strings.Contains(code, "application/x-www-form-urlencoded") {
					t.Fatalf("%s %s lost the form Content-Type:\n%s", tc.name, language, code)
				}
				if tc.mode == "Multipart" && !strings.Contains(code, "multipart/form-data; boundary=") {
					t.Fatalf("%s %s lost the multipart Content-Type:\n%s", tc.name, language, code)
				}
			}
		})
	}
}

func TestCaptureCanGenerateCodeWithoutOpeningADraft(t *testing.T) {
	a := workspaceApp()
	a.syncDocument()
	if a.requestDocument == nil {
		t.Fatal("the selected capture did not open as a request document")
	}
	if x := pick(a.actions(nil), "request.code"); !x.enabled {
		t.Fatalf("Generate code is unavailable for a capture: %s", x.why())
	}
}

func TestGeneratedCodeOmitsNTLMAuth(t *testing.T) {
	a := workspaceApp()
	openDraft(a, a.flows[0])
	d := a.requestDocument
	d.auth = request.Auth{Type: request.AuthNTLM, NTLM: request.NTLMAuth{
		Username: "ann", Password: "{{vault:pw}}", Domain: "EXAMPLE", Workstation: "PC",
	}}
	for _, language := range codeLanguages {
		a.code.language = language
		code, err := a.codeText(d)
		if err != nil {
			t.Fatalf("%s: %v", language, err)
		}
		if strings.Contains(code, "Authorization") || strings.Contains(code, "NTLM") {
			t.Errorf("%s carries NTLM material:\n%s", language, code)
		}
	}
	a.openCode()
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if !tt.HasText("NTLM needs the server’s challenge on one connection") {
		t.Fatalf("the code dialog does not explain the missing auth: %v", tt.Texts())
	}
}
