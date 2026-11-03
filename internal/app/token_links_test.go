package app

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

const inferredTestToken = "Abcd0123-._~+/xyZ=="

func tokenTestFlow(id int, body string) flow {
	return flow{
		id: id, method: "GET", url: "https://example.test/private?secret=url-secret",
		status: 200, respHeader: http.Header{"Content-Type": {"application/json"}},
		resBody: body, resSize: len(body), millis: 10,
		at: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(id) * time.Second),
	}
}

func tokenTestPair() []flow {
	flows := []flow{tokenTestFlow(1, `{"token":"`+inferredTestToken+`"}`), tokenTestFlow(2, `{}`)}
	flows[1].reqHeader = http.Header{"Authorization": {"Bearer " + inferredTestToken}}
	return flows
}

func TestRecognizeTokenLinksNestedDeterministicAndPrivate(t *testing.T) {
	second, third := "second-token-012345", "third-token-0123456"
	source := tokenTestFlow(1, `{"z":[{"a/b~\".[]":{"accessToken":"`+inferredTestToken+`"}}],"a":{"id_token":"`+second+`"}}`)
	other := tokenTestFlow(5, `[{"idToken":"`+third+`"}]`)
	consumers := []flow{tokenTestFlow(9, `{}`), tokenTestFlow(7, `{}`), tokenTestFlow(8, `{}`), tokenTestFlow(6, `{}`)}
	for i, value := range []string{inferredTestToken, inferredTestToken, second, third} {
		consumers[i].reqHeader = http.Header{"authorization": {"bEaReR  " + value}}
		consumers[i].url = "https://EXAMPLE.TEST:443/resource"
	}
	flows := append(consumers, other, source)
	before := slices.Clone(flows)
	for i := range before {
		before[i].reqHeader = before[i].reqHeader.Clone()
		before[i].respHeader = before[i].respHeader.Clone()
	}
	got, err := recognizeTokenLinks(t.Context(), flows)
	if err != nil {
		t.Fatal(err)
	}
	want := tokenAnalysis{Links: []tokenLink{
		{Source: 1, Path: []string{"a", "id_token"}, Consumers: []int{8}, token: second},
		{Source: 1, Path: []string{"z", "0", `a/b~".[]`, "accessToken"}, Consumers: []int{7, 9}, token: inferredTestToken},
		{Source: 5, Path: []string{"0", "idToken"}, Consumers: []int{6}, token: third},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("analysis = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(flows, before) {
		t.Fatal("analysis mutated input snapshots")
	}
	slices.Reverse(flows)
	again, err := recognizeTokenLinks(t.Context(), flows)
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatalf("shuffled analysis changed: %#v, %v", again, err)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{inferredTestToken, second, third, "Bearer", "example.test", "url-secret", "private", "resBody"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("exported analysis contains forbidden data %q", secret)
		}
	}
	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil || len(wire) != 2 {
		t.Fatalf("unexpected wire shape: %s, %v", data, err)
	}
	for _, link := range wire["links"].([]any) {
		if len(link.(map[string]any)) != 3 {
			t.Fatalf("unexpected link wire fields: %s", data)
		}
	}
}

func TestRecognizeTokenLinksEligibility(t *testing.T) {
	cases := []struct {
		name    string
		change  func([]flow) []flow
		linked  bool
		skipped int
	}{
		{name: "ordinary", linked: true},
		{name: "http default port", linked: true, change: func(f []flow) []flow {
			f[0].url = "http://EXAMPLE.test:80/a"
			f[1].url = "http://example.test/b"
			return f
		}},
		{name: "numeric default port", linked: true, change: func(f []flow) []flow { f[1].url = "https://example.test:0443/b"; return f }},
		{name: "ipv6", linked: true, change: func(f []flow) []flow { f[0].url = "https://[::1]/a"; f[1].url = "https://[::1]:443/b"; return f }},
		{name: "wrong host", change: func(f []flow) []flow { f[1].url = "https://other.test/b"; return f }},
		{name: "wrong scheme", change: func(f []flow) []flow { f[1].url = "http://example.test/b"; return f }},
		{name: "wrong port", change: func(f []flow) []flow { f[1].url = "https://example.test:8443/b"; return f }},
		{name: "invalid URL", skipped: 1, change: func(f []flow) []flow { f[1].url = "https://bad:invalid/"; return f }},
		{name: "non HTTP", skipped: 1, change: func(f []flow) []flow { f[1].url = "ftp://example.test/b"; return f }},
		{name: "source incomplete", skipped: 1, change: func(f []flow) []flow { f[0].resError = "private OS detail"; return f }},
		{name: "no source completion", skipped: 1, change: func(f []flow) []flow { f[0].at = time.Time{}; return f }},
		{name: "no consumer completion", skipped: 1, change: func(f []flow) []flow { f[1].at = time.Time{}; return f }},
		{name: "overlap", change: func(f []flow) []flow { f[1].millis = 1500; return f }},
		{name: "equal request start", change: func(f []flow) []flow { f[1].millis = 1000; return f }},
		{name: "under rounding margin", change: func(f []flow) []flow { f[1].at = f[0].at.Add(10*time.Millisecond + time.Millisecond/2); return f }},
		{name: "exact rounding margin", linked: true, change: func(f []flow) []flow { f[1].at = f[0].at.Add(11 * time.Millisecond); return f }},
		{name: "zero duration margin", linked: true, change: func(f []flow) []flow { f[1].millis = 0; f[1].at = f[0].at.Add(time.Millisecond); return f }},
		{name: "negative duration", change: func(f []flow) []flow { f[1].millis = -1; return f }},
		{name: "future source", change: func(f []flow) []flow { f[0].at = f[1].at.Add(time.Second); return f }},
		{name: "ambiguous fields", change: func(f []flow) []flow {
			f[0].resBody = `{"token":"` + inferredTestToken + `","access_token":"` + inferredTestToken + `"}`
			f[0].resSize = len(f[0].resBody)
			return f
		}},
		{name: "ambiguous sources", change: func(f []flow) []flow { other := f[0]; other.id = 3; return append(f, other) }},
		{name: "ineligible duplicate not ambiguous", linked: true, change: func(f []flow) []flow {
			other := f[0]
			other.id = 3
			other.url = "https://other.test/"
			return append(f, other)
		}},
		{name: "overlapping duplicate not ambiguous", linked: true, change: func(f []flow) []flow { other := f[0]; other.id = 3; other.at = f[1].at; return append(f, other) }},
		{name: "duplicate auth spelling", change: func(f []flow) []flow {
			f[1].reqHeader["authorization"] = []string{"Bearer " + inferredTestToken}
			return f
		}},
		{name: "duplicate auth values", change: func(f []flow) []flow {
			f[1].reqHeader["Authorization"] = []string{"Bearer " + inferredTestToken, "Bearer " + inferredTestToken}
			return f
		}},
		{name: "formatted headers", linked: true, change: func(f []flow) []flow {
			f[1].reqHeader = nil
			f[1].reqHeaders = "GET / HTTP/1.1\r\naUtHoRiZaTiOn: Bearer " + inferredTestToken + "\r\n"
			return f
		}},
		{name: "json suffix", linked: true, change: func(f []flow) []flow {
			f[0].respHeader.Set("Content-Type", "Application/Problem+JSON; charset=utf-8")
			return f
		}},
		{name: "non json", skipped: 1, change: func(f []flow) []flow { f[0].respHeader.Set("Content-Type", "text/plain"); return f }},
		{name: "non success", skipped: 1, change: func(f []flow) []flow { f[0].status = 401; return f }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := tokenTestPair()
			if tc.change != nil {
				f = tc.change(f)
			}
			got, err := recognizeTokenLinks(t.Context(), f)
			if err != nil || (len(got.Links) == 1) != tc.linked || got.Skipped != tc.skipped {
				t.Fatalf("analysis = %#v, err = %v; linked=%v skipped=%d", got, err, tc.linked, tc.skipped)
			}
		})
	}
}

