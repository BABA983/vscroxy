package errorsx

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestGuardReportsPanicOutsideTestsWithoutLeakingItsValue(t *testing.T) {
	previousRepanic := repanicInTests
	repanicInTests = func() bool { return false }
	t.Cleanup(func() { repanicInTests = previousRepanic })

	var log bytes.Buffer
	previousLog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&log, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLog) })

	var reported []error
	SetUnexpectedErrorHandler(func(err error) { reported = append(reported, err) })
	t.Cleanup(func() { SetUnexpectedErrorHandler(nil) })

	Guard(func() { panic("secret request content") })

	if len(reported) != 1 || !strings.Contains(reported[0].Error(), "background goroutine panicked (string)") {
		t.Fatalf("reported = %v, want one panic with its type", reported)
	}
	output := log.String()
	if !strings.Contains(output, "errorsx_test.go") {
		t.Errorf("stack missing from log: %s", output)
	}
	if strings.Contains(output, "secret request content") {
		t.Errorf("panic value leaked into log: %s", output)
	}
}
