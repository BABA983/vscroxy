package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/egoist/mygo/ui"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const grpcFixtureSource = `syntax = "proto3";
package demo;
import "google/protobuf/wrappers.proto";
service Echo {
  rpc Unary(google.protobuf.StringValue) returns (google.protobuf.StringValue);
  rpc Server(google.protobuf.StringValue) returns (stream google.protobuf.StringValue);
  rpc Client(stream google.protobuf.StringValue) returns (google.protobuf.StringValue);
  rpc Bidi(stream google.protobuf.StringValue) returns (stream google.protobuf.StringValue);
  rpc Wait(google.protobuf.StringValue) returns (stream google.protobuf.StringValue);
  rpc Fail(google.protobuf.StringValue) returns (google.protobuf.StringValue);
}`

func grpcFixture(t *testing.T) (string, *protoSchema, []byte, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "echo.proto")
	if err := store.WriteFile(path, []byte(grpcFixtureSource)); err != nil {
		t.Fatal(err)
	}
	schema, data, err := importProtoSchema(t.Context(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := grpc.NewServer()
	desc := &grpc.ServiceDesc{ServiceName: "demo.Echo", HandlerType: (*interface{})(nil), Metadata: "echo.proto"}
	for _, name := range []string{"Unary", "Server", "Client", "Bidi", "Wait", "Fail"} {
		desc.Streams = append(desc.Streams, grpc.StreamDesc{StreamName: name, ClientStreams: name == "Client" || name == "Bidi", ServerStreams: name == "Server" || name == "Bidi" || name == "Wait", Handler: func(_ any, stream grpc.ServerStream) error {
			md, _ := metadata.FromIncomingContext(stream.Context())
			if got := md.Get("authorization"); len(got) != 1 || got[0] != "Bearer local-test" {
				return status.Error(codes.Unauthenticated, "expected test auth")
			}
			stream.SendHeader(metadata.Pairs("x-server", "demo"))
			stream.SetTrailer(metadata.Pairs("x-result", "complete", "proof-bin", string([]byte{0, 255, 42})))
			var messages []string
			for {
				in := new(wrapperspb.StringValue)
				err := stream.RecvMsg(in)
				if errors.Is(err, io.EOF) {
					if name == "Client" {
						return stream.SendMsg(wrapperspb.String(strings.Join(messages, ":")))
					}
					return nil
				}
				if err != nil {
					return err
				}
				messages = append(messages, in.Value)
				switch name {
				case "Wait":
					<-stream.Context().Done()
					return stream.Context().Err()
				case "Fail":
					problem, _ := status.New(codes.PermissionDenied, "fixture denied").WithDetails(wrapperspb.String("reason-73"))
					return problem.Err()
				case "Client":
					continue
				default:
					if err := stream.SendMsg(wrapperspb.String("reply:" + in.Value)); err != nil {
						return err
					}
					if name == "Server" {
						return stream.SendMsg(wrapperspb.String("last:" + in.Value))
					}
				}
				if name != "Bidi" {
					return nil
				}
			}
		}})
	}
	s.RegisterService(desc, struct{}{})
	if err := schema.files.RegisterFile(reflectionv1.File_grpc_reflection_v1_reflection_proto); err != nil {
		t.Fatal(err)
	}
	reflectionv1.RegisterServerReflectionServer(s, reflection.NewServerV1(reflection.ServerOptions{Services: s, DescriptorResolver: schema.files}))
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(l)
	t.Cleanup(s.Stop)
	return path, schema, data, "http://" + l.Addr().String()
}

func waitGRPC(t *testing.T, tt *ui.Tester, condition func() bool) {
	t.Helper()
	guard, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	for !condition() {
		if guard.Err() != nil {
			t.Fatalf("gRPC did not settle: %v", tt.Texts())
		}
		runtime.Gosched()
		tt.Frame()
	}
	tt.Frame()
}

func TestGRPCReflectCertificateFailureClearsLoading(t *testing.T) {
	a := libraryApp(t)
	dir := t.TempDir()
	a.certificates = certs.Settings{Clients: []certs.Client{{
		ID: "one", Host: "api.example.com", Format: certs.FormatPEM,
		CertFile: filepath.Join(dir, "missing.pem"), KeyFile: filepath.Join(dir, "missing.key"),
	}}}
	a.addDocument(savedRequest{Protocol: "gRPC", Name: "Broken certificate", URL: "https://api.example.com"}, "")
	d := a.requestDocument
	g := d.grpcState()
	a.loadGRPCSchema(d, true, false)
	if g.loading != nil {
		t.Fatal("reflect failure left the schema loading state set")
	}
	if g.cancelImport != nil {
		t.Fatal("reflect failure left a cancel function")
	}
	if g.problem == "" {
		t.Fatal("reflect failure did not report the certificate problem")
	}
	for _, action := range a.grpcActions(d, true) {
		if action.id == "request.grpc-reflect" {
			if !action.enabled {
				t.Fatalf("reflect is still disabled: %s", action.reason)
			}
			return
		}
	}
	t.Fatal("reflect action missing")
}

func TestGRPCDocumentImportReflectionCallsAndPersistence(t *testing.T) {
	path, _, _, endpoint := grpcFixture(t)
	a := libraryApp(t)
	a.addDocument(savedRequest{Protocol: "gRPC", Name: "Echo", URL: endpoint, Body: `"first"`, Auth: request.Auth{Type: "Bearer", Token: "{{token}}"}}, "")
	a.envs, a.activeEnv = []environment{
		{Name: "Local", Current: map[string]string{"token": "local-test", "next": "second"}},
		{Name: "Other", Current: map[string]string{"token": "wrong", "next": "wrong"}},
	}, "Local"
	d := a.requestDocument
	t.Cleanup(func() { d.grpcState().stop() })
	tt := ui.NewTester(a.view, 1100, 800)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	click("gRPC editor: Schema")
	click("gRPC schema file")
	tt.Type(path)
	click("Import schema")
	waitGRPC(t, tt, func() bool { return d.grpcUI.loading == nil })
	if d.grpcUI.schema == nil || len(d.grpcUI.schema.methods) != 6 || !tt.HasText("6 methods · descriptors saved with this request") {
		t.Fatalf("import: %v", tt.Texts())
	}
	// Reflection uses auth but does not invoke a method, and stores a complete
	// portable descriptor set (including imported wrapper messages).
	click("Reflect services")
	waitGRPC(t, tt, func() bool { return d.grpcUI.loading == nil })
	if d.grpcUI.problem != "" || len(d.grpcConfig.Schema) == 0 {
		t.Fatalf("reflection: %s", d.grpcUI.problem)
	}
	for _, name := range []string{"Unary", "Server", "Client", "Bidi", "Wait", "Fail"} {
		t.Run(name, func(t *testing.T) {
			d.grpcConfig.Method, d.composeBody = "/demo.Echo/"+name, `"first"`
			d.grpcUI.tab = 0
			tt.Frame()
			click("Send")
			waitGRPC(t, tt, func() bool {
				s := d.grpcUI.session.snapshot()
				return s.finished || !s.sending && len(s.events) > 0
			})
			if name == "Client" || name == "Bidi" {
				if name == "Bidi" {
					waitGRPC(t, tt, func() bool { return len(d.grpcUI.session.snapshot().events) == 2 })
				}
				// A running call keeps its original variable/auth scope, even if
				// another environment is selected before the next message.
				a.activeEnv, d.composeBody = "Other", `"{{next}}"`
				tt.Frame()
				click("Send message")
				waitGRPC(t, tt, func() bool { return !d.grpcUI.session.snapshot().sending })
				click("Finish sending")
				a.activeEnv = "Local"
			}
			if name == "Wait" {
				click("Cancel send")
			}
			waitGRPC(t, tt, func() bool { return !d.composeWaiting })
			s := d.grpcUI.session.snapshot()
			if name == "Wait" {
				if !strings.Contains(s.state, "Canceled") {
					t.Fatalf("cancel: %+v", s)
				}
				return
			}
			if name == "Fail" {
				if s.state != "gRPC PermissionDenied" || !strings.Contains(s.problem, "fixture denied") || !strings.Contains(s.details, "reason-73") {
					t.Fatalf("error status: %+v", s)
				}
				click("gRPC response: Status")
				if !strings.Contains(strings.Join(tt.Texts(), "\n"), "reason-73") {
					t.Fatal("structured error details not inspectable")
				}
				click("gRPC response: Messages")
				return
			}
			var received []string
			for _, event := range s.events {
				if event.direction == "In" {
					received = append(received, event.text)
				}
			}
			want := map[string][]string{"Unary": {`"reply:first"`}, "Server": {`"reply:first"`, `"last:first"`}, "Client": {`"first:second"`}, "Bidi": {`"reply:first"`, `"reply:second"`}}[name]
			if s.state != "gRPC OK" || !reflect.DeepEqual(received, want) {
				t.Fatalf("messages: %v; want %v; status %s %s", received, want, s.state, s.problem)
			}
			click("gRPC response: Metadata")
			text := strings.Join(tt.Texts(), "\n")
			for _, want := range []string{"x-server: demo", "proof-bin: AP8q", "x-result: complete"} {
				if !strings.Contains(text, want) {
					t.Fatalf("metadata missing %q: %v", want, tt.Texts())
				}
			}
			click("gRPC response: Messages")
		})
	}
	r := d.value()
	loaded := libraryRoundTrip(t, []savedRequest{r})
	if len(loaded) != 1 || !reflect.DeepEqual(loaded[0].GRPC, r.GRPC) || loaded[0].Protocol != "gRPC" || loaded[0].Body != r.Body {
		t.Fatal("gRPC document lost portable schema, method or message on disk")
	}
	a.addDocument(loaded[0], loaded[0].key())
	t.Cleanup(func() { a.requestDocument.grpcState().stop() })
	tt.Frame()
	waitGRPC(t, tt, func() bool { return a.requestDocument.grpcUI.loading == nil })
	if a.requestDocument.dirty() {
		t.Fatal("loading a saved schema dirtied the request")
	}
}

func TestGRPCImportFailureKeepsSchemaAndCloseDiscardsLateDiscovery(t *testing.T) {
	_, schema, data, _ := grpcFixture(t)
	a := libraryApp(t)
	a.addDocument(savedRequest{Protocol: "gRPC", GRPC: request.GRPCConfig{Schema: data, Method: "/demo.Echo/Unary"}}, "")
	d := a.requestDocument
	g := d.grpcState()
	g.schema, g.loaded = schema, true
	tt := ui.NewTester(a.view, 960, 600)
	g.source = filepath.Join(t.TempDir(), "missing.proto")
	a.loadGRPCSchema(d, false, false)
	waitGRPC(t, tt, func() bool { return g.loading == nil })
	if g.problem == "" || g.schema != schema || !reflect.DeepEqual(d.grpcConfig.Schema, data) {
		t.Fatal("failed import replaced working schema")
	}
	answers := make(chan grpcSchemaResult, 1)
	g.loading = answers
	a.closeDocument(d)
	answers <- grpcSchemaResult{err: fmt.Errorf("late result")}
	tt.Frame()
	if len(a.documents) != 0 || g.problem == "late result" {
		t.Fatal("closed document accepted late discovery")
	}
}
