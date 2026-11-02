package app

import (
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/control"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/runner"
)

// Serialize the app like Window.Update does, but exercise the public wire
// contract through a real authenticated listener and client.
func desktopClient(t *testing.T, a *app) *control.Client {
	t.Helper()
	a.control = &desktopControl{jobs: map[string]*controlJob{}}
	var ui sync.Mutex
	path := filepath.Join(t.TempDir(), "desktop.json")
	server, err := control.Start(path, func(ctx context.Context, c control.Command) (any, error) {
		return a.dispatchControl(ctx, c, func(fn func()) {
			ui.Lock()
			defer ui.Unlock()
			fn()
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(); a.closeControl() })
	client, err := control.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func desktopCall(t *testing.T, client *control.Client, action string, params any) jsontext.Value {
	t.Helper()
	var value jsontext.Value
	if err := client.Call(t.Context(), action, params, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestDesktopControlQueriesRedactionAndPaging(t *testing.T) {
	a := &app{requests: []savedRequest{{ID: "saved", Name: "Read", Collection: "Shop/Child", Method: "GET", URL: "http://user:password@example.com/items?token=query-secret#fragment", Headers: "Authorization: header-secret", Body: "body-secret"}}}
	a.flows = []flow{
		{id: 7, live: true, url: "http://user:password@example.com/items?token=query-secret#fragment", reqHeader: http.Header{"Authorization": {"header-secret"}}, reqBody: "\xff\x00body-secret", reqSize: 13, resBody: strings.Repeat("a", 64<<10) + "b", resSize: 64<<10 + 1},
		{id: 2, live: true}, {id: 9}, {id: 5, live: true},
	}
	client := desktopClient(t, a)
	for _, action := range []string{"requests.list", "requests.get", "flows.list", "flows.get"} {
		params := map[string]any{}
		if action == "requests.get" {
			params["id"] = "saved"
		} else if action == "flows.get" {
			params["id"] = 7
		}
		value := desktopCall(t, client, action, params)
		for _, secret := range []string{"password", "query-secret", "fragment", "header-secret", "body-secret"} {
			if strings.Contains(string(value), secret) {
				t.Fatalf("%s leaked %s: %s", action, secret, value)
			}
		}
	}
	var page struct {
		Items []struct {
			ID int `json:"id"`
		} `json:"items"`
		NextBefore int `json:"nextBefore"`
	}
	value := desktopCall(t, client, "flows.list", map[string]int{"limit": 2})
	if json.Unmarshal(value, &page) != nil || len(page.Items) != 2 || page.Items[0].ID != 7 || page.Items[1].ID != 5 || page.NextBefore != 5 {
		t.Fatalf("first page: %s", value)
	}
	value = desktopCall(t, client, "flows.list", map[string]int{"limit": 2, "before": 5})
	if json.Unmarshal(value, &page) != nil || len(page.Items) != 1 || page.Items[0].ID != 2 || page.NextBefore != 0 {
		t.Fatalf("second page: %s", value)
	}
	value = desktopCall(t, client, "flows.get", map[string]any{"id": 7, "details": true})
	var details struct {
		RequestBodyBase64     string `json:"requestBodyBase64"`
		ResponseBodyBase64    string `json:"responseBodyBase64"`
		ResponseBodyTruncated bool   `json:"responseBodyTruncated"`
	}
	if err := json.Unmarshal(value, &details); err != nil {
		t.Fatal(err)
	}
	requestBody, _ := base64.StdEncoding.DecodeString(details.RequestBodyBase64)
	responseBody, _ := base64.StdEncoding.DecodeString(details.ResponseBodyBase64)
	if string(requestBody) != "\xff\x00body-secret" || string(responseBody) != strings.Repeat("a", 64<<10) || !details.ResponseBodyTruncated {
		t.Fatal("binary preview lost bytes or truncation marker")
	}
	value = desktopCall(t, client, "collections.list", nil)
	var collections []string
	if json.Unmarshal(value, &collections) != nil || !slices.Equal(collections, []string{"Shop", "Shop/Child"}) {
		t.Fatalf("collections: %s", value)
	}
	for _, tc := range []struct {
		action string
		params any
	}{{"status", map[string]string{"path": "/etc/passwd"}}, {"unknown", nil}, {"requests.get", map[string]string{"id": "missing"}}, {"flows.get", map[string]int{"id": 9}}, {"flows.list", map[string]int{"limit": 201}}, {"capture.recording", map[string]bool{"enabled": false}}, {"capture.recording", map[string]bool{"confirm": true}}} {
		value := desktopCall(t, client, tc.action, tc.params)
		if !strings.Contains(string(value), `"error":`) {
			t.Fatalf("invalid %s accepted: %s", tc.action, value)
		}
	}
	value = desktopCall(t, client, "capture.recording", map[string]bool{"enabled": false, "confirm": true})
	if string(value) != `{"recording":false}` {
		t.Fatalf("explicit false was lost: %s", value)
	}
}

func TestDesktopControlRuleToggleRejectsStaleIdentityAndSaveFailure(t *testing.T) {
	a := &app{rulesPath: filepath.Join(t.TempDir(), "rules.json"), rules: []capture.Rule{{Name: "Block", Type: capture.RuleBlock, Match: "example.com"}}}
	client := desktopClient(t, a)
	var rows []struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(desktopCall(t, client, "rules.list", nil), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("rules: %+v %v", rows, err)
	}
	params := map[string]any{"index": 0, "key": rows[0].Key, "enabled": false, "confirm": true}
	if value := desktopCall(t, client, "rules.set-enabled", params); string(value) != `{"enabled":false}` {
		t.Fatalf("toggle: %s", value)
	}
	data, err := os.ReadFile(a.rulesPath)
	if err != nil || !strings.Contains(string(data), `"disabled": true`) && !strings.Contains(string(data), `"disabled":true`) {
		t.Fatalf("not persisted: %s %v", data, err)
	}
	params["enabled"] = true
	if value := desktopCall(t, client, "rules.set-enabled", params); !strings.Contains(string(value), `"error":`) {
		t.Fatalf("stale key accepted: %s", value)
	}
	// Even an idempotent toggle must not claim persistence succeeded when the
	// storage owner rejected it; exercise that through the same public action.
	a.rulesPath = ""
	params["key"], params["enabled"] = controlRuleKey(a.rules[0]), false
	if value := desktopCall(t, client, "rules.set-enabled", params); !strings.Contains(string(value), `"error":`) {
		t.Fatalf("failed write accepted: %s", value)
	}
}

func waitControlRun(t *testing.T, client *control.Client, id string) runner.Report {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for {
		var result struct {
			Done   bool          `json:"done"`
			Report runner.Report `json:"report"`
		}
		if err := client.Call(ctx, "runs.get", map[string]string{"id": id}, &result); err != nil {
			t.Fatal(err)
		}
		if result.Done {
			return result.Report
		}
	}
}

func TestDesktopControlCollectionRunSharesExecutionWithoutPersistingWrites(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-token" || r.URL.Path == "/two" && r.Header.Get("X-Chain") != "carried" {
			http.Error(w, "missing inherited context", 401)
			return
		}
		if r.URL.Path == "/one" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "cookie-secret", Path: "/"})
		} else if cookie, err := r.Cookie("session"); err != nil || cookie.Value != "cookie-secret" {
			http.Error(w, "missing cookie", 401)
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	a := &app{srv: requestProxy(t), activeEnv: "local", envs: []environment{{Name: "local", Vars: map[string]string{"token": "shared"}, Current: map[string]string{"token": "private-token"}}}, folders: []request.Folder{{Path: "Shop", Auth: request.Auth{Type: "Bearer", Token: "{{token}}"}}}}
	a.requests = []savedRequest{
		{ID: "one", Name: "One", Collection: "Shop", Method: "GET", URL: upstream.URL + "/one", Auth: request.Auth{Type: request.AuthInherit}, Post: `pm.environment.set("chain","carried"); pm.test(pm.environment.get("token"),()=>pm.expect(true).to.be.true);`},
		{ID: "two", Name: "Two", Collection: "Shop/Child", Method: "GET", URL: upstream.URL + "/two", Headers: "X-Chain: {{chain}}", Auth: request.Auth{Type: request.AuthInherit}, Checks: request.Checks{Status: "200"}},
		{ID: "excluded", Name: "Never", Collection: "Other", Method: "GET", URL: "http://must-not-run.invalid"},
	}
	client := desktopClient(t, a)
	var job struct {
		ID string `json:"id"`
	}
	value := desktopCall(t, client, "collection.run", map[string]any{"collection": "Shop", "iterations": 2, "confirm": true})
	if err := json.Unmarshal(value, &job); err != nil || job.ID == "" {
		t.Fatalf("start: %s %v", value, err)
	}
	report := waitControlRun(t, client, job.ID)
	if report.Completed != 4 || report.Failed != 0 || report.Incomplete || len(report.Results) != 4 || report.Results[1].ID != "two" || report.Results[3].Iteration != 2 {
		t.Fatalf("run: %+v", report)
	}
	data, _ := report.JSON()
	for _, secret := range []string{"private-token", "cookie-secret", "carried"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("report leaked %s", secret)
		}
	}
	if a.envs[0].Vars["chain"] != "" || a.envs[0].Current["token"] != "private-token" || a.runner.running || len(a.runner.rows) != 0 {
		t.Fatal("remote run modified desktop variables or Runner drafts")
	}
}

func TestDesktopControlRunCancellationAndBusyGuard(t *testing.T) {
	started := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer upstream.Close()
	a := &app{srv: requestProxy(t), requests: []savedRequest{{ID: "wait", Name: "Wait", Method: "GET", URL: upstream.URL}}}
	client := desktopClient(t, a)
	params := map[string]any{"id": "wait", "confirm": true}
	var job struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(desktopCall(t, client, "request.run", params), &job); err != nil || job.ID == "" {
		t.Fatal("request did not start")
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("upstream never reached")
	}
	if value := desktopCall(t, client, "request.run", params); !strings.Contains(string(value), `"error":`) {
		t.Fatalf("overlapping run accepted: %s", value)
	}
	desktopCall(t, client, "runs.cancel", map[string]any{"id": job.ID, "confirm": true})
	report := waitControlRun(t, client, job.ID)
	if !report.Incomplete || report.Problem != "cancelled" {
		t.Fatalf("cancellation became a passing run: %+v", report)
	}
}

func TestDesktopControlHARExportRedactionAndBinaryRoundTrip(t *testing.T) {
	body := "\xff\x00binary-secret"
	path := filepath.Join(t.TempDir(), "body")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &app{flows: []flow{
		{id: 2, live: true, method: "POST", url: "http://user:password@example.com/upload?token=query-secret#fragment", status: 201,
			reqHeader:  http.Header{"Authorization": {"header-secret"}, "Content-Type": {"application/octet-stream"}},
			respHeader: http.Header{"Set-Cookie": {"cookie-secret"}, "Location": {"http://example.com/?key=redirect-secret"}},
			reqBody:    body, reqSize: len(body), resBody: "preview", resFile: path, resSize: len(body)},
		{id: 7, live: true, method: "GET", url: "http://example.com/second", status: 204},
		{id: 9, live: false}, {id: 10, live: true, kind: "WebSocket"},
	}}
	client := desktopClient(t, a)
	params := map[string]any{"ids": []int{7, 2}}
	value := desktopCall(t, client, "flows.export-har", params)
	for _, secret := range []string{"password", "query-secret", "fragment", "header-secret", "cookie-secret", "redirect-secret", "binary-secret"} {
		if strings.Contains(string(value), secret) {
			t.Fatalf("redacted HAR leaked %s: %s", secret, value)
		}
	}
	var doc harFile
	if err := json.Unmarshal(value, &doc); err != nil || doc.Log.Version != "1.2" || len(doc.Log.Entries) != 2 || doc.Log.Entries[0].Request.URL != "http://example.com/second" || doc.Log.Entries[1].Request.PostData != nil || doc.Log.Entries[1].Response.Content.Text != "" {
		t.Fatalf("redacted HAR or requested order incorrect: %s, %v", value, err)
	}
	params["details"] = true
	value = desktopCall(t, client, "flows.export-har", params)
	if err := json.Unmarshal(value, &doc); err != nil || len(doc.Log.Entries) != 2 {
		t.Fatalf("raw HAR: %s, %v", value, err)
	}
	e := doc.Log.Entries[1]
	if e.Request.URL != a.flows[0].url || e.Request.PostData == nil || e.Request.PostData.Encoding != "base64" || e.Response.Content.Encoding != "base64" || harBody(e.Response.Content) != body || harBody(harContent{Text: e.Request.PostData.Text, Encoding: e.Request.PostData.Encoding}) != body {
		t.Fatalf("raw binary bodies did not round trip: %+v", e)
	}
	if a.flows[0].reqHeader.Get("Authorization") != "header-secret" || a.flows[0].resFile != path {
		t.Fatal("export changed captured originals")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if value := desktopCall(t, client, "flows.export-har", params); !strings.Contains(string(value), `"error":`) || strings.Contains(string(value), path) {
		t.Fatalf("missing body silently exported or path disclosed: %s", value)
	}
	delete(params, "details")
	if value := desktopCall(t, client, "flows.export-har", params); strings.Contains(string(value), `"error":`) {
		t.Fatalf("redacted export tried to read missing body: %s", value)
	}
	for _, params := range []any{nil, map[string]any{"ids": []int{}}, map[string]any{"ids": []int{2, 2}}, map[string]any{"ids": []int{-1}}, map[string]any{"ids": []int{9}}, map[string]any{"ids": []int{2, 999}}, map[string]any{"ids": []int{10}}, map[string]any{"ids": []int{2}, "path": "/tmp/not-allowed"}} {
		if value := desktopCall(t, client, "flows.export-har", params); !strings.Contains(string(value), `"error":`) {
			t.Fatalf("invalid selection accepted: %s", value)
		}
	}
}

func TestDesktopControlHARExportRejectsPartialAndOversizedPayloads(t *testing.T) {
	a := &app{flows: []flow{{id: 1, live: true, method: "POST", url: "http://example.com", status: 200}}}
	client := desktopClient(t, a)
	params := map[string]any{"ids": []int{1}, "details": true}
	// Exactly the aggregate limit fits. Neither a per-body check nor checking
	// only the preview length would reject the second, cumulatively larger case.
	a.flows[0].reqBody = strings.Repeat("a", controlHARBodyLimit-1)
	a.flows[0].resBody = "b"
	value := desktopCall(t, client, "flows.export-har", params)
	var doc harFile
	if err := json.Unmarshal(value, &doc); err != nil || len(doc.Log.Entries) != 1 || doc.Log.Entries[0].Request.PostData == nil || len(doc.Log.Entries[0].Request.PostData.Text) != controlHARBodyLimit-1 || doc.Log.Entries[0].Response.Content.Text != "b" {
		t.Fatalf("boundary export rejected: %s", value[:min(len(value), 200)])
	}
	a.flows[0].resBody = "bc"
	if value := desktopCall(t, client, "flows.export-har", params); !strings.Contains(string(value), `"error":`) {
		t.Fatal("aggregate body budget not enforced")
	}
	a.flows[0].reqBody, a.flows[0].resBody, a.flows[0].resSize = "", "short", 6
	if value := desktopCall(t, client, "flows.export-har", params); !strings.Contains(string(value), `"error":`) {
		t.Fatal("partial retained body exported as complete")
	}
	a.flows[0].resSize, a.flows[0].resError = 5, "private-error-contents"
	if value := desktopCall(t, client, "flows.export-har", params); !strings.Contains(string(value), `"error":`) || strings.Contains(string(value), "private-error-contents") {
		t.Fatalf("body error leaked or was ignored: %s", value)
	}
}

func TestDesktopControlRuleImportIsAtomicPersistentAndAppliesToTraffic(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) }))
	defer upstream.Close()
	a := &app{srv: requestProxy(t), rulesPath: filepath.Join(t.TempDir(), "rules.json"), rules: []capture.Rule{{Name: "Existing", Type: capture.RuleBlock, Match: "other.invalid"}}}
	a.ruleUI.open, a.ruleUI.index, a.ruleUI.draft.Name = true, 0, "Unsaved draft"
	client := desktopClient(t, a)
	if status := getThrough(t, a, upstream.URL); status != 201 {
		t.Fatalf("before import status=%d", status)
	}
	document, err := json.Marshal([]capture.Rule{{Name: "Imported", Type: capture.RuleBlock, Match: upstream.URL}})
	if err != nil {
		t.Fatal(err)
	}
	params := map[string]any{"document": string(document)}
	if value := desktopCall(t, client, "rules.import", params); !strings.Contains(string(value), `"error":`) {
		t.Fatalf("unconfirmed import succeeded: %s", value)
	}
	params["confirm"] = true
	if value := desktopCall(t, client, "rules.import", params); string(value) != `{"imported":1,"total":2}` && string(value) != `{"total":2,"imported":1}` {
		t.Fatalf("import: %s", value)
	}
	saved, err := capture.LoadRules(a.rulesPath)
	if err != nil || len(saved) != 2 || saved[0].Name != "Existing" || saved[1].Name != "Imported" || a.ruleUI.draft.Name != "Unsaved draft" || a.ruleUI.index != 0 {
		t.Fatalf("save or existing draft changed: %+v %v", saved, err)
	}
	if status := getThrough(t, a, upstream.URL); status != 403 {
		t.Fatalf("import did not affect subsequent traffic: status=%d", status)
	}
	original, err := os.ReadFile(a.rulesPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		`null`, `[]`, `{}`, `[null]`, `[{"type":"block","match":"ok"},{"type":"invalid","match":"bad"}]`,
		`[{"type":"block","match":"ok","typo":true}]`,
		`[{"type":"block","match":"secret-pattern[","matchMode":"regex"}]`,
		`[{"type":"block","match":"ok","when":[{"name":"X-Test","op":"equals","extra":"bad"}]}]`,
		`[{"type":"block","match":"ok","scenario":"A","scenarioActive":true},{"type":"block","match":"other","scenario":"B","scenarioActive":true}]`,
	} {
		params["document"] = invalid
		value := desktopCall(t, client, "rules.import", params)
		if !strings.Contains(string(value), `"error":`) || strings.Contains(string(value), "secret-pattern") {
			t.Fatalf("invalid batch accepted or disclosed: %s", value)
		}
		data, err := os.ReadFile(a.rulesPath)
		if err != nil || string(data) != string(original) || len(a.rules) != 2 {
			t.Fatal("rejected batch changed saved or active rules")
		}
	}
	// An external edit must not be overwritten or applied to the running proxy.
	if err := os.WriteFile(a.rulesPath, []byte("externally changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	params["document"] = string(document)
	if value := desktopCall(t, client, "rules.import", params); !strings.Contains(string(value), `"error":`) || len(a.rules) != 2 {
		t.Fatalf("failed save was applied: %s", value)
	}
	data, err := os.ReadFile(a.rulesPath)
	if err != nil || string(data) != "externally changed" {
		t.Fatal("external edit overwritten")
	}
}
