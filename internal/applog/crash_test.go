package applog

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestTakeCrashReportsOnceAndKeepsTheReport(t *testing.T) {
	root := t.TempDir()
	path := CrashLogPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	report := []byte("panic: boom\n\ngoroutine 1 [running]:\n...\n")
	if err := os.WriteFile(path, report, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := TakeCrash(root)
	if err != nil || got != string(report) {
		t.Fatalf("take = %q, %v; want the report", got, err)
	}
	if data, err := os.ReadFile(path); err != nil || len(data) != 0 {
		t.Fatalf("live crash file = %q, %v; want it cleared", data, err)
	}
	if data, err := os.ReadFile(CrashReportPath(root)); err != nil || !bytes.Equal(data, report) {
		t.Fatalf("kept report = %q, %v", data, err)
	}
	if again, err := TakeCrash(root); err != nil || again != "" {
		t.Fatalf("second take = %q, %v; want nothing", again, err)
	}
}

// TestFatalPanicIsWrittenToTheCrashFile runs a crashing copy of this test
// binary, the same way the desktop entry installs the crash file, so a panic
// that bypasses the test's own output still reaches the log directory.
func TestFatalPanicIsWrittenToTheCrashFile(t *testing.T) {
	if os.Getenv("VSCROXY_CRASH_HELPER") == "1" {
		if err := SetCrashOutput(os.Getenv("VSCROXY_CRASH_ROOT")); err != nil {
			os.Exit(3)
		}
		panic("crash helper panic")
	}
	root := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestFatalPanicIsWrittenToTheCrashFile$")
	cmd.Env = append(os.Environ(), "VSCROXY_CRASH_HELPER=1", "VSCROXY_CRASH_ROOT="+root)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the helper did not crash: %s", out)
	}
	path := CrashLogPath(root)
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(data, []byte("crash helper panic")) {
		t.Fatalf("crash file = %q, %v; helper output: %s", data, err, out)
	}
	if info, err := os.Stat(path); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("crash file mode = %v, %v; want 0600", info, err)
	}
}
