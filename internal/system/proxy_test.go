package system

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeCommands answers platform helpers from a table, so a readback runs
// against fixed output instead of this machine's settings.
func fakeCommands(t *testing.T, replies map[string]string) outputFunc {
	t.Helper()
	return func(name string, args ...string) ([]byte, error) {
		key := strings.Join(append([]string{name}, args...), " ")
		reply, ok := replies[key]
		if !ok {
			return nil, fmt.Errorf("unexpected command %q", key)
		}
		return []byte(reply), nil
	}
}

func endpoint(host string, port int) ProxyEndpoint {
	return ProxyEndpoint{Enabled: true, Host: host, Port: port}
}

func manual(scope string, http, https ProxyEndpoint) ProxyConfig {
	return ProxyConfig{Scope: scope, Mode: ProxyModeManual, HTTP: http, HTTPS: https}
}

func TestCheckProxyDescribesWhatTheOSWouldApply(t *testing.T) {
	target := "127.0.0.1:7890"
	proxy := endpoint("127.0.0.1", 7890)
	for _, tc := range []struct {
		name    string
		target  string
		configs []ProxyConfig
		err     error
		want    ProxyStatus
	}{
		{"every scope points at this app", target, []ProxyConfig{manual("Wi-Fi", proxy, proxy)}, nil, ProxyMatches},
		{"another address", target, []ProxyConfig{manual("Wi-Fi", endpoint("10.0.0.2", 8080), endpoint("10.0.0.2", 8080))}, nil, ProxyMismatch},
		{"only HTTP is set", target, []ProxyConfig{manual("Wi-Fi", proxy, ProxyEndpoint{})}, nil, ProxyMismatch},
		{"HTTPS differs", target, []ProxyConfig{manual("Wi-Fi", proxy, endpoint("127.0.0.1", 7891))}, nil, ProxyMismatch},
		{"one service left behind", target, []ProxyConfig{manual("Wi-Fi", proxy, proxy), {Scope: "Ethernet"}}, nil, ProxyMismatch},
		{"off", target, []ProxyConfig{{Scope: "Wi-Fi"}, {Scope: "Ethernet"}}, nil, ProxyOff},
		{"automatic wins over a matching manual proxy", target, []ProxyConfig{
			manual("Wi-Fi", proxy, proxy),
			{Scope: "Ethernet", Mode: ProxyModeAutomatic, PAC: "http://pac.example/proxy.pac"},
		}, nil, ProxyAutomatic},
		{"off when no fixed proxy applies", "", []ProxyConfig{{Scope: "Wi-Fi"}}, nil, ProxyOff},
		{"a fixed proxy remains after turning off", "", []ProxyConfig{manual("Wi-Fi", proxy, proxy)}, nil, ProxyMismatch},
		{"unreadable", target, nil, errors.New("gsettings not found"), ProxyUnknown},
		{"no scope reports a setting", target, nil, nil, ProxyUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check := checkProxy(tc.target, tc.configs, tc.err)
			if check.Status != tc.want {
				t.Fatalf("status = %v, want %v (configs %+v)", check.Status, tc.want, tc.configs)
			}
			if tc.err != nil && !errors.Is(check.Err, tc.err) {
				t.Fatalf("err = %v, want %v", check.Err, tc.err)
			}
			if tc.want == ProxyUnknown && check.Err == nil {
				t.Fatal("an unknown configuration must say why it is unknown")
			}
			if check.Target != tc.target {
				t.Fatalf("target = %q, want %q", check.Target, tc.target)
			}
		})
	}
}

func TestProxyEndpointNamesTheServer(t *testing.T) {
	for _, tc := range []struct {
		name string
		ep   ProxyEndpoint
		addr string
		text string
	}{
		{"off", ProxyEndpoint{Host: "127.0.0.1", Port: 7890}, "127.0.0.1:7890", "off"},
		{"IPv4", endpoint("127.0.0.1", 7890), "127.0.0.1:7890", "127.0.0.1:7890"},
		{"IPv6", endpoint("::1", 7890), "[::1]:7890", "[::1]:7890"},
		{"bracketed IPv6", endpoint("[::1]", 7890), "[::1]:7890", "[::1]:7890"},
		{"no server", ProxyEndpoint{Enabled: true}, "", "on, no server"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ep.Addr(); got != tc.addr {
				t.Fatalf("Addr() = %q, want %q", got, tc.addr)
			}
			if got := tc.ep.String(); got != tc.text {
				t.Fatalf("String() = %q, want %q", got, tc.text)
			}
		})
	}
}

func TestParseEndpointAddr(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want ProxyEndpoint
	}{
		{"127.0.0.1:7890", endpoint("127.0.0.1", 7890)},
		{" localhost:8080 ", endpoint("localhost", 8080)},
		{"127.0.0.1", ProxyEndpoint{}},
		{"127.0.0.1:", ProxyEndpoint{}},
		{"", ProxyEndpoint{}},
	} {
		if got := parseEndpointAddr(tc.addr); got != tc.want {
			t.Errorf("parseEndpointAddr(%q) = %+v, want %+v", tc.addr, got, tc.want)
		}
	}
}
