package request

import (
	"fmt"
	"strings"
)

// percentEscape encodes every byte but RFC 3986's unreserved A-Z a-z 0-9 - _ . ~,
// in upper-case hex. OAuth 1.0 signs and sends with it; AWS Signature signs with it.
func percentEscape(s string) string {
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		if 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
