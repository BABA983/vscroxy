package capture

// The tests in this file drive a real gRPC server through the proxy: a gRPC
// client dials the proxy with CONNECT, the proxy decrypts the tunnel with its
// own CA and speaks HTTP/2 to both sides, and the tests read what the capture
// kept. Nothing here synthesizes a gRPC body: every message is one grpc-go
// marshalled, and every trailer is one grpc-go wrote.
//
// The service is defined without generated code, because the test needs no
// schema: the messages are protobuf strings, so the wire is the real protocol.

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	grpcgzip "google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	statuspb "google.golang.org/genproto/googleapis/rpc/status"
)

// grpcService is the name of the service the test server answers.
const grpcService = "vscroxy.test.Streams"

// grpcMethod is the full method name of one call of that service.
func grpcMethod(name string) string { return "/" + grpcService + "/" + name }

// grpcTestServer is the interface the service is registered against, and
// grpcTestImpl its implementation: the handlers below ignore both.
type grpcTestServer interface{}

type grpcTestImpl struct{}

// grpcTestDesc is the service the test server registers and the client calls.
// The method set is the one the tests need: a unary call, a server stream, a
// client stream, a bidirectional stream, a stream that never ends by itself,
// and a stream that fails before it sends anything.
func grpcTestDesc() *grpc.ServiceDesc {
	return &grpc.ServiceDesc{
		ServiceName: grpcService,
		HandlerType: (*grpcTestServer)(nil),
		Methods:     []grpc.MethodDesc{{MethodName: "Echo", Handler: echoHandler}},
		Streams: []grpc.StreamDesc{
			{StreamName: "ServerStream", Handler: serverStreamHandler, ServerStreams: true},
			{StreamName: "ClientStream", Handler: clientStreamHandler, ClientStreams: true},
			{StreamName: "Bidi", Handler: bidiHandler, ClientStreams: true, ServerStreams: true},
			{StreamName: "Slow", Handler: slowHandler, ServerStreams: true},
			{StreamName: "Fail", Handler: failHandler, ServerStreams: true},
		},
		Metadata: "grpc_test.proto",
	}
}

func echoHandler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(wrapperspb.StringValue)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor != nil {
		info := &grpc.UnaryServerInfo{Server: srv, FullMethod: grpcMethod("Echo")}
		return interceptor(ctx, in, info, func(ctx context.Context, req any) (any, error) {
			return echo(ctx, req.(*wrapperspb.StringValue))
		})
	}
	return echo(ctx, in)
}

// echo answers one message, with a header and a trailer of its own, and turns
// two prefixes into the cases the tests need: a failure that carries binary
// details, and a message too large to keep inline.
func echo(ctx context.Context, in *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
	if err := grpc.SetHeader(ctx, metadata.Pairs("x-server-header", "header-"+in.Value)); err != nil {
		return nil, err
	}
	grpc.SetTrailer(ctx, metadata.Pairs("x-server-trailer", "trailer-"+in.Value))
	if detail, ok := strings.CutPrefix(in.Value, "fail:"); ok {
		withDetails, err := status.New(codes.InvalidArgument, "no such value").
			WithDetails(wrapperspb.String(detail))
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		return nil, withDetails.Err()
	}
	if strings.HasPrefix(in.Value, "big:") {
		return wrapperspb.String(strings.Repeat("x", 1<<20)), nil
	}
	return in, nil
}

func serverStreamHandler(_ any, stream grpc.ServerStream) error {
	in := new(wrapperspb.StringValue)
	if err := stream.RecvMsg(in); err != nil {
		return err
	}
	if err := stream.SendHeader(metadata.Pairs("x-server-header", "stream")); err != nil {
		return err
	}
	stream.SetTrailer(metadata.Pairs("x-server-trailer", "stream-end"))
	for i := range 3 {
		if err := stream.SendMsg(wrapperspb.String(fmt.Sprintf("msg-%d-%s", i, in.Value))); err != nil {
			return err
		}
	}
	return nil
}

