package vault

import (
	"bytes"
	"errors"
	"testing"
)

func TestSealedVaultOpensOnlyWithItsKey(t *testing.T) {
	key := NewKey()
	secrets := Secrets{{Name: "token", Value: "s3cr3t-value", Domains: []string{"api.example.com"}}}
	data, err := Seal(key, secrets)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("s3cr3t-value")) || bytes.Contains(data, []byte("token")) {
		t.Fatalf("sealed vault shows its contents: %s", data)
	}
	got, err := Open(key, data)
	if err != nil || len(got) != 1 || got[0].Value != "s3cr3t-value" || got[0].Domains[0] != "api.example.com" {
		t.Fatalf("Open = %+v, %v", got, err)
	}
	if _, err := Open(NewKey(), data); err == nil {
		t.Fatal("another key opened the vault")
	}
	tampered := bytes.Clone(data)
	tampered[len(tampered)-5] ^= 1 // inside the base64 ciphertext
	if got, err := Open(key, tampered); err == nil {
		t.Fatalf("a damaged vault opened: %+v", got)
	}
}

func TestSecretsReachOnlyTheirDomains(t *testing.T) {
	s := Secrets{
		{Name: "open", Value: "1"},
		{Name: "exact", Value: "2", Domains: []string{"API.example.com"}},
		{Name: "wild", Value: "3", Domains: []string{"*.example.com", "other.test"}},
	}
	for _, tc := range []struct {
		name, host string
		ok         bool
	}{
		{"open", "anything.test", true},
		{"exact", "api.example.com", true},
		{"exact", "api.example.com.", true},
		{"exact", "www.example.com", false},
		{"exact", "api.example.com.evil.test", false},
		{"wild", "a.b.example.com", true},
		{"wild", "example.com", false}, // the wildcard names subdomains only
		{"wild", "notexample.com", false},
		{"wild", "other.test", true},
	} {
		value, err := s.Lookup(tc.name, tc.host)
		if (err == nil) != tc.ok || tc.ok && value == "" {
			t.Errorf("Lookup(%q, %q) = %q, %v; want allowed=%t", tc.name, tc.host, value, err, tc.ok)
		}
	}
	if _, err := s.Lookup("missing", "x.test"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing secret: %v", err)
	}
}
