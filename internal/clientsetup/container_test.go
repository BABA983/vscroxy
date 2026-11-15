package clientsetup

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestContainerBundleIsReadableByAnotherUser(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client-ca-bundle.pem")
	data := []byte("-----BEGIN CERTIFICATE-----\npublic\n-----END CERTIFICATE-----\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := containerBundle(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == path {
		t.Fatal("the container would mount the private bundle")
	}
	info, err := os.Stat(got)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Fatalf("container bundle mode = %o, want 644", perm)
	}
	if content, err := os.ReadFile(got); err != nil || !bytes.Equal(content, data) {
		t.Fatalf("container bundle content = %q, %v", content, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the private bundle changed: %v, %v", info, err)
	}
	if again, err := containerBundle(path); err != nil || again != got {
		t.Fatalf("second call = %q, %v; want %q", again, err, got)
	}
	changed := append(data, []byte("more\n")...)
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := containerBundle(path); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(got); err != nil || !bytes.Equal(content, changed) {
		t.Fatalf("a changed bundle was not copied: %q, %v", content, err)
	}
}

func TestContainerRunArgsCarryTheProxyAndTrustBundle(t *testing.T) {
	p := Proxy{Addr: "127.0.0.1:7890", CAPath: "/tmp/ca.pem", BundlePath: "/tmp/bundle.pem"}
	cfg := containerConfig{
		Image: "app:latest", Entrypoint: []string{"/entry"}, Cmd: []string{"serve"},
		WorkingDir: "/app", User: "1000", Env: []string{"HTTP_PROXY=http://old:1", "DB=postgres://db"},
	}
	linux, inherited, err := containerRunArgsFor("linux", p, cfg)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(linux, " ")
	for _, want := range []string{
		"--network host", "-e HTTP_PROXY=http://127.0.0.1:7890", "-e HTTPS_PROXY=http://127.0.0.1:7890",
		"-e SSL_CERT_FILE=/vscroxy/ca.pem", "-v /tmp/bundle.pem:/vscroxy/ca.pem:ro",
		"-e DB", "--entrypoint /entry", "app:latest serve",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("linux command lacks %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "HTTP_PROXY=http://old:1") {
		t.Fatalf("the original proxy survived:\n%s", joined)
	}
	if strings.Contains(joined, "DB=postgres://db") {
		t.Fatalf("an inherited value is in argv:\n%s", joined)
	}
	if !slices.Equal(inherited, []string{"DB=postgres://db"}) {
		t.Fatalf("inherited environment = %v", inherited)
	}
	if slices.Contains(linux, "--add-host") {
		t.Fatalf("host networking does not need a host alias:\n%s", joined)
	}

	mac, _, err := containerRunArgsFor("darwin", p, cfg)
	if err != nil {
		t.Fatal(err)
	}
	macJoined := strings.Join(mac, " ")
	if !strings.Contains(macJoined, "-e HTTP_PROXY=http://host.docker.internal:7890") || strings.Contains(macJoined, "--network host") {
		t.Fatalf("darwin command:\n%s", macJoined)
	}
	if !strings.Contains(macJoined, "app:latest serve") {
		t.Fatalf("darwin command lost the image or command:\n%s", macJoined)
	}
}

func TestContainerRunArgsDoNotRepeatTheEntrypoint(t *testing.T) {
	p := Proxy{Addr: "127.0.0.1:7890", CAPath: "/tmp/ca.pem"}
	for _, tc := range []struct {
		name string
		cfg  containerConfig
		want string
	}{
		{"single entrypoint", containerConfig{Image: "img", Entrypoint: []string{"docker-entrypoint.sh"}, Cmd: []string{"node", "app.js"}}, "img node app.js"},
		{"multi-word entrypoint", containerConfig{Image: "img", Entrypoint: []string{"/bin/sh", "-c"}, Cmd: []string{"echo hi"}}, "img -c echo hi"},
		{"no entrypoint", containerConfig{Image: "img", Cmd: []string{"node", "app.js"}}, "img node app.js"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, _, err := containerRunArgsFor("linux", p, tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(args, " ")
			if !strings.HasSuffix(joined, tc.want) {
				t.Fatalf("command ends %q, want %q", joined, tc.want)
			}
			if tc.cfg.Entrypoint != nil && len(tc.cfg.Entrypoint) > 0 {
				if !strings.Contains(joined, "--entrypoint "+tc.cfg.Entrypoint[0]) {
					t.Fatalf("missing entrypoint override:\n%s", joined)
				}
				if got := strings.Count(joined, tc.cfg.Entrypoint[0]); got != 1 {
					t.Fatalf("entrypoint appears %d times:\n%s", got, joined)
				}
			}
		})
	}
}

func TestContainerScriptExportsInheritedValuesPrivately(t *testing.T) {
	value := `a b"c$d'`
	p := Proxy{Addr: "127.0.0.1:7890", CAPath: "/tmp/ca.pem"}
	cfg := containerConfig{Image: "img", Cmd: []string{"run"}, Env: []string{"DB_PASSWORD=" + value}}
	args, inherited, err := containerRunArgsFor("linux", p, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), value) {
		t.Fatalf("the secret is in argv: %v", args)
	}
	script := containerScript("/usr/bin/docker", args, inherited)
	if !strings.Contains(script, "export DB_PASSWORD="+POSIXQuote(value)) {
		t.Fatalf("script does not quote the value:\n%s", script)
	}
	if !strings.Contains(script, `rm -f "$0"`) || !strings.Contains(script, "exec /usr/bin/docker run") {
		t.Fatalf("script does not clean up or run docker:\n%s", script)
	}
}
