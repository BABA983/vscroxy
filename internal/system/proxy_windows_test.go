//go:build windows

package system

import "testing"

func TestWinINetConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		flags  uint32
		server string
		pac    string
		want   ProxyStatus
	}{
		{"single server", proxyManual, "127.0.0.1:7890", "", ProxyMatches},
		{"split protocols", proxyManual, "http=127.0.0.1:7890;https=127.0.0.1:7891", "", ProxyMismatch},
		{"HTTP only", proxyManual, "http=127.0.0.1:7890", "", ProxyMismatch},
		{"disabled retains server", proxyDirect, "127.0.0.1:7890", "", ProxyOff},
		{"PAC enabled", proxyManual | proxyAutoURL, "127.0.0.1:7890", "http://pac.example/proxy.pac", ProxyAutomatic},
		{"PAC retained but disabled", proxyManual, "127.0.0.1:7890", "http://pac.example/proxy.pac", ProxyMatches},
		{"automatic discovery", proxyManual | proxyAutoDetect, "127.0.0.1:7890", "", ProxyAutomatic},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := windowsProxyConfig(tc.flags, tc.server, tc.pac)
			check := checkProxy("127.0.0.1:7890", []ProxyConfig{cfg}, nil)
			if check.Status != tc.want {
				t.Fatalf("configuration %+v: got %v, want %v", cfg, check.Status, tc.want)
			}
			if tc.flags&proxyAutoURL != 0 && cfg.PAC != tc.pac {
				t.Fatalf("PAC URL = %q, want %q", cfg.PAC, tc.pac)
			}
		})
	}
}
