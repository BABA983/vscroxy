package request

import (
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