func clientStreamHandler(_ any, stream grpc.ServerStream) error {
	count := 0
	for {
		in := new(wrapperspb.StringValue)
		err := stream.RecvMsg(in)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		count++
	}
	return stream.SendMsg(wrapperspb.String(fmt.Sprint(count)))
}

// bidiHandler answers each message as it arrives, without waiting for the
// client to end its side: a proxy that buffers the request body deadlocks here.
func bidiHandler(_ any, stream grpc.ServerStream) error {
	for {
		in := new(wrapperspb.StringValue)
		err := stream.RecvMsg(in)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.SendMsg(wrapperspb.String("echo-" + in.Value)); err != nil {
			return err
		}
	}
}

// slowHandler sends one message and then waits, so a client's deadline or
// cancellation ends a stream that is still open.
func slowHandler(_ any, stream grpc.ServerStream) error {
	in := new(wrapperspb.StringValue)
	if err := stream.RecvMsg(in); err != nil {
		return err
	}
	if err := stream.SendMsg(wrapperspb.String("first")); err != nil {
		return err
	}
	select {
	case <-stream.Context().Done():
		return stream.Context().Err()
	case <-time.After(20 * time.Second):
		return nil
	}
}

// failHandler ends a call before it sends anything, which grpc-go answers as a
// trailers-only response: the status is in the header frame itself.
func failHandler(_ any, _ grpc.ServerStream) error {
	return status.Error(codes.NotFound, "no such stream")
}

// testCertificate is the certificate of the test gRPC server. The proxy dials
// it without verification, and the client only ever verifies the proxy's own
// CA, so a self-signed certificate for the name the client asks for is enough.
func testCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// startGRPCUpstream serves the test gRPC service over TLS and returns the
// address the client asks for, named localhost so the proxy's certificate for
// it verifies.
func startGRPCUpstream(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{testCertificate(t)}})),
		grpc.MaxSendMsgSize(16<<20),
	)
	server.RegisterService(grpcTestDesc(), grpcTestImpl{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split listen address: %v", err)
	}
	return "localhost:" + port
}

// connectDialer dials the proxy and opens a CONNECT tunnel to target, so the
// gRPC client's own TLS runs inside the one the proxy terminates.
func connectDialer(proxyAddr, target string) func(context.Context, string) (net.Conn, error) {
	return func(ctx context.Context, _ string) (net.Conn, error) {
		var dialer net.Dialer
		conn, err := dialer.DialContext(ctx, "tcp", proxyAddr)
		if err != nil {
			return nil, err
		}
		if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
			_ = conn.Close()
			return nil, err
		}
		reader := bufio.NewReader(conn)
		resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			_ = conn.Close()
			return nil, fmt.Errorf("CONNECT %s: %s", target, resp.Status)
		}
		if reader.Buffered() > 0 {
			_ = conn.Close()
			return nil, fmt.Errorf("CONNECT %s: %d bytes before the tunnel", target, reader.Buffered())
		}
		return conn, nil
	}
}

// startGRPCThroughProxy starts a proxy that decrypts everything and a gRPC
// client for it, trusting the proxy's CA as a machine would after installing
// it.
func startGRPCThroughProxy(t *testing.T) (*Server, *grpc.ClientConn) {
	t.Helper()
	target := startGRPCUpstream(t)
	s, caPath := startServer(t)
	s.SetSSL(SSLConfig{All: true})
	pem, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read proxy CA: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("proxy CA is not PEM")
	}
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{RootCAs: roots, ServerName: "localhost"})),
		grpc.WithContextDialer(connectDialer(s.Addr(), target)),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(16<<20)),
	)
	if err != nil {
		t.Fatalf("new gRPC client: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return s, conn
}

// waitFlowFor returns the next captured flow whose path ends with want, since
// a test may also see the tunnels and connections around it.
func waitFlowFor(t *testing.T, s *Server, want string) Flow {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case f := <-s.Flows():
			if strings.HasSuffix(f.Path, want) {
				return f
			}
		case <-deadline:
			t.Fatalf("no captured flow for %s", want)
			return Flow{}
		}
	}
}

