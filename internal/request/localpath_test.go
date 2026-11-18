package request

import (
	"strings"
	"testing"
)

func TestLocalRequestPathRejectsRemoteAndDevicePaths(t *testing.T) {
	for _, path := range []string{
		`\\attacker\share\x`,
		`//attacker/share/x`,
		`\/attacker/share/x`,
		`\\?\C:\Windows\file`,
		`//?/C:/Windows/file`,
		`\\?\UNC\attacker\share\x`,
		`\\.\PhysicalDrive0`,
		`//./pipe/name`,
		`\??\UNC\attacker\share\x`,
		`/??/C:/Windows/file`,
		`\??\C:\Windows\file`,
	} {
		if _, err := localRequestPath(path, "body file"); err == nil || !strings.Contains(err.Error(), "UNC") {
			t.Errorf("localRequestPath(%q) = %v, want a clear UNC error", path, err)
		}
	}
	for _, path := range []string{
		`body.bin`,
		`/tmp/body.bin`,
		`C:\Users\me\body.bin`,
		`\relative\body.bin`,
	} {
		if got, err := localRequestPath(path, "body file"); err != nil || got != path {
			t.Errorf("localRequestPath(%q) = %q, %v; want the path unchanged", path, got, err)
		}
	}
}

func TestPrepareRejectsRemoteBodyAndFormFiles(t *testing.T) {
	remote := `\\attacker\share\x`
	cases := []struct {
		name string
		req  Request
	}{
		{"binary body", Request{URL: "http://example.test", BodyMode: "Binary", BodyFile: "{{upload}}"}},
		{"multipart file", Request{URL: "http://example.test", BodyMode: "Multipart", Form: []Field{{Name: "file", Value: "{{upload}}", File: true}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.req.Prepare(map[string]string{"upload": remote})
			if err == nil || !strings.Contains(err.Error(), "UNC") {
				t.Fatalf("Prepare() = %v, want a clear UNC error", err)
			}
		})
	}
}
