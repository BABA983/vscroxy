package imports

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
)

func captureFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func capturePhase(t *testing.T, timing *CaptureTiming, name string) CaptureTimingPhase {
	t.Helper()
	if timing == nil {
		t.Fatalf("no timing; wanted phase %q", name)
	}
	for _, phase := range timing.Phases {
		if phase.Name == name {
			return phase
		}
	}
	t.Fatalf("no phase %q in %+v", name, timing.Phases)
	return CaptureTimingPhase{}
}

func TestReadCharlesJSONCapture(t *testing.T) {
	capture, err := ReadCapture(captureFixture(t, "charles-session.chlsj"), "charles-session.chlsj")
	if err != nil {
		t.Fatal(err)
	}
	if capture.Format != CaptureCharlesJSON {
		t.Fatalf("format = %q", capture.Format)
	}
	if len(capture.Flows) != 4 {
		t.Fatalf("%d flows: %+v", len(capture.Flows), capture.Flows)
	}
	if len(capture.Warnings) != 0 {
		t.Fatalf("warnings = %q", capture.Warnings)
	}
	flow := capture.Flows[0]
	if flow.Method != "POST" || flow.URL != "https://api.example.com/projects?page=1" ||
		flow.Status != 200 || flow.ReqProto != "HTTP/2.0" || flow.ResProto != "HTTP/2.0" {
		t.Errorf("request line = %+v", flow)
	}
	wantAt := time.Date(2026, 10, 1, 10, 0, 0, 0, time.FixedZone("", 8*60*60))
	if !flow.At.Equal(wantAt) {
		t.Errorf("at = %v, want %v", flow.At, wantAt)
	}
	if flow.Millis != 45 || flow.Wait != 30 || flow.Download != 5 {
		t.Errorf("timing = %d/%d/%d", flow.Millis, flow.Wait, flow.Download)
	}
	if got := flow.ReqHeader.Values("X-Trace"); !slices.Equal(got, []string{"first", "second"}) {
		t.Errorf("X-Trace = %q", got)
	}
	if got := flow.ResHeader.Values("Set-Cookie"); len(got) != 2 {
		t.Errorf("Set-Cookie = %q", got)
	}
	if flow.ResHeader.Get("Content-Encoding") != "gzip" {
		t.Errorf("Content-Encoding = %q", flow.ResHeader.Get("Content-Encoding"))
	}
	if string(flow.ReqBody) != `{"name":"Ada"}` || flow.ReqSize != 14 {
		t.Errorf("request body = %q size %d", flow.ReqBody, flow.ReqSize)
	}
	if string(flow.ResBody) != `{"id":73}` || flow.ResSize != 9 {
		t.Errorf("response body = %q size %d", flow.ResBody, flow.ResSize)
	}
	if flow.Bytes != 123 || flow.Remote != "203.0.113.7" || flow.Client != "198.51.100.5" {
		t.Errorf("sizes/addresses = %+v", flow)
	}
	if flow.TLS != "TLSv1.3" || flow.Cipher != "TLS_AES_128_GCM_SHA256" {
		t.Errorf("TLS = %q %q", flow.TLS, flow.Cipher)
	}
	if flow.Timing == nil || flow.Timing.Total != 45*time.Millisecond {
		t.Fatalf("timing = %+v", flow.Timing)
	}
	for _, want := range []struct {
		name  string
		start time.Duration
		dur   time.Duration
	}{
		{"DNS", 0, 1 * time.Millisecond},
		{"Connect", 1 * time.Millisecond, 2 * time.Millisecond},
		{"TLS", 3 * time.Millisecond, 1 * time.Millisecond},
		{"Send", 4 * time.Millisecond, 5 * time.Millisecond},
		{"Wait", 9 * time.Millisecond, 30 * time.Millisecond},
		{"Receive", 39 * time.Millisecond, 5 * time.Millisecond},
	} {
		got := capturePhase(t, flow.Timing, want.name)
		if got.Start != want.start || got.Duration != want.dur {
			t.Errorf("%s = start %v duration %v, want %v/%v", want.name, got.Start, got.Duration, want.start, want.dur)
		}
	}

	binary := capture.Flows[1]
	if !bytes.Equal(binary.ResBody, []byte{0x00, 0xff, 0xfe, 0x01}) || binary.ResSize != 4 {
		t.Errorf("binary response = %x size %d", binary.ResBody, binary.ResSize)
	}
	websocket := capture.Flows[2]
	if websocket.Kind != "ws" || websocket.Scheme != "wss" || websocket.URL != "wss://ws.example.com/socket" ||
		websocket.Status != 101 || len(websocket.Frames) != 3 || len(websocket.ResBody) != 0 {
		t.Errorf("WebSocket = %+v", websocket)
	}
	if got := websocket.Frames[0]; got.Direction != "out" || got.Kind != "text" || string(got.Data) != "hello" {
		t.Errorf("first frame = %+v", got)
	}
	failed := capture.Flows[3]
	if failed.Status != 0 || !strings.Contains(failed.Error, "SSLHandshakeException") {
		t.Errorf("failure = %+v", failed)
	}
}

func TestReadCharlesXMLCapture(t *testing.T) {
	capture, err := ReadCapture(captureFixture(t, "charles-session.chlsx"), "charles-session.chlsx")
	if err != nil {
		t.Fatal(err)
	}
	if capture.Format != CaptureCharlesXML || len(capture.Flows) != 3 {
		t.Fatalf("format %q, %d flows: %+v", capture.Format, len(capture.Flows), capture.Flows)
	}
	if len(capture.Warnings) != 0 {
		t.Fatalf("warnings = %q", capture.Warnings)
	}
	flow := capture.Flows[0]
	if flow.Method != "GET" || flow.URL != "https://api.example.com/users?page=2" || flow.Status != 200 {
		t.Errorf("request line = %+v", flow)
	}
	if flow.Millis != 45 || flow.Wait != 30 || flow.Download != 5 {
		t.Errorf("timing = %d/%d/%d", flow.Millis, flow.Wait, flow.Download)
	}
	if !flow.At.Equal(time.UnixMilli(1790820000000)) {
		t.Errorf("at = %v", flow.At)
	}
	if got := flow.ReqHeader.Values("X-Trace"); !slices.Equal(got, []string{"first", "second"}) {
		t.Errorf("X-Trace = %q", got)
	}
	if got := flow.ResHeader.Values("Set-Cookie"); len(got) != 2 {
		t.Errorf("Set-Cookie = %q", got)
	}
	if string(flow.ResBody) != `{"users":[]}` || flow.ResSize != 12 {
		t.Errorf("response body = %q size %d", flow.ResBody, flow.ResSize)
	}
	if flow.TLS != "TLSv1.3" || flow.Cipher != "TLS_AES_128_GCM_SHA256" || flow.Remote != "203.0.113.8" || flow.Client != "198.51.100.6" {
		t.Errorf("TLS/remote/client = %+v", flow)
	}
	if flow.Timing == nil || flow.Timing.Total != 45*time.Millisecond {
		t.Fatalf("timing = %+v", flow.Timing)
	}
	upload := capture.Flows[1]
	if !bytes.Equal(upload.ReqBody, []byte{0x00, 0xff, 0xfe, 0x01}) || string(upload.ResBody) != "ok" || upload.Status != 201 {
		t.Errorf("upload = %+v body %x/%q", upload, upload.ReqBody, upload.ResBody)
	}
	websocket := capture.Flows[2]
	if websocket.Kind != "ws" || websocket.Scheme != "wss" || websocket.URL != "wss://ws.example.com/socket" || websocket.Status != 101 || len(websocket.Frames) != 3 {
		t.Errorf("WebSocket = %+v", websocket)
	}
}

