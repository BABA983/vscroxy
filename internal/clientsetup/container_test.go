package clientsetup

import (
	"slices"
	"strings"
	"testing"
)

func TestContainerRunArgsCarryTheProxyAndTrustBundle(t *testing.T) {
	p := Proxy{Addr: "127.0.0.1:7890", CAPath: "/tmp/ca.pem", BundlePath: "/tmp/bundle.pem"}
	cfg := containerConfig{
		Image: "app:latest", Entrypoint: []string{"/entry"}, Cmd: []string{"serve"},
		WorkingDir: "/app", User: "1000", Env: []string{"HTTP_PROXY=http://old:1", "DB=postgres://db"},
	}
	linux, err := containerRunArgsFor("linux", p, cfg)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(linux, " ")
	for _, want := range []string{
		"--network host", "-e HTTP_PROXY=http://127.0.0.1:7890", "-e HTTPS_PROXY=http://127.0.0.1:7890",
		"-e SSL_CERT_FILE=/vscroxy/ca.pem", "-v /tmp/bundle.pem:/vscroxy/ca.pem:ro",
		"-e DB=postgres://db", "-w /app", "-u 1000", "app:latest /entry serve",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("linux command lacks %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "HTTP_PROXY=http://old:1") {
		t.Fatalf("the original proxy survived:\n%s", joined)
	}
	if slices.Contains(linux, "--add-host") {
		t.Fatalf("host networking does not need a host alias:\n%s", joined)
	}

	mac, err := containerRunArgsFor("darwin", p, cfg)
	if err != nil {
		t.Fatal(err)
	}
	macJoined := strings.Join(mac, " ")
	if !strings.Contains(macJoined, "-e HTTP_PROXY=http://host.docker.internal:7890") || strings.Contains(macJoined, "--network host") {
		t.Fatalf("darwin command:\n%s", macJoined)
	}
	if !strings.Contains(macJoined, "app:latest /entry serve") {
		t.Fatalf("darwin command lost the image or command:\n%s", macJoined)
	}
}
