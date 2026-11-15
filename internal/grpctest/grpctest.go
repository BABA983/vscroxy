// Package grpctest runs a descriptor-defined gRPC service for real-socket
// tests. It is test support, not application code.
package grpctest

import (
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// Token is the x-token metadata every method requires.
const Token = "t-1"

// Echo starts a grpc.Echo service and returns its plaintext endpoint and the
// FileDescriptorSet to save with a request. Unary echoes "reply:" plus its
// message, Fail returns PermissionDenied, Stream sends one message, Collect
// answers once after every client message, Chat echoes each message as it
// arrives, and Wait blocks until the caller cancels. Every call sends
// x-reply: header-1 and finishes with x-finish: trailer-1.
func Echo(t *testing.T) (endpoint string, schema []byte) {
	t.Helper()
	file := &descriptorpb.FileDescriptorProto{
		Name: new("grpctest.proto"), Package: new("grpctest"), Syntax: new("proto3"),
		Dependency: []string{"google/protobuf/wrappers.proto"},
		Service: []*descriptorpb.ServiceDescriptorProto{{Name: new("Echo"), Method: []*descriptorpb.MethodDescriptorProto{
			{Name: new("Unary"), InputType: new(".google.protobuf.StringValue"), OutputType: new(".google.protobuf.StringValue")},
			{Name: new("Fail"), InputType: new(".google.protobuf.StringValue"), OutputType: new(".google.protobuf.StringValue")},
			{Name: new("Stream"), InputType: new(".google.protobuf.StringValue"), OutputType: new(".google.protobuf.StringValue"), ServerStreaming: new(true)},
			{Name: new("Collect"), InputType: new(".google.protobuf.StringValue"), OutputType: new(".google.protobuf.StringValue"), ClientStreaming: new(true)},
			{Name: new("Chat"), InputType: new(".google.protobuf.StringValue"), OutputType: new(".google.protobuf.StringValue"), ClientStreaming: new(true), ServerStreaming: new(true)},
			{Name: new("Wait"), InputType: new(".google.protobuf.StringValue"), OutputType: new(".google.protobuf.StringValue")},
		}}},
	}
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}
	schema, err = proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{
		protodesc.ToFileDescriptorProto(fd),
		protodesc.ToFileDescriptorProto(wrapperspb.File_google_protobuf_wrappers_proto),
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	desc := &grpc.ServiceDesc{ServiceName: "grpctest.Echo", HandlerType: (*interface{})(nil), Metadata: "grpctest.proto"}
	for _, method := range file.Service[0].Method {
		name := method.GetName()
		desc.Streams = append(desc.Streams, grpc.StreamDesc{StreamName: name, ClientStreams: method.GetClientStreaming(), ServerStreams: method.GetServerStreaming(), Handler: func(_ any, stream grpc.ServerStream) error {
			md, _ := metadata.FromIncomingContext(stream.Context())
			if len(md.Get("x-token")) == 0 || md.Get("x-token")[0] != Token {
				return status.Error(codes.Unauthenticated, "missing x-token")
			}
			stream.SendHeader(metadata.Pairs("x-reply", "header-1"))
			stream.SetTrailer(metadata.Pairs("x-finish", "trailer-1"))
			recv := func() (*wrapperspb.StringValue, error) {
				in := new(wrapperspb.StringValue)
				err := stream.RecvMsg(in)
				return in, err
			}
			switch name {
			case "Collect":
				var values []string
				for {
					in, err := recv()
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						return err
					}
					values = append(values, in.Value)
				}
				return stream.SendMsg(wrapperspb.String("all:" + strings.Join(values, ",")))
			case "Chat":
				for {
					in, err := recv()
					if errors.Is(err, io.EOF) {
						return nil
					}
					if err != nil {
						return err
					}
					if err := stream.SendMsg(wrapperspb.String("echo:" + in.Value)); err != nil {
						return err
					}
				}
			}
			in, err := recv()
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			switch name {
			case "Fail":
				return status.Error(codes.PermissionDenied, "not permitted")
			case "Stream":
				return stream.SendMsg(wrapperspb.String("first:" + in.Value))
			case "Wait":
				<-stream.Context().Done()
				return stream.Context().Err()
			default:
				return stream.SendMsg(wrapperspb.String("reply:" + in.Value))
			}
		}})
	}
	server.RegisterService(desc, struct{}{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	return "http://" + listener.Addr().String(), schema
}
