package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/editor"
)

// largeJSONBody is a few-MiB response resembling a captured listing: many
// small records with escaped strings, numbers and booleans.
func largeJSONBody() string {
	var b strings.Builder
	b.Grow(4 << 20)
	b.WriteString(`{"records":[`)
	for i := range 50_000 {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"name":"record-%d-with-a-long-description","active":%t,"score":%d.%03d}`,
			i, i, i%2 == 0, i/1000, i%1000)
	}
	b.WriteString(`]}`)
	return b.String()
}

// truncatedJSONBody is a minified listing cut off mid-array, like a capture
// ended by a closed connection or a body-size limit.
func truncatedJSONBody(size int) string {
	var b strings.Builder
	b.Grow(size)
	b.WriteString(`{"records":[`)
	for b.Len() < size {
		fmt.Fprintf(&b, `{"id":%d,"name":"record-with-a-long-description"},`, b.Len())
	}
	return b.String()[:size]
}

func BenchmarkJSONTreeParse(b *testing.B) {
	body := largeJSONBody()
	b.ReportAllocs()
	for b.Loop() {
		if len(parseJSON(body).roots) == 0 {
			b.Fatal("no tree")
		}
	}
}

// BenchmarkPrettyBody formats and builds a read-only editor buffer the way
// the Pretty view does on the first draw of a response.
func BenchmarkPrettyBody(b *testing.B) {
	body := largeJSONBody()
	b.ReportAllocs()
	for b.Loop() {
		text, _ := prettyBodyText(body, nil)
		buf := editor.NewBuffer(text)
		buf.SetLanguage(editor.LanguageNamed("json"))
	}
}

func BenchmarkFaultTolerantLargeBodies(b *testing.B) {
	for _, mib := range []int{4, 8} {
		body := truncatedJSONBody(mib << 20)
		b.Run(fmt.Sprintf("%dMiB/tree", mib), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if len(parseJSON(body).roots) == 0 {
					b.Fatal("no partial tree")
				}
			}
		})
		b.Run(fmt.Sprintf("%dMiB/pretty", mib), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if result := computeFaultTolerantBody(body); !result.prettyOK {
					b.Fatal("no Pretty text")
				}
			}
		})
	}
}
