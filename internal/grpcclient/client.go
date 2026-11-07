// Package grpcclient invokes descriptor-defined gRPC methods without generated
// code. Connections and calls belong to the caller; nothing reconnects a call
// or replays a message after failure.
package grpcclient

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const MaxMessageBytes = 1 << 20

// Dial accepts an explicit http:// (plaintext HTTP/2) or https:// endpoint.
// TLS verifies the server using system roots plus ca (a DER certificate).
// HTTPS uses the capture proxy when supplied. Plaintext h2c goes directly:
// the capture engine does not support h2c. No ambient proxy is used.
func Dial(endpoint, proxy string, ca []byte) (*grpc.ClientConn, error) {
	u, err := parseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	var tlsConfig *tls.Config
	if u.Scheme == "https" {
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if len(ca) != 0 {
			cert, err := x509.ParseCertificate(ca)
			if err != nil {
				return nil, fmt.Errorf("proxy CA: %w", err)
			}
			roots.AddCert(cert)
		}
		tlsConfig = &tls.Config{RootCAs: roots}
	}
	return dial(u, proxy, tlsConfig)
}

// DialWithTLS is Dial with a caller-built client TLS configuration: it can
// present a client certificate and trust the same custom roots as a request
// send. The endpoint still supplies the server name and TLS 1.2 minimum.
func DialWithTLS(endpoint, proxy string, tlsConfig *tls.Config) (*grpc.ClientConn, error) {
	u, err := parseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "https" {
		if tlsConfig == nil {
			roots, err := x509.SystemCertPool()
			if err != nil {
				roots = x509.NewCertPool()
			}
			tlsConfig = &tls.Config{RootCAs: roots}
		} else {
			tlsConfig = tlsConfig.Clone()
		}
		if tlsConfig.ServerName == "" {
			tlsConfig.ServerName = u.Hostname()
		}
		if tlsConfig.MinVersion == 0 {
			tlsConfig.MinVersion = tls.VersionTLS12
		}
	}
	return dial(u, proxy, tlsConfig)
}

func parseEndpoint(endpoint string) (*url.URL, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("use an http:// or https:// gRPC endpoint without a path, credentials or query")
	}
	return u, nil
}

func dial(u *url.URL, proxy string, tlsConfig *tls.Config) (*grpc.ClientConn, error) {
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	target := net.JoinHostPort(u.Hostname(), port)
	var transport credentials.TransportCredentials = insecure.NewCredentials()
	if u.Scheme == "https" {
		if tlsConfig.ServerName == "" {
			tlsConfig.ServerName = u.Hostname()
		}
		if tlsConfig.MinVersion == 0 {
			tlsConfig.MinVersion = tls.VersionTLS12
		}
		transport = credentials.NewTLS(tlsConfig)
	}
	return grpc.NewClient("passthrough:///"+target,
		grpc.WithTransportCredentials(transport),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			if u.Scheme == "https" && proxy != "" {
				return dialTunnel(ctx, proxy, addr)
			}
			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		}),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(MaxMessageBytes), grpc.MaxCallSendMsgSize(MaxMessageBytes)),
		grpc.WithDisableRetry())
}

func dialTunnel(ctx context.Context, proxy, target string) (net.Conn, error) {
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", proxy)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			c.Close()
		}
	}()
	// DialContext only cancels dialing; the CONNECT exchange also needs to
	// unblock on cancellation. Wait for its callback before clearing deadlines.
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { c.SetDeadline(time.Now()); close(stopped) })
	defer func() {
		if !stop() {
			<-stopped
		}
		if ok {
			c.SetDeadline(time.Time{})
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	if d, has := ctx.Deadline(); has && d.Before(deadline) {
		deadline = d
	}
	c.SetDeadline(deadline)
	r := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: http.Header{}}
	if err = r.Write(c); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(c)
	resp, err := http.ReadResponse(reader, r)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("proxy CONNECT: %s", resp.Status)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ok = true
	return c, nil
}

