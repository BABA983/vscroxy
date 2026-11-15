package request

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// The additional generators keep the method, repeated headers, a raw body
// and the no-redirect behavior, and quote what their language needs.
func TestSnippetAdditionalLanguages(t *testing.T) {
	body := "a\"b\\c\n$d 'e'"
	req := &Prepared{Method: "POST", URL: "https://api.example.com/items?q=1", Body: body,
		Header: http.Header{"Content-Type": {"application/json"}, "X-Dup": {"a", "b"}}}
	for _, tc := range []struct {
		language string
		want     []string
	}{
		{"Dart (http)", []string{`request.followRedirects = false;`, `request.headers["X-Dup"] = "a, b";`, `utf8.encode("a\"b\\c\n\$d 'e'")`}},
		{"C (libcurl)", []string{`CURLOPT_CUSTOMREQUEST, "POST"`, `"X-Dup: b"`, `CURLOPT_POSTFIELDS, "a\"b\\c\n$d 'e'"`, `CURLOPT_FOLLOWLOCATION, 0L`}},
		{"Objective-C (NSURLSession)", []string{`request.HTTPMethod = @"POST";`, `forHTTPHeaderField:@"X-Dup"`, `@"a\"b\\c\n$d 'e'"`, `completionHandler(nil)`}},
		{"wget", []string{`--method='POST'`, `--header='X-Dup: b'`, `--max-redirect=0`, `--body-data='a"b\c`}},
		{"PHP (Guzzle)", []string{`'allow_redirects' => false`, `"X-Dup" => ["a", "b"]`, `'body' => "a\"b\\c\n\$d 'e'"`}},
		{"C# (RestSharp)", []string{`FollowRedirects = false`, `Method.Post`, `AddHeader("X-Dup", "b")`, `AddStringBody("a\"b\\c\n$d 'e'", "application/json")`}},
	} {
		got, err := Snippet(tc.language, req)
		if err != nil {
			t.Fatalf("%s: %v", tc.language, err)
		}
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s lacks %q:\n%s", tc.language, want, got)
			}
		}
	}
}

// Dart's \xHH is a code point, not a byte; non-ASCII text must stay one
// character and decode back to the same string.
func TestSnippetDartKeepsNonASCII(t *testing.T) {
	body := `{"名字":"é"}`
	req := &Prepared{Method: "POST", URL: "https://api.example.com/你好?q=é", Body: body,
		Header: http.Header{"Content-Type": {"application/json"}}}
	code, err := Snippet("Dart (http)", req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(code, `\xc3`) || strings.Contains(code, `\xa9`) {
		t.Fatalf("Dart snippet wrote UTF-8 bytes as code points:\n%s", code)
	}
	start := strings.Index(code, "utf8.encode(")
	if start < 0 {
		t.Fatalf("no body literal:\n%s", code)
	}
	literal := code[start+len("utf8.encode("):]
	literal = strings.TrimSuffix(strings.TrimSpace(literal[:strings.Index(literal, ");")]), ",")
	if got := decodeDartString(t, literal); got != body {
		t.Fatalf("body literal decodes to %q, want %q\n%s", got, body, code)
	}
	if !strings.Contains(code, `"https://api.example.com/你好?q=é"`) {
		t.Fatalf("URL was mangled:\n%s", code)
	}
}

func decodeDartString(t *testing.T, literal string) string {
	t.Helper()
	if len(literal) < 2 || literal[0] != '"' || literal[len(literal)-1] != '"' {
		t.Fatalf("not a Dart string literal: %q", literal)
	}
	var b strings.Builder
	for i := 1; i < len(literal)-1; {
		if literal[i] != '\\' {
			b.WriteByte(literal[i])
			i++
			continue
		}
		i++
		switch literal[i] {
		case 'n':
			b.WriteByte('\n')
			i++
		case 'r':
			b.WriteByte('\r')
			i++
		case 't':
			b.WriteByte('\t')
			i++
		case '\\', '"', '$':
			b.WriteByte(literal[i])
			i++
		case 'x':
			if i+2 >= len(literal) {
				t.Fatalf("short \\x escape in %q", literal)
			}
			var v int
			if _, err := fmt.Sscanf(literal[i+1:i+3], "%02x", &v); err != nil {
				t.Fatalf("bad \\x escape in %q", literal)
			}
			b.WriteByte(byte(v))
			i += 3
		case 'u':
			end := strings.IndexByte(literal[i:], '}')
			if end < 0 {
				t.Fatalf("unterminated \\u escape in %q", literal)
			}
			var v int
			if _, err := fmt.Sscanf(literal[i+2:i+end], "%x", &v); err != nil {
				t.Fatalf("bad \\u escape in %q", literal)
			}
			b.WriteRune(rune(v))
			i += end + 1
		default:
			t.Fatalf("unknown escape \\%c in %q", literal[i], literal)
		}
	}
	return b.String()
}

// Objective-C strings are byte strings, so a control character that is not
// \n, \r or \t must be escaped rather than pasted raw.
func TestSnippetObjectiveCEscapesControls(t *testing.T) {
	code, err := Snippet("Objective-C (NSURLSession)", &Prepared{Method: "POST", URL: "https://example.test", Body: "a\x01b"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(code, `@"a\001b"`) {
		t.Fatalf("control byte was not escaped:\n%s", code)
	}
}
