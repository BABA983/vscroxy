package request

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/grpcclient"
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

// grpcMaxMessageBytes bounds one message, as the interactive client does.
const grpcMaxMessageBytes = 1 << 20

// grpcMaxResponses bounds what one streaming call retains, so a chatty server
// cannot grow a run's result without limit.
const grpcMaxResponses = 1000

// grpcMethod resolves the saved descriptor set and the method it calls.
func grpcMethod(schema []byte, path string) (protoreflect.MethodDescriptor, *dynamicpb.Types, error) {
	if len(schema) == 0 {
		return nil, nil, errors.New("import a schema or reflect services first")
	}
	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(schema, &set); err != nil {
		return nil, nil, fmt.Errorf("read FileDescriptorSet: %w", err)
	}
	files, err := protodesc.NewFiles(&set)
	if err != nil {
		return nil, nil, fmt.Errorf("link descriptors; include all imports: %w", err)
	}
	service, name, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(path), "/"), "/")
	if !ok || service == "" || name == "" {
		return nil, nil, errors.New("select a gRPC service/method")
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

// GRPCResult is one unary call's final status and metadata, for the app's
// status pane. The response message is the Execution's response body.
type GRPCResult struct {
	// Completed is set once the call reached a final status; a failure before
	// that (schema, dial, metadata) never ran the RPC.
	Completed bool
	Code      codes.Code
	Message   string
	// Sent and Received count the messages this call exchanged.
	Sent, Received int
	// Details is the status proto as JSON, including any typed details.
	Details string
	Header  metadata.MD
	Trailer metadata.MD
}

// grpcCallResult adds the response message to what the Execution reports.
type grpcCallResult struct {
	GRPCResult
	Body []byte
}

// grpcCall runs one call through the proxy, sending messages in order and
// collecting every response. A unary call sends one message and receives one;
// a client, server or bidirectional stream sends its saved list and closes the
// send side, then reads until the server ends the stream. A non-OK status is a
// completed call, so the result keeps its code and metadata; err is set so the
// run reports the failure.
func grpcCall(ctx context.Context, proxy string, req *Prepared, cfg GRPCConfig, ca []byte, certificates certs.Settings, messages []string) (grpcCallResult, error) {
	method, types, err := grpcMethod(cfg.Schema, cfg.Method)
	if err != nil {
		return grpcCallResult{}, err
	}
	var tlsConfig *tls.Config
	if u, err := url.Parse(req.URL); err == nil && u.Scheme == "https" {
		tlsConfig, err = certificates.TLSConfig(certs.Address(u), ca, false)
		if err != nil {
			return grpcCallResult{}, err
		}
	}
	md, err := grpcclient.Metadata(req.Header)
	if err != nil {
		return grpcCallResult{}, err
	}
	conn, err := grpcclient.DialWithTLS(req.URL, proxy, tlsConfig)
	if err != nil {
		return grpcCallResult{}, err
	}
	defer conn.Close()
	call, err := grpcclient.Start(ctx, conn, method, types, md)
	if err != nil {
		return grpcCallResult{}, err
	}
	result := grpcCallResult{GRPCResult: GRPCResult{Completed: true}}
	for i, message := range messages {
		if len(message) > grpcMaxMessageBytes {
			return grpcCallResult{}, fmt.Errorf("messages are limited to %d MiB", grpcMaxMessageBytes>>20)
		}
		if err := call.Send([]byte(message)); err != nil {
			result.Sent = i
			return withGRPCStatus(result, err, types), err
		}
		result.Sent++
	}
	if err := call.CloseSend(); err != nil {
		return withGRPCStatus(result, err, types), err
	}
	var bodies [][]byte
	for {
		body, err := call.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			result.Received = len(bodies)
			return withGRPCMetadata(withGRPCStatus(result, err, types), call), err
		}
		bodies = append(bodies, body)
		if len(bodies) >= grpcMaxResponses {
			return withGRPCMetadata(withGRPCStatus(result, fmt.Errorf("more than %d response messages", grpcMaxResponses), types), call), fmt.Errorf("more than %d response messages", grpcMaxResponses)
		}
	}
	result.Received = len(bodies)
	// One response keeps the unary shape; several become a JSON array of the
	// messages, and none is an empty array.
	switch len(bodies) {
	case 0:
		result.Body = []byte("[]")
	case 1:
		result.Body = bodies[0]
	default:
		result.Body = append(append([]byte("["), bytes.Join(bodies, []byte(","))...), ']')
	}
	result.Code = codes.OK
	return withGRPCMetadata(result, call), nil
}

