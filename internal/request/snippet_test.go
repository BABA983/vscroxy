package request

import (
	"go/format"
	"net/http"
	"strings"
	"testing"
)

// snippetRequest has a repeated header, a header and a body that need
// escaping in every language, and headers the clients compute themselves.
func snippetRequest() *Prepared {
	return &Prepared{
		Method: "POST",
		URL:    "https://api.example.com/items?a=1&b=%20x",
		Body:   "{\n  \"q\": \"say \\\"hi\\\"\",\n  \"tpl\": \"${x} `tick`\"\n}",
		Header: http.Header{
			"Content-Type":   {"application/json"},
			"X-Dup":          {"a", "b"},
			"X-Quote":        {`say "hi" \ 'x'`},
			"Content-Length": {"42"},
			"Host":           {"API.example.com"},
		},
	}
}

const bt = "`"

func TestSnippet(t *testing.T) {
	jsBody := bt + `{
  "q": "say \\"hi\\"",
  "tpl": "\${x} \` + bt + `tick\` + bt + `"
}` + bt
	want := map[string]string{
		"JavaScript (fetch)": `const response = await fetch("https://api.example.com/items?a=1&b=%20x", {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    "X-Dup": "a, b",
    "X-Quote": "say \"hi\" \\ 'x'",
  },
  body: ` + jsBody + `,
  redirect: "manual",
});
console.log(response.status, await response.text());
`,
		"Node.js (axios)": `const axios = require("axios");

axios.request({
  method: "POST",
  url: "https://api.example.com/items?a=1&b=%20x",
  headers: {
    "Content-Type": "application/json",
    "X-Dup": ["a", "b"],
    "X-Quote": "say \"hi\" \\ 'x'",
  },
  data: ` + jsBody + `,
  transformRequest: (data) => data,
  transformResponse: (data) => data,
  validateStatus: () => true,
  maxRedirects: 0,
}).then((response) => {
  console.log(response.status, response.data);
});
`,
		"Python (requests)": `import requests

url = "https://api.example.com/items?a=1&b=%20x"
headers = {
    "Content-Type": "application/json",
    "X-Dup": "a, b",
    "X-Quote": "say \"hi\" \\ 'x'",
}
data = """{
  "q": "say \\"hi\\"",
  "tpl": "${x} ` + bt + `tick` + bt + `"
}""".encode()

response = requests.request("POST", url, headers=headers, data=data, allow_redirects=False)
print(response.status_code, response.text)
`,
		"Go (net/http)": `package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

