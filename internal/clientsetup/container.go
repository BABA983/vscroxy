package clientsetup

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// containerConfig is the part of docker inspect's Config that a re-run needs.
type containerConfig struct {
	Image      string
	Entrypoint []string
	Cmd        []string
	WorkingDir string
	User       string
	Env        []string
}

// OpenContainer starts a new container from the image and command of an
// existing one, with this proxy and the app's CA bundle. The original
// container's environment is copied except for proxy and trust variables,
// which the launcher replaces. Volumes, published ports and network settings
// are not reproduced: the point is to watch the app's traffic, not to clone
// its deployment.
func OpenContainer(p Proxy, container string) (Argv, error) {
	container = strings.TrimSpace(container)
	if container == "" {
		return Argv{}, errors.New("container name is empty")
	}
	docker, err := toolPath("docker")
	if err != nil {
		return Argv{}, &MissingToolError{Tool: "docker", Hint: "install Docker and put docker on PATH"}
	}
	cfg, err := inspectContainer(docker, container)
	if err != nil {
		return Argv{}, err
	}
	if p.BundlePath != "" {
		// The terminal bundle is private to this user; a container may run as
		// another user and must still read the mounted trust file.
		if p.BundlePath, err = containerBundle(p.BundlePath); err != nil {
			return Argv{}, err
		}
	}
	args, env, err := containerRunArgs(p, cfg)
	if err != nil {
		return Argv{}, err
	}
	if runtime.GOOS == "windows" {
		comspec := os.Getenv("ComSpec")
		if comspec == "" {
			comspec = "cmd.exe"
		}
		if _, err := toolPath(comspec); err != nil {
			return Argv{}, err
		}
		return Argv{Name: comspec, Args: []string{"/k", WindowsCommandLine(docker, args)},
			Env: MergeEnv(os.Environ(), env), NewConsole: true}, nil
	}
	path, err := writeTerminalScript(containerScript(docker, args, env), "container.sh")
	if err != nil {
		return Argv{}, err
	}
	return OpenScript(path)
}

func inspectContainer(docker, container string) (containerConfig, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, docker, "inspect", "--format", "{{json .Config}}", container).Output()
	if err != nil {
		return containerConfig{}, fmt.Errorf("docker inspect %s: %w", container, err)
	}
	var cfg containerConfig
	if err := json.Unmarshal(out, &cfg); err != nil {
		return containerConfig{}, fmt.Errorf("read docker inspect %s: %w", container, err)
	}
	if strings.TrimSpace(cfg.Image) == "" {
		return containerConfig{}, fmt.Errorf("container %s has no image", container)
	}
	return cfg, nil
}

// containerBundle copies the public CA bundle beside the original with a mode
// a container's user can read. The terminal bundle is 0600 for this user, and
// a bind mount keeps that mode inside the container, where the process may run
// as another user. The bundle holds only public certificates, so 0644 is safe.
func containerBundle(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	out := filepath.Join(filepath.Dir(path), "client-ca-bundle.container.pem")
	if existing, err := os.ReadFile(out); err == nil && bytes.Equal(existing, data) {
		if info, err := os.Stat(out); err == nil && info.Mode().Perm() == 0o644 {
			return out, nil
		}
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Chmod(out, 0o644); err != nil {
		return "", err
	}
	return out, nil
}

func containerRunArgs(p Proxy, cfg containerConfig) ([]string, []string, error) {
	return containerRunArgsFor(runtime.GOOS, p, cfg)
}

// containerRunArgsFor builds the docker arguments and the environment that the
// terminal script exports. The original container's values travel in that
// environment, not in argv: a database password in argv is readable by any
// local user through ps. Proxy and trust variables stay inline because they
// are not secrets.
func containerRunArgsFor(goos string, p Proxy, cfg containerConfig) ([]string, []string, error) {
	_, port, err := p.HostPort()
	if err != nil {
		return nil, nil, err
	}
	proxyHost := "host.docker.internal"
	args := []string{"run", "--rm", "-it"}
	if goos == "linux" {
		// The proxy listens on loopback, which a bridge container cannot
		// reach; host networking puts the container on the host's loopback.
		args = append(args, "--network", "host")
		proxyHost = "127.0.0.1"
	}
	proxy := fmt.Sprintf("http://%s:%d", proxyHost, port)
	env := []string{
		"HTTP_PROXY=" + proxy, "HTTPS_PROXY=" + proxy,
		"http_proxy=" + proxy, "https_proxy=" + proxy,
		"NO_PROXY=localhost,127.0.0.1,::1", "no_proxy=localhost,127.0.0.1,::1",
		"NODE_USE_ENV_PROXY=1",
		"NODE_EXTRA_CA_CERTS=/vscroxy/ca.pem",
	}
	bundle := cmp.Or(p.BundlePath, p.CAPath)
	if bundle == "" {
		return nil, nil, errors.New("CA bundle is empty")
	}
	env = append(env,
		"SSL_CERT_FILE=/vscroxy/ca.pem",
		"REQUESTS_CA_BUNDLE=/vscroxy/ca.pem",
		"CURL_CA_BUNDLE=/vscroxy/ca.pem",
		"GIT_SSL_CAINFO=/vscroxy/ca.pem",
		"PIP_CERT=/vscroxy/ca.pem",
		"DENO_CERT=/vscroxy/ca.pem",
	)
	for _, entry := range env {
		args = append(args, "-e", entry)
	}
	args = append(args, "-v", bundle+":/vscroxy/ca.pem:ro")
	var inherited []string
	for _, entry := range cfg.Env {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || name == "" || containerManagedEnv[name] {
			continue
		}
		args = append(args, "-e", name)
		inherited = append(inherited, entry)
	}
	if cfg.WorkingDir != "" {
		args = append(args, "-w", cfg.WorkingDir)
	}
	if cfg.User != "" {
		args = append(args, "-u", cfg.User)
	}
	if len(cfg.Entrypoint) > 0 {
		// docker run IMAGE ARGS keeps the image's ENTRYPOINT; passing the
		// entrypoint again after the image would run it twice. Override it
		// explicitly and pass only its remaining words, then the command.
		args = append(args, "--entrypoint", cfg.Entrypoint[0])
	}
	args = append(args, cfg.Image)
	if len(cfg.Entrypoint) > 1 {
		args = append(args, cfg.Entrypoint[1:]...)
	}
	args = append(args, cfg.Cmd...)
	return args, inherited, nil
}

// containerScript is the private terminal script: it exports the original
// container's environment for docker to read through `-e NAME`, removes
// itself, and replaces itself with the docker command.
func containerScript(docker string, args, env []string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n# Generated by vscroxy for this container only. No system settings change.\n")
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			continue
		}
		fmt.Fprintf(&b, "export %s=%s\n", name, POSIXQuote(value))
	}
	b.WriteString("rm -f \"$0\"\n")
	b.WriteString("rmdir \"${0%/*}\" 2>/dev/null || true\n")
	b.WriteString("exec " + POSIXCommandLine(docker, args) + "\n")
	return b.String()
}

var containerManagedEnv = map[string]bool{
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "http_proxy": true, "https_proxy": true,
	"NO_PROXY": true, "no_proxy": true, "NODE_USE_ENV_PROXY": true,
	"NODE_EXTRA_CA_CERTS": true, "SSL_CERT_FILE": true, "REQUESTS_CA_BUNDLE": true,
	"CURL_CA_BUNDLE": true, "GIT_SSL_CAINFO": true, "PIP_CERT": true, "DENO_CERT": true,
}