func TestReadProxymanZipCapture(t *testing.T) {
	archive := proxymanArchive(t, map[string][]byte{
		"request_0_1001": captureFixture(t, "proxyman-request_0_1001.json"),
		"request_1_1002": captureFixture(t, "proxyman-request_1_1002.json"),
		"request_2_1003": captureFixture(t, "proxyman-request_2_1003.json"),
		"request_3_1004": captureFixture(t, "proxyman-request_3_1004.json"),
	})
	capture, err := ReadCapture(archive, "fixture.proxymanlogv2")
	if err != nil {
		t.Fatal(err)
	}
	if capture.Format != CaptureProxymanLog || len(capture.Flows) != 4 {
		t.Fatalf("format %q, %d flows: %+v", capture.Format, len(capture.Flows), capture.Flows)
	}
	if len(capture.Warnings) != 0 {
		t.Fatalf("warnings = %q", capture.Warnings)
	}
	flow := capture.Flows[0]
	if flow.Method != "GET" || flow.URL != "https://api.example.com/projects?page=1" || flow.Status != 200 {
		t.Errorf("request line = %+v", flow)
	}
	if flow.Millis != 80 || flow.Wait != 30 || flow.Download != 30 {
		t.Errorf("timing = %d/%d/%d", flow.Millis, flow.Wait, flow.Download)
	}
	if !flow.At.Equal(time.Unix(1760000000, 100_000_000).UTC()) {
		t.Errorf("at = %v", flow.At)
	}
	if got := flow.ReqHeader.Values("X-Trace"); !slices.Equal(got, []string{"first", "second"}) {
		t.Errorf("X-Trace = %q", got)
	}
	if flow.ReqHeader.Get("X-Disabled") != "" {
		t.Errorf("disabled header imported: %q", flow.ReqHeader.Get("X-Disabled"))
	}
	if got := flow.ResHeader.Values("Set-Cookie"); len(got) != 2 {
		t.Errorf("Set-Cookie = %q", got)
	}
	if string(flow.ResBody) != `{"projects":[]}` {
		t.Errorf("body = %q", flow.ResBody)
	}
	if flow.Remote != "203.0.113.9" || flow.Client != "198.51.100.6" {
		t.Errorf("addresses = %+v", flow)
	}
	if flow.TLS != "TLSv1.3" || flow.Cipher != "TLS_AES_128_GCM_SHA256" ||
		flow.CertCN != "api.example.com" || flow.Issuer != "Example CA" || flow.CertUntil == "" {
		t.Errorf("TLS = %+v", flow)
	}
	if flow.Note != "imported fixture" || flow.Process != "Fixture Browser" {
		t.Errorf("annotations = %q/%q", flow.Note, flow.Process)
	}

	websocket := capture.Flows[1]
	if websocket.Kind != "ws" || websocket.Scheme != "wss" || websocket.URL != "wss://ws.example.com/socket" || websocket.Status != 101 {
		t.Errorf("WebSocket = %+v", websocket)
	}
	if len(websocket.Frames) != 4 {
		t.Fatalf("%d frames: %+v", len(websocket.Frames), websocket.Frames)
	}
	for i, want := range []struct {
		direction, kind string
		data            []byte
	}{
		{"out", "text", []byte("hello")},
		{"in", "text", []byte("welcome")},
		{"in", "binary", []byte{0x00, 0xff, 0xfe, 0x01}},
		{"out", "ping", nil},
	} {
		got := websocket.Frames[i]
		if got.Direction != want.direction || got.Kind != want.kind || !bytes.Equal(got.Data, want.data) {
			t.Errorf("frame %d = %+v, want %+v", i, got, want)
		}
	}

	failed := capture.Flows[2]
	if failed.Status != 0 || failed.Error != "Could not connect to the server" {
		t.Errorf("failure = %+v", failed)
	}
	binary := capture.Flows[3]
	if !bytes.Equal(binary.ResBody, []byte{0x00, 0xff, 0xfe, 0x01}) {
		t.Errorf("binary response = %x", binary.ResBody)
	}
}

func TestReadProxymanDirCapture(t *testing.T) {
	dir := t.TempDir()
	for name, fixture := range map[string]string{
		"request_0_1001": "proxyman-request_0_1001.json",
		"request_1_1002": "proxyman-request_1_1002.json",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), captureFixture(t, fixture), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "request_0_1001"), filepath.Join(dir, "request_2_1003")); err == nil {
		defer os.Remove(filepath.Join(dir, "request_2_1003"))
	}
	capture, err := ReadProxymanDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Flows) != 2 {
		t.Fatalf("%d flows: %+v", len(capture.Flows), capture.Flows)
	}
	if capture.Flows[0].URL != "https://api.example.com/projects?page=1" || capture.Flows[1].Kind != "ws" {
		t.Errorf("flows = %+v", capture.Flows)
	}
}

func TestReadProxymanDirFindsOneNestedFolder(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "fixture.proxymanlog")
	if err := os.Mkdir(nested, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "request_0_1001"), captureFixture(t, "proxyman-request_0_1001.json"), 0644); err != nil {
		t.Fatal(err)
	}
	capture, err := ReadProxymanDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Flows) != 1 || capture.Flows[0].Status != 200 {
		t.Fatalf("flows = %+v", capture.Flows)
	}
}

