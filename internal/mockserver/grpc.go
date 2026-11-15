package mockserver

import (
	"fmt"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/grpcclient"
	"github.com/baba983/vscroxy/internal/request"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// GRPCExample is one unary gRPC response a saved example supplies. Schema is
// the request's FileDescriptorSet, Body the output message as protojson, and
// Status the gRPC code: 0 or an HTTP 2xx is OK, 1–16 is used as the code, and
// common HTTP errors map to their nearest code (404 to NotFound, 503 to
// Unavailable). A non-OK example's Body becomes the status message.
type GRPCExample struct {
	Method  string
	Schema  []byte
	Body    string
	Headers string
	Status  int
	Delay   time.Duration
}

type grpcMock struct {
	method  protoreflect.MethodDescriptor
	types   *dynamicpb.Types
	body    []byte
	headers metadata.MD
	code    codes.Code
	delay   time.Duration
}

// NewGRPC serves unary calls from examples, keyed by their full method path.
// Unknown methods answer Unimplemented; a streaming method is refused when
// the server is built, because a saved example is one response.
func NewGRPC(examples []GRPCExample) (*grpc.Server, error) {
	mocks := make(map[string]grpcMock, len(examples))
	for i, example := range examples {
		method, types, err := grpcMockMethod(example.Schema, example.Method)
		if err != nil {
			return nil, fmt.Errorf("gRPC example %d: %w", i+1, err)
		}
		if method.IsStreamingClient() || method.IsStreamingServer() {
			return nil, fmt.Errorf("gRPC example %d: %s is a streaming method; examples mock unary calls", i+1, example.Method)
		}
		body := strings.TrimSpace(example.Body)
		if body == "" {
			body = "{}"
		}
		code := grpcExampleCode(example.Status)
		// A successful example is an output message; validate it at
		// construction so a bad example cannot fail a call later. A failing
		// example's body is its status message, not JSON.
		if code == codes.OK {
			message := dynamicpb.NewMessage(method.Output())
			if err := (protojson.UnmarshalOptions{Resolver: types}).Unmarshal([]byte(body), message); err != nil {
				return nil, fmt.Errorf("gRPC example %d: response JSON: %w", i+1, err)
			}
		}
		headers, _ := grpcclient.Metadata(request.ParseHeaderBlock(example.Headers))
		mocks["/"+string(method.Parent().FullName())+"/"+string(method.Name())] = grpcMock{
			method: method, types: types, body: []byte(body), headers: headers,
			code: code, delay: min(example.Delay, time.Minute),
		}
	}
	return grpc.NewServer(grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
		name, _ := grpc.MethodFromServerStream(stream)
		mock, ok := mocks[name]
		if !ok {
			return status.Errorf(codes.Unimplemented, "no example for %s", name)
		}
		if len(mock.headers) > 0 {
			_ = stream.SetHeader(mock.headers)
		}
		// Read the request message so the client's send completes; the
		// example answers regardless of its contents.
		if err := stream.RecvMsg(dynamicpb.NewMessage(mock.method.Input())); err != nil {
			return err
		}
		if mock.delay > 0 {
			timer := time.NewTimer(mock.delay)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-stream.Context().Done():
				return stream.Context().Err()
			}
		}
		if mock.code != codes.OK {
			message := string(mock.body)
			if len(message) > 4<<10 {
				message = message[:4<<10]
			}
			return status.Error(mock.code, message)
		}
		out := dynamicpb.NewMessage(mock.method.Output())
		if err := (protojson.UnmarshalOptions{Resolver: mock.types}).Unmarshal(mock.body, out); err != nil {
			return status.Error(codes.Internal, "mock response JSON")
		}
		return stream.SendMsg(out)
	})), nil
}

// grpcMockMethod resolves a saved descriptor set and method, as the gRPC
// client does.
func grpcMockMethod(schema []byte, path string) (protoreflect.MethodDescriptor, *dynamicpb.Types, error) {
	if len(schema) == 0 {
		return nil, nil, fmt.Errorf("the request has no saved schema")
	}
	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(schema, &set); err != nil {
		return nil, nil, fmt.Errorf("read FileDescriptorSet: %w", err)
	}
	files, err := protodesc.NewFiles(&set)
	if err != nil {
		return nil, nil, fmt.Errorf("link descriptors: %w", err)
	}
	service, name, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(path), "/"), "/")
	if !ok || service == "" || name == "" {
		return nil, nil, fmt.Errorf("the request has no service/method")
	}
	descriptor, err := files.FindDescriptorByName(protoreflect.FullName(service + "." + name))
	if err != nil {
		return nil, nil, err
	}
	method, ok := descriptor.(protoreflect.MethodDescriptor)
	if !ok {
		return nil, nil, fmt.Errorf("%s is not a gRPC method", path)
	}
	return method, dynamicpb.NewTypes(files), nil
}

// grpcExampleCode reads an example's status as a gRPC code.
func grpcExampleCode(status int) codes.Code {
	switch {
	case status == 0 || status >= 200 && status < 300:
		return codes.OK
	case status > 0 && status <= 16:
		return codes.Code(status)
	}
	switch status {
	case 400:
		return codes.InvalidArgument
	case 401:
		return codes.Unauthenticated
	case 403:
		return codes.PermissionDenied
	case 404:
		return codes.NotFound
	case 409:
		return codes.Aborted
	case 412:
		return codes.FailedPrecondition
	case 429:
		return codes.ResourceExhausted
	case 499:
		return codes.Canceled
	case 501:
		return codes.Unimplemented
	case 503:
		return codes.Unavailable
	case 504:
		return codes.DeadlineExceeded
	default:
		return codes.Internal
	}
}
