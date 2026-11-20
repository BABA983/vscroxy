package mockserver

import (
	"testing"

	"github.com/baba983/vscroxy/internal/leakcheck"
)

func TestMain(m *testing.M) {
	// grpc-go starts this GOAWAY worker in
	// internal/transport/http2_server.go's outgoingGoAwayHandler when a server
	// drains a connection. It returns when the transport is done or after its
	// five-second ping timer, but neither Server.GracefulStop, Server.Stop nor
	// ClientConn.Close offers a handle to wait for it. Everything else in the
	// test—the Serve loop and the client connection—is joined on shutdown.
	leakcheck.Run(m, "google.golang.org/grpc/internal/transport.(*http2Server).outgoingGoAwayHandler.func1")
}
