package app

import (
	"encoding/json/v2"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/runner"
)

func TestDesktopControlLoadUsesSavedSnapshotsWithoutPersistence(t *testing.T) {
	const secret = "load-private-credentials"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "unauthorized", 401)
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	a := &app{srv: requestProxy(t), activeEnv: "local", envs: []environment{{Name: "local", Current: map[string]string{"token": secret}}}, folders: []request.Folder{{Path: "API", Auth: request.Auth{Type: "Bearer", Token: "{{token}}"}}}}
	a.requests = []savedRequest{{ID: "one", Collection: "API", Method: "GET", URL: upstream.URL, Auth: request.Auth{Type: request.AuthInherit}, Post: `pm.environment.set("new", "private"); pm.globals.set("global", "private"); pm.execution.setNextRequest(null);`}}
	client := desktopClient(t, a)
	var job struct {
		ID string `json:"id"`
	}
	value := desktopCall(t, client, "collection.load", map[string]any{"collection": "API", "environment": "local", "users": 3, "durationSeconds": 60, "confirm": true})
	if err := json.Unmarshal(value, &job); err != nil || job.ID == "" {
		t.Fatalf("start: %s %v", value, err)
	}
	// The worker never needs UI publication to finish, including shutdown.
	a.control.wg.Wait()
	var got struct {
		ID     string            `json:"id"`
		Kind   string            `json:"kind"`
		Done   bool              `json:"done"`
		Report runner.LoadReport `json:"report"`
	}
	value = desktopCall(t, client, "runs.get", map[string]string{"id": job.ID})
	if err := json.Unmarshal(value, &got); err != nil || got.ID != job.ID || got.Kind != "load" || !got.Done || !got.Report.Done || got.Report.Total.Completed != 3 || got.Report.Total.Failed != 0 || got.Report.Reason != "finished" {
		t.Fatalf("report: %s %v", value, err)
	}
	if strings.Contains(string(value), secret) || strings.Contains(string(value), upstream.URL) || !maps.Equal(a.envs[0].Current, map[string]string{"token": secret}) || len(a.globals) != 0 || len(a.runner.rows) != 0 {
		t.Fatal("load persisted or disclosed execution state")
	}
	select {
	case flow := <-a.srv.Flows():
		t.Fatalf("load was captured: %+v", flow)
	default:
	}
}

func TestDesktopControlLoadValidationAndBusyGuards(t *testing.T) {
	a := &app{srv: requestProxy(t), requests: []savedRequest{{ID: "one", Collection: "API", URL: "http://must-not-run.invalid"}}}
	client := desktopClient(t, a)
	base := map[string]any{"collection": "API", "users": 1, "durationSeconds": 1, "confirm": true}
	for _, change := range []map[string]any{
		{"confirm": false}, {"collection": nil}, {"collection": "unknown"}, {"users": 0}, {"users": 65}, {"durationSeconds": 0}, {"durationSeconds": 601}, {"durationSeconds": int64(9223372036854775807)}, {"maxAttempts": 0}, {"maxAttempts": 1000001}, {"requestTimeoutSeconds": 0}, {"requestTimeoutSeconds": 61}, {"environment": "missing"}, {"details": true},
	} {
		params := maps.Clone(base)
		maps.Copy(params, change)
		if value := desktopCall(t, client, "collection.load", params); !strings.Contains(string(value), `"error":`) {
			t.Fatalf("accepted invalid %v: %s", change, value)
		}
	}
	if len(a.control.jobs) != 0 {
		t.Fatal("invalid input created jobs")
	}
	for _, mode := range []string{"runner", "native-load", "remote"} {
		a.runner.running, a.loadRunner.running = mode == "runner", mode == "native-load"
		if mode == "remote" {
			a.control.jobs["busy"] = &controlJob{cancel: func() {}}
		}
		for _, action := range []string{"collection.load", "collection.run"} {
			params := base
			if action == "collection.run" {
				params = map[string]any{"collection": "API", "confirm": true}
			}
			if value := desktopCall(t, client, action, params); !strings.Contains(string(value), `"error":`) {
				t.Fatalf("%s allowed alongside %s: %s", action, mode, value)
			}
		}
	}
}

func TestDesktopControlLoadCancelAndQuit(t *testing.T) {
	for _, quit := range []bool{false, true} {
		t.Run(fmt.Sprint(quit), func(t *testing.T) {
			started := make(chan struct{}, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				started <- struct{}{}
				<-r.Context().Done()
			}))
			defer upstream.Close()
			a := &app{srv: requestProxy(t), requests: []savedRequest{{ID: "one", Collection: "API", URL: upstream.URL}}}
			client := desktopClient(t, a)
			var job struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(desktopCall(t, client, "collection.load", map[string]any{"collection": "API", "users": 1, "durationSeconds": 600, "confirm": true}), &job); err != nil || job.ID == "" {
				t.Fatalf("start failed: %v %+v", err, job)
			}
			<-started
			if value := desktopCall(t, client, "collection.load", map[string]any{"collection": "API", "users": 1, "durationSeconds": 1, "confirm": true}); !strings.Contains(string(value), `"error":`) {
				t.Fatalf("concurrent load accepted: %s", value)
			}
			if quit {
				a.closeControl()
			} else {
				if value := desktopCall(t, client, "runs.cancel", map[string]string{"id": job.ID}); !strings.Contains(string(value), `"error":`) {
					t.Fatalf("unconfirmed cancellation accepted: %s", value)
				}
				desktopCall(t, client, "runs.cancel", map[string]any{"id": job.ID, "confirm": true})
				a.control.wg.Wait()
			}
			var got struct {
				Report runner.LoadReport `json:"report"`
			}
			value := desktopCall(t, client, "runs.get", map[string]string{"id": job.ID})
			if err := json.Unmarshal(value, &got); err != nil || !got.Report.Done || got.Report.Reason != "cancelled" || got.Report.Total.Interrupted != 1 || got.Report.Total.Failed != 0 {
				t.Fatalf("cancelled load: %s %v", value, err)
			}
		})
	}
}
