//go:build darwin

package system

import (
	"errors"
	"strings"
	"testing"
)

// networksetupReplies is one machine's networksetup answers.
type networksetupReplies map[string]string

func (r networksetupReplies) out(t *testing.T) outputFunc {
	t.Helper()
	return fakeCommands(t, r)
}

const serviceList = `An asterisk (*) denotes that a network service is disabled.
Wi-Fi
*Bluetooth PAN
Thunderbolt Bridge
`

// serviceReplies describes one enabled service and all of its query replies.
// Multi-service tests must extend both the list and the replies explicitly.
func serviceReplies(service, webEnabled, secureEnabled, pacURL, pacEnabled, discovery string) networksetupReplies {
	return networksetupReplies{
		"networksetup -listallnetworkservices": "An asterisk (*) denotes that a network service is disabled.\n" + service + "\n*Bluetooth PAN\n",
		"networksetup -getwebproxy " + service: `Enabled: ` + webEnabled + `
Server: 127.0.0.1
Port: 7890
Authenticated Proxy Enabled: 0
`,
		"networksetup -getsecurewebproxy " + service: `Enabled: ` + secureEnabled + `
Server: 127.0.0.1
Port: 7890
Authenticated Proxy Enabled: 0
`,
		"networksetup -getautoproxyurl " + service: `URL: ` + pacURL + `
Enabled: ` + pacEnabled + `
`,
		"networksetup -getproxyautodiscovery " + service: `Auto Proxy Discovery: ` + discovery + `
`,
	}
}

func TestReadProxyReadsEveryEnabledService(t *testing.T) {
	replies := serviceReplies("Wi-Fi", "Yes", "Yes", "(null)", "No", "Off")
	replies["networksetup -listallnetworkservices"] = serviceList
	for key, value := range serviceReplies("Thunderbolt Bridge", "Yes", "Yes", "(null)", "No", "Off") {
		if !strings.Contains(key, "listallnetworkservices") {
			replies[key] = value
		}
	}
	configs, err := readProxy(replies.out(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []ProxyConfig{
		manual("Wi-Fi", endpoint("127.0.0.1", 7890), endpoint("127.0.0.1", 7890)),
		manual("Thunderbolt Bridge", endpoint("127.0.0.1", 7890), endpoint("127.0.0.1", 7890)),
	}
	if len(configs) != len(want) {
		t.Fatalf("configs = %+v, want %+v", configs, want)
	}
	for i := range want {
		if configs[i] != want[i] {
			t.Fatalf("service %d = %+v, want %+v", i, configs[i], want[i])
		}
	}
	if got := checkProxy("127.0.0.1:7890", configs, nil).Status; got != ProxyMatches {
		t.Fatalf("status = %v, want a match", got)
	}
}

func TestReadProxyReportsAutomaticConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name      string
		pacURL    string
		pacOn     string
		discovery string
		want      ProxyConfig
	}{
		{
			name: "script", pacURL: "http://pac.example/proxy.pac", pacOn: "Yes", discovery: "Off",
			want: ProxyConfig{Scope: "Wi-Fi", Mode: ProxyModeAutomatic, PAC: "http://pac.example/proxy.pac",
				HTTP: endpoint("127.0.0.1", 7890), HTTPS: endpoint("127.0.0.1", 7890)},
		},
		{
			name: "discovery", pacURL: "(null)", pacOn: "No", discovery: "On",
			want: ProxyConfig{Scope: "Wi-Fi", Mode: ProxyModeAutomatic,
				HTTP: endpoint("127.0.0.1", 7890), HTTPS: endpoint("127.0.0.1", 7890)},
		},
		{
			name: "disabled script is not a conflict", pacURL: "http://pac.example/proxy.pac", pacOn: "No", discovery: "Off",
			want: manual("Wi-Fi", endpoint("127.0.0.1", 7890), endpoint("127.0.0.1", 7890)),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configs, err := readProxy(serviceReplies("Wi-Fi", "Yes", "Yes", tc.pacURL, tc.pacOn, tc.discovery).out(t))
			if err != nil {
				t.Fatal(err)
			}
			if len(configs) != 1 || configs[0] != tc.want {
				t.Fatalf("configs = %+v, want %+v", configs, tc.want)
			}
			wantStatus := ProxyAutomatic
			if tc.pacOn == "No" && tc.discovery == "Off" {
				wantStatus = ProxyMatches
			}
			if got := checkProxy("127.0.0.1:7890", configs, nil).Status; got != wantStatus {
				t.Fatalf("status = %v, want %v; automatic configuration must take precedence over stored manual endpoints", got, wantStatus)
			}
		})
	}
}

func TestReadProxySkipsDisabledServices(t *testing.T) {
	replies := serviceReplies("Wi-Fi", "Yes", "Yes", "(null)", "No", "Off")
	replies["networksetup -listallnetworkservices"] = "An asterisk (*) denotes that a network service is disabled.\n*Wi-Fi\n"
	configs, err := readProxy(replies.out(t))
	if err == nil || configs != nil {
		t.Fatalf("read = %+v, %v; a machine without an enabled service has nothing to verify", configs, err)
	}
}

func TestReadProxyReportsAHalfConfiguredService(t *testing.T) {
	replies := serviceReplies("Wi-Fi", "Yes", "No", "(null)", "No", "Off")
	configs, err := readProxy(replies.out(t))
	if err != nil {
		t.Fatal(err)
	}
	if configs[0].Mode != ProxyModeManual || configs[0].HTTPS.Enabled {
		t.Fatalf("config = %+v; HTTPS must stay off when the service disables it", configs[0])
	}
	if got := checkProxy("127.0.0.1:7890", configs, nil).Status; got != ProxyMismatch {
		t.Fatalf("status = %v; a half-configured service must not report a match", got)
	}
}

func TestReadProxyReportsAMissingTool(t *testing.T) {
	out := func(string, ...string) ([]byte, error) { return nil, errors.New("networksetup not found") }
	if configs, err := readProxy(out); err == nil || configs != nil {
		t.Fatalf("read = %+v, %v; want an unreadable configuration", configs, err)
	}
}

func TestReadProxyDoesNotTreatHelperErrorsAsDisabled(t *testing.T) {
	for _, flag := range []string{"-getwebproxy", "-getsecurewebproxy", "-getautoproxyurl", "-getproxyautodiscovery"} {
		t.Run(flag, func(t *testing.T) {
			replies := serviceReplies("Wi-Fi", "Yes", "Yes", "(null)", "No", "Off")
			replies["networksetup -listallnetworkservices"] = "Wi-Fi\n"
			replies["networksetup "+flag+" Wi-Fi"] = "An error occurred while reading settings.\n"
			configs, err := readProxy(replies.out(t))
			if err == nil || checkProxy("127.0.0.1:7890", configs, err).Status != ProxyUnknown {
				t.Fatalf("read = %+v, %v; malformed readback must not claim success or off", configs, err)
			}
		})
	}
}
