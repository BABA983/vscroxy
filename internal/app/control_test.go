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
		ui.Lock()
		defer ui.Unlock()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		value, err := a.controlValue(c)
		if err != nil {
			value = map[string]string{"error": err.Error()}
		}
		data, err := json.Marshal(value)
		return jsontext.Value(data), err
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
