package system

import "testing"

func TestSplitAddr(t *testing.T) {
	cases := []struct {
		addr   string
		host   string
		port   string
		wantOk bool
	}{
		{"127.0.0.1:7890", "127.0.0.1", "7890", true},
		{"localhost:8080", "localhost", "8080", true},
		{"127.0.0.1", "", "", false},
		{":7890", "", "", false},
		{"127.0.0.1:", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		host, port, ok := splitAddr(c.addr)
		if host != c.host || port != c.port || ok != c.wantOk {
			t.Errorf("splitAddr(%q) = %q, %q, %v; want %q, %q, %v", c.addr, host, port, ok, c.host, c.port, c.wantOk)
		}
	}
}