func TestRecognizeTokenLinksExactFieldAndTokenMatching(t *testing.T) {
	for _, field := range []string{"token", "access_token", "accessToken", "id_token", "idToken", "refresh_token", "refreshToken", "Token", "password", "not_token"} {
		t.Run(field, func(t *testing.T) {
			f := tokenTestPair()
			f[0].resBody = fmt.Sprintf(`{%q:%q}`, field, inferredTestToken)
			f[0].resSize = len(f[0].resBody)
			got, err := recognizeTokenLinks(t.Context(), f)
			want := slices.Contains([]string{"token", "access_token", "accessToken", "id_token", "idToken"}, field)
			if err != nil || (len(got.Links) == 1) != want || got.Skipped != 0 {
				t.Fatalf("analysis = %#v, %v", got, err)
			}
		})
	}
	for _, auth := range []string{"Basic " + inferredTestToken, "Bearer " + inferredTestToken + "suffix", "Bearer prefix" + inferredTestToken, "Bearer {{token}}", "Bearer " + inferredTestToken + " ", "Bearer\t" + inferredTestToken, "Bearer " + inferredTestToken + ",other"} {
		t.Run(auth, func(t *testing.T) {
			f := tokenTestPair()
			f[1].reqHeader.Set("Authorization", auth)
			f[1].reqHeader.Set("Cookie", "token="+inferredTestToken)
			got, err := recognizeTokenLinks(t.Context(), f)
			if err != nil || len(got.Links) != 0 {
				t.Fatalf("nonexact authorization matched: %#v, %v", got, err)
			}
		})
	}
	for _, value := range []string{strings.Repeat("a", 15), strings.Repeat("a", 16), strings.Repeat("a", 8192), strings.Repeat("a", 8193), "abcdefghijkl=mno", "================", "abcdefghijklmnö", "abcdefghijkl mno", "{{abcdefghijkl}}"} {
		t.Run(fmt.Sprintf("token %d %q", len(value), value[:min(16, len(value))]), func(t *testing.T) {
			f := tokenTestPair()
			f[0].resBody = fmt.Sprintf(`{"token":%q}`, value)
			f[0].resSize = len(f[0].resBody)
			f[1].reqHeader.Set("Authorization", "Bearer "+value)
			got, err := recognizeTokenLinks(t.Context(), f)
			want := value == strings.Repeat("a", 16) || value == strings.Repeat("a", 8192)
			if err != nil || (len(got.Links) == 1) != want || got.Skipped != 0 {
				t.Fatalf("token validation: %#v, %v", got, err)
			}
		})
	}
}

