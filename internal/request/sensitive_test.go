package request

import (
	"strings"
	"testing"
)

func TestIsSensitiveHeader(t *testing.T) {
	for _, name := range []string{
		"Authorization", "Cookie", "X-Api-Key", "X-Amz-Signature", "X-Amz-Credential",
		"sessionid", "authToken", "X-Session-Token", "signature",
	} {
		if !IsSensitiveHeader(name) {
			t.Errorf("IsSensitiveHeader(%q) = false", name)
		}
	}
	for _, name := range []string{"Content-Type", "Accept", "User-Agent", "X-Request-ID", "code"} {
		if IsSensitiveHeader(name) {
			t.Errorf("IsSensitiveHeader(%q) = true", name)
		}
	}
}

func TestRedactURLKeepsBodyCodesButHidesQueryCodes(t *testing.T) {
	got := RedactURL("https://user:password@api.example.com/items?code=oauth-code&token=query-token&page=2")
	if strings.Contains(got, "oauth-code") || strings.Contains(got, "query-token") || strings.Contains(got, "password") {
		t.Fatalf("URL = %q", got)
	}
	if !strings.Contains(got, "page=2") {
		t.Fatalf("URL lost a safe query value: %q", got)
	}
	if IsSensitiveHeader("code") {
		t.Fatal("a body field named code must not be redacted by the header predicate")
	}
}
