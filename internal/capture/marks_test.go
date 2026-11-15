package capture

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRuleColorMarksTheRow(t *testing.T) {
	s, _ := startServer(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()
	if err := s.SetRules([]Rule{{Type: RuleBlock, Match: "/colored", Color: "Blue"}}); err != nil {
		t.Fatal(err)
	}
	client := clientFor(t, s, nil)
	resp, err := client.Get(upstream.URL + "/colored")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	f := waitFlow(t, s)
	if f.Status != http.StatusForbidden || f.Color != "Blue" || f.Strike {
		t.Fatalf("row status %d color %q strike %t", f.Status, f.Color, f.Strike)
	}
}

func TestScriptMarkSetsColorAndStrikethrough(t *testing.T) {
	s := startServerWithScripts(t, fakeScripts{"mark.js": {func(x *ScriptExchange) error {
		if x.Phase != "request" {
			return nil
		}
		x.Color, x.Strike = "Purple", true
		return nil
	}, nil}})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()
	if err := s.SetRules([]Rule{{Type: RuleScript, Match: "/marked", Target: "mark.js"}}); err != nil {
		t.Fatal(err)
	}
	client := clientFor(t, s, nil)
	resp, err := client.Get(upstream.URL + "/marked")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	f := waitFlow(t, s)
	if f.Color != "Purple" || !f.Strike {
		t.Fatalf("row color %q strike %t", f.Color, f.Strike)
	}
}

func TestUnknownMarkColorIsRejectedAndIgnored(t *testing.T) {
	if err := ValidateRules([]Rule{{Type: RuleBlock, Match: "/x", Color: "Chartreuse"}}); err == nil {
		t.Fatal("a rule with an unknown color was accepted")
	}
	s, _ := startServer(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()
	if err := s.SetRules([]Rule{{Type: RuleBlock, Match: "/x", Color: "Green"}}); err != nil {
		t.Fatal(err)
	}
	client := clientFor(t, s, nil)
	resp, err := client.Get(upstream.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	f := waitFlow(t, s)
	if f.Color != "Green" {
		t.Fatalf("color %q", f.Color)
	}
}