func TestRecognizeTokenLinksResponseBoundsAndSpills(t *testing.T) {
	base := tokenTestPair()[0].resBody
	path := filepath.Join(t.TempDir(), "private-token-body.json")
	if err := os.WriteFile(path, []byte(base), 0600); err != nil {
		t.Fatal(err)
	}
	oversized := filepath.Join(t.TempDir(), "large.json")
	if err := os.WriteFile(oversized, []byte(strings.Repeat(" ", tokenResponseLimit+1)), 0600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		change func(*flow)
		linked bool
	}{
		{"spill not preview", func(f *flow) { f.resFile = path; f.resBody = `{"token":"wrong-preview-token"}` }, true},
		{"spill missing", func(f *flow) { f.resFile = path + "-missing" }, false},
		{"spill directory", func(f *flow) { f.resFile = filepath.Dir(path) }, false},
		{"spill oversized", func(f *flow) { f.resFile = oversized; f.resSize = 0 }, false},
		{"spill incomplete", func(f *flow) { f.resFile = path; f.resSize++ }, false},
		{"memory incomplete", func(f *flow) { f.resSize++ }, false},
		{"negative size", func(f *flow) { f.resSize = -1 }, false},
		{"memory oversized", func(f *flow) { f.resBody = base + strings.Repeat(" ", tokenResponseLimit); f.resSize = 0 }, false},
		{"exact response limit", func(f *flow) {
			f.resBody = base + strings.Repeat(" ", tokenResponseLimit-len(base))
			f.resSize = len(f.resBody)
		}, true},
		{"invalid json", func(f *flow) { f.resBody = base + "!"; f.resSize = len(f.resBody) }, false},
		{"multiple json values", func(f *flow) { f.resBody = base + "{}"; f.resSize = len(f.resBody) }, false},
		{"duplicate names", func(f *flow) {
			f.resBody = `{"token":"` + inferredTestToken + `","token":"other-token-012345"}`
			f.resSize = len(f.resBody)
		}, false},
		{"depth bound", func(f *flow) {
			f.resBody = strings.Repeat("[", tokenJSONDepth+1) + base + strings.Repeat("]", tokenJSONDepth+1)
			f.resSize = len(f.resBody)
		}, false},
		{"node bound", func(f *flow) {
			f.resBody = "[" + base + strings.Repeat(",0", tokenJSONNodes) + "]"
			f.resSize = len(f.resBody)
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := tokenTestPair()
			tc.change(&f[0])
			got, err := recognizeTokenLinks(t.Context(), f)
			wantSkipped := 1
			if tc.linked {
				wantSkipped = 0
			}
			if err != nil || (len(got.Links) == 1) != tc.linked || got.Skipped != wantSkipped {
				t.Fatalf("analysis = %#v, %v; want linked=%v skipped=%d", got, err, tc.linked, wantSkipped)
			}
		})
	}
}