func TestReadCaptureRejectsNativeCharlesSession(t *testing.T) {
	_, err := ReadCapture(captureFixture(t, "charles-native.chls"), "charles-native.chls")
	if !errors.Is(err, ErrCharlesNative) {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"Java-serialized", ".chlsj", "HAR"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestReadCaptureRejectsMalformedInput(t *testing.T) {
	truncated := []byte(`[{"host":"api.example.com","method":"GET"`)
	xmlBroken := []byte(`<charles-session><transaction status="COMPLETE">`)
	for name, data := range map[string][]byte{
		"empty":         nil,
		"truncated":     truncated,
		"xml":           xmlBroken,
		"trailing-json": []byte(`[{"status":"COMPLETE","method":"GET","scheme":"https","host":"example.com","path":"/","request":{"header":{"headers":[]}},"response":{"status":200,"header":{"headers":[]}}}] trailing`),
		"trailing-xml":  []byte(`<charles-session><transaction status="COMPLETE" method="GET" protocol="https" host="example.com" path="/"><request headers="0" body="0"/><response status="200" headers="0" body="0"/></transaction></charles-session> trailing`),
		"random":        []byte("not a capture"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadCapture(data, name); err == nil {
				t.Fatal("malformed input was accepted")
			}
		})
	}
	if _, err := ReadCapture([]byte("[]"), "empty.chlsj"); err == nil {
		t.Fatal("empty session was accepted")
	}
	if _, err := ReadCapture([]byte("<html></html>"), "page.chlsx"); err == nil {
		t.Fatal("non-Charles XML was accepted")
	}
}

func TestReadProxymanZipIgnoresPathTraversalAndKeepsValidEntries(t *testing.T) {
	archive := proxymanArchive(t, map[string][]byte{
		"../request_0_1001": captureFixture(t, "proxyman-request_0_1001.json"),
		"request_1_1002":    captureFixture(t, "proxyman-request_1_1002.json"),
	})
	capture, err := ReadCapture(archive, "fixture.proxymanlogv2")
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Flows) != 1 || capture.Flows[0].Kind != "ws" {
		t.Fatalf("flows = %+v", capture.Flows)
	}
	if len(capture.Warnings) != 1 || !strings.Contains(capture.Warnings[0], "flat") {
		t.Fatalf("warnings = %q", capture.Warnings)
	}
}

func TestReadProxymanZipRejectsTruncatedArchive(t *testing.T) {
	archive := proxymanArchive(t, map[string][]byte{
		"request_0_1001": captureFixture(t, "proxyman-request_0_1001.json"),
	})
	if _, err := ReadCapture(archive[:len(archive)/2], "truncated.proxymanlogv2"); err == nil {
		t.Fatal("truncated archive was accepted")
	}
}

func TestReadCaptureLimitsEntries(t *testing.T) {
	var data strings.Builder
	data.WriteString("[")
	for i := range maxCaptureEntries + 5 {
		if i > 0 {
			data.WriteString(",")
		}
		data.WriteString(`{"status":"COMPLETE","method":"GET","protocolVersion":"HTTP/1.1","scheme":"https","host":"api.example.com","actualPort":443,"path":"/x","request":{"header":{"headers":[]}},"response":{"status":200,"header":{"headers":[]}}}`)
	}
	data.WriteString("]")
	capture, err := ReadCapture([]byte(data.String()), "many.chlsj")
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Flows) != maxCaptureEntries {
		t.Fatalf("%d flows", len(capture.Flows))
	}
	if len(capture.Warnings) != 1 || !strings.Contains(capture.Warnings[0], "5 transactions beyond") {
		t.Fatalf("warnings = %q", capture.Warnings)
	}
}

func TestReadCaptureInvalidBase64KeepsTheExchange(t *testing.T) {
	data := []byte(`[{"status":"COMPLETE","method":"POST","scheme":"https","host":"api.example.com","actualPort":443,"path":"/x","request":{"header":{"headers":[]},"body":{"encoded":"not base64!","size":12}},"response":{"status":200,"header":{"headers":[]}}}]`)
	capture, err := ReadCapture(data, "bad-body.chlsj")
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Flows) != 1 || len(capture.Flows[0].ReqBody) != 0 || capture.Flows[0].ReqSize != 12 {
		t.Fatalf("flows = %+v", capture.Flows)
	}
	if len(capture.Warnings) != 1 || !strings.Contains(capture.Warnings[0], "not valid base64") {
		t.Fatalf("warnings = %q", capture.Warnings)
	}
}

func proxymanArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		member, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := member.Write(files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestReadCharlesXMLToleratesMissingHeaderBlocks(t *testing.T) {
	data := []byte(`<charles-session><transaction status="COMPLETE" method="GET" protocol="https" host="api.example.com" path="/x"><request/><response status="200"/></transaction></charles-session>`)
	capture, err := ReadCapture(data, "sparse.chlsx")
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Flows) != 1 || capture.Flows[0].Status != 200 || len(capture.Flows[0].ResHeader) != 0 {
		t.Fatalf("flows = %+v", capture.Flows)
	}
}

func TestReadProxymanZipRejectsDeclaredExpansion(t *testing.T) {
	archive := proxymanArchive(t, map[string][]byte{
		"request_0_1001": captureFixture(t, "proxyman-request_0_1001.json"),
		"request_1_1002": captureFixture(t, "proxyman-request_1_1002.json"),
		"request_2_1003": captureFixture(t, "proxyman-request_2_1003.json"),
	})
	// The writer records the real sizes; rewrite each central directory
	// uncompressed size to what a zip bomb would declare. Each stays below
	// the per-entry cap, and together they exceed the archive cap.
	for offset := 0; ; {
		at := bytes.Index(archive[offset:], []byte("PK\x01\x02"))
		if at < 0 {
			break
		}
		at += offset
		if at+28 > len(archive) {
			t.Fatal("truncated central directory")
		}
		binary.LittleEndian.PutUint32(archive[at+24:at+28], 100<<20)
		offset = at + 4
	}
	if _, err := ReadCapture(archive, "bomb.proxymanlogv2"); err == nil || !strings.Contains(err.Error(), "expands beyond") {
		t.Fatalf("err = %v", err)
	}
}

