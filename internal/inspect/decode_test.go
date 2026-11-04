package inspect

import (
	"strings"
	"testing"
)

func TestDecode(t *testing.T) {
	for _, tc := range []struct{ kind, input, want string }{
		{"Base64", "5L2g5aW977yM", "你好，"},
		{"Base64", "aGVsbG8", "hello"},
		{"Base64", "_wA=", "Binary · 2 bytes\n\n00000000  ff 00                                             |..|\n"},
		{"URL component", "%E4%BD%A0%2B+", "你++"},
		{"URL query", "%E4%BD%A0%2B+", "你+ "},
		{"Unix seconds", "-1", "1969-12-31T23:59:59Z"},
		{"Unix milliseconds", "1700000000123", "2023-11-14T22:13:20.123Z"},
	} {
		t.Run(tc.kind+tc.input, func(t *testing.T) {
			got, err := Decode(tc.kind, tc.input)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	jwt, err := Decode("JWT", "eyJhbGciOiJub25lIn0.eyJpZCI6OTAwNzE5OTI1NDc0MDk5M30.")
	if err != nil || !strings.Contains(jwt, "Unverified JWT") || !strings.Contains(jwt, "9007199254740993") {
		t.Fatalf("JWT lost precision or verification warning: %s %v", jwt, err)
	}
	for _, tc := range []struct{ kind, input string }{{"Base64", "Zh=="}, {"Base64", "a Gk="}, {"URL component", "%FF"}, {"URL query", "%Q0"}, {"JWT", "abc.def"}, {"JWT", "W10.e30."}, {"Unix seconds", "9223372036854775807"}, {"Unix seconds", "1.25"}, {"Base64", strings.Repeat("A", 64<<10+1)}} {
		if _, err := Decode(tc.kind, tc.input); err == nil {
			t.Fatalf("accepted invalid %s input", tc.kind)
		}
	}
}