func TestRecognizeTokenLinksAggregateBudget(t *testing.T) {
	// Sixteen complete 256 KiB responses consume exactly 4 MiB. The next
	// response is skipped even though its preview contains a candidate.
	body := `{}` + strings.Repeat(" ", tokenResponseLimit-2)
	path := filepath.Join(t.TempDir(), "budget.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	var flows []flow
	for i := range 17 {
		f := tokenTestFlow(i+1, body)
		if i == 16 {
			f = tokenTestPair()[0]
			f.id = 17
		}
		flows = append(flows, f)
	}
	f := tokenTestPair()[1]
	f.id = 18
	flows = append(flows, f)
	for _, spilled := range []bool{false, true} {
		if spilled {
			for i := range 16 {
				flows[i].resFile = path
				flows[i].resBody = "not the retained body"
			}
		}
		for range 2 {
			got, err := recognizeTokenLinks(t.Context(), flows)
			if err != nil || len(got.Links) != 0 || got.Skipped != 2 {
				t.Fatalf("aggregate budget (spill=%v): %#v, %v", spilled, got, err)
			}
			slices.Reverse(flows)
		}
	}
}

func TestRecognizeTokenLinksInvalidSelectionAndCancellation(t *testing.T) {
	cases := map[string][]flow{
		"empty": nil, "zero": {{id: 0}}, "negative": {{id: -1}}, "duplicate": {{id: 1}, {id: 1}},
		"connect": {{id: 1, method: "CONNECT"}}, "stream": {{id: 1, kind: "websocket"}},
		"metadata URL":    {{id: 1, url: strings.Repeat("x", tokenMetadataLimit+1)}},
		"metadata header": {{id: 1, reqHeader: http.Header{"Authorization": {strings.Repeat("x", tokenMetadataLimit+1)}}}},
	}
	tooMany := make([]flow, 201)
	for i := range tooMany {
		tooMany[i].id = i + 1
	}
	cases["too many"] = tooMany
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := recognizeTokenLinks(t.Context(), f)
			if err == nil || len(got.Links) != 0 || got.Skipped != 0 {
				t.Fatalf("invalid selection returned %#v, %v", got, err)
			}
			if strings.Contains(err.Error(), inferredTestToken) || strings.Contains(err.Error(), "https://") {
				t.Fatalf("error disclosed captured data: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got, err := recognizeTokenLinks(ctx, tokenTestPair())
	if !errors.Is(err, context.Canceled) || len(got.Links) != 0 || got.Skipped != 0 {
		t.Fatalf("cancellation returned %#v, %v", got, err)
	}
	// The selection ceiling itself is inclusive.
	for i := range tooMany[:200] {
		tooMany[i] = tokenTestFlow(i+1, `{}`)
	}
	if _, err := recognizeTokenLinks(t.Context(), tooMany[:200]); err != nil {
		t.Fatalf("200-flow selection rejected: %v", err)
	}
}