// grpcFrames splits a captured body into the messages of a gRPC stream: a
// one-byte compressed flag, a four-byte big-endian length, then that many
// bytes of a protobuf message. It reads the messages a gRPC client compressed
// with gzip, and it fails on a body that is not a whole number of messages,
// which is how a test says the capture kept all of it.
func grpcFrames(t *testing.T, body string) []string {
	t.Helper()
	var messages []string
	rest := []byte(body)
	for len(rest) > 0 {
		if len(rest) < 5 {
			t.Fatalf("%d trailing bytes are not a gRPC message header", len(rest))
		}
		flag, size := rest[0], int(binary.BigEndian.Uint32(rest[1:5]))
		if len(rest) < 5+size {
			t.Fatalf("gRPC message of %d bytes is short by %d", size, 5+size-len(rest))
		}
		payload := rest[5 : 5+size]
		rest = rest[5+size:]
		switch flag {
		case 0:
		case 1:
			reader, err := gzip.NewReader(bytes.NewReader(payload))
			if err != nil {
				t.Fatalf("read a compressed message: %v", err)
			}
			payload, err = io.ReadAll(reader)
			if err != nil {
				t.Fatalf("read a compressed message: %v", err)
			}
		default:
			t.Fatalf("a message has compressed flag %d", flag)
		}
		message := new(wrapperspb.StringValue)
		if err := proto.Unmarshal(payload, message); err != nil {
			t.Fatalf("a message is not protobuf: %v", err)
		}
		messages = append(messages, message.Value)
	}
	return messages
}

// recvWithin receives one message, failing instead of blocking forever: a
// stream that only answers after the client ended its side would hang here.
func recvWithin(t *testing.T, stream grpc.ClientStream, timeout time.Duration) *wrapperspb.StringValue {
	t.Helper()
	type received struct {
		message *wrapperspb.StringValue
		err     error
	}
	done := make(chan received, 1)
	go func() {
		message := new(wrapperspb.StringValue)
		err := stream.RecvMsg(message)
		done <- received{message, err}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("receive a message: %v", got.err)
		}
		return got.message
	case <-time.After(timeout):
		t.Fatalf("no message within %s: the proxy is waiting for the request to end", timeout)
		return nil
	}
}