func TestProxymanWebSocketKeepsNonDefaultPort(t *testing.T) {
	entry := proxymanEntry{
		IsSSL: true,
		Request: &proxymanRequest{
			Scheme: "https", Host: "ws.example.com", Port: 8443, IsSSL: true,
			FullPath: "https://ws.example.com:8443/socket?room=1", URI: "/socket?room=1",
			IsWebSocketUpgrade: true, Method: proxymanMethod{Name: "GET"},
		},
		Response: &proxymanResponse{Status: &proxymanStatus{Code: 101}},
	}
	flow, err := entry.toCapture(&captureWarnings{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flow.URL != "wss://ws.example.com:8443/socket?room=1" || flow.Scheme != "wss" || flow.Kind != "ws" {
		t.Fatalf("flow = %+v", flow)
	}
}

func TestCaptureURLBracketsIPv6(t *testing.T) {
	if got := captureURL("https", "2001:db8::1", 443, "/x", ""); got != "https://[2001:db8::1]/x" {
		t.Errorf("default port = %q", got)
	}
	if got := captureURL("https", "2001:db8::1", 8443, "/x", ""); got != "https://[2001:db8::1]:8443/x" {
		t.Errorf("non-default port = %q", got)
	}
}

func captureAllocated(t *testing.T, run func()) uint64 {
	t.Helper()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	run()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func TestCharlesJSONStreamingBoundsAllocations(t *testing.T) {
	var data strings.Builder
	data.Grow(800 << 10)
	data.WriteString("[")
	for i := range 262_144 {
		if i > 0 {
			data.WriteString(",")
		}
		data.WriteString("{}")
	}
	data.WriteString("]")
	input := []byte(data.String())
	allocated := captureAllocated(t, func() {
		if _, err := ReadCapture(input, "empty-objects.chlsj"); err != nil {
			t.Fatalf("ReadCapture: %v", err)
		}
	})
	if allocated > 128<<20 {
		t.Fatalf("empty JSON transactions allocated %d MiB; want under 128 MiB", allocated>>20)
	}
}

func TestCharlesXMLStreamingBoundsAllocations(t *testing.T) {
	var data strings.Builder
	data.WriteString("<charles-session>")
	for range 131_072 {
		data.WriteString("<transaction/>")
	}
	data.WriteString("</charles-session>")
	input := []byte(data.String())
	var capture Capture
	allocated := captureAllocated(t, func() {
		var err error
		capture, err = ReadCapture(input, "empty-transactions.chlsx")
		if err != nil {
			t.Fatalf("ReadCapture: %v", err)
		}
	})
	if allocated > 128<<20 {
		t.Fatalf("empty XML transactions allocated %d MiB; want under 128 MiB", allocated>>20)
	}
	if len(capture.Warnings) == 0 || !strings.Contains(capture.Warnings[0], "transactions beyond") {
		t.Fatalf("warnings = %q", capture.Warnings)
	}
}

func TestCharlesJSONHeaderListIsBounded(t *testing.T) {
	var headers strings.Builder
	for i := range 1_000_000 {
		if i > 0 {
			headers.WriteString(",")
		}
		headers.WriteString("{}")
	}
	data := []byte(`[{"status":"COMPLETE","method":"GET","scheme":"https","host":"example.com","path":"/","request":{"header":{"headers":[` + headers.String() + `]}},"response":{"status":200,"header":{"headers":[]}}}]`)
	allocated := captureAllocated(t, func() {
		capture, err := ReadCapture(data, "headers.chlsj")
		if err != nil {
			t.Fatalf("ReadCapture: %v", err)
		}
		if len(capture.Flows) != 1 || len(capture.Flows[0].ReqHeader) != 0 {
			t.Fatalf("flow = %+v", capture.Flows)
		}
		if len(capture.Warnings) == 0 || !strings.Contains(strings.Join(capture.Warnings, ";"), "headers beyond") {
			t.Fatalf("warnings = %q", capture.Warnings)
		}
	})
	if allocated > 128<<20 {
		t.Fatalf("million headers allocated %d MiB; want under 128 MiB", allocated>>20)
	}
}

func TestProxymanWebSocketMessagesAreBounded(t *testing.T) {
	var messages strings.Builder
	for i := range 1_000_000 {
		if i > 0 {
			messages.WriteString(",")
		}
		messages.WriteString("{}")
	}
	entry := []byte(`{"request":{"scheme":"https","host":"example.com","uri":"/ws","isWebSocketUpgrade":true,"method":{"name":"GET"}},"response":{"status":{"code":101}},"websocketMessageStorage":{"messages":[` + messages.String() + `]}}`)
	archive := proxymanArchive(t, map[string][]byte{"request_0_1": entry})
	allocated := captureAllocated(t, func() {
		capture, err := ReadCapture(archive, "messages.proxymanlogv2")
		if err != nil {
			t.Fatalf("ReadCapture: %v", err)
		}
		if len(capture.Flows) != 1 || len(capture.Flows[0].Frames) != maxCaptureMessages {
			t.Fatalf("frames = %d", len(capture.Flows[0].Frames))
		}
		if len(capture.Warnings) == 0 || !strings.Contains(strings.Join(capture.Warnings, ";"), "WebSocket messages beyond") {
			t.Fatalf("warnings = %q", capture.Warnings)
		}
	})
	if allocated > 128<<20 {
		t.Fatalf("million messages allocated %d MiB; want under 128 MiB", allocated>>20)
	}
}

func TestCharlesExceptionAndConnectTunnel(t *testing.T) {
	data := []byte(`[
{"status":"EXCEPTION","method":"CONNECT","protocolVersion":"HTTP/1.1","scheme":"https","host":"httpbin.org","actualPort":443,"path":null,"tunnel":false,"webSocket":false,"exception":"SSLHandshakeException: Remote host closed connection during handshake","request":{"header":{"headers":[]}},"response":{"header":{"headers":[]}}},
{"status":"COMPLETE","method":"GET","protocolVersion":"HTTP/1.1","scheme":"https","host":"api.example.com","actualPort":443,"path":"/x","tunnel":false,"exception":"Client closed connection before receiving entire response","request":{"header":{"headers":[]}},"response":{"status":200,"header":{"headers":[]}}}
]`)
	capture, err := ReadCapture(data, "connect.chlsj")
	if err != nil {
		t.Fatal(err)
	}
	connect := capture.Flows[0]
	if connect.Kind != "connect" || connect.Method != "CONNECT" || connect.URL != "https://httpbin.org:443" || connect.Path != "" {
		t.Errorf("CONNECT = %+v", connect)
	}
	if !strings.Contains(connect.Error, "SSLHandshakeException") || strings.Contains(connect.Error, "Charles reported") {
		t.Errorf("CONNECT error = %q", connect.Error)
	}
	if got := capture.Flows[1].Error; got != "Client closed connection before receiving entire response" {
		t.Errorf("COMPLETE with exception error = %q", got)
	}
}

func TestCharlesStatusFallsBackToPseudoHeaderAndFirstLine(t *testing.T) {
	data := []byte(`[
{"status":"COMPLETE","method":"GET","scheme":"https","host":"firstline.example","path":"/","request":{"header":{"headers":[]}},"response":{"header":{"firstLine":"HTTP/1.1 200 OK","headers":[]}}},
{"status":"COMPLETE","method":"GET","scheme":"https","host":"pseudo.example","path":"/","request":{"header":{"headers":[]}},"response":{"header":{"headers":[{"name":":status","value":"204"}]}}}
]`)
	capture, err := ReadCapture(data, "status.chlsj")
	if err != nil {
		t.Fatal(err)
	}
	if capture.Flows[0].Status != 200 || capture.Flows[1].Status != 204 {
		t.Fatalf("statuses = %d, %d", capture.Flows[0].Status, capture.Flows[1].Status)
	}
}

func TestCharlesTimingSkipsIdleGapAndUsesSetupDurations(t *testing.T) {
	data := []byte(`[{"status":"COMPLETE","method":"GET","scheme":"https","host":"api.example.com","path":"/","times":{"start":"2026-10-01T10:00:00.000Z","requestBegin":"2026-10-01T10:00:00.908Z","requestComplete":"2026-10-01T10:00:00.908Z","responseBegin":"2026-10-01T10:00:00.945Z","end":"2026-10-01T10:00:00.947Z"},"durations":{"total":69,"dns":3,"connect":6,"ssl":21,"request":0,"latency":37,"response":2},"request":{"header":{"headers":[]}},"response":{"status":200,"header":{"headers":[]}}}]`)
	capture, err := ReadCapture(data, "timing.chlsj")
	if err != nil {
		t.Fatal(err)
	}
	timing := capture.Flows[0].Timing
	if timing == nil || timing.Total != 69*time.Millisecond {
		t.Fatalf("timing = %+v", timing)
	}
	for _, want := range []struct {
		name  string
		start time.Duration
		dur   time.Duration
	}{
		{"DNS", 0, 3 * time.Millisecond},
		{"Connect", 3 * time.Millisecond, 6 * time.Millisecond},
		{"TLS", 9 * time.Millisecond, 21 * time.Millisecond},
		{"Send", 30 * time.Millisecond, 0},
		{"Wait", 30 * time.Millisecond, 37 * time.Millisecond},
		{"Receive", 67 * time.Millisecond, 2 * time.Millisecond},
	} {
		got := capturePhase(t, timing, want.name)
		if got.Start != want.start || got.Duration != want.dur {
			t.Errorf("%s = %v/%v, want %v/%v", want.name, got.Start, got.Duration, want.start, want.dur)
		}
	}
	if _, ok := func() (CaptureTimingPhase, bool) {
		for _, phase := range timing.Phases {
			if phase.Name == "Connection" {
				return phase, true
			}
		}
		return CaptureTimingPhase{}, false
	}(); ok {
		t.Error("idle start-to-requestBegin gap was imported as a phase")
	}
}

func TestCharlesCompressedEncodedBodyIsDecoded(t *testing.T) {
	plain := []byte(`{"ok":true}`)
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(compressed.Bytes())
	data := []byte(`[{"status":"COMPLETE","method":"GET","scheme":"https","host":"api.example.com","path":"/","request":{"header":{"headers":[]}},"response":{"status":200,"contentEncoding":"gzip","header":{"headers":[]},"body":{"encoded":"` + encoded + `","size":` + strconv.Itoa(compressed.Len()) + `,"decoded":false}}}]`)
	capture, err := ReadCapture(data, "gzip.chlsj")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(capture.Flows[0].ResBody, plain) {
		t.Errorf("body = %x, want %s", capture.Flows[0].ResBody, plain)
	}
	if capture.Flows[0].ResHeader.Get("Content-Encoding") != "gzip" {
		t.Errorf("header = %q", capture.Flows[0].ResHeader.Get("Content-Encoding"))
	}

	invalid := base64.StdEncoding.EncodeToString([]byte("not gzip"))
	data = []byte(`[{"status":"COMPLETE","method":"GET","scheme":"https","host":"api.example.com","path":"/","request":{"header":{"headers":[]}},"response":{"status":200,"contentEncoding":"gzip","header":{"headers":[]},"body":{"encoded":"` + invalid + `","size":8}}}]`)
	capture, err = ReadCapture(data, "bad-gzip.chlsj")
	if err != nil {
		t.Fatal(err)
	}
	if string(capture.Flows[0].ResBody) != "not gzip" {
		t.Errorf("bad gzip body = %q", capture.Flows[0].ResBody)
	}
	if len(capture.Warnings) == 0 || !strings.Contains(strings.Join(capture.Warnings, ";"), "could not be decompressed") {
		t.Errorf("warnings = %q", capture.Warnings)
	}
}

func TestCharlesXMLCompressedBodyAndStatusFallback(t *testing.T) {
	plain := []byte("hello")
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(compressed.Bytes())
	data := []byte(`<charles-session><transaction status="COMPLETE" method="GET" protocol="https" host="api.example.com" actualPort="443" path="/" requestBeginTimeMillis="1000" responseTimeMillis="1001" endTimeMillis="1002"><request headers="0" body="0"><headers><first-line>GET / HTTP/1.1</first-line></headers></request><response headers="0" body="5" contentEncoding="gzip"><headers><first-line>HTTP/1.1 200 OK</first-line></headers><body encoding="base64">` + encoded + `</body></response></transaction></charles-session>`)
	capture, err := ReadCapture(data, "gzip.chlsx")
	if err != nil {
		t.Fatal(err)
	}
	if capture.Flows[0].Status != 200 || !bytes.Equal(capture.Flows[0].ResBody, plain) {
		t.Fatalf("flow = %+v body %q", capture.Flows[0], capture.Flows[0].ResBody)
	}
}

func TestReadProxymanDirDoesNotRecurseDeeperThanOneFolder(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c", "d")
	if err := os.MkdirAll(deep, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "request_0_1"), captureFixture(t, "proxyman-request_0_1001.json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadProxymanDir(root); err == nil {
		t.Fatal("a request more than one folder down was imported")
	}
}

func TestWebSocketFramesReassembleFragmentsAndRejectTruncation(t *testing.T) {
	frames, truncated, compressed := parseWebSocketFrames([]byte{0x01, 0x03, 'o', 'n', 'e', 0x80, 0x03, 't', 'w', 'o'}, "in", 100)
	if truncated || compressed || len(frames) != 1 || frames[0].Kind != "text" || string(frames[0].Data) != "onetwo" {
		t.Fatalf("fragmented frames = %+v truncated %v compressed %v", frames, truncated, compressed)
	}
	if _, truncated, _ := parseWebSocketFrames([]byte{0x81, 0x05, 'a'}, "in", 100); !truncated {
		t.Fatal("truncated frame was accepted")
	}
	if _, _, compressed := parseWebSocketFrames([]byte{0xc1, 0x00}, "in", 100); !compressed {
		t.Fatal("permessage-deflate frame was not reported")
	}
}

func TestCaptureTimingTotalCoversOverlappingPhases(t *testing.T) {
	start := time.Unix(1760000000, 0)
	requestEnd := start.Add(100 * time.Millisecond)
	responseStart := start.Add(60 * time.Millisecond)
	end := start.Add(80 * time.Millisecond)
	timing := proxymanTimingOf(start, requestEnd, responseStart, end, 60*time.Millisecond)
	if timing == nil || timing.Total != 100*time.Millisecond {
		t.Fatalf("timing = %+v", timing)
	}
	for _, phase := range timing.Phases {
		if phase.Start+phase.Duration > timing.Total {
			t.Fatalf("phase %s ends at %v beyond total %v", phase.Name, phase.Start+phase.Duration, timing.Total)
		}
	}
}

func TestCharlesXMLExceptionAndConnectTunnel(t *testing.T) {
	data := []byte(`<charles-session><transaction status="EXCEPTION" exception="SSLHandshakeException: Remote host closed connection during handshake" method="CONNECT" protocol="" host="httpbin.org" actualPort="443" path=""><request headers="0" body="0"><headers><first-line>CONNECT httpbin.org:443 HTTP/1.1</first-line></headers></request><response headers="0" body="0"><headers/></response></transaction></charles-session>`)
	capture, err := ReadCapture(data, "connect.chlsx")
	if err != nil {
		t.Fatal(err)
	}
	flow := capture.Flows[0]
	if flow.Kind != "connect" || flow.Method != "CONNECT" || flow.URL != "https://httpbin.org:443" || flow.Path != "" {
		t.Errorf("CONNECT = %+v", flow)
	}
	if !strings.Contains(flow.Error, "SSLHandshakeException") {
		t.Errorf("error = %q", flow.Error)
	}
}

func TestCharlesXMLHeaderListIsBounded(t *testing.T) {
	var headers strings.Builder
	for i := range 5000 {
		headers.WriteString("<header><name>X-")
		headers.WriteString(strconv.Itoa(i))
		headers.WriteString("</name><value>v</value></header>")
	}
	data := []byte(`<charles-session><transaction status="COMPLETE" method="GET" protocol="https" host="example.com" path="/"><request headers="0" body="0"><headers>` + headers.String() + `</headers></request><response status="200" headers="0" body="0"><headers/></response></transaction></charles-session>`)
	capture, err := ReadCapture(data, "headers.chlsx")
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Flows[0].ReqHeader) != maxCaptureHeaders {
		t.Fatalf("%d headers", len(capture.Flows[0].ReqHeader))
	}
	if len(capture.Warnings) == 0 || !strings.Contains(strings.Join(capture.Warnings, ";"), "headers beyond") {
		t.Fatalf("warnings = %q", capture.Warnings)
	}
}

func TestProxymanHeaderListIsBounded(t *testing.T) {
	var headers strings.Builder
	for i := range 5000 {
		if i > 0 {
			headers.WriteString(",")
		}
		headers.WriteString(`{"isEnabled":true,"key":{"name":"X-`)
		headers.WriteString(strconv.Itoa(i))
		headers.WriteString(`"},"value":"v"}`)
	}
	entry := []byte(`{"request":{"scheme":"https","host":"example.com","uri":"/","method":{"name":"GET"},"header":{"entries":[` + headers.String() + `]}},"response":{"status":{"code":200},"header":{"entries":[]}}}`)
	archive := proxymanArchive(t, map[string][]byte{"request_0_1": entry})
	capture, err := ReadCapture(archive, "headers.proxymanlogv2")
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Flows[0].ReqHeader) != maxCaptureHeaders {
		t.Fatalf("%d headers", len(capture.Flows[0].ReqHeader))
	}
	if len(capture.Warnings) == 0 || !strings.Contains(strings.Join(capture.Warnings, ";"), "headers beyond") {
		t.Fatalf("warnings = %q", capture.Warnings)
	}
}

func TestProxymanZipEntryListIsBounded(t *testing.T) {
	files := make(map[string][]byte, maxCaptureEntries+5)
	for i := range maxCaptureEntries + 5 {
		files["request_"+strconv.Itoa(i)+"_1"] = []byte(`{"request":{"scheme":"https","host":"example.com","uri":"/` + strconv.Itoa(i) + `","method":{"name":"GET"}},"response":{"status":{"code":200}}}`)
	}
	capture, err := ReadCapture(proxymanArchive(t, files), "many.proxymanlogv2")
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Flows) != maxCaptureEntries {
		t.Fatalf("%d flows", len(capture.Flows))
	}
	if capture.Flows[0].Path != "/0" || capture.Flows[len(capture.Flows)-1].Path != "/1999" {
		t.Fatalf("first/last paths = %q/%q; archive member order was not sorted by index", capture.Flows[0].Path, capture.Flows[len(capture.Flows)-1].Path)
	}
	if len(capture.Warnings) == 0 || !strings.Contains(capture.Warnings[0], "5 entries beyond") {
		t.Fatalf("warnings = %q", capture.Warnings)
	}
}

