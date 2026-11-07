package imports

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
	if len(capture.Warnings) != 1 || !strings.Contains(capture.Warnings[0], "WebSocket message frames") {
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
		{"Connection", 0, 5 * time.Millisecond},
		{"Send", 5 * time.Millisecond, 5 * time.Millisecond},
		{"Wait", 10 * time.Millisecond, 30 * time.Millisecond},
		{"Receive", 40 * time.Millisecond, 5 * time.Millisecond},
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
		websocket.Status != 101 || len(websocket.Frames) != 0 {
		t.Errorf("WebSocket = %+v", websocket)
	}
	failed := capture.Flows[3]
	if failed.Status != 0 || !strings.Contains(failed.Error, "FAILED") {
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
	if len(capture.Warnings) != 1 || !strings.Contains(capture.Warnings[0], "WebSocket message frames") {
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
	if flow.TLS != "TLSv1.3" || flow.Cipher != "TLS_AES_128_GCM_SHA256" || flow.Remote != "203.0.113.8" {
		t.Errorf("TLS/remote = %+v", flow)
	}
	if flow.Timing == nil || flow.Timing.Total != 45*time.Millisecond {
		t.Fatalf("timing = %+v", flow.Timing)
	}
	upload := capture.Flows[1]
	if !bytes.Equal(upload.ReqBody, []byte{0x00, 0xff, 0xfe, 0x01}) || string(upload.ResBody) != "ok" || upload.Status != 201 {
		t.Errorf("upload = %+v body %x/%q", upload, upload.ReqBody, upload.ResBody)
	}
	websocket := capture.Flows[2]
	if websocket.Kind != "ws" || websocket.Scheme != "wss" || websocket.URL != "wss://ws.example.com/socket" || websocket.Status != 101 {
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
		"empty":     nil,
		"truncated": truncated,
		"xml":       xmlBroken,
		"random":    []byte("not a capture"),
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
