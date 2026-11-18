package app

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestGuardedRepanicsInTests(t *testing.T) {
	defer func() {
		if got := recover(); got != "background bug" {
			t.Errorf("guarded swallowed the panic: %v", got)
		}
	}()
	guarded(func() { panic("background bug") })
	t.Fatal("guarded returned instead of re-panicking")
}

func TestReportUnexpectedPanicLogsStackWithoutPanicValue(t *testing.T) {
	var log bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&log, nil)))
	defer slog.SetDefault(previous)
	var reported string
	setUnexpectedErrorHandler(func(what string, err error) {
		reported = what + ": " + err.Error()
	})
	defer setUnexpectedErrorHandler(nil)

	reportUnexpectedPanic("secret request content", []byte("stack: worker.go:12"))

	if !strings.Contains(log.String(), "stack: worker.go:12") {
		t.Errorf("stack missing from log: %s", log.String())
	}
	if strings.Contains(log.String(), "secret request content") {
		t.Errorf("panic value leaked into log: %s", log.String())
	}
	if !strings.Contains(reported, "background goroutine panicked (string)") {
		t.Errorf("fail did not receive the panic: %q", reported)
	}
}
