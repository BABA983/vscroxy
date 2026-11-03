package app

import (
	"encoding/json/v2"
	"strings"
	"testing"
	"time"
)

func TestDesktopTokenLinkWorkflow(t *testing.T) {
	a := tokenPlanApp(t)
	// Prefer complete saved evidence even if the remaining table row only has
	// an unavailable spill, and allow consumers no longer in the table.
	a.collectionRecording = &collectionRecording{evidence: a.flows}
	a.flows = []flow{a.flows[0]}
	a.flows[0].resBody, a.flows[0].resFile = "display preview", "/unavailable-capture-spill"
	client := desktopClient(t, a)
	var started struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(desktopCall(t, client, "token-links.analyze", map[string]any{"ids": []int{2, 1}}), &started); err != nil || started.ID == "" {
		t.Fatalf("start: %+v %v", started, err)
	}
	if a.tokenLinks.open {
		t.Fatal("automation opened a desktop modal")
	}
	value := desktopCall(t, client, "token-links.get", map[string]string{"id": started.ID})
	var result struct {
		Done bool `json:"done"`
		tokenAnalysis
	}
	if json.Unmarshal(value, &result) != nil || !result.Done || len(result.Links) != 1 || result.Links[0].Source != 1 || len(result.Links[0].Consumers) != 1 || result.Links[0].Consumers[0] != 2 || strings.Contains(string(value), "preview-secret") {
		t.Fatalf("masked evidence: %s", value)
	}
	params := map[string]any{"id": started.ID, "index": 0, "collection": "Shop", "environment": "Local", "variable": "linked_token"}
	value = desktopCall(t, client, "token-links.prepare", params)
	var proposal tokenLinkPlan
	if json.Unmarshal(value, &proposal) != nil || proposal.ID == "" || proposal.Source != "login" || proposal.ResetBeforeSetup.Variable != "linked_token" || strings.Contains(string(value), "preview-secret") || strings.Contains(string(value), "existing login pre") {
		t.Fatalf("safe proposal: %s", value)
	}
	if _, ok := a.envs[0].Current["linked_token"]; ok {
		t.Fatal("read-only preparation created a Current value")
	}
	if value = desktopCall(t, client, "token-links.apply", map[string]any{"key": proposal.ID}); !strings.Contains(string(value), `"error"`) {
		t.Fatal("unconfirmed apply accepted")
	}
	// A new proposal invalidates the previous key even when its inputs match.
	value = desktopCall(t, client, "token-links.prepare", params)
	var next tokenLinkPlan
	if json.Unmarshal(value, &next) != nil || next.ID == proposal.ID {
		t.Fatal("proposal key was reused")
	}
	if value = desktopCall(t, client, "token-links.apply", map[string]any{"key": proposal.ID, "confirm": true}); !strings.Contains(string(value), `"error"`) {
		t.Fatal("replaced proposal applied")
	}
	if value = desktopCall(t, client, "token-links.apply", map[string]any{"key": next.ID, "confirm": true}); string(value) != `{"applied":true}` {
		t.Fatalf("apply: %s", value)
	}
	if len(a.requests[0].TokenSources) != 1 || a.requests[1].HeaderList[1].Value != "Bearer {{linked_token}}" {
		t.Fatal("actual saved requests were not changed")
	}
	if value = desktopCall(t, client, "token-links.apply", map[string]any{"key": next.ID, "confirm": true}); !strings.Contains(string(value), `"error"`) {
		t.Fatal("successful proposal applied twice")
	}
}

func TestDesktopTokenLinkValidationAndLifetime(t *testing.T) {
	a := tokenPlanApp(t)
	client := desktopClient(t, a)
	old := a.tokenLinks.id
	for _, params := range []any{nil, map[string]any{"ids": nil}, map[string]any{"ids": []int{}}, map[string]any{"ids": []int{1, 1}}, map[string]any{"ids": []int{-1}}, map[string]any{"ids": []int{999}}, map[string]any{"ids": []int{1}, "path": "/private"}} {
		if v := desktopCall(t, client, "token-links.analyze", params); !strings.Contains(string(v), `"error"`) || a.tokenLinks.id != old {
			t.Fatalf("invalid selection replaced analysis: %s", v)
		}
	}
	for name, expire := range map[string]func(){
		"closed":   func() { a.tokenLinks.close() },
		"expired":  func() { a.tokenLinks.expires = time.Now().Add(-time.Second) },
		"replaced": func() { a.openTokenLinks(a.flows) },
	} {
		t.Run(name, func(t *testing.T) {
			a.openTokenLinks(a.flows)
			id := a.tokenLinks.id
			expire()
			if v := desktopCall(t, client, "token-links.get", map[string]string{"id": id}); !strings.Contains(string(v), `"error"`) {
				t.Fatalf("obsolete analysis accessible: %s", v)
			}
		})
	}
	a.openTokenLinks(a.flows)
	params := map[string]any{"id": a.tokenLinks.id, "environment": "Local", "variable": "linked_token"}
	for _, missing := range []string{"index", "collection"} {
		params["index"], params["collection"] = 0, "Shop"
		delete(params, missing)
		if v := desktopCall(t, client, "token-links.prepare", params); !strings.Contains(string(v), `"error"`) {
			t.Fatalf("required %s missing: %s", missing, v)
		}
	}
	// Explicit root is distinct from omission and is supported by native storage.
	for i := range a.requests {
		a.requests[i].Collection = ""
	}
	params["index"], params["collection"] = 0, ""
	if v := desktopCall(t, client, "token-links.prepare", params); strings.Contains(string(v), `"error"`) {
		t.Fatalf("explicit root rejected: %s", v)
	}
}