func TestGRPCUnaryKeepsBodiesHeadersAndTrailers(t *testing.T) {
	s, conn := startGRPCThroughProxy(t)
	ctx, cancel := context.WithTimeout(metadata.AppendToOutgoingContext(t.Context(), "x-client-meta", "client-value"), 20*time.Second)
	defer cancel()

	response := new(wrapperspb.StringValue)
	var header, trailer metadata.MD
	err := conn.Invoke(ctx, grpcMethod("Echo"), wrapperspb.String("hello"), response,
		grpc.Header(&header), grpc.Trailer(&trailer))
	if err != nil {
		t.Fatalf("unary call: %v", err)
	}
	if response.Value != "hello" {
		t.Fatalf("response = %q", response.Value)
	}
	if got := header.Get("x-server-header"); len(got) != 1 || got[0] != "header-hello" {
		t.Fatalf("response header = %v", got)
	}
	if got := trailer.Get("x-server-trailer"); len(got) != 1 || got[0] != "trailer-hello" {
		t.Fatalf("response trailer = %v", got)
	}
	// grpc-go filters its own reserved trailers out of the metadata it hands
	// back, so the status is checked on the captured flow below.

	f := waitFlowFor(t, s, "/Echo")
	if f.Method != "POST" || f.Status != http.StatusOK || f.Scheme != "https" {
		t.Fatalf("flow = %s %s %d", f.Method, f.Scheme, f.Status)
	}
	if f.Conn.Proto != "HTTP/2.0" || f.ResponseProto != "HTTP/2.0" {
		t.Fatalf("protocols: client %q, response %q", f.Conn.Proto, f.ResponseProto)
	}
	if !strings.HasPrefix(f.ResponseHeaders, "HTTP/2.0 200 ") {
		t.Fatalf("response headers = %q", f.ResponseHeaders)
	}
	if f.RequestBodyError != "" || f.ResponseBodyError != "" {
		t.Fatalf("body errors: request %q, response %q", f.RequestBodyError, f.ResponseBodyError)
	}
	if got := grpcFrames(t, f.RequestBody); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("captured request messages = %q", got)
	}
	if got := grpcFrames(t, f.ResponseBody); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("captured response messages = %q", got)
	}
	if got := f.RequestHeader.Get("X-Client-Meta"); got != "client-value" {
		t.Fatalf("captured request metadata = %q", got)
	}
	if got := f.ResponseHeader.Get("X-Server-Header"); got != "header-hello" {
		t.Fatalf("captured response header = %q", got)
	}
	if got := f.ResponseHeader.Get("Grpc-Status"); got != "" {
		t.Fatalf("the status was merged into the headers: %q", got)
	}
	if got := f.ResponseTrailer.Get("Grpc-Status"); got != "0" {
		t.Fatalf("captured status = %q", got)
	}
	if got := f.ResponseTrailer.Get("X-Server-Trailer"); got != "trailer-hello" {
		t.Fatalf("captured response trailer = %q", got)
	}
	if strings.Contains(f.ResponseHeaders, "Grpc-Status") {
		t.Fatalf("the status is in the header text: %q", f.ResponseHeaders)
	}
}

func TestGRPCStatusWithBinaryDetails(t *testing.T) {
	s, conn := startGRPCThroughProxy(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	err := conn.Invoke(ctx, grpcMethod("Echo"), wrapperspb.String("fail:boom"), new(wrapperspb.StringValue))
	got, ok := status.FromError(err)
	if !ok || got.Code() != codes.InvalidArgument || got.Message() != "no such value" {
		t.Fatalf("call failed with %v", err)
	}
	if len(got.Details()) != 1 {
		t.Fatalf("details = %v", got.Details())
	}
	detail, ok := got.Details()[0].(*wrapperspb.StringValue)
	if !ok || detail.Value != "boom" {
		t.Fatalf("detail = %#v", got.Details()[0])
	}

	f := waitFlowFor(t, s, "/Echo")
	if f.Status != http.StatusOK {
		t.Fatalf("status = %d", f.Status)
	}
	if got := f.ResponseTrailer.Get("Grpc-Status"); got != "3" {
		t.Fatalf("captured status = %q", got)
	}
	if got := f.ResponseTrailer.Get("Grpc-Message"); got != "no such value" {
		t.Fatalf("captured message = %q", got)
	}
	if got := f.ResponseHeader.Get("Grpc-Status"); got != "" {
		t.Fatalf("the status was merged into the headers: %q", got)
	}
	encoded := f.ResponseTrailer.Get("Grpc-Status-Details-Bin")
	if encoded == "" {
		t.Fatal("the binary details are missing from the trailers")
	}
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode grpc-status-details-bin %q: %v", encoded, err)
	}
	details := new(statuspb.Status)
	if err := proto.Unmarshal(raw, details); err != nil {
		t.Fatalf("unmarshal the details: %v", err)
	}
	if details.Code != int32(codes.InvalidArgument) || details.Message != "no such value" || len(details.Details) != 1 {
		t.Fatalf("details = %v", details)
	}
}

