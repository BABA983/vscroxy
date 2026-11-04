package inspect

import (
	"bytes"
	"encoding/hex"
	"encoding/json/jsontext"
	"mime/multipart"
	"net/textproto"
	"reflect"
	"strings"
	"testing"
)

func TestInspectBodyFormAndMultipart(t *testing.T) {
	form, err := InspectBody("application/x-www-form-urlencoded; charset=utf-8", []byte("tag=first&tag=a+b&plus=%2B&empty=&flag&%E5%90%8D=%E5%80%BC"))
	want := []FormField{{"tag", "first", "tag", "first", true}, {"tag", "a+b", "tag", "a b", true}, {"plus", "%2B", "plus", "+", true}, {"empty", "", "empty", "", true}, {"flag", "", "flag", "", false}, {"%E5%90%8D", "%E5%80%BC", "名", "值", true}}
	if err != nil || form.Format != "Form" || !reflect.DeepEqual(form.Fields, want) || !jsontext.Value(form.Text).IsValid() {
		t.Fatalf("form: %+v, %v", form, err)
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.SetBoundary("fixture-boundary"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct{ disposition, contentType, transfer, data string }{
		{`form-data; name="tag"`, "", "", "first"},
		{`form-data; name="tag"`, "", "", "second"},
		{`form-data; name="upload"; filename="../unsafe.txt"`, "text/plain", "", "never-preview-file"},
		{`form-data; name="encoded"`, "text/plain", "base64", "c2VjcmV0"},
		{`form-data; name="blob"`, "application/octet-stream", "", "\x00\xff"},
	} {
		h := textproto.MIMEHeader{"Content-Disposition": {p.disposition}, "X-Part": {"first", "second"}}
		if p.contentType != "" {
			h.Set("Content-Type", p.contentType)
		}
		if p.transfer != "" {
			h.Set("Content-Transfer-Encoding", p.transfer)
		}
		part, err := w.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(p.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	view, err := InspectBody(w.FormDataContentType(), body.Bytes())
	if err != nil || len(view.Parts) != 5 {
		t.Fatalf("multipart: %+v %v", view, err)
	}
	parts := view.Parts
	if parts[0].Name != "tag" || *parts[0].Preview != "first" || parts[1].Name != "tag" || *parts[1].Preview != "second" || parts[2].Filename != "../unsafe.txt" || !parts[2].File || parts[2].Size != len("never-preview-file") {
		t.Fatalf("order, duplicates, filename or size lost: %+v", parts)
	}
	if parts[2].Preview != nil || parts[3].Preview != nil || parts[4].Preview != nil || strings.Contains(view.Text, "never-preview-file") || !reflect.DeepEqual(parts[2].Headers["X-Part"], []string{"first", "second"}) {
		t.Fatal("file/binary/encoded payload previewed or repeated header lost")
	}
	if _, err := InspectBody(w.FormDataContentType(), body.Bytes()[:body.Len()-30]); err == nil {
		t.Fatal("accepted truncated multipart")
	}
}

func TestInspectBodyXML(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`<root><name>A &amp; B</name><empty/></root>`, "<root>\n  <name>A &amp; B</name>\n  <empty/>\n</root>"},
		{`<p>Hello <b>world</b> !</p>`, `<p>Hello <b>world</b> !</p>`},
		{`<r xml:space="preserve">  <a><b/></a> </r>`, `<r xml:space="preserve">  <a><b/></a> </r>`},
		{`<r><![CDATA[ a < b ]]><b/></r>`, `<r><![CDATA[ a < b ]]><b/></r>`},
		{`<?xml version="1.0"?><p:r xmlns:p="urn:fixture"><p:item k="&quot;">值</p:item><!--note--></p:r>`, "<?xml version=\"1.0\"?><p:r\n  xmlns:p=\"urn:fixture\">\n  <p:item k=\"&quot;\">值</p:item>\n  <!--note-->\n</p:r>"},
	} {
		result, err := InspectBody("application/problem+xml", []byte(tc.input))
		if err != nil || result.Text != tc.want || result.Format != "XML" {
			t.Fatalf("XML %q: %q, %v; want %q", tc.input, result.Text, err, tc.want)
		}
	}
}

func TestInspectBodyMessagePack(t *testing.T) {
	for _, tc := range []struct{ wire, want string }{
		{"cf0020000000000001", "9007199254740993"},
		{"cfffffffffffffffff", "18446744073709551615"},
		{"d38000000000000000", "-9223372036854775808"},
		{"c0", "null"},
		{"90", "[]"},
		{"93c2c3a0", "[false,true,\"\"]"},
		{"c40300ff80", `{"$binary":{"base64":"AP+A"}}`},
		{"d407ff", `{"$extension":{"base64":"/w==","type":7}}`},
		{"82a16101a16102", `{"$map":[{"key":"a","value":1},{"key":"a","value":2}]}`},
		{"8101a178", `{"$map":[{"key":1,"value":"x"}]}`},
		{"cb7ff0000000000000", `{"$float":"+Infinity"}`},
	} {
		data, err := hex.DecodeString(tc.wire)
		if err != nil {
			t.Fatal(err)
		}
		result, err := InspectBody("application/msgpack", data)
		value := jsontext.Value(result.Text)
		if err != nil || value.Compact() != nil || string(value) != tc.want {
			t.Fatalf("MessagePack %s: %s, %v; want %s", tc.wire, result.Text, err, tc.want)
		}
	}
}

func TestInspectBodyRejectsInvalidAndBoundedEvidence(t *testing.T) {
	for _, tc := range []struct{ contentType, input string }{
		{"application/json", "{}"},
		{"application/x-www-form-urlencoded", "a=%Q0"},
		{"application/x-www-form-urlencoded", "a=%FF"},
		{"application/x-www-form-urlencoded", strings.Repeat("x", 1<<20+1)},
		{"application/x-www-form-urlencoded", strings.Repeat("a&", bodyNodeLimit)},
		{"multipart/form-data", "missing boundary"},
		{"multipart/form-data; boundary=x", "--x\r\nContent-Disposition: form-data; name=\"a\"\r\n\r\ntruncated"},
		{"application/xml", ""},
		{"application/xml", "<a></b>"},
		{"application/xml", "<a/><b/>"},
		{"application/xml", "<a x='1' x='2'/>"},
		{"application/xml", "<p:a/>"},
		{"application/xml", `<!DOCTYPE a [<!ENTITY x SYSTEM "file:///etc/passwd">]><a>&x;</a>`},
		{"application/xml", strings.Repeat("<a>", bodyDepthLimit+1) + strings.Repeat("</a>", bodyDepthLimit+1)},
		{"application/msgpack", ""},
		{"application/msgpack", "\xc1"},
		{"application/msgpack", "\xc0\xc0"},
		{"application/msgpack", "\xa1\xff"},
		{"application/msgpack", "\xdd\xff\xff\xff\xff"},
		{"application/msgpack", "\xc6\xff\xff\xff\xff"},
		{"application/msgpack", strings.Repeat("\x91", bodyDepthLimit+1) + "\xc0"},
	} {
		view, err := InspectBody(tc.contentType, []byte(tc.input))
		if err == nil || view.Text != "" || len(view.Fields) != 0 || len(view.Parts) != 0 {
			t.Fatalf("accepted invalid/partial %s input (%d bytes): %+v, %v", tc.contentType, len(tc.input), view, err)
		}
	}
}

func FuzzInspectBody(f *testing.F) {
	for _, seed := range [][]byte{[]byte("a=1&a=a+b"), []byte("<a><b/></a>"), []byte("<p>hi <b>there</b></p>"), {0x81, 0x01, 0xc4, 0x01, 0xff}, []byte("--x--\r\n")} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, ct := range []string{"application/x-www-form-urlencoded", "application/xml", "application/msgpack", "multipart/form-data; boundary=x"} {
			view, err := InspectBody(ct, data)
			if len(view.Text) > bodyOutputLimit || err != nil && (view.Text != "" || len(view.Fields) != 0 || len(view.Parts) != 0) {
				t.Fatalf("inspection output contract: %s, %d bytes, %v", ct, len(view.Text), err)
			}
		}
	})
}
