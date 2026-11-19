package app

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// BenchmarkUnicodeUnchangedCJKBodyFrame builds one request-editor frame with a
// 2 MiB unchanged CJK body. Before caching, the build scanned all of it on
// every frame; after caching, the scan happens once when the editor is made.
func BenchmarkUnicodeUnchangedCJKBodyFrame(b *testing.B) {
	a := workspaceApp()
	r := savedRequest{
		Method: "POST", URL: "https://api.example.com/projects",
		Body: strings.Repeat("中文测试", 175_000), // about 2.1 MiB of UTF-8
	}
	a.addDocument(r, "draft:bench")
	d := a.requestDocument
	d.composeTab = composeTabBody
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	if d.bodyEditor == nil || len(d.composeBody) < 2<<20 {
		b.Fatalf("benchmark body was not prepared: editor=%v bytes=%d", d.bodyEditor != nil, len(d.composeBody))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		tt.Frame()
	}
}
