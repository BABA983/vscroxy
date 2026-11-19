package inspect

import (
	"strings"
	"testing"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
)

// encoded builds the fixture bytes with the same x/text encoder the real
// servers used, so the expected text is a known string and not a decode of
// the bytes under test.
func encoded(t *testing.T, enc encoding.Encoding, text string) []byte {
	t.Helper()
	b, err := enc.NewEncoder().Bytes([]byte(text))
	if err != nil {
		t.Fatalf("encode %q: %v", text, err)
	}
	return b
}

func TestDisplayText(t *testing.T) {
	const chinese = "中文测试：你好，世界！"
	shiftJIS := "こんにちは、世界"
	eucKR := "안녕하세요, 세계"
	latin1 := "Café · naïve"
	utf16Text := "Hello, 中文!"
	utf16ASCII := "Hello, world!"

	gbkBytes := encoded(t, simplifiedchinese.GBK, chinese)
	utf16LE := encoded(t, unicode.UTF16(unicode.LittleEndian, unicode.UseBOM), utf16Text)
	// VS Code's zero-byte check only sees alternating ASCII/NUL bytes, so
	// the undeclared fixture uses ASCII and a declared one carries CJK.
	utf16BE := encoded(t, unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM), utf16ASCII)
	cases := []struct {
		name, contentType, wantText, wantEncoding string
		body                                      []byte
		wantOK                                    bool
	}{
		{"gbk header", "text/plain; charset=GBK", chinese, "GBK", gbkBytes, true},
		{"shift_jis header", "text/plain; charset=shift_jis", shiftJIS, "Shift_JIS", encoded(t, japanese.ShiftJIS, shiftJIS), true},
		{"euc-kr header", "text/plain; charset=euc-kr", eucKR, "EUC-KR", encoded(t, korean.EUCKR, eucKR), true},
		{"latin-1 header", "text/plain; charset=iso-8859-1", latin1, "windows-1252", encoded(t, charmap.ISO8859_1, latin1), true},
		{"utf-16le bom", "application/octet-stream", utf16Text, "UTF-16LE", utf16LE, true},
		{"utf-16be zero bytes", "text/plain", utf16ASCII, "UTF-16BE", utf16BE, true},
		{"utf-16be declared", "text/plain; charset=utf-16be", utf16Text, "UTF-16BE",
			encoded(t, unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM), utf16Text), true},
		{"bom beats charset", "text/plain; charset=gbk", "from the BOM", "UTF-8 BOM",
			append([]byte("\xef\xbb\xbf"), "from the BOM"...), true},
		{"unknown charset", "text/plain; charset=x-not-real", "", "", gbkBytes, false},
		{"invalid bytes without a signal", "application/octet-stream", "", "",
			[]byte{0x81, 0x30, 0xfe, 0x41}, false},
		{"valid utf-8 unchanged", "text/plain; charset=utf-8", "", "", []byte(chinese), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, name, ok := DisplayText(tc.contentType, tc.body)
			if ok != tc.wantOK || text != tc.wantText || name != tc.wantEncoding {
				t.Fatalf("DisplayText = (%q, %q, %v), want (%q, %q, %v)",
					text, name, ok, tc.wantText, tc.wantEncoding, tc.wantOK)
			}
		})
	}
}

func TestDisplayTextHTMLMeta(t *testing.T) {
	const chinese = "页面标题：你好"
	encodedTitle := encoded(t, simplifiedchinese.GBK, chinese)
	html := append([]byte(`<html><head><meta charset="gbk"></head><body>`), encodedTitle...)
	html = append(html, []byte(`</body></html>`)...)
	text, name, ok := DisplayText("text/html", html)
	want := "<html><head><meta charset=\"gbk\"></head><body>" + chinese + "</body></html>"
	if !ok || text != want || name != "GBK" {
		t.Fatalf("<meta charset> = (%q, %q, %v), want (%q, GBK, true)", text, name, ok, want)
	}

	// Only the first 4 KiB is a declaration; a later tag is not a signal.
	late := append([]byte(strings.Repeat(" ", htmlCharsetScanMaxLen)), html...)
	if text, name, ok := DisplayText("text/html", late); ok || text != "" || name != "" {
		t.Fatalf("late <meta charset> was used: (%q, %q, %v)", text, name, ok)
	}
}

func TestDisplayTextBoundsInputAndOutput(t *testing.T) {
	tooBig := make([]byte, displayBodyLimit+1)
	if text, name, ok := DisplayText("text/plain; charset=gbk", tooBig); ok || text != "" || name != "" {
		t.Fatalf("oversized body was decoded: (%q, %q, %v)", text, name, ok)
	}
	// A GBK byte becomes at most three UTF-8 bytes: 8 MiB in, 24 MiB out.
	b := encoded(t, simplifiedchinese.GBK, strings.Repeat("中", displayBodyLimit/3))
	text, name, ok := DisplayText("text/plain; charset=gbk", b)
	if !ok || name != "GBK" || len(text) > decodedDisplayLimit {
		t.Fatalf("bounded decode: %d bytes, %q, %v", len(text), name, ok)
	}
}

func BenchmarkDisplayTextGBK8MiB(b *testing.B) {
	body, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(strings.Repeat("中文abc", displayBodyLimit/7)))
	if err != nil {
		b.Fatal(err)
	}
	body = body[:displayBodyLimit]
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	var text string
	var name string
	for b.Loop() {
		text, name, _ = DisplayText("text/plain; charset=gbk", body)
	}
	if text == "" || name != "GBK" {
		b.Fatal("GBK body was not decoded")
	}
}