func TestGRPCServerStreaming(t *testing.T) {
	s, conn := startGRPCThroughProxy(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, grpcMethod("ServerStream"))
	if err != nil {
		t.Fatalf("start a server stream: %v", err)
	}
	if err := stream.SendMsg(wrapperspb.String("x")); err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("close send: %v", err)
	}
	var received []string
	for {
		message := new(wrapperspb.StringValue)
		err := stream.RecvMsg(message)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("receive: %v", err)
		}
		received = append(received, message.Value)
	}
	want := []string{"msg-0-x", "msg-1-x", "msg-2-x"}
	if strings.Join(received, ",") != strings.Join(want, ",") {
		t.Fatalf("client received %v", received)
	}

	f := waitFlowFor(t, s, "/ServerStream")
	if got := grpcFrames(t, f.ResponseBody); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("captured messages = %v", got)
	}
	if got := f.ResponseHeader.Get("X-Server-Header"); got != "stream" {
		t.Fatalf("captured header = %q", got)
	}
	if got := f.ResponseTrailer.Get("X-Server-Trailer"); got != "stream-end" {
		t.Fatalf("captured trailer = %q", got)
	}
	if got := f.ResponseTrailer.Get("Grpc-Status"); got != "0" {
		t.Fatalf("captured status = %q", got)
	}
}

func TestGRPCClientStreaming(t *testing.T) {
	s, conn := startGRPCThroughProxy(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{ClientStreams: true}, grpcMethod("ClientStream"))
	if err != nil {
		t.Fatalf("start a client stream: %v", err)
	}
	for _, value := range []string{"a", "b", "c"} {
		if err := stream.SendMsg(wrapperspb.String(value)); err != nil {
			t.Fatalf("send %q: %v", value, err)
		}
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("close send: %v", err)
	}
	response := new(wrapperspb.StringValue)
	if err := stream.RecvMsg(response); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if response.Value != "3" {
		t.Fatalf("response = %q", response.Value)
	}

	f := waitFlowFor(t, s, "/ClientStream")
	if got := grpcFrames(t, f.RequestBody); strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("captured request messages = %v", got)
	}
	if got := grpcFrames(t, f.ResponseBody); len(got) != 1 || got[0] != "3" {
		t.Fatalf("captured response messages = %v", got)
	}
	if got := f.ResponseTrailer.Get("Grpc-Status"); got != "0" {
		t.Fatalf("captured status = %q", got)
	}
}

// TestGRPCBidirectionalStreamsBeforeTheRequestEnds is the regression test for
// buffering a gRPC request: the server answers the first message while the
// client still holds its side of the stream open.
func TestGRPCBidirectionalStreamsBeforeTheRequestEnds(t *testing.T) {
	s, conn := startGRPCThroughProxy(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{ClientStreams: true, ServerStreams: true}, grpcMethod("Bidi"))
	if err != nil {
		t.Fatalf("start a bidirectional stream: %v", err)
	}
	if err := stream.SendMsg(wrapperspb.String("one")); err != nil {
		t.Fatalf("send: %v", err)
	}
	// No CloseSend before this receive: the reply must arrive while the
	// request side is still open.
	if got := recvWithin(t, stream, 10*time.Second); got.Value != "echo-one" {
		t.Fatalf("first reply = %q", got.Value)
	}
	if err := stream.SendMsg(wrapperspb.String("two")); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := recvWithin(t, stream, 10*time.Second); got.Value != "echo-two" {
		t.Fatalf("second reply = %q", got.Value)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("close send: %v", err)
	}
	last := new(wrapperspb.StringValue)
	if err := stream.RecvMsg(last); !errors.Is(err, io.EOF) {
		t.Fatalf("end of stream = %v", err)
	}

	f := waitFlowFor(t, s, "/Bidi")
	if got := grpcFrames(t, f.RequestBody); strings.Join(got, ",") != "one,two" {
		t.Fatalf("captured request messages = %v", got)
	}
	if got := grpcFrames(t, f.ResponseBody); strings.Join(got, ",") != "echo-one,echo-two" {
		t.Fatalf("captured response messages = %v", got)
	}
	if got := f.ResponseTrailer.Get("Grpc-Status"); got != "0" {
		t.Fatalf("captured status = %q", got)
	}
}