// Call permits one receiver concurrently with serialized Send/CloseSend.
// It validates JSON against the method's input descriptor before writing.
type Call struct {
	stream grpc.ClientStream
	method protoreflect.MethodDescriptor
	types  *dynamicpb.Types
	write  sync.Mutex
	sent   int
	closed bool
	recv   int
}

func validateMetadata(md metadata.MD) error {
	for key, values := range md {
		// These belong to the transport; grpc-go would silently discard
		// authored values for them rather than forward them as metadata.
		if key == "" || strings.HasPrefix(key, "grpc-") || strings.HasPrefix(key, ":") || key != strings.ToLower(key) ||
			key == "content-type" || key == "user-agent" || key == "te" {
			return fmt.Errorf("invalid or reserved metadata key %q", key)
		}
		for _, r := range key {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.", r)) {
				return fmt.Errorf("invalid metadata key %q", key)
			}
		}
		if !strings.HasSuffix(key, "-bin") {
			for _, value := range values {
				if strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r > 0x7e }) {
					return fmt.Errorf("metadata %q must be printable ASCII", key)
				}
			}
		}
	}
	return nil
}

func Start(ctx context.Context, conn grpc.ClientConnInterface, method protoreflect.MethodDescriptor, types *dynamicpb.Types, md metadata.MD) (*Call, error) {
	if method == nil {
		return nil, fmt.Errorf("select a gRPC method")
	}
	if err := validateMetadata(md); err != nil {
		return nil, err
	}
	path := "/" + string(method.Parent().FullName()) + "/" + string(method.Name())
	stream, err := conn.NewStream(metadata.NewOutgoingContext(ctx, md.Copy()), &grpc.StreamDesc{
		ClientStreams: method.IsStreamingClient(), ServerStreams: method.IsStreamingServer(),
	}, path)
	if err != nil {
		return nil, err
	}
	return &Call{stream: stream, method: method, types: types}, nil
}

func (c *Call) Send(data []byte) error {
	c.write.Lock()
	defer c.write.Unlock()
	if c.closed || c.sent > 0 && !c.method.IsStreamingClient() {
		return fmt.Errorf("this call no longer accepts messages")
	}
	if len(data) > MaxMessageBytes {
		return fmt.Errorf("message JSON exceeds 1 MiB")
	}
	m := dynamicpb.NewMessage(c.method.Input())
	options := protojson.UnmarshalOptions{RecursionLimit: 64}
	if c.types != nil {
		options.Resolver = c.types
	}
	if err := options.Unmarshal(data, m); err != nil {
		return fmt.Errorf("request JSON: %w", err)
	}
	if err := c.stream.SendMsg(m); err != nil {
		return err
	}
	c.sent++
	return nil
}

func (c *Call) CloseSend() error {
	c.write.Lock()
	defer c.write.Unlock()
	if c.closed {
		return nil
	}
	if c.sent == 0 && !c.method.IsStreamingClient() {
		return fmt.Errorf("send one request before finishing")
	}
	c.closed = true
	return c.stream.CloseSend()
}

// Recv returns the next message as protobuf JSON. EOF means successful final
// status, not just a successful HTTP response. Other errors retain gRPC codes
// and binary status details via status.FromError.
func (c *Call) Recv() ([]byte, error) {
	m := dynamicpb.NewMessage(c.method.Output())
	err := c.stream.RecvMsg(m)
	if errors.Is(err, io.EOF) && !c.method.IsStreamingServer() && c.recv != 1 {
		return nil, status.Error(codes.Internal, "expected exactly one response message")
	}
	if err != nil {
		return nil, err
	}
	c.recv++
	if !c.method.IsStreamingServer() && c.recv > 1 {
		return nil, status.Error(codes.Internal, "received multiple responses for a non-server-streaming method")
	}
	options := protojson.MarshalOptions{Multiline: true, Indent: "  "}
	if c.types != nil {
		options.Resolver = c.types
	}
	return options.Marshal(m)
}

func (c *Call) Header() (metadata.MD, error) { return c.stream.Header() }
func (c *Call) Trailer() metadata.MD         { return c.stream.Trailer().Copy() }
