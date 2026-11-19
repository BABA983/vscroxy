package capture

import (
	"testing"

	"github.com/google/uuid"
	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

func TestSendsAfterCloseAreDropped(t *testing.T) {
	s, err := New("127.0.0.1:0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s.SetBreak(true)
	if decision := s.waitBreakpoint(t.Context(), Breakpoint{}); decision != nil {
		t.Fatalf("breakpoint queued after Close: %+v", decision)
	}
	if _, open := <-s.breakpoints; open {
		t.Fatal("a breakpoint reached the closed queue")
	}

	id := uuid.New()
	s.flowIDs.Set(id, 42)
	s.emitFrame(&proxy.Flow{Id: id}, frameInput{Direction: "in", Kind: "text", Data: []byte("frame")})
	if _, open := <-s.frames; open {
		t.Fatal("a frame reached the closed queue")
	}
}
