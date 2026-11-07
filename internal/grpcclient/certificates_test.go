package grpcclient

import (
	"context"
	"crypto/tls"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/testcert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
)

// mtlsEndpoint serves gRPC over TLS and requires a client certificate signed
// by ca. No service is registered: a READY connection proves the handshake.
func mtlsEndpoint(t *testing.T, ca *testcert.CA) string {
	t.Helper()
	_, _, server := ca.Issue(t, "localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, false)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverGRPC := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{server},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.Pool(),
	})))
	go serverGRPC.Serve(listener)
	t.Cleanup(func() {
		serverGRPC.Stop()
		listener.Close()
	})
	return "https://" + listener.Addr().String()
}

func waitReady(t *testing.T, conn *grpc.ClientConn) error {
	t.Helper()
	conn.Connect()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		state := conn.GetState()
		if state == connectivity.Ready {
			return nil
		}
		if state == connectivity.TransientFailure || state == connectivity.Shutdown {
			return context.DeadlineExceeded
		}
		if !conn.WaitForStateChange(ctx, state) {
			return ctx.Err()
		}
	}
}

func TestGRPCClientCertificateAndCustomRoot(t *testing.T) {
	ca := testcert.NewCA(t)
	dir := t.TempDir()
	caPath := ca.WritePEM(t, dir)
	clientPEM, clientKey, _ := ca.Issue(t, "grpc-client", nil, nil, true)
	certPath, keyPath := testcert.WritePair(t, dir, "grpc-client", clientPEM, clientKey)
	endpoint := mtlsEndpoint(t, ca)
	u, _ := url.Parse(endpoint)

	settings := certs.Settings{
		Clients: []certs.Client{{ID: "one", Host: "127.0.0.1", Format: certs.FormatPEM, CertFile: certPath, KeyFile: keyPath}},
		RootCAs: []string{caPath},
	}
	tlsConfig, err := settings.TLSConfig(certs.Address(u), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := DialWithTLS(endpoint, "", tlsConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := waitReady(t, conn); err != nil {
		t.Fatalf("configured gRPC mTLS did not connect: %v", err)
	}

	// No client certificate: the server rejects the handshake.
	noClient, err := (certs.Settings{RootCAs: []string{caPath}}).TLSConfig(certs.Address(u), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	conn, err = DialWithTLS(endpoint, "", noClient)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := waitReady(t, conn); err == nil {
		t.Fatal("gRPC connected without a client certificate")
	}

	// No custom root: the private server certificate is not trusted.
	noRoot, err := (certs.Settings{Clients: settings.Clients}).TLSConfig(certs.Address(u), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	conn, err = DialWithTLS(endpoint, "", noRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := waitReady(t, conn); err == nil {
		t.Fatal("gRPC connected without its custom root")
	}
}
