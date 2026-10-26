package capture

import (
	"bytes"
	"io"
	"testing"
	"time"
)

func TestThrottledReaderPacesReads(t *testing.T) {
	in := bytes.NewReader(make([]byte, 16<<10))
	r := throttleReader(in, Throttle{DownPerSec: 64 << 10}, true)
	start := time.Now()
	if _, err := io.ReadAll(r); err != nil {
		t.Fatalf("read: %v", err)
	}
	// 16 KB at 64 KB/s is a quarter second; allow scheduling slack below it.
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Fatalf("read took %v, want about 250ms", elapsed)
	}
}

func TestThrottledReaderLatency(t *testing.T) {
	r := throttleReader(bytes.NewReader([]byte("hi")), Throttle{Latency: 80 * time.Millisecond}, true)
	start := time.Now()
	if _, err := io.ReadAll(r); err != nil {
		t.Fatalf("read: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 60*time.Millisecond {
		t.Fatalf("read took %v, want the latency first", elapsed)
	}
}

func TestThrottleOffLeavesReader(t *testing.T) {
	in := bytes.NewReader([]byte("hi"))
	if got := throttleReader(in, Throttle{}, true); got != in {
		t.Fatal("an off throttle wrapped the reader")
	}
}
