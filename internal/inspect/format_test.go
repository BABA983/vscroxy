package inspect

import (
	"strings"
	"testing"
)

func TestBeautifyJSPreservesTokens(t *testing.T) {
	src := `function f(){if(a){return "a;b"}else{return 'c'}/* note */}`
	got := BeautifyJS(src)
	for _, want := range []string{"function f()", "  if(a)", `"a;b"`, "'c'", "/* note */"} {
		if !strings.Contains(got, want) {
			t.Fatalf("beautified JS misses %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "\n") < 4 {
		t.Fatalf("JS was not broken into lines:\n%s", got)
	}
	if again := BeautifyJS(got); again != got {
		t.Fatalf("JS formatting is not idempotent:\n%s\n---\n%s", got, again)
	}
}

func TestBeautifyCSSIndentsDeclarations(t *testing.T) {
	got := BeautifyCSS(`.a{color:red;margin:0}@media (x){.b{display:none}}`)
	for _, want := range []string{".a {", "  color:red;", "  margin:0", "@media (x) {", "  .b {"} {
		if !strings.Contains(got, want) {
			t.Fatalf("beautified CSS misses %q:\n%s", want, got)
		}
	}
}

func TestMergeStreamJoinsOpenAIChunks(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	got, ok := MergeStream("text/event-stream", body)
	if !ok || got != "Hello\n[finish: stop]" {
		t.Fatalf("merged SSE = %q, %v", got, ok)
	}
	ndjson := "{\"choices\":[{\"delta\":{\"content\":\"A\"}}]}\n{\"choices\":[{\"delta\":{\"content\":\"B\"}}]}\n"
	if got, ok := MergeStream("application/x-ndjson", ndjson); !ok || got != "AB" {
		t.Fatalf("merged NDJSON = %q, %v", got, ok)
	}
}

func TestMergeStreamKeepsGenericEventsReadable(t *testing.T) {
	body := "event: price\ndata: {\"usd\":7}\n\nevent: price\ndata: {\"usd\":8}\n\n"
	got, ok := MergeStream("text/event-stream", body)
	if !ok || !strings.Contains(got, "event: price") || !strings.Contains(got, `{"usd":8}`) {
		t.Fatalf("generic SSE = %q, %v", got, ok)
	}
	if _, ok := MergeStream("application/json", `{"a":1}`); ok {
		t.Fatal("a plain JSON body was treated as a stream")
	}
}

func TestMultipartPartBytes(t *testing.T) {
	const boundary = "x-boundary"
	body := "--x-boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.txt\"\r\n\r\none\r\n" +
		"--x-boundary\r\nContent-Disposition: form-data; name=\"field\"\r\n\r\ntwo\r\n--x-boundary--\r\n"
	contentType := "multipart/form-data; boundary=" + boundary
	got, name, err := MultipartPartBytes(contentType, []byte(body), 0)
	if err != nil || string(got) != "one" || name != "a.txt" {
		t.Fatalf("part 0 = %q %q %v", got, name, err)
	}
	got, name, err = MultipartPartBytes(contentType, []byte(body), 1)
	if err != nil || string(got) != "two" || name != "part-2.bin" {
		t.Fatalf("part 1 = %q %q %v", got, name, err)
	}
	if _, _, err := MultipartPartBytes(contentType, []byte(body), 2); err == nil {
		t.Fatal("a missing part was returned")
	}
}