func TestGRPCGzipCompressedMessages(t *testing.T) {
	s, conn := startGRPCThroughProxy(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, grpcMethod("ServerStream"), grpc.UseCompressor(grpcgzip.Name))
	if err != nil {
		t.Fatalf("start a compressed stream: %v", err)
	}
	if err := stream.SendMsg(wrapperspb.String("z")); err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("close send: %v", err)
	}
	var received []string
	for {
		message := new(wrapperspb.StringValue)
		err := stream.RecvMsg(message)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("receive: %v", err)
		}
		received = append(received, message.Value)
	}
	if len(received) != 3 {
		t.Fatalf("client received %v", received)
	}

	f := waitFlowFor(t, s, "/ServerStream")
	if got := f.ResponseHeader.Get("Grpc-Encoding"); got != "gzip" {
		t.Fatalf("captured grpc-encoding = %q", got)
	}
	// The capture keeps the frames as they went on the wire, compressed flag
	// included: every message is still whole, which is what the flag and the
	// lengths say.
	for _, body := range []string{f.RequestBody, f.ResponseBody} {
		if len(body) < 5 {
			t.Fatalf("body = %q", body)
		}
		if flag := body[0]; flag != 1 {
			t.Fatalf("a compressed message has flag %d", flag)
		}
	}
	if got := grpcFrames(t, f.ResponseBody); len(got) != 3 || got[0] != "msg-0-z" {
		t.Fatalf("captured messages = %v", got)
	}
	if got := grpcFrames(t, f.RequestBody); len(got) != 1 || got[0] != "z" {
		t.Fatalf("captured request messages = %v", got)
	}
}

func TestGRPCDeadlineAndCancellation(t *testing.T) {
	s, conn := startGRPCThroughProxy(t)

	// A deadline the server never meets: the client gives up, and the call it
	// leaves behind is still captured, with the part that arrived.
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, grpcMethod("Slow"))
	if err != nil {
		cancel()
		t.Fatalf("start a slow stream: %v", err)
	}
	if err := stream.SendMsg(wrapperspb.String("x")); err != nil {
		cancel()
		t.Fatalf("send: %v", err)
	}
	first := new(wrapperspb.StringValue)
	if err := stream.RecvMsg(first); err != nil || first.Value != "first" {
		cancel()
		t.Fatalf("first message = %q, %v", first.Value, err)
	}
	last := new(wrapperspb.StringValue)
	err = stream.RecvMsg(last)
	cancel()
	if got, ok := status.FromError(err); !ok || got.Code() != codes.DeadlineExceeded {
		t.Fatalf("end of the slow stream = %v", err)
	}
	slow := waitFlowFor(t, s, "/Slow")
	if got := grpcFrames(t, slow.ResponseBody); len(got) != 1 || got[0] != "first" {
		t.Fatalf("captured messages = %v", got)
	}
	// The call did not finish on its own: either the body was cut off when the
	// client gave up, or the server ended the stream with the cancellation
	// status after it saw the stream reset.
	if slow.ResponseBodyError == "" && slow.ResponseTrailer.Get("Grpc-Status") == "0" {
		t.Fatalf("a deadline was captured as a successful call: %q", slow.ResponseBody)
	}

	// A call the client cancels: the flow is published once the stream is
	// gone, rather than waiting for a body that will never end.
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	bidi, err := conn.NewStream(ctx, &grpc.StreamDesc{ClientStreams: true, ServerStreams: true}, grpcMethod("Bidi"))
	if err != nil {
		t.Fatalf("start a bidirectional stream: %v", err)
	}
	if err := bidi.SendMsg(wrapperspb.String("one")); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := recvWithin(t, bidi, 10*time.Second); got.Value != "echo-one" {
		t.Fatalf("reply = %q", got.Value)
	}
	cancel()
	cancelled := waitFlowFor(t, s, "/Bidi")
	if got := grpcFrames(t, cancelled.ResponseBody); len(got) != 1 || got[0] != "echo-one" {
		t.Fatalf("captured messages = %v", got)
	}
}