func TestProxymanOutOfOrderTimingRaisesTotal(t *testing.T) {
	start, requestEnd, responseStart, end := 1760000000.0, 1760000000.1, 1760000000.06, 1760000000.08
	entry := proxymanEntry{
		Request:  &proxymanRequest{Scheme: "https", Host: "api.example.com", URI: "/", Method: proxymanMethod{Name: "POST"}},
		Response: &proxymanResponse{Status: &proxymanStatus{Code: 413}},
		Timing:   proxymanTiming{RequestStartedAt: &start, RequestEndedAt: &requestEnd, ResponseStartedAt: &responseStart, ResponseEndedAt: &end},
	}
	flow, err := entry.toCapture(&captureWarnings{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flow.Timing == nil || flow.Timing.Total != 100*time.Millisecond {
		t.Fatalf("timing = %+v", flow.Timing)
	}
}

func brotliZerosBase64(t *testing.T, size int) (string, int) {
	t.Helper()
	var compressed bytes.Buffer
	writer := brotli.NewWriter(&compressed)
	if _, err := writer.Write(make([]byte, size)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(compressed.Bytes()), compressed.Len()
}

func TestCaptureBodyBudgetIsShared(t *testing.T) {
	warnings := &captureWarnings{budget: captureBudget{maxBodyBytes: 2 << 20}}
	body := &charlesJSONEntry{
		Body: &charlesJSONBody{Encoded: base64.StdEncoding.EncodeToString(make([]byte, 1<<20)), Size: flexInt(1 << 20)},
	}
	for i := range 3 {
		data, _ := charlesBody(body, "response", warnings, i)
		if i < 2 && len(data) != 1<<20 {
			t.Fatalf("body %d = %d bytes, want 1 MiB", i, len(data))
		}
		if i == 2 && len(data) != 0 {
			t.Fatalf("third body retained %d bytes after the 2 MiB budget", len(data))
		}
	}
	if !strings.Contains(strings.Join(warnings.finish(), ";"), "body budget") {
		t.Fatalf("warnings = %q", warnings.finish())
	}
}

func TestWebSocketFrameBudgetIsShared(t *testing.T) {
	warnings := &captureWarnings{budget: captureBudget{maxFrames: 10}}
	frames, _ := charlesWebSocketFrames(bytes.Repeat([]byte{0x81, 0x00}, 20), nil, true, false, warnings, 0)
	if len(frames) != 10 {
		t.Fatalf("%d frames, want 10", len(frames))
	}
	if !strings.Contains(strings.Join(warnings.finish(), ";"), "WebSocket message budget") {
		t.Fatalf("warnings = %q", warnings.finish())
	}
}

func TestBrotliBodiesRespectImportBudget(t *testing.T) {
	encoded, compressedSize := brotliZerosBase64(t, 31_999_999)
	warnings := &captureWarnings{budget: captureBudget{maxBodyBytes: 64 << 20}}
	body := &charlesJSONEntry{
		ContentEncoding: "br",
		Body:            &charlesJSONBody{Encoded: encoded, Size: flexInt(31_999_999)},
	}
	for i := range 3 {
		data, _ := charlesBody(body, "response", warnings, i)
		if i < 2 && len(data) != 31_999_999 {
			t.Fatalf("body %d = %d bytes, want 31,999,999", i, len(data))
		}
		if i == 2 && len(data) != 0 {
			t.Fatalf("third brotli body retained %d bytes after the 64 MiB budget", len(data))
		}
	}
	if compressedSize >= 1<<20 {
		t.Fatalf("brotli fixture is %d bytes, expected a small compression bomb", compressedSize)
	}
	if !strings.Contains(strings.Join(warnings.finish(), ";"), "body budget") {
		t.Fatalf("warnings = %q", warnings.finish())
	}
}

func TestCharlesXMLNestingIsRejectedBeforeDecoding(t *testing.T) {
	deep := strings.Repeat("<a>", 2_000_000) + strings.Repeat("</a>", 2_000_000)
	input := []byte("<charles-session>" + deep + `<transaction status="COMPLETE" method="GET" protocol="https" host="example.com" path="/"><request headers="0" body="0"/><response status="200" headers="0" body="0"/></transaction></charles-session>`)
	allocated := captureAllocated(t, func() {
		_, err := ReadCapture(input, "deep.chlsx")
		if err == nil || !strings.Contains(err.Error(), "nesting") {
			t.Fatalf("err = %v, want a nesting error", err)
		}
	})
	if allocated > 64<<20 {
		t.Fatalf("deep XML allocated %d MiB before rejection; want under 64 MiB", allocated>>20)
	}
}

func TestCharlesTruncationWarningComesFirst(t *testing.T) {
	var data strings.Builder
	data.WriteString("[")
	for i := range 260_000 {
		if i > 0 {
			data.WriteString(",")
		}
		data.WriteString("{}")
	}
	data.WriteString("]")
	capture, err := ReadCapture([]byte(data.String()), "many.chlsj")
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Warnings) == 0 || !strings.Contains(capture.Warnings[0], "transactions beyond") {
		t.Fatalf("warnings = %q", capture.Warnings)
	}
}

func TestCharlesMissingTotalIncludesSetup(t *testing.T) {
	data := []byte(`[{"status":"COMPLETE","method":"GET","scheme":"https","host":"api.example.com","path":"/","times":{"start":"2026-10-01T10:00:00.000Z","requestBegin":"2026-10-01T10:00:00.100Z","requestComplete":"2026-10-01T10:00:00.100Z","responseBegin":"2026-10-01T10:00:00.150Z","end":"2026-10-01T10:00:00.172Z"},"durations":{"dns":10,"connect":20,"ssl":30,"latency":50,"response":22},"request":{"header":{"headers":[]}},"response":{"status":200,"header":{"headers":[]}}}]`)
	capture, err := ReadCapture(data, "no-total.chlsj")
	if err != nil {
		t.Fatal(err)
	}
	flow := capture.Flows[0]
	if flow.Millis != 132 || flow.Timing == nil || flow.Timing.Total != 132*time.Millisecond {
		t.Fatalf("millis = %d timing = %+v, want 132 ms", flow.Millis, flow.Timing)
	}
}

func TestCharlesXMLMissingTotalIncludesSetup(t *testing.T) {
	data := []byte(`<charles-session><transaction status="COMPLETE" method="GET" protocol="https" host="api.example.com" actualPort="443" path="/" requestBeginTimeMillis="1000" requestTimeMillis="1000" responseTimeMillis="1050" endTimeMillis="1072" dnsDuration="10" connectDuration="20" sslDuration="30" latency="50" responseDuration="22"><request headers="0" body="0"><headers/></request><response status="200" headers="0" body="0"><headers/></response></transaction></charles-session>`)
	capture, err := ReadCapture(data, "no-total.chlsx")
	if err != nil {
		t.Fatal(err)
	}
	flow := capture.Flows[0]
	if flow.Millis != 132 || flow.Timing == nil || flow.Timing.Total != 132*time.Millisecond {
		t.Fatalf("millis = %d timing = %+v, want 132 ms", flow.Millis, flow.Timing)
	}
}

func TestCharlesWebSocketRSV1Warns(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte{0xc1, 0x00})
	data := []byte(`[{"status":"COMPLETE","method":"GET","scheme":"https","host":"ws.example.com","path":"/","webSocket":true,"request":{"header":{"headers":[]}},"response":{"status":101,"header":{"headers":[]},"body":{"encoded":"` + encoded + `","size":2}}}]`)
	capture, err := ReadCapture(data, "deflate.chlsj")
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Flows[0].Frames) != 0 {
		t.Fatalf("compressed frame produced %d messages", len(capture.Flows[0].Frames))
	}
	if !strings.Contains(strings.Join(capture.Warnings, ";"), "permessage-deflate") {
		t.Fatalf("warnings = %q", capture.Warnings)
	}
}

