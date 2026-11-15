package clientsetup

import (
	"os"
	"testing"

	"github.com/baba983/vscroxy/internal/testcert"
)

// TestAndroidSystemCAInstallsOnADevice is opt-in because it needs an adb
// device with root (an emulator with a Google APIs image, not Play Store). Run
// it with:
//
//	VSCROXY_ANDROID_TEST=1 go test ./internal/clientsetup -run TestAndroidSystemCAInstallsOnADevice -count=1 -v
//
// It installs a throwaway CA in the device's system store, checks the file
// where apps read it, then unmounts the store so the device keeps its own CAs.
func TestAndroidSystemCAInstallsOnADevice(t *testing.T) {
	if os.Getenv("VSCROXY_ANDROID_TEST") == "" {
		t.Skip("set VSCROXY_ANDROID_TEST=1 with a rooted adb device")
	}
	serial := os.Getenv("ANDROID_SERIAL")
	if serial == "" {
		devices, err := AndroidDevices()
		if err != nil {
			t.Fatal(err)
		}
		for _, device := range devices {
			if device.State == "device" {
				serial = device.Serial
				break
			}
		}
		if serial == "" {
			t.Fatal("no adb device in the device state")
		}
	}
	ca := testcert.NewCA(t)
	caPath := writeTemp(t, "ca.pem", ca.PEM)

	root, err := AndroidRootMode(serial)
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() {
		out, err := RunOutput(AndroidZygoteCommand(serial))
		if err != nil {
			t.Logf("cleanup: zygotes: %v", err)
			return
		}
		runAll(AndroidUnmountCACommands(serial, root, ParseAndroidPids(string(out))))
	})

	install, err := InstallAndroidSystemCA(serial, caPath)
	if err != nil {
		t.Fatal(err)
	}
	if install.Name == "" || install.Store == "" {
		t.Fatalf("install: %+v", install)
	}
	verify, err := AndroidCAVerifyCommands(serial, install.Store, install.Name, androidZygotes(t, serial))
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range verify {
		if err := command.Run(); err != nil {
			t.Fatalf("the CA is not where apps read it: %v", err)
		}
	}
}

func androidZygotes(t *testing.T, serial string) []int {
	t.Helper()
	out, err := RunOutput(AndroidZygoteCommand(serial))
	if err != nil {
		t.Fatal(err)
	}
	pids := ParseAndroidPids(string(out))
	if len(pids) == 0 {
		t.Fatal("no zygote process")
	}
	return pids
}

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := t.TempDir() + "/" + name
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
