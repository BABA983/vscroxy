package control

import (
	"context"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func windowsTestUser(t *testing.T) *windows.SID {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return user.User.Sid
}

func setWindowsTestDACL(t *testing.T, path, sddl string, protected bool) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	flags := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if !protected {
		flags = windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, flags, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsPrivateCreationUnderBroadParent(t *testing.T) {
	sid := windowsTestUser(t).String()
	dir := t.TempDir()
	// Children normally inherit Everyone read access from this directory.
	setWindowsTestDACL(t, dir, "D:P(A;OICI;FA;;;"+sid+")(A;OICI;FR;;;WD)", true)
	data, err := json.Marshal(discovery{Version: 1, Address: "127.0.0.1:12345", Token: strings.Repeat("ab", 32)})
	if err != nil {
		t.Fatal(err)
	}
	inherited := filepath.Join(dir, "inherited.json")
	if err := os.WriteFile(inherited, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(inherited); err == nil {
		t.Fatal("Chmod-style permissions hid a broad inherited DACL")
	}
	f, err := createPrivateTemp(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		t.Fatal(err)
	}
	if info.Size() != 0 || !privateFile(f, info) {
		f.Close()
		t.Fatal("empty temporary file was not already private")
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	// Start must use the same protection, not just the test helper.
	path := filepath.Join(dir, "control.json")
	s, err := Start(path, func(context.Context, Command) (any, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.http.CloseIdleConnections()
	if err := c.Call(t.Context(), "test", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsLoadRejectsBroadACLs(t *testing.T) {
	sid := windowsTestUser(t).String()
	for _, tt := range []struct {
		name      string
		dacl      string
		protected bool
	}{
		{"everyone read", "D:P(A;;FA;;;" + sid + ")(A;;FR;;;WD)", true},
		{"authenticated users read", "D:P(A;;FA;;;" + sid + ")(A;;FR;;;AU)", true},
		{"users write", "D:P(A;;FA;;;" + sid + ")(A;;FW;;;BU)", true},
		{"unprotected", "D:(A;;FA;;;" + sid + ")", false},
		{"null DACL", "D:NO_ACCESS_CONTROL", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := discovery{Version: 1, Address: "127.0.0.1:12345", Token: strings.Repeat("ab", 32)}
			path := writeDiscovery(t, d)
			setWindowsTestDACL(t, path, tt.dacl, tt.protected)
			if _, err := Load(path); err == nil {
				t.Fatal("accepted insecure discovery ACL")
			} else if strings.Contains(err.Error(), d.Token) {
				t.Fatal("token leaked in ACL error")
			}
		})
	}
}

func TestWindowsDescriptorPolicy(t *testing.T) {
	user := windowsTestUser(t)
	sid := user.String()
	for _, tt := range []struct {
		name string
		sddl string
		want bool
	}{
		{"private", "O:" + sid + "D:P(A;;FA;;;" + sid + ")", true},
		{"private read only", "O:" + sid + "D:P(A;;FR;;;" + sid + ")", true},
		{"wrong owner", "O:WDD:P(A;;FA;;;" + sid + ")", false},
		{"missing owner", "D:P(A;;FA;;;" + sid + ")", false},
		{"missing DACL", "O:" + sid, false},
		{"empty DACL", "O:" + sid + "D:P", false},
		{"world only", "O:" + sid + "D:P(A;;FA;;;WD)", false},
		{"extra principal", "O:" + sid + "D:P(A;;FA;;;" + sid + ")(A;;FR;;;WD)", false},
		{"unprotected", "O:" + sid + "D:(A;;FA;;;" + sid + ")", false},
		{"inherited ACE", "O:" + sid + "D:P(A;ID;FA;;;" + sid + ")", false},
		{"inheritable ACE", "O:" + sid + "D:P(A;OI;FA;;;" + sid + ")", false},
		{"deny ACE", "O:" + sid + "D:P(D;;FR;;;" + sid + ")", false},
		{"write only", "O:" + sid + "D:P(A;;FW;;;" + sid + ")", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString(tt.sddl)
			if err != nil {
				t.Fatal(err)
			}
			if got := privateWindowsDescriptor(sd, user); got != tt.want {
				t.Fatalf("private descriptor = %v, want %v", got, tt.want)
			}
		})
	}
	if privateWindowsDescriptor(nil, user) {
		t.Fatal("accepted missing security descriptor")
	}
	t.Run("protected null DACL", func(t *testing.T) {
		sd, err := windows.NewSecurityDescriptor()
		if err != nil {
			t.Fatal(err)
		}
		if err := sd.SetOwner(user, false); err != nil {
			t.Fatal(err)
		}
		if err := sd.SetDACL(nil, true, false); err != nil {
			t.Fatal(err)
		}
		if err := sd.SetControl(windows.SE_DACL_PROTECTED, windows.SE_DACL_PROTECTED); err != nil {
			t.Fatal(err)
		}
		if privateWindowsDescriptor(sd, user) {
			t.Fatal("accepted a fully permissive NULL DACL")
		}
	})
}

func TestWindowsChecksOpenedFileNotPath(t *testing.T) {
	f, err := createPrivateTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	// Move the opened file and replace its name with an insecure file. The
	// verifier must still inspect the original handle, not either path's ACL.
	if err := os.Rename(f.Name(), f.Name()+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.Name(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	setWindowsTestDACL(t, f.Name(), "D:NO_ACCESS_CONTROL", true)
	if !privateFile(f, info) {
		t.Fatal("validated a replacement path instead of the opened file")
	}
}