func withGRPCMetadata(result grpcCallResult, call *grpcclient.Call) grpcCallResult {
	result.Header, _ = call.Header()
	result.Trailer = call.Trailer()
	return result
}

func withGRPCStatus(result grpcCallResult, err error, types *dynamicpb.Types) grpcCallResult {
	st := status.Convert(err)
	result.Code, result.Message = st.Code(), st.Message()
	if data, err := (protojson.MarshalOptions{Multiline: true, Resolver: types}).Marshal(st.Proto()); err == nil {
		result.Details = string(data)
	}
	return result
}

// executeGRPC runs one unary call and reports it as an HTTP-like exchange: the
// response message is the body, and grpc-status/grpc-message join the response
// headers so Checks can assert them. Post-response scripts and Checks run even
// when the status is not OK, so a failure can be asserted.
func (r Request) executeGRPC(ctx context.Context, srv *capture.Server, in Context, effective map[string]string, run *scriptRun, out Execution) Execution {
	req, err := r.prepareGRPC(effective)
	if err != nil {
		out.Err = err
		return out
	}
	out.Request = req
	out.Stage, run.phase = "gRPC call failed", "network"
	// Plaintext h2c calls go direct; HTTPS uses the capture proxy when it runs.
	var proxy string
	var ca []byte
	if srv != nil && srv.Running() {
		proxy, ca = srv.Addr(), srv.CACertificate()
	}
	start := time.Now()
	messages := make([]string, 0, max(1, len(r.GRPC.Messages)))
	for _, message := range r.GRPC.Messages {
		messages = append(messages, ExpandVars(message, effective))
	}
	if len(messages) == 0 {
		messages = []string{req.Body}
	}
	if seconds := r.GRPC.TimeoutSeconds; seconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
		defer cancel()
	}
	result, callErr := grpcCall(ctx, proxy, req, r.GRPC, ca, in.Certificates, messages)
	flow := capture.Flow{
		Method: "POST", URL: req.URL, RequestHeader: req.Header, RequestBody: req.Body,
		Status: 200, ResponseHeader: headerFromMetadata(result.Header),
		ResponseBody: string(result.Body), ResponseBodySize: len(result.Body),
		Millis: int(time.Since(start).Milliseconds()), Bytes: len(result.Body),
	}
	flow.ResponseHeader.Set("Grpc-Status", strconv.Itoa(int(result.Code)))
	flow.ResponseHeader.Set("Grpc-Sent", strconv.Itoa(result.Sent))
	flow.ResponseHeader.Set("Grpc-Received", strconv.Itoa(result.Received))
	if result.Message != "" {
		flow.ResponseHeader.Set("Grpc-Message", result.Message)
	}
	for key, values := range result.Trailer {
		for _, value := range values {
			flow.ResponseHeader.Add("Grpc-Trailer-"+key, value)
		}
	}
	if result.Completed {
		out.Response = &flow
		out.GRPC = &result.GRPCResult
		run.tests = append(run.tests, r.Checks.Evaluate(flow)...)
		run.target = func() string { return req.URL }
		res := &jsResponse{Status: flow.Status, Body: flow.ResponseBody, Header: flow.ResponseHeader, Millis: flow.Millis, Bytes: flow.Bytes}
		if err := run.scripts(ctx, "post-response", in.Folders, func(f Folder) string { return f.Post }, r.Post, map[string]any{"res": res, "req": req}); err != nil {
			callErr = errors.Join(callErr, err)
		}
	}
	out.Err = callErr
	out.Tests, out.Vars = run.tests, run.vars
	return out
}

// headerFromMetadata turns call metadata into response headers, so the app and
// Checks read it like any other response.
func headerFromMetadata(md metadata.MD) http.Header {
	header := http.Header{}
	for key, values := range md {
		for _, value := range values {
			header.Add(key, value)
		}
	}
	return header
}