func TestCharlesImportBoundsWebSocketFramesAcrossTransactions(t *testing.T) {
	frameBytes := bytes.Repeat([]byte{0x81, 0x00}, 100_000)
	encoded := base64.StdEncoding.EncodeToString(frameBytes)
	var data strings.Builder
	data.WriteString("[")
	for i := range 20 {
		if i > 0 {
			data.WriteString(",")
		}
		data.WriteString(`{"status":"COMPLETE","method":"GET","scheme":"https","host":"ws.example.com","path":"/","webSocket":true,"request":{"header":{"headers":[]}},"response":{"status":101,"header":{"headers":[]},"body":{"encoded":"`)
		data.WriteString(encoded)
		data.WriteString(`","size":200000}}}`)
	}
	data.WriteString("]")
	var capture Capture
	allocated := captureAllocated(t, func() {
		var err error
		capture, err = ReadCapture([]byte(data.String()), "frames.chlsj")
		if err != nil {
			t.Fatal(err)
		}
	})
	total := 0
	for _, flow := range capture.Flows {
		total += len(flow.Frames)
	}
	if total != maxCaptureImportFrames {
		t.Fatalf("%d frames across 20 transactions, want %d", total, maxCaptureImportFrames)
	}
	if allocated > 256<<20 {
		t.Fatalf("frame import allocated %d MiB; want under 256 MiB", allocated>>20)
	}
	if len(capture.Warnings) == 0 || !strings.Contains(capture.Warnings[0], "WebSocket message budget") {
		t.Fatalf("warnings = %q", capture.Warnings)
	}
}