func main() {
	req, err := http.NewRequest("POST", "https://api.example.com/items?a=1&b=%20x", strings.NewReader("{\n  \"q\": \"say \\\"hi\\\"\",\n  \"tpl\": \"${x} ` + bt + `tick` + bt + `\"\n}"))
	if err != nil {
		panic(err)
	}
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("X-Dup", "a")
	req.Header.Add("X-Dup", "b")
	req.Header.Add("X-Quote", "say \"hi\" \\ 'x'")
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	fmt.Println(resp.Status)
	fmt.Println(string(data))
}
`,
		"HTTPie": `http --ignore-stdin POST 'https://api.example.com/items?a=1&b=%20x' \
  Content-Type:application/json \
  X-Dup:a \
  X-Dup:b \
  'X-Quote:say "hi" \ '\''x'\''' \
  Accept: \
  Accept-Encoding: \
  User-Agent: \
  --raw='{
  "q": "say \"hi\"",
  "tpl": "${x} ` + bt + `tick` + bt + `"
}'
`,
	}
	for _, language := range SnippetLanguages {
		got, err := Snippet(language, snippetRequest())
		if err != nil {
			t.Fatalf("%s: %v", language, err)
		}
		if got != want[language] {
			t.Errorf("%s:\n%s\nwant:\n%s", language, got, want[language])
		}
	}
	if len(want) != len(SnippetLanguages) {
		t.Errorf("tested %d languages, SnippetLanguages has %d", len(want), len(SnippetLanguages))
	}
}

func TestSnippetGoIsFormatted(t *testing.T) {
	for _, req := range []*Prepared{snippetRequest(), {Method: "GET", URL: "http://x/", Body: "a\nb"}, {URL: "http://x/"}} {
		got, err := Snippet("Go (net/http)", req)
		if err != nil {
			t.Fatal(err)
		}
		formatted, err := format.Source([]byte(got))
		if err != nil || string(formatted) != got {
			t.Errorf("not gofmt-clean (%v):\n%s", err, got)
		}
	}
}

func TestSnippetBinaryBody(t *testing.T) {
	req := &Prepared{Method: "POST", URL: "http://x/", Body: "\x89PNG\xff"}
	for _, language := range SnippetLanguages {
		if _, err := Snippet(language, req); err == nil || err.Error() != "the body is binary; copy it as cURL instead" {
			t.Errorf("%s: err = %v", language, err)
		}
	}
}

func TestSnippetDropsComputedHeaders(t *testing.T) {
	req := &Prepared{Method: "PURGE", URL: "http://x.example/a", Header: http.Header{
		"Content-Length":    {"0"},
		"Host":              {"X.example"},
		"Connection":        {"keep-alive"},
		"Transfer-Encoding": {"chunked"},
		"Proxy-Connection":  {"keep-alive"},
		"Keep-Alive":        {"timeout=5"},
		"Te":                {"trailers"},
		"Upgrade":           {"h2c"},
		"X-Kept":            {"1"},
	}}
	for _, language := range SnippetLanguages {
		got, err := Snippet(language, req)
		if err != nil {
			t.Fatalf("%s: %v", language, err)
		}
		if !strings.Contains(got, "PURGE") || !strings.Contains(got, "X-Kept") {
			t.Errorf("%s lost the method or a header:\n%s", language, got)
		}
		for name := range req.Header {
			if name != "X-Kept" && strings.Contains(got, name) {
				t.Errorf("%s kept %s:\n%s", language, name, got)
			}
		}
		// No body means no body argument.
		for _, arg := range []string{"body:", "data:", "data=", "strings.NewReader", "--raw", "printf"} {
			if strings.Contains(got, arg) {
				t.Errorf("%s has %s without a body:\n%s", language, arg, got)
			}
		}
	}
}

func TestSnippetKeepsAnotherHost(t *testing.T) {
	req := &Prepared{Method: "GET", URL: "http://127.0.0.1:8080/", Header: http.Header{"Host": {"virtual.example"}}}
	for language, want := range map[string]string{
		"Node.js (axios)":   `"Host": "virtual.example",`,
		"Python (requests)": `"Host": "virtual.example",`,
		"Go (net/http)":     `req.Host = "virtual.example"`,
		"HTTPie":            `Host:virtual.example \`,
	} {
		if got, err := Snippet(language, req); err != nil || !strings.Contains(got, want) {
			t.Errorf("%s (%v) has no %s:\n%s", language, err, want, got)
		}
	}
	if _, err := Snippet("JavaScript (fetch)", req); err == nil {
		t.Error("fetch silently drops the Host header")
	}
}

// Clients label an unlabeled body unless the snippet stops them.
func TestSnippetBodyWithoutContentType(t *testing.T) {
	req := &Prepared{Method: "POST", URL: "http://x/", Body: "a=1\r\n"}
	for language, want := range map[string]string{
		"JavaScript (fetch)": "  body: new TextEncoder().encode(`a=1\\r\n`),\n",
		"Node.js (axios)":    "  headers: {\n    \"Content-Type\": false,\n  },\n",
		"Python (requests)":  "data = \"\"\"a=1\\r\n\"\"\".encode()\n",
		// A pasted carriage return is not safe even inside quotes.
		"HTTPie": "printf '%b' 'a=1\\r\n' | http POST http://x/ \\\n  Accept: \\\n  Accept-Encoding: \\\n  User-Agent: \\\n  Content-Type:\n",
	} {
		if got, err := Snippet(language, req); err != nil || !strings.Contains(got, want) {
			t.Errorf("%s (%v) has no %q:\n%s", language, err, want, got)
		}
	}
}