func TestGRPCTrailersOnlyError(t *testing.T) {
	s, conn := startGRPCThroughProxy(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, grpcMethod("Fail"))
	if err != nil {
		t.Fatalf("start a failing stream: %v", err)
	}
	if err := stream.SendMsg(wrapperspb.String("x")); err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("close send: %v", err)
	}
	message := new(wrapperspb.StringValue)
	err = stream.RecvMsg(message)
	got, ok := status.FromError(err)
	if !ok || got.Code() != codes.NotFound || got.Message() != "no such stream" {
		t.Fatalf("the call failed with %v", err)
	}

	f := waitFlowFor(t, s, "/Fail")
	if got := f.ResponseHeader.Get("Content-Type"); got != "application/grpc" {
		t.Fatalf("captured content type = %q", got)
	}
	if f.ResponseBody != "" || f.ResponseBodySize != 0 {
		t.Fatalf("a trailers-only call kept a body: %q (%d bytes)", f.ResponseBody, f.ResponseBodySize)
	}
	if got := f.ResponseTrailer.Get("Grpc-Status"); got != "5" {
		t.Fatalf("captured status = %q", got)
	}
	if got := f.ResponseTrailer.Get("Grpc-Message"); got != "no such stream" {
		t.Fatalf("captured message = %q", got)
	}
	if got := f.ResponseHeader.Get("Grpc-Status"); got != "" {
		t.Fatalf("the status stayed in the headers: %q", got)
	}
}

