package app

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
)

func TestExperimentsIndependentSends(t *testing.T) {
	var log experimentLog
	draft := savedRequest{Method: "POST", URL: "https://example.test/same", Body: "first"}
	flow := &capture.Flow{Status: 200, RequestBody: "first", ResponseBody: "reply one"}
	first := snapshotExperiment(0, draft, "staging", request.Execution{Response: flow})
	if err := log.retain(first); err != nil {
		t.Fatal(err)
	}
	draft.Body, flow.RequestBody, flow.ResponseBody = "second", "second", "reply two"
	if err := log.retain(snapshotExperiment(0, draft, "production", request.Execution{Response: flow})); err != nil {
		t.Fatal(err)
	}
	failed := snapshotExperiment(0, draft, "offline", request.Execution{
		Request: &request.Prepared{Method: "POST", URL: draft.URL, Body: "attempted"}, Err: errors.New("dial failed"),
	})
	if err := log.retain(failed); err != nil {
		t.Fatal(err)
	}
	if log.NextID != 3 || len(log.Records) != 3 || log.Records[0].ID != 3 || log.Records[2].ID != 1 {
		t.Fatalf("IDs/order = %+v", log)
	}
	if log.Records[2].Draft.Body != "first" || log.Records[2].response().ResponseBody != "reply one" || log.Records[1].response().ResponseBody != "reply two" {
		t.Fatal("sending the same URL replaced an earlier snapshot")
	}
	if failed.response() != nil || failed.Summary != "dial failed" || string(failed.RequestBody) != "attempted" {
		t.Fatalf("failed send = %+v", failed)
	}
	if got := snapshotExperiment(4, draft, "", request.Execution{Err: errors.New("invalid draft")}); got.response() != nil || got.Summary != "invalid draft" {
		t.Fatalf("failure before preparation = %+v", got)
	}
	if err := log.retain(snapshotExperiment(42, draft, "", request.Execution{Replies: []string{"a", "b"}})); err != nil {
		t.Fatal(err)
	}
	if log.NextID != 42 || log.Records[0].ID != 42 || log.Records[0].response() != nil || log.Records[0].Summary != "WebSocket: 2 replies" {
		t.Fatal("explicit IDs or WebSocket sends lost")
	}
}

func TestExperimentMutationIsolation(t *testing.T) {
	draft := savedRequest{Form: []request.Field{{Name: "before", Value: "value"}}}
	f := &capture.Flow{Status: 201, Actions: []string{"rule"},
		RequestHeader: http.Header{"X-Request": {"request"}}, ResponseHeader: http.Header{"X-Response": {"response"}},
		RequestTrailer: http.Header{"X-Trailer": {"request trailer"}}, ResponseTrailer: http.Header{"X-Trailer": {"response trailer"}},
		RequestBody: "request", ResponseBody: "response", Frames: []capture.Frame{{}},
	}
	got := request.Execution{Response: f, Tests: []request.TestResult{{Name: "before", Pass: true}}, Replies: []string{"before"}}
	r := snapshotExperiment(1, draft, "local", got)
	draft.Form[0].Name, got.Tests[0].Name, got.Replies[0], f.Actions[0] = "changed", "changed", "changed", "changed"
	for _, h := range []http.Header{f.RequestHeader, f.ResponseHeader, f.RequestTrailer, f.ResponseTrailer} {
		for key := range h {
			h[key][0] = "changed"
		}
	}
	f.RequestBody, f.ResponseBody = "changed", "changed"
	if r.Draft.Form[0].Name != "before" || r.Tests[0].Name != "before" || r.Replies[0] != "before" || r.Exchange.Actions[0] != "rule" {
		t.Fatal("snapshot retained mutable input")
	}
	if r.Exchange.RequestBody != "" || r.Exchange.ResponseBody != "" || r.Exchange.RequestBodyFile != "" || r.Exchange.ResponseBodyFile != "" || r.Exchange.Frames != nil {
		t.Fatal("exchange is not metadata only")
	}
	reply := r.response()
	if reply.RequestHeader.Get("X-Request") != "request" || reply.ResponseHeader.Get("X-Response") != "response" || reply.RequestTrailer.Get("X-Trailer") != "request trailer" || reply.ResponseTrailer.Get("X-Trailer") != "response trailer" {
		t.Fatal("snapshot retained caller-owned header values")
	}
	reply.Actions[0] = "edited"
	reply.RequestHeader["X-Request"][0] = "edited"
	reply.ResponseHeader["X-Response"][0] = "edited"
	reply.RequestTrailer["X-Trailer"][0] = "edited"
	reply.ResponseTrailer["X-Trailer"][0] = "edited"
	reply.RequestBody, reply.ResponseBody = "edited", "edited"
	if again := r.response(); again.Actions[0] != "rule" || again.RequestHeader.Get("X-Request") != "request" || again.ResponseHeader.Get("X-Response") != "response" || again.RequestTrailer.Get("X-Trailer") != "request trailer" || again.ResponseTrailer.Get("X-Trailer") != "response trailer" || again.RequestBody != "request" || again.ResponseBody != "response" {
		t.Fatal("response() returned mutable snapshot storage")
	}
}

