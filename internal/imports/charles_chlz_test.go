package imports

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// charlesChlzArchive builds a .chlz in memory from named entries, so the
// fixture is written here and not copied from a third-party file.
func charlesChlzArchive(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const chlzLogin = `{"status":"COMPLETE","method":"POST","protocolVersion":"HTTP/1.1","scheme":"https","host":"api.example.test",
"actualPort":443,"path":"/login","query":"x=1","remoteAddress":"api.example.test/203.0.113.7","clientAddress":"/127.0.0.1",
"totalSize":99,"times":{"start":"2026-11-14T10:00:00.000+08:00"},"durations":{"total":42},
"request":{"sizes":{"body":9},"header":{"headers":[{"name":"Content-Type","value":"application/json"}]}},
"response":{"status":200,"sizes":{"body":0},"header":{"headers":[{"name":"Set-Cookie","value":"a=1"},{"name":"Set-Cookie","value":"b=2"}]}}}`

const chlzHome = `{"status":"COMPLETE","method":"GET","scheme":"http","host":"example.test","actualPort":80,
"times":{"start":"2026-11-14T10:00:01.000+08:00"},"durations":{"total":3},
"request":{"sizes":{"body":0},"header":{"headers":[]}},"response":{"status":304,"sizes":{"body":0},"header":{"headers":[]}}}`

// chlzExchange is a bodiless exchange on http://example.test/items/<n>.
func chlzExchange(method, path string, status int) string {
	return fmt.Sprintf(`{"status":"COMPLETE","method":%q,"scheme":"http","host":"example.test","actualPort":80,"path":%q,`+
		`"times":{"start":"2026-11-14T10:00:02.000+08:00"},"durations":{"total":1},`+
		`"request":{"sizes":{"body":0},"header":{"headers":[]}},"response":{"status":%d,"sizes":{"body":0},"header":{"headers":[]}}}`,
		method, path, status)
}

func TestCharlesChlzReadsNumberedEntriesInOrder(t *testing.T) {
	archive := charlesChlzArchive(t, map[string]string{
		"1-meta.json":  chlzLogin,
		"1-req.dat":    `{"a":"b"}`,
		"0-meta.json":  chlzHome,
		"2-meta.json":  `{"method":`,
		"9-meta.json":  chlzExchange("PUT", "/items/9", 204),
		"10-meta.json": chlzExchange("DELETE", "/items/10", 202),
		"notes.txt":    "not an entry",
		"x-meta.json":  chlzHome,
	})
	got, err := ReadCapture(archive, "session.chlz")
	if err != nil {
		t.Fatal(err)
	}
	if got.Format != CaptureCharlesChlz {
		t.Fatalf("format = %q", got.Format)
	}
	// Index 9 comes before 10: a text sort would put 10 first.
	if len(got.Flows) != 4 {
		t.Fatalf("flows = %d; want the valid entries 0, 1, 9 and 10 only", len(got.Flows))
	}
	home, login, put, del := got.Flows[0], got.Flows[1], got.Flows[2], got.Flows[3]
	if home.Method != "GET" || home.URL != "http://example.test/" || home.Status != 304 {
		t.Errorf("entry 0 = %+v", home)
	}
	if login.Method != "POST" || login.URL != "https://api.example.test/login?x=1" || login.Status != 200 {
		t.Errorf("entry 1 = %+v", login)
	}
	if put.URL != "http://example.test/items/9" || put.Status != 204 || del.URL != "http://example.test/items/10" || del.Status != 202 {
		t.Errorf("entries 9 and 10 = %q %d and %q %d", put.URL, put.Status, del.URL, del.Status)
	}
	if string(login.ReqBody) != `{"a":"b"}` || login.ReqSize != 9 || login.Remote != "203.0.113.7" {
		t.Errorf("entry 1 bodies or remote = %q %d %q", login.ReqBody, login.ReqSize, login.Remote)
	}
	if got := login.ResHeader.Values("Set-Cookie"); !slices.Equal(got, []string{"a=1", "b=2"}) {
		t.Errorf("repeated headers = %v", got)
	}
	if login.ReqHeader.Get("Content-Type") != "application/json" || login.ReqHeader.Get("Host") != "" {
		t.Errorf("request headers = %v", login.ReqHeader)
	}
	if login.At.IsZero() || login.Millis != 42 {
		t.Errorf("time or duration = %v %d", login.At, login.Millis)
	}
	if !strings.Contains(strings.Join(got.Warnings, "\n"), "malformed or incomplete meta.json") {
		t.Errorf("warnings = %v; want the malformed entry reported", got.Warnings)
	}
}

func TestCharlesChlzRefusesNativeChls(t *testing.T) {
	// Charles' native .chls is a Java serialization stream: refuse it whether
	// it is named as such or hidden under a numbered entry name.
	for name, entries := range map[string]map[string]string{
		"named":   {"0-meta.json": chlzHome, "0-req.chls": "\xac\xed\x00\x05payload"},
		"sniffed": {"0-meta.json": chlzHome, "0-res.dat": "\xac\xed\x00\x05payload"},
	} {
		_, err := ReadCapture(charlesChlzArchive(t, entries), "session.chlz")
		if !errors.Is(err, ErrCharlesNative) {
			t.Errorf("%s: err = %v; want ErrCharlesNative", name, err)
		}
		if err != nil && !strings.Contains(err.Error(), ".chlsj") {
			t.Errorf("%s: err = %v; want the .chlsj export hint", name, err)
		}
	}
}