func TestGRPCLargeResponseIsKeptWhole(t *testing.T) {
	s, conn := startGRPCThroughProxy(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	response := new(wrapperspb.StringValue)
	if err := conn.Invoke(ctx, grpcMethod("Echo"), wrapperspb.String("big:x"), response); err != nil {
		t.Fatalf("unary call: %v", err)
	}
	if len(response.Value) != 1<<20 {
		t.Fatalf("response is %d bytes", len(response.Value))
	}

	// The captured body is the gRPC framing around the protobuf message the
	// server sent, so its length is the message, its length prefix, and the
	// frame header.
	message, err := proto.Marshal(response)
	if err != nil {
		t.Fatalf("marshal the response: %v", err)
	}
	want := 5 + len(message)

	f := waitFlowFor(t, s, "/Echo")
	if f.ResponseBodyError != "" {
		t.Fatalf("body error: %q", f.ResponseBodyError)
	}
	if f.ResponseBodySize != want {
		t.Fatalf("captured body size = %d, want %d", f.ResponseBodySize, want)
	}
	if f.ResponseBodyFile == "" {
		t.Fatal("a body past the inline cap was not spilled to a file")
	}
	whole, err := os.ReadFile(f.ResponseBodyFile)
	if err != nil {
		t.Fatalf("read the spilled body: %v", err)
	}
	if len(whole) != want {
		t.Fatalf("the spilled body is %d bytes, want %d", len(whole), want)
	}
	if got := grpcFrames(t, string(whole)); len(got) != 1 || len(got[0]) != 1<<20 {
		t.Fatalf("the spilled body holds %d messages", len(got))
	}
	if got := f.ResponseTrailer.Get("Grpc-Status"); got != "0" {
		t.Fatalf("captured status = %q", got)
	}
}

// TestGRPCWebIsCapturedAsOrdinaryHTTP states the boundary: gRPC-Web is not a
// stream. A browser sends one complete POST, so the proxy buffers it like any
// other exchange and keeps all of it, rather than treating it as gRPC.
func TestGRPCWebIsCapturedAsOrdinaryHTTP(t *testing.T) {
	body := "\x00\x00\x00\x00\x05hello"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read the request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/grpc-web+proto")
		_, _ = w.Write(request)
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	request, err := http.NewRequest(http.MethodPost, upstream.URL+"/web", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	request.Header.Set("Content-Type", "application/grpc-web+proto")
	resp, err := clientFor(t, s, nil).Do(request)
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	response, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(response) != body {
		t.Fatalf("response = %q, %v", response, err)
	}

	f := waitFlowFor(t, s, "/web")
	if f.ResponseProto != "HTTP/1.1" {
		t.Fatalf("response protocol = %q", f.ResponseProto)
	}
	if f.RequestBody != body || f.ResponseBody != body {
		t.Fatalf("captured bodies: request %q, response %q", f.RequestBody, f.ResponseBody)
	}
	if f.RequestBodyError != "" || f.ResponseBodyError != "" {
		t.Fatalf("body errors: request %q, response %q", f.RequestBodyError, f.ResponseBodyError)
	}
}

// TestHTTP1ChunkedTrailersStayOutOfHeaders is the control for the trailer
// plumbing: an ordinary HTTP/1.1 exchange, whose trailers must reach the client
// and the capture after the body, in both directions.
func TestHTTP1ChunkedTrailersStayOutOfHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read the request body: %v", err)
		}
		w.Header().Set("Trailer", "X-Res-Trailer")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "%s|%s", body, r.Trailer.Get("X-Req-Trailer"))
		w.Header().Add("X-Res-Trailer", "one")
		w.Header().Add("X-Res-Trailer", "two")
	}))
	defer upstream.Close()

	s, _ := startServer(t)
	conn, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatalf("dial the proxy: %v", err)
	}
	defer conn.Close()
	request := fmt.Sprintf("POST %s/h1 HTTP/1.1\r\nHost: %s\r\nTransfer-Encoding: chunked\r\nTrailer: X-Req-Trailer\r\n\r\n"+
		"5\r\nhello\r\n0\r\nX-Req-Trailer: from-client\r\n\r\n", upstream.URL, strings.TrimPrefix(upstream.URL, "http://"))
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatalf("write the request: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodPost})
	if err != nil {
		t.Fatalf("read the response: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read the response body: %v", err)
	}
	if string(body) != "hello|from-client" {
		t.Fatalf("response body = %q: the request trailers did not reach the server", body)
	}
	if got := resp.Trailer.Values("X-Res-Trailer"); strings.Join(got, ",") != "one,two" {
		t.Fatalf("response trailers at the client = %v", got)
	}

	f := waitFlowFor(t, s, "/h1")
	if f.ResponseProto != "HTTP/1.1" {
		t.Fatalf("response protocol = %q", f.ResponseProto)
	}
	if got := f.RequestTrailer.Values("X-Req-Trailer"); strings.Join(got, ",") != "from-client" {
		t.Fatalf("captured request trailers = %v", got)
	}
	if got := f.ResponseTrailer.Values("X-Res-Trailer"); strings.Join(got, ",") != "one,two" {
		t.Fatalf("captured response trailers = %v", got)
	}
	if got := f.RequestHeader.Get("X-Req-Trailer"); got != "" {
		t.Fatalf("a request trailer was merged into the headers: %q", got)
	}
	if got := f.ResponseHeader.Get("X-Res-Trailer"); got != "" {
		t.Fatalf("a response trailer was merged into the headers: %q", got)
	}
	if strings.Contains(f.ResponseHeaders, "X-Res-Trailer") {
		t.Fatalf("a trailer is in the header text: %q", f.ResponseHeaders)
	}
	// A local rule publishes without an upstream Response callback. The
	// completed request's trailers must still be part of that captured flow.
	s.SetRules([]Rule{{Type: RuleBlock, Match: "/h1"}})
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}
	blocked, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodPost})
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, blocked.Body)
	blocked.Body.Close()
	f = waitFlowFor(t, s, "/h1")
	if blocked.StatusCode != http.StatusForbidden || f.Status != http.StatusForbidden || f.RequestTrailer.Get("X-Req-Trailer") != "from-client" {
		t.Fatalf("local response lost request trailers: status %d, capture %+v", blocked.StatusCode, f)
	}
}