func TestExperimentPayloadRetention(t *testing.T) {
	for _, size := range []int{0, 513, 256 << 10, (256 << 10) + 123} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			body := make([]byte, size)
			for i := range body {
				body[i] = byte(i)
			}
			path := filepath.Join(t.TempDir(), "spill")
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			f := &capture.Flow{Status: 200, RequestBody: string(body), RequestBodySize: size,
				ResponseBody: string(body[:min(size, 7)]), ResponseBodyFile: path, ResponseBodySize: size, ResponseBodyError: "original transfer error"}
			r := snapshotExperiment(1, savedRequest{}, "", request.Execution{Response: f})
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			want := body[:min(size, 256<<10)]
			if !bytes.Equal(r.RequestBody, want) || !bytes.Equal(r.ResponseBody, want) || r.Exchange.RequestBodySize != size || r.Exchange.ResponseBodySize != size {
				t.Fatalf("payload/size not retained for %d bytes", size)
			}
			if !strings.Contains(r.Exchange.ResponseBodyError, "original transfer error") {
				t.Fatal("original diagnostic lost")
			}
			if strings.Contains(r.Exchange.RequestBodyError, "truncated") != (size > 256<<10) || strings.Contains(r.Exchange.ResponseBodyError, "truncated") != (size > 256<<10) {
				t.Fatalf("truncation diagnostics = %q, %q", r.Exchange.RequestBodyError, r.Exchange.ResponseBodyError)
			}
			if string(want) != r.response().ResponseBody {
				t.Fatal("binary response was changed")
			}
			log := experimentLog{Version: 1, Records: []experimentRecord{r}}
			store := filepath.Join(t.TempDir(), "experiments.json")
			if err := saveExperiments(store, log); err != nil {
				t.Fatal(err)
			}
			loaded, err := loadExperiments(store)
			if err != nil || !bytes.Equal(loaded.Records[0].ResponseBody, want) {
				t.Fatalf("binary roundtrip: %v", err)
			}
		})
	}
	missing := snapshotExperiment(1, savedRequest{}, "", request.Execution{Response: &capture.Flow{
		Status: 200, ResponseBody: "head", ResponseBodyFile: filepath.Join(t.TempDir(), "missing"), ResponseBodySize: 800,
	}})
	if string(missing.ResponseBody) != "head" || !strings.Contains(missing.Exchange.ResponseBodyError, "read:") || !strings.Contains(missing.Exchange.ResponseBodyError, "truncated") {
		t.Fatalf("missing spill = %+v", missing)
	}
}

