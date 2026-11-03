package grpcclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	v1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	alpha "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// A real grpc-go service with generated messages and dynamic method descriptors.
// Asymmetric values distinguish accumulation, per-message replies and ordering.
func testService(t *testing.T, version string, secure bool) (string, *protoregistry.Files, []byte) {
	t.Helper()
	file := &descriptorpb.FileDescriptorProto{
		Name: new("calls.proto"), Package: new("clienttest"), Syntax: new("proto3"),
		Dependency: []string{"google/protobuf/wrappers.proto"},
		Service:    []*descriptorpb.ServiceDescriptorProto{{Name: new("Calls")}},
	}
	names := []string{"Unary", "Server", "Client", "Bidi", "Wait", "Fail", "Empty", "Big"}
	for _, name := range names {
		file.Service[0].Method = append(file.Service[0].Method, &descriptorpb.MethodDescriptorProto{
			Name: new(name), InputType: new(".google.protobuf.StringValue"), OutputType: new(".google.protobuf.StringValue"),
			ClientStreaming: new(name == "Client" || name == "Bidi"),
			ServerStreaming: new(name == "Server" || name == "Bidi" || name == "Wait" || name == "Fail" || name == "Empty" || name == "Big"),
		})
	}
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}
	files := new(protoregistry.Files)
	for _, f := range []protoreflect.FileDescriptor{fd, wrapperspb.File_google_protobuf_wrappers_proto, v1.File_grpc_reflection_v1_reflection_proto, alpha.File_grpc_reflection_v1alpha_reflection_proto} {
		if err := files.RegisterFile(f); err != nil {
			t.Fatal(err)
		}
	}
	var opts []grpc.ServerOption
	var der []byte
	if secure {
		// httptest provisions a locally verifiable certificate without shell tools.
		h := httptest.NewTLSServer(http.NotFoundHandler())
		cert := h.TLS.Certificates[0]
		h.Close()
		der = cert.Certificate[0]
		opts = append(opts, grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}})))
	}
	s := grpc.NewServer(opts...)
	desc := &grpc.ServiceDesc{ServiceName: "clienttest.Calls", HandlerType: (*interface{})(nil), Metadata: "calls.proto"}
	for _, method := range file.Service[0].Method {
		name := method.GetName()
		desc.Streams = append(desc.Streams, grpc.StreamDesc{StreamName: name, ClientStreams: method.GetClientStreaming(), ServerStreams: method.GetServerStreaming(), Handler: func(_ any, stream grpc.ServerStream) error {
			md, _ := metadata.FromIncomingContext(stream.Context())
			if md.Get("authorization")[0] != "Bearer test-27" {
				return status.Error(codes.Unauthenticated, "missing credential")
			}
			stream.SendHeader(metadata.Pairs("x-reply", "header-73"))
			stream.SetTrailer(metadata.Pairs("x-finish", "trailer-19", "proof-bin", string([]byte{0, 255, 42})))
			var all []string
			for {
				in := new(wrapperspb.StringValue)
				err := stream.RecvMsg(in)
				if errors.Is(err, io.EOF) {
					if name == "Client" {
						return stream.SendMsg(wrapperspb.String(strings.Join(all, "|")))
					}
					return nil
				}
				if err != nil {
					return err
				}
				all = append(all, in.Value)
				switch name {
				case "Wait":
					<-stream.Context().Done()
					return stream.Context().Err()
				case "Empty":
					return nil
				case "Fail":
					problem, _ := status.New(codes.PermissionDenied, "not permitted").WithDetails(wrapperspb.String("detail-42"))
					return problem.Err()
				case "Client":
					continue
				case "Server":
					for _, prefix := range []string{"first:", "second:"} {
						if err := stream.SendMsg(wrapperspb.String(prefix + in.Value)); err != nil {
							return err
						}
					}
				case "Big":
					return stream.SendMsg(wrapperspb.String(strings.Repeat("x", MaxMessageBytes+1)))
				default:
					if err := stream.SendMsg(wrapperspb.String("reply:" + in.Value)); err != nil {
						return err
					}
				}
				if name != "Bidi" {
					return nil
				}
			}
		}})
	}
	s.RegisterService(desc, struct{}{})
	rOpts := reflection.ServerOptions{Services: s, DescriptorResolver: files}
	if version == "v1" {
		v1.RegisterServerReflectionServer(s, reflection.NewServerV1(rOpts))
	} else {
		alpha.RegisterServerReflectionServer(s, reflection.NewServer(rOpts))
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(l)
	t.Cleanup(s.Stop)
	scheme := "http://"
	if secure {
		scheme = "https://"
	}
	return scheme + l.Addr().String(), files, der
}

