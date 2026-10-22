package main

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// pbVarint writes a varint field.
func pbVarint(number int, value uint64) []byte {
	out := binary.AppendUvarint(nil, uint64(number)<<3)
	return binary.AppendUvarint(out, value)
}

// pbBytes writes a length-delimited field.
func pbBytes(number int, data []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(number)<<3|2)
	out = binary.AppendUvarint(out, uint64(len(data)))
	return append(out, data...)
}

// pbMessage is what a gRPC frame carries: a flag and the length, then the
// message.
func pbMessage(data []byte) []byte {
	out := []byte{0}
	out = binary.BigEndian.AppendUint32(out, uint32(len(data)))
	return append(out, data...)
}

func TestDecodeProtoWithoutASchema(t *testing.T) {
	// {1: 7, 2: "vscroxy", 3: {1: "id", 2: 42}, 4: "x"}
	nested := append(pbBytes(1, []byte("id")), pbVarint(2, 42)...)
	body := pbVarint(1, 7)
	body = append(body, pbBytes(2, []byte("vscroxy"))...)
	body = append(body, pbBytes(3, nested)...)
	body = append(body, pbBytes(4, []byte("x"))...)

	fields, err := decodeProto(body)
	if err != nil {
		t.Fatal("the message did not read as protobuf")
	}
	text := protoText(fields)
	for _, want := range []string{
		"1: 7",
		`2: "vscroxy"`,
		"3 {",
		`  1: "id"`,
		"  2: 42",
		`4: "x"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("decoded text misses %q:\n%s", want, text)
		}
	}
	// A string stays a string; a message stays a message.
	if fields[1].Nested != nil || fields[1].Text != "vscroxy" {
		t.Errorf("field 2 = %+v, want the string", fields[1])
	}
	if len(fields[2].Nested) != 2 {
		t.Errorf("field 3 = %+v, want the nested message", fields[2])
	}
}

func TestProtoBodyReadsAGrpcFrame(t *testing.T) {
	message := append(pbVarint(1, 1), pbBytes(2, []byte("owner"))...)
	f := flow{resBody: string(pbMessage(message)), respHeader: http.Header{"Content-Type": {"application/grpc"}}}
	r := inspectProto(t.Context(), f, true, nil, "")
	if r.problem != "" || len(r.messages) != 1 || !strings.Contains(r.messages[0].text, `2: "owner"`) {
		t.Fatalf("a gRPC frame did not read as protobuf: %+v", r)
	}
	// A frame whose length runs past the body is not a frame: its header must
	// not be read as a message either.
	f.resBody = string(append([]byte{0, 0, 0, 0, 200}, message...))
	if r := inspectProto(t.Context(), f, true, nil, ""); r.problem == "" {
		t.Error("a frame with a wrong length was accepted")
	}
}

func TestProtoBodyRejectsWhatItCannotRead(t *testing.T) {
	for _, body := range [][]byte{
		{},
		{0x08},                   // a key with no value
		[]byte("GET / HTTP/1.1"), // text
		{0x0a, 0x05, 0x01, 0x02}, // a length past the body
	} {
		if _, err := decodeProto(body); err == nil {
			t.Errorf("%q read as protobuf, want it rejected", body)
		}
	}
}

func protoFixture(t *testing.T) (string, *protoSchema, []byte) {
	t.Helper()
	dir := t.TempDir()
	source := `syntax = "proto3"; package api;
import "common.proto";
import "google/protobuf/timestamp.proto";
message Input { string query = 1; }
message Output {
  string title = 1;
  sint32 delta = 2;
  repeated int32 counts = 3;
  common.State state = 4;
  map<string, string> attrs = 5;
  oneof choice { string label = 6; int64 id = 7; }
  bytes raw = 8;
  google.protobuf.Timestamp created_at = 9;
}
service Catalog { rpc Fetch(Input) returns (stream Output); }`
	path := filepath.Join(dir, "catalog.proto")
	for name, data := range map[string]string{
		"catalog.proto": source,
		"common.proto":  `syntax = "proto3"; package common; enum State { UNKNOWN = 0; ACTIVE = 1; }`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	schema, data, err := importProtoSchema(t.Context(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	return path, schema, data
}

func TestDescriptorAwareMessagesAndSchemaRoundTrip(t *testing.T) {
	_, _, data := protoFixture(t)
	path := filepath.Join(t.TempDir(), "schema.protoset")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	schema, _, err := importProtoSchema(t.Context(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Hand-built wire values distinguish signed zigzag, packed repeated ints,
	// enum names, map entries, oneof and unknown fields from schema-less guesses.
	message := append(pbBytes(1, []byte("alpha")), pbVarint(2, 13)...)
	message = append(message, pbBytes(3, []byte{3, 9})...)
	message = append(message, pbVarint(4, 1)...)
	entry := append(pbBytes(1, []byte("tier")), pbBytes(2, []byte("dev"))...)
	message = append(message, pbBytes(5, entry)...)
	message = append(message, pbBytes(6, []byte("chosen"))...)
	message = append(message, pbBytes(8, []byte{0, 255})...)
	message = append(message, pbVarint(99, 2)...)
	f := flow{path: "/api.Catalog/Fetch", reqBody: string(pbMessage(pbBytes(1, []byte("find")))), resBody: string(pbMessage(message)), reqHeader: http.Header{"Content-Type": {"application/grpc+proto"}}, respHeader: http.Header{"Content-Type": {"application/grpc+proto"}}}
	r := inspectProto(t.Context(), f, true, schema, "Auto")
	if r.problem != "" || r.typeName != "api.Output" || len(r.messages) != 1 {
		t.Fatalf("response type: %+v", r)
	}
	text := strings.Join(strings.Fields(r.messages[0].text), " ")
	for _, want := range []string{`title: "alpha"`, "delta: -7", "counts: 3", "counts: 9", "state: ACTIVE", `key: "tier"`, `value: "dev"`, `label: "chosen"`, "raw:", "99: 2"} {
		if !strings.Contains(text, want) {
			t.Errorf("decoded message misses %q: %s", want, text)
		}
	}
	r = inspectProto(t.Context(), f, false, schema, "Auto")
	if r.problem != "" || r.typeName != "api.Input" || !strings.Contains(r.messages[0].text, "query:") {
		t.Fatalf("request must use the input descriptor: %+v", r)
	}
	f.resBody, f.respHeader = string(message), http.Header{"Content-Type": {"application/x-protobuf"}}
	r = inspectProto(t.Context(), f, true, schema, "api.Output")
	if r.problem != "" || r.typeName != "api.Output" || !strings.Contains(r.messages[0].text, "delta:") {
		t.Fatalf("explicit raw protobuf type: %+v", r)
	}
	if _, err := parseProtoSchema([]byte("not descriptors")); err == nil {
		t.Fatal("invalid descriptor set was accepted")
	}
}

func TestGRPCMultipleMessagesCompressionAndFramingErrors(t *testing.T) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	zw.Write(pbBytes(1, []byte("compressed second")))
	zw.Close()
	zipped := pbMessage(compressed.Bytes())
	zipped[0] = 1
	body := append(pbMessage(pbVarint(1, 7)), zipped...)
	body = append(body, pbMessage(nil)...)
	f := flow{resBody: string(body), respHeader: http.Header{"Content-Type": {"application/grpc"}, "Grpc-Encoding": {"gzip"}}}
	r := inspectProto(t.Context(), f, true, nil, "")
	if r.problem != "" || len(r.messages) != 3 || !r.messages[1].compressed || !strings.Contains(r.messages[1].text, "compressed second") || r.messages[2].text != "(empty message)" {
		t.Fatalf("all frames, including gzip and empty: %+v", r)
	}
	for _, test := range []struct {
		name     string
		data     []byte
		encoding string
		want     string
	}{
		{"header", []byte{0, 0, 0}, "", "header"},
		{"payload", []byte{0, 0, 0, 0, 2, 8}, "", "incomplete"},
		{"flag", []byte{2, 0, 0, 0, 0}, "", "invalid flag"},
		{"encoding", zipped, "snappy", "unsupported grpc-encoding"},
		{"gzip", []byte{1, 0, 0, 0, 1, 0}, "gzip", "invalid gzip"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f.resBody = string(append(pbMessage(pbVarint(1, 3)), test.data...))
			f.respHeader.Set("Grpc-Encoding", test.encoding)
			r := inspectProto(t.Context(), f, true, nil, "")
			if len(r.messages) != 1 || !strings.Contains(r.problem, "Message 2:") || !strings.Contains(r.problem, test.want) {
				t.Fatalf("must retain the valid first message and identify the broken second: %+v", r)
			}
		})
	}
}

func TestProtoInspectionReadsPastBodyPreview(t *testing.T) {
	body := pbMessage(pbBytes(1, bytes.Repeat([]byte("x"), 9<<20)))
	body = append(body, pbMessage(pbBytes(2, []byte("tail-only")))...)
	path := filepath.Join(t.TempDir(), "body.bin")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	f := flow{resBody: string(body[:64<<10]), resFile: path, resSize: len(body), respHeader: http.Header{"Content-Type": {"application/grpc"}}}
	r := inspectProto(t.Context(), f, true, nil, "")
	if r.problem != "" || len(r.messages) != 2 || !strings.Contains(r.messages[1].text, "tail-only") {
		t.Fatal("inspection used a preview instead of the complete spilled frames")
	}
	f.resError = "client aborted"
	if r := inspectProto(t.Context(), f, true, nil, ""); !strings.Contains(r.problem, "Incomplete") {
		t.Fatal("partial captures must not decode as complete")
	}
}

func TestGRPCStatusAndTrailersSurviveSession(t *testing.T) {
	f := flow{id: 1, status: 200, respHeader: http.Header{"Content-Type": {"application/grpc"}, "Grpc-Status": {"0"}}, resTrailer: http.Header{"Grpc-Status": {"7"}, "Grpc-Message": {"denied%20a+b%25"}, "Grpc-Status-Details-Bin": {"AAH/"}, "X-Trace": {"one", "two"}}, reqTrailer: http.Header{"X-Request-Trailer": {"request"}}, resProto: "HTTP/2.0", conn: conn{proto: "HTTP/2.0"}}
	path := filepath.Join(t.TempDir(), "session.json")
	if err := writeSession(path, []flow{f}, nil); err != nil {
		t.Fatal(err)
	}
	flows, _, err := readSession(path)
	if err != nil {
		t.Fatal(err)
	}
	got := flows[0]
	if grpcStatus(got) != "gRPC 7 · PermissionDenied · denied a+b%" || got.resProto != "HTTP/2.0" || got.conn.proto != "HTTP/2.0" || len(got.resTrailer.Values("X-Trace")) != 2 || got.reqTrailer.Get("X-Request-Trailer") != "request" || got.resTrailer.Get("Grpc-Status-Details-Bin") != "AAH/" {
		t.Fatalf("lost trailers/status/protocol: %+v", got)
	}
	got.resTrailer, got.respHeader = nil, nil
	if !strings.Contains(grpcStatus(got), "missing") {
		t.Fatal("HTTP 200 must not stand in for grpc-status OK")
	}
}

func TestCaptureErrorsIncludeGRPCFailures(t *testing.T) {
	a := workspaceApp()
	a.flows = []flow{
		{id: 1, host: "api.test", path: "/successful", status: 200, respHeader: http.Header{"Content-Type": {"application/grpc"}}, resTrailer: http.Header{"Grpc-Status": {"0"}}},
		{id: 2, host: "api.test", path: "/trailer-failure", status: 200, respHeader: http.Header{"Content-Type": {"application/grpc"}, "Grpc-Status": {"0"}}, resTrailer: http.Header{"Grpc-Status": {"7"}}},
		{id: 3, host: "api.test", path: "/trailers-only-failure", status: 200, respHeader: http.Header{"Content-Type": {"application/grpc+proto"}, "Grpc-Status": {"5"}}},
		{id: 4, host: "api.test", path: "/not-grpc", status: 200, respHeader: http.Header{"Content-Type": {"application/json"}, "Grpc-Status": {"7"}}},
		{id: 5, host: "api.test", path: "/http-failure", status: 503},
	}
	a.applyFilter()
	tt := ui.NewTester(a.view, 1100, 700)
	if err := tt.Click("Errors"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/trailer-failure", "/trailers-only-failure", "/http-failure"} {
		if !tt.HasText("api.test" + path) {
			t.Errorf("Errors omitted %s", path)
		}
	}
	if tt.HasText("api.test/successful") || tt.HasText("api.test/not-grpc") || !tt.HasText("3 shown") {
		t.Fatal("Errors must retain only actual HTTP/gRPC failures")
	}
}

func TestProtobufInspectorLoadsSchemaAndSelectsMessages(t *testing.T) {
	path, _, _ := protoFixture(t)
	a := workspaceApp()
	f := flow{id: 4, method: "POST", scheme: "https", host: "api.example.com", path: "/api.Catalog/Fetch", status: 200, reqBody: string(pbMessage(pbBytes(1, []byte("find")))), resBody: string(append(pbMessage(pbBytes(1, []byte("alpha"))), pbMessage(pbBytes(1, []byte("beta")))...)), reqHeader: http.Header{"Content-Type": {"application/grpc"}}, respHeader: http.Header{"Content-Type": {"application/grpc"}}, resTrailer: http.Header{"Grpc-Status": {"0"}}}
	a.flows = []flow{f}
	a.applyFilter()
	t.Cleanup(a.protobuf.stop)
	a.protobuf.store = filepath.Join(t.TempDir(), "protobuf.pb")
	tt := ui.NewTester(a.view, 1240, 850)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(text string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !tt.HasText(text) && time.Now().Before(deadline) {
			tt.Frame()
			time.Sleep(time.Millisecond)
		}
		if !tt.HasText(text) {
			t.Fatalf("missing %q: %v", text, tt.Texts())
		}
	}
	click("Body")
	wait("2 of 2 messages")
	click("Schema…")
	click("Schema file")
	tt.Type(path)
	click("Load schema")
	wait("Loaded: 3 message types") // Input, Output and imported Timestamp.
	click("Close")
	wait("Message type: api.Output")
	click("Message 2")
	click("Copy decoded")
	if text := strings.Join(strings.Fields(tt.Clipboard()), " "); text != `title: "beta"` {
		t.Fatalf("copied selected named message: %q", text)
	}
	click("Search protobuf messages")
	tt.Type("alpha")
	if !tt.HasText("1 of 2 messages") || !tt.HasText("alpha") {
		t.Fatal("search must keep the matching message and safely remap selection")
	}
	click("Request")
	wait("Message type: api.Input")
	click("Copy decoded")
	if !strings.Contains(tt.Clipboard(), "query:") || !strings.Contains(tt.Clipboard(), "find") {
		t.Fatal("request side used the response descriptor")
	}
	// Opening another session may replace a flow without changing its ID.
	// The old async result must not stay cached under that reused ID.
	a.flows[0].reqBody = string(pbMessage(pbBytes(1, []byte("replacement"))))
	a.applyFilter()
	tt.Frame()
	wait("query:")
	click("Copy decoded")
	if !strings.Contains(tt.Clipboard(), "replacement") {
		t.Fatalf("a replaced flow displayed stale decoded data: %q", tt.Clipboard())
	}
	bad := filepath.Join(t.TempDir(), "broken.pb")
	if err := os.WriteFile(bad, []byte("not descriptors"), 0o600); err != nil {
		t.Fatal(err)
	}
	click("Schema…")
	click("Schema file")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type(bad)
	click("Load schema")
	wait("read FileDescriptorSet")
	click("Close")
	click("Copy decoded")
	if !strings.Contains(tt.Clipboard(), "query:") || !strings.Contains(tt.Clipboard(), "replacement") {
		t.Fatal("a failed import discarded the previous schema")
	}
	var restored protoInspector
	restored.init(a.protobuf.store)
	if restored.schema == nil {
		t.Fatal("loaded descriptors did not survive restart")
	}
}
