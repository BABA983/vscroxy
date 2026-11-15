package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		current, latest string
		want            bool
	}{
		{"0.1.0", "v0.1.1", true},
		{"0.1.0", "0.2.0", true},
		{"0.1.0", "1.0.0", true},
		{"0.9.9", "v0.10.0", true},
		{"0.1.1", "0.1.0", false},
		{"0.1.0", "v0.1.0", false},
		{"0.2.0-beta", "0.2.0", true},
		{"0.2.0", "0.2.0-beta", false},
		{"0.1.0", "not a version", false},
		{"", "1.0.0", false},
	} {
		if got := Newer(tc.current, tc.latest); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestLatestReadsTheTagAndPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "vscroxy-update-check" {
			t.Errorf("User-Agent = %q", got)
		}
		w.Write([]byte(`{"tag_name":"v0.2.0","html_url":"https://github.com/BABA983/vscroxy/releases/tag/v0.2.0"}`))
	}))
	defer server.Close()
	release, err := latestAt(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if release.Tag != "v0.2.0" || !strings.Contains(release.URL, "releases/tag/v0.2.0") {
		t.Fatalf("release = %+v", release)
	}
	if !Newer("0.1.0", release.Tag) {
		t.Fatal("the latest release should be newer")
	}
}

func TestLatestRefusesFailures(t *testing.T) {
	for name, body := range map[string]string{
		"not found":  `{"message":"Not Found"}`,
		"malformed":  `{"tag_name":`,
		"no release": `{"tag_name":"","html_url":""}`,
		"draft":      `{"tag_name":"v0.2.0","draft":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if name == "not found" {
					http.NotFound(w, r)
					return
				}
				w.Write([]byte(body))
			}))
			defer server.Close()
			if _, err := latestAt(context.Background(), server.Client(), server.URL); err == nil {
				t.Fatal("a failed lookup was accepted")
			}
		})
	}
}

func TestLatestBoundsTheResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v0.2.0","padding":"` + strings.Repeat("x", releaseSizeLimit) + `"}`))
	}))
	defer server.Close()
	if _, err := latestAt(context.Background(), server.Client(), server.URL); err == nil {
		t.Fatal("an oversized response was accepted")
	}
}
