package store

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
)

var errBusyFile = errors.New("the file is open in another process")

// fakeClock advances only when the retry sleeps, so these tests never wait
// for real time or spin on a scheduling primitive.
type fakeClock struct {
	now   time.Time
	slept time.Duration
}

func withFakeRename(t *testing.T, c *fakeClock, renameErr func(int) error) *int {
	t.Helper()
	oldRename, oldClock, oldSleep, oldRetry := rename, renameClock, renameSleep, retryOnRename
	t.Cleanup(func() { rename, renameClock, renameSleep, retryOnRename = oldRename, oldClock, oldSleep, oldRetry })
	attempts := new(int)
	renameClock = func() time.Time { return c.now }
	renameSleep = func(d time.Duration) { c.now = c.now.Add(d); c.slept += d }
	rename = func(from, to string) error {
		*attempts = *attempts + 1
		return renameErr(*attempts)
	}
	retryOnRename = func(err error) bool { return errors.Is(err, errBusyFile) }
	return attempts
}

func TestReplaceRetriesTransientSharingViolation(t *testing.T) {
	c := &fakeClock{now: time.Unix(0, 0)}
	attempts := withFakeRename(t, c, func(n int) error {
		if n <= 2 {
			return errBusyFile
		}
		return nil
	})
	if err := replaceFile("temp", "target"); err != nil {
		t.Fatal(err)
	}
	if *attempts != 3 || c.slept != 30*time.Millisecond {
		t.Fatalf("attempts = %d, slept = %v; want 3 and 30ms", *attempts, c.slept)
	}
}

func TestReplaceGivesUpAfterTheRetryWindow(t *testing.T) {
	c := &fakeClock{now: time.Unix(0, 0)}
	attempts := withFakeRename(t, c, func(int) error { return errBusyFile })
	err := replaceFile("temp", "target")
	if !errors.Is(err, errBusyFile) {
		t.Fatalf("error = %v, want the rename's error", err)
	}
	if c.slept > renameRetryFor || c.slept < renameRetryFor-renameRetryMax {
		t.Fatalf("slept %v, want the whole %v window", c.slept, renameRetryFor)
	}
	if *attempts < 2 {
		t.Fatalf("only %d attempts", *attempts)
	}
}

func TestReplaceDoesNotRetryOtherErrors(t *testing.T) {
	c := &fakeClock{now: time.Unix(0, 0)}
	other := errors.New("cross-device link")
	attempts := withFakeRename(t, c, func(int) error { return other })
	if err := replaceFile("temp", "target"); !errors.Is(err, other) {
		t.Fatalf("error = %v", err)
	}
	if *attempts != 1 || c.slept != 0 {
		t.Fatalf("attempts = %d, slept = %v; want 1 and none", *attempts, c.slept)
	}
}

func TestOnlyWindowsTreatsSharingErrorsAsRetryable(t *testing.T) {
	for _, code := range []syscall.Errno{5, 32, 33} {
		err := &os.LinkError{Op: "rename", Err: code}
		want := runtime.GOOS == "windows"
		if got := isSharingViolation(err); got != want {
			t.Errorf("errno %d retryable = %v on %s, want %v", code, got, runtime.GOOS, want)
		}
	}
	if isSharingViolation(os.ErrPermission) {
		t.Error("a plain permission error is not a Windows sharing violation")
	}
}