func TestDynamicCallsAndReflection(t *testing.T) {
	for _, version := range []string{"v1", "v1alpha"} {
		t.Run(version, func(t *testing.T) {
			endpoint, _, _ := testService(t, version, false)
			conn, err := Dial(endpoint, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			md := metadata.Pairs("authorization", "Bearer test-27")
			data, err := Reflect(ctx, conn, md)
			if err != nil {
				t.Fatal(err)
			}
			var set descriptorpb.FileDescriptorSet
			if err := proto.Unmarshal(data, &set); err != nil {
				t.Fatal(err)
			}
			files, err := protodesc.NewFiles(&set)
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name string
				want []string
			}{{"Unary", []string{`"reply:orange"`}}, {"Server", []string{`"first:orange"`, `"second:orange"`}}, {"Client", []string{`"orange|purple"`}}, {"Bidi", []string{`"reply:orange"`, `"reply:purple"`}}, {"Empty", nil}, {"Fail", nil}, {"Big", nil}, {"Wait", nil}} {
				t.Run(tc.name, func(t *testing.T) {
					d, err := files.FindDescriptorByName(protoreflect.FullName("clienttest.Calls." + tc.name))
					if err != nil {
						t.Fatal(err)
					}
					method := d.(protoreflect.MethodDescriptor)
					ctx, cancel := context.WithCancel(ctx)
					defer cancel()
					call, err := Start(ctx, conn, method, nil, md)
					if err != nil {
						t.Fatal(err)
					}
					if err := call.Send([]byte(`{"unknown":1}`)); err == nil {
						t.Fatal("invalid JSON was accepted")
					}
					if err := call.Send([]byte(`"orange"`)); err != nil {
						t.Fatal(err)
					}
					if tc.name == "Bidi" {
						// Must arrive before the second write and before half-close.
						got, err := call.Recv()
						if err != nil || string(got) != tc.want[0] {
							t.Fatalf("live bidi response %s: %v", got, err)
						}
					}
					if method.IsStreamingClient() {
						if err := call.Send([]byte(`"purple"`)); err != nil {
							t.Fatal(err)
						}
					} else if err := call.Send([]byte(`"extra"`)); err == nil {
						t.Fatal("extra unary request accepted")
					}
					if err := call.CloseSend(); err != nil {
						t.Fatal(err)
					}
					if err := call.Send([]byte(`"after close"`)); err == nil {
						t.Fatal("send after half-close accepted")
					}
					if tc.name == "Wait" {
						if _, err := call.Header(); err != nil {
							t.Fatal(err)
						}
						cancel()
					}
					want := tc.want
					if tc.name == "Bidi" {
						want = want[1:]
					}
					for _, expected := range want {
						data, err := call.Recv()
						if err != nil || string(data) != expected {
							t.Fatalf("message %s, want %s: %v", data, expected, err)
						}
					}
					_, err = call.Recv()
					switch tc.name {
					case "Fail":
						s := status.Convert(err)
						if s.Code() != codes.PermissionDenied || len(s.Details()) != 1 || s.Details()[0].(*wrapperspb.StringValue).Value != "detail-42" {
							t.Fatalf("lost final status/details: %v", s)
						}
					case "Big":
						if status.Code(err) != codes.ResourceExhausted {
							t.Fatalf("receive limit: %v", err)
						}
					case "Wait":
						if status.Code(err) != codes.Canceled {
							t.Fatalf("cancellation: %v", err)
						}
					default:
						if !errors.Is(err, io.EOF) {
							t.Fatalf("final status: %v", err)
						}
					}
					// Local receive-limit failures cancel transport before final
					// trailers; never claim metadata that was not observed.
					if tc.name != "Wait" && tc.name != "Big" {
						header, _ := call.Header()
						trailer := call.Trailer()
						if fmt.Sprint(header.Get("x-reply")) != "[header-73]" || fmt.Sprint(trailer.Get("x-finish")) != "[trailer-19]" || trailer.Get("proof-bin")[0] != string([]byte{0, 255, 42}) {
							t.Fatalf("metadata lost: %v %v", header, trailer)
						}
					}
				})
			}
		})
	}
}

func TestInvalidEndpointsAndMetadataDoNotDial(t *testing.T) {
	for _, endpoint := range []string{"localhost:80", "http://:80", "https://user:pass@host", "https://host/service", "http://host?x=1", "ftp://host"} {
		if conn, err := Dial(endpoint, "", nil); err == nil {
			conn.Close()
			t.Errorf("accepted %q", endpoint)
		}
	}
	for _, md := range []metadata.MD{{"Bad-Key": {"value"}}, {"grpc-timeout": {"1S"}}, {"x-token": {"line\nbreak"}},
		{"content-type": {"application/json"}}, {"user-agent": {"auth-placeholder"}}, {"te": {"trailers"}}} {
		if _, err := Reflect(t.Context(), nil, md); err == nil {
			t.Errorf("accepted invalid metadata %v", md)
		}
	}
}

func TestTLSCallsVerifyCertificatesAndTraverseCapture(t *testing.T) {
	endpoint, files, der := testService(t, "v1", true)
	proxy, err := capture.New("127.0.0.1:0", filepath.Join(t.TempDir(), "ca"))
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.Start(); err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	proxy.SetSSL(capture.SSLConfig{All: true})
	method, _ := files.FindDescriptorByName("clienttest.Calls.Unary")
	for _, tc := range []struct {
		name, proxy string
		ca          []byte
		ok          bool
	}{{"untrusted", "", nil, false}, {"direct trusted", "", der, true}, {"proxy trusted", proxy.Addr(), proxy.CACertificate(), true}} {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := Dial(endpoint, tc.proxy, tc.ca)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			call, err := Start(ctx, conn, method.(protoreflect.MethodDescriptor), nil, metadata.Pairs("authorization", "Bearer test-27"))
			if err == nil {
				err = call.Send([]byte(`"verified"`))
			}
			if err == nil {
				err = call.CloseSend()
			}
			if err == nil {
				var got []byte
				got, err = call.Recv()
				if err == nil && string(got) != `"reply:verified"` {
					t.Fatalf("incorrect response %s", got)
				}
			}
			if (err == nil) != tc.ok {
				t.Fatalf("TLS result %v; want success %t", err, tc.ok)
			}
		})
	}
}