func TestCharlesImportBoundsBrotliBodiesAcrossTransactions(t *testing.T) {
	if raceEnabled {
		t.Skip("the 512 MiB brotli budget test exceeds race-detector memory")
	}
	encoded, _ := brotliZerosBase64(t, 31_999_999)
	var data strings.Builder
	data.WriteString("[")
	for i := range 17 {
		if i > 0 {
			data.WriteString(",")
		}
		data.WriteString(`{"status":"COMPLETE","method":"GET","scheme":"https","host":"api.example.com","path":"/","request":{"header":{"headers":[]}},"response":{"status":200,"contentEncoding":"br","header":{"headers":[]},"body":{"encoded":"`)
		data.WriteString(encoded)
		data.WriteString(`","size":31999999,"decoded":false}}}`)
	}
	data.WriteString("]")
	var capture Capture
	allocated := captureAllocated(t, func() {
		var err error
		capture, err = ReadCapture([]byte(data.String()), "brotli.chlsj")
		if err != nil {
			t.Fatal(err)
		}
	})
	var retained int
	for _, flow := range capture.Flows {
		retained += len(flow.ResBody)
	}
	if retained > maxCaptureImportBytes {
		t.Fatalf("retained %d MiB of bodies, budget is %d MiB", retained>>20, maxCaptureImportBytes>>20)
	}
	if retained == 0 {
		t.Fatal("no brotli body was retained")
	}
	if allocated > 3_500<<20 {
		t.Fatalf("brotli import allocated %d MiB; want under 3500 MiB", allocated>>20)
	}
	if len(capture.Warnings) == 0 || !strings.Contains(capture.Warnings[0], "body budget") {
		t.Fatalf("warnings = %q", capture.Warnings)
	}
}

