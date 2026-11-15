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

func TestDecodeValueShapes(t *testing.T) {
	json, err := DecodeValue("JSON", `{"b":1,"a":[2,3]}`)
	if want := "{\n  \"b\": 1,\n  \"a\": [\n    2,\n    3\n  ]\n}"; err != nil || json.Text != want {
		t.Fatalf("JSON: %q, %v; want %q", json.Text, err, want)
	}
	xml, err := DecodeValue("XML", `<root><name>A &amp; B</name></root>`)
	if want := "<root>\n  <name>A &amp; B</name>\n</root>"; err != nil || xml.Text != want {
		t.Fatalf("XML: %q, %v; want %q", xml.Text, err, want)
	}
	form, err := DecodeValue("Key-value", "a=1&b=hello+world&a=2")
	if err != nil || len(form.Fields) != 3 || form.Fields[1].Key != "b" || form.Fields[1].Value != "hello world" {
		t.Fatalf("form: %+v, %v", form.Fields, err)
	}
	lines, err := DecodeValue("Key-value", "Authorization: Bearer x\nX-Trace = 42")
	if err != nil || len(lines.Fields) != 2 || lines.Fields[0].Key != "Authorization" || lines.Fields[1].Value != "42" {
		t.Fatalf("lines: %+v, %v", lines.Fields, err)
	}
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	picture, err := DecodeValue("Base64 image", png)
	if err != nil || picture.MediaType != "image/png" || len(picture.Image) == 0 {
		t.Fatalf("image: %s, %d bytes, %v", picture.MediaType, len(picture.Image), err)
	}
	for _, tc := range []struct{ kind, input string }{
		{"JSON", `42`},
		{"JSON", `{`},
		{"XML", `<a>`},
		{"Key-value", `no separators here`},
		{"Base64 image", `aGVsbG8=`},
	} {
		if _, err := DecodeValue(tc.kind, tc.input); err == nil {
			t.Fatalf("accepted invalid %s input %q", tc.kind, tc.input)
		}
	}
}
