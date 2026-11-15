package request

import "testing"

func TestIsSensitiveHeader(t *testing.T) {
	for _, name := range []string{
		"Authorization", "Cookie", "X-Api-Key", "X-Amz-Signature", "X-Amz-Credential",
		"sessionid", "authToken", "code", "X-Session-Token", "signature",
	} {
		if !IsSensitiveHeader(name) {
			t.Errorf("IsSensitiveHeader(%q) = false", name)
		}
	}
	for _, name := range []string{"Content-Type", "Accept", "User-Agent", "X-Request-ID"} {
		if IsSensitiveHeader(name) {
			t.Errorf("IsSensitiveHeader(%q) = true", name)
		}
	}
}