func TestWebSocketFrameBytesShareBodyBudget(t *testing.T) {
	warnings := &captureWarnings{budget: captureBudget{maxBodyBytes: 3}}
	storage := &proxymanWS{Messages: []proxymanWSMessage{{Data: "ab"}, {Data: "cd"}}}
	frames := proxymanFrames(storage, time.Time{}, warnings, 0)
	if len(frames) != 1 || string(frames[0].Data) != "ab" {
		t.Fatalf("frames = %+v, want only the first 2 bytes", frames)
	}
	if !strings.Contains(strings.Join(warnings.finish(), ";"), "body budget") {
		t.Fatalf("warnings = %q", warnings.finish())
	}
}

func TestCharlesXMLNestingInsideTransactionIsRejected(t *testing.T) {
	nested := strings.Repeat("<a>", 100) + strings.Repeat("</a>", 100)
	data := []byte(`<charles-session><transaction status="COMPLETE" method="GET" protocol="https" host="example.com" path="/"><request headers="0" body="0"><headers>` + nested + `</headers></request><response status="200" headers="0" body="0"><headers/></response></transaction></charles-session>`)
	if _, err := ReadCapture(data, "deep.chlsx"); err == nil || !strings.Contains(err.Error(), "nesting") {
		t.Fatalf("err = %v, want a nesting error", err)
	}
}

func TestProxymanFrameCountBudgetIsShared(t *testing.T) {
	warnings := &captureWarnings{budget: captureBudget{maxFrames: 1}}
	storage := &proxymanWS{Messages: []proxymanWSMessage{{Data: "one"}, {Data: "two"}}}
	frames := proxymanFrames(storage, time.Time{}, warnings, 0)
	if len(frames) != 1 || string(frames[0].Data) != "one" {
		t.Fatalf("frames = %+v", frames)
	}
	if !strings.Contains(strings.Join(warnings.finish(), ";"), "WebSocket message budget") {
		t.Fatalf("warnings = %q", warnings.finish())
	}
}

func TestBrotliBodiesStopAfterBodyBudget(t *testing.T) {
	encoded, _ := brotliZerosBase64(t, 31_999_999)
	warnings := &captureWarnings{budget: captureBudget{maxBodyBytes: 64 << 20}}
	body := &charlesJSONEntry{
		ContentEncoding: "br",
		Body:            &charlesJSONBody{Encoded: encoded, Size: flexInt(31_999_999)},
	}
	for i := range 2000 {
		data, _ := charlesBody(body, "response", warnings, i)
		if i < 2 && len(data) != 31_999_999 {
			t.Fatalf("body %d = %d bytes, want 31,999,999", i, len(data))
		}
		if i >= 2 && len(data) != 0 {
			t.Fatalf("body %d retained %d bytes after the body budget", i, len(data))
		}
	}
	if warnings.budget.decompressions > 3 {
		t.Fatalf("%d brotli decoders were created, want at most 3", warnings.budget.decompressions)
	}
	if !strings.Contains(strings.Join(warnings.finish(), ";"), "body budget") {
		t.Fatalf("warnings = %q", warnings.finish())
	}
}

func TestBrotliOversizedBodiesStopAfterWorkBudget(t *testing.T) {
	if raceEnabled {
		t.Skip("the decompression work budget test is too memory-heavy under -race")
	}
	encoded, _ := brotliZerosBase64(t, 64<<20)
	warnings := &captureWarnings{}
	body := &charlesJSONEntry{
		ContentEncoding: "br",
		Body:            &charlesJSONBody{Encoded: encoded, Size: flexInt(64 << 20)},
	}
	for i := range 200 {
		if data, _ := charlesBody(body, "response", warnings, i); len(data) != 0 {
			t.Fatalf("body %d retained %d bytes after the 32 MiB per-body limit", i, len(data))
		}
	}
	if warnings.budget.decompressions > 40 {
		t.Fatalf("%d decompressions, want at most 40", warnings.budget.decompressions)
	}
	if !strings.Contains(strings.Join(warnings.finish(), ";"), "decompression budget") {
		t.Fatalf("warnings = %q", warnings.finish())
	}
}

func TestWebSocketRSV1SkipsOnlyTheCompressedMessage(t *testing.T) {
	data := []byte{
		0x81, 0x01, 'a',
		0xc1, 0x01, 'x',
		0x81, 0x01, 'b',
	}
	frames, truncated, compressed := parseWebSocketFrames(data, "in", 100)
	if truncated || !compressed || len(frames) != 2 || string(frames[0].Data) != "a" || string(frames[1].Data) != "b" {
		t.Fatalf("frames = %+v truncated %v compressed %v", frames, truncated, compressed)
	}

	withPing := []byte{
		0xc1, 0x01, 'x',
		0x89, 0x00,
		0x81, 0x01, 'b',
	}
	frames, truncated, compressed = parseWebSocketFrames(withPing, "in", 100)
	if truncated || !compressed || len(frames) != 2 || frames[0].Kind != "ping" || string(frames[1].Data) != "b" {
		t.Fatalf("frames with ping = %+v truncated %v compressed %v", frames, truncated, compressed)
	}

	fragmented := []byte{
		0x41, 0x01, 'x',
		0x80, 0x01, 'y',
		0x81, 0x01, 'b',
	}
	frames, truncated, compressed = parseWebSocketFrames(fragmented, "in", 100)
	if truncated || !compressed || len(frames) != 1 || string(frames[0].Data) != "b" {
		t.Fatalf("fragmented frames = %+v truncated %v compressed %v", frames, truncated, compressed)
	}
}

func TestCharlesWebSocketRawBodyIsNotDoubleCounted(t *testing.T) {
	payload := bytes.Repeat([]byte{0x01}, 600_000)
	frame := append([]byte{0x82, 0x7f, 0, 0, 0, 0, 0, 0x09, 0x27, 0xc0}, payload...)
	encoded := base64.StdEncoding.EncodeToString(frame)
	transaction := charlesJSONTransaction{
		Status: "COMPLETE", Method: "GET", Scheme: "https", Host: "ws.example.com", Path: "/", WebSocket: true,
		Request: &charlesJSONEntry{Header: charlesJSONHeaders{}},
		Response: &charlesJSONEntry{
			Status: 101,
			Header: charlesJSONHeaders{},
			Body:   &charlesJSONBody{Encoded: encoded, Size: flexInt(len(frame))},
		},
	}
	warnings := &captureWarnings{budget: captureBudget{maxBodyBytes: 1 << 20}}
	flow, err := transaction.toCapture(warnings, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(flow.Frames) != 1 || len(flow.Frames[0].Data) != len(payload) {
		t.Fatalf("frames = %d, want one 600000-byte frame; warnings %q", len(flow.Frames), warnings.finish())
	}
	if strings.Contains(strings.Join(warnings.finish(), ";"), "body budget") {
		t.Fatalf("raw WebSocket body was charged twice: %q", warnings.finish())
	}
}