func TestExperimentsRetentionPreservesBaseline(t *testing.T) {
	log := experimentLog{Version: 1, Baseline: 1}
	for range 110 {
		if err := log.retain(experimentRecord{}); err != nil {
			t.Fatal(err)
		}
	}
	if len(log.Records) != 100 || log.NextID != 110 || log.Records[0].ID != 110 || log.Records[98].ID != 12 || log.Records[99].ID != 1 {
		t.Fatal("count eviction did not keep baseline and newest 99 records")
	}
	// Serialized bytes, including base64 expansion and editable drafts, count.
	large := strings.Repeat("x", 12<<20)
	log = experimentLog{Version: 1, Baseline: 1, Drafts: []experimentDraft{{Value: savedRequest{Body: large}}}}
	for i := range 3 {
		if err := log.retain(experimentRecord{ID: i + 1, Note: strings.Repeat("n", 8<<20)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(log.Records) != 2 || log.Records[0].ID != 3 || log.Records[1].ID != 1 || log.Drafts[0].Value.Body != large {
		t.Fatal("size eviction changed baseline, newest record, or editable draft")
	}
	before, err := json.Marshal(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []experimentRecord{{Note: strings.Repeat("z", 22<<20)}, {ID: 1}, {Note: string([]byte{0xff})}} {
		if err := log.retain(record); err == nil {
			t.Fatal("impossible/duplicate/malformed candidate accepted")
		}
		after, err := json.Marshal(log)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("failed retain did not roll back")
		}
	}
}

func TestExperimentsStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "experiments.json")
	if log, err := loadExperiments(path); err != nil || log.Version != 1 || log.NextID != 0 || len(log.Records) != 0 {
		t.Fatalf("missing file: %+v, %v", log, err)
	}
	draft := savedRequest{Name: "unsaved", Body: strings.Repeat("exact body\x00\n", 30000), Form: []request.Field{{Name: "file", Value: "/tmp/a", File: true}}}
	r := snapshotExperiment(9, draft, "development", request.Execution{Response: &capture.Flow{Status: 204}})
	r.At, r.Note = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC), "important baseline"
	want := experimentLog{Version: 1, NextID: 9, Baseline: 9, Records: []experimentRecord{r}, Active: 1,
		Drafts: []experimentDraft{{Value: draft, Baseline: savedRequest{Body: "saved original"}, SavedKey: "collection/name"}, {Value: savedRequest{URL: "https://another.test"}}}}
	if err := os.WriteFile(path, []byte("old public file"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := saveExperiments(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private mode: %v, %v", info, err)
	}
	got, err := loadExperiments(path)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("roundtrip differs: %v", err)
	}
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []experimentLog{
		{Version: 2}, {Version: 1, Records: make([]experimentRecord, 101)},
		{Version: 1, Drafts: []experimentDraft{{Value: savedRequest{Body: strings.Repeat("x", 32<<20)}}}},
	} {
		if err := saveExperiments(path, bad); err == nil {
			t.Fatal("invalid save accepted")
		}
		if after, err := os.ReadFile(path); err != nil || !bytes.Equal(old, after) {
			t.Fatal("failed save replaced existing file")
		}
	}
	if files, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".request-*")); err != nil || len(files) != 0 {
		t.Fatalf("temporary files left behind: %v, %v", files, err)
	}
}

func TestExperimentsRejectInvalidInput(t *testing.T) {
	tooMany, err := json.Marshal(experimentLog{Version: 1, Records: make([]experimentRecord, 101)})
	if err != nil {
		t.Fatal(err)
	}
	for i, data := range []string{"", "{", "null", "{}", `{"Version":2}`, `{"Version":1} trailing`, `{"Version":1,"Version":1}`, string(tooMany), strings.Repeat(" ", (32<<20)+1)} {
		path := filepath.Join(t.TempDir(), "invalid.json")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadExperiments(path); err == nil {
			t.Errorf("invalid input case %d accepted", i)
		}
	}
	log := experimentLog{Version: 1, Records: []experimentRecord{{ID: 1}}, Drafts: []experimentDraft{{Value: savedRequest{Body: strings.Repeat("x", 32<<20)}}}}
	original := slices.Clone(log.Records)
	if err := log.retain(experimentRecord{}); err == nil || !reflect.DeepEqual(log.Records, original) || log.NextID != 0 {
		t.Fatal("oversized drafts did not roll back retention")
	}
}
