package imports

// Charles .chlz sessions are ZIP archives of flat, numbered entries: N-meta.json
// holds one exchange's metadata, and N-req.* and N-res.* hold its bodies, kept
// as stored (not decoded). This layout comes from public material (a Go parser
// and a product issue describing the archive). No real Charles .chlz has been
// checked against this reader.

import (
	"archive/zip"
	"bytes"
	"encoding/json/v2"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// charlesChlzEntry names the entries the reader uses; anything else is ignored.
var charlesChlzEntry = regexp.MustCompile(`^(\d+)-(meta\.json|req\.[^.]+|res\.[^.]+)$`)

// charlesChlzMeta is the part of an N-meta.json this reader keeps.
type charlesChlzMeta struct {
	Status          string  `json:"status"`
	Method          string  `json:"method"`
	ProtocolVersion string  `json:"protocolVersion"`
	Scheme          string  `json:"scheme"`
	Host            string  `json:"host"`
	ActualPort      int     `json:"actualPort"`
	Path            *string `json:"path"`
	Query           *string `json:"query"`
	Tunnel          bool    `json:"tunnel"`
	RemoteAddress   string  `json:"remoteAddress"`
	ClientAddress   string  `json:"clientAddress"`
	TotalSize       int     `json:"totalSize"`
	Times           struct {
		Start string `json:"start"`
	} `json:"times"`
	Durations struct {
		Total int `json:"total"`
	} `json:"durations"`
	SSL *struct {
		Protocol    string `json:"protocol"`
		CipherSuite string `json:"cipherSuite"`
	} `json:"ssl"`
	Request  charlesChlzMessage `json:"request"`
	Response charlesChlzMessage `json:"response"`
}

type charlesChlzMessage struct {
	Sizes struct {
		Body int `json:"body"`
	} `json:"sizes"`
	Header struct {
		Headers []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"headers"`
	} `json:"header"`
	Status int `json:"status"`
}

// isCharlesChlz reports whether data is a ZIP with Charles-numbered meta
// entries. Proxyman's archives use other names.
func isCharlesChlz(data []byte) bool {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false
	}
	for _, f := range zr.File {
		if m := charlesChlzEntry.FindStringSubmatch(f.Name); m != nil && m[2] == "meta.json" {
			return true
		}
	}
	return false
}

// readCharlesChlz reads the exchanges of a .chlz archive, ordered by their
// numeric index. A native .chls inside the archive is refused with the same
// conversion hint as a loose .chls: reading one would mean deserializing it.
func readCharlesChlz(data []byte, name string) (Capture, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Capture{}, fmt.Errorf("%s: not a readable Charles .chlz archive", name)
	}
	var total uint64
	type parts struct{ meta, req, res *zip.File }
	entries := map[int]*parts{}
	for _, f := range zr.File {
		total += f.UncompressedSize64
		if total > maxCaptureArchive {
			return Capture{}, fmt.Errorf("%s: the archive expands past %d MiB", name, maxCaptureArchive>>20)
		}
		if strings.EqualFold(path.Ext(f.Name), ".chls") || entryIsJavaSerialized(f) {
			return Capture{}, fmt.Errorf("%s: %w", name, ErrCharlesNative)
		}
		m := charlesChlzEntry.FindStringSubmatch(f.Name)
		if m == nil || f.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		index, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		p := entries[index]
		if p == nil {
			p = &parts{}
			entries[index] = p
		}
		switch {
		case m[2] == "meta.json":
			p.meta = f
		case strings.HasPrefix(m[2], "req."):
			p.req = f
		default:
			p.res = f
		}
	}
	indexes := make([]int, 0, len(entries))
	for index := range entries {
		indexes = append(indexes, index)
	}
	slices.Sort(indexes)

	w := &captureWarnings{}
	w.add("Charles .chlz: bodies are imported as stored; compressed bodies are not decoded. The layout was read from public material, not from a checked real export.")
	out := Capture{Format: CaptureCharlesChlz}
	for _, index := range indexes {
		if len(out.Flows) >= maxCaptureEntries {
			w.add("more than %d exchanges; the rest were not imported", maxCaptureEntries)
			break
		}
		p := entries[index]
		if p.meta == nil {
			w.add("entry %d: no meta.json; its bodies were skipped", index)
			continue
		}
		var meta charlesChlzMeta
		raw, err := readZipEntry(p.meta)
		if err == nil {
			err = json.Unmarshal(raw, &meta)
		}
		if err != nil || meta.Method == "" || meta.Host == "" {
			w.add("entry %d: malformed or incomplete meta.json; skipped", index)
			continue
		}
		reqBody := readChlzBody(p.req, index, "request", w)
		resBody := readChlzBody(p.res, index, "response", w)
		out.Flows = append(out.Flows, chlzFlow(meta, reqBody, resBody))
	}
	out.Warnings = w.finish()
	return out, nil
}

// entryIsJavaSerialized sniffs a member for the Java serialization magic, so a
// .chls renamed inside an archive is refused too.
func entryIsJavaSerialized(f *zip.File) bool {
	if f.UncompressedSize64 < 4 || f.UncompressedSize64 > maxCaptureBody {
		return false
	}
	rc, err := f.Open()
	if err != nil {
		return false
	}
	defer rc.Close()
	head := make([]byte, 4)
	_, err = io.ReadFull(rc, head)
	return err == nil && isJavaSerialized(head)
}

func readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return readAllLimited(rc, maxCaptureBody)
}

// readChlzBody reads one body entry, or nothing. A body over the per-body limit
// is left out and reported, and the import-wide budget applies as for other
// formats.
func readChlzBody(f *zip.File, index int, side string, w *captureWarnings) []byte {
	if f == nil {
		return nil
	}
	data, err := readZipEntry(f)
	if err != nil {
		w.add("entry %d: the %s body could not be read and was left out", index, side)
		return nil
	}
	if !w.takeBody(data) {
		return nil
	}
	return data
}

func chlzFlow(meta charlesChlzMeta, reqBody, resBody []byte) CaptureFlow {
	scheme := strings.ToLower(meta.Scheme)
	pathValue, query := "/", ""
	if meta.Path != nil && *meta.Path != "" {
		pathValue = *meta.Path
	}
	if meta.Query != nil {
		query = *meta.Query
	}
	flow := CaptureFlow{
		Method: strings.ToUpper(meta.Method), Scheme: scheme, Host: meta.Host, Path: pathValue,
		Status: meta.Response.Status, Millis: meta.Durations.Total, Bytes: meta.TotalSize,
		At:        parseCharlesTime(meta.Times.Start),
		ReqHeader: chlzHeader(meta.Request), ResHeader: chlzHeader(meta.Response),
		ReqBody: reqBody, ResBody: resBody,
		ReqSize:  captureBodySize(meta.Request.Sizes.Body, reqBody),
		ResSize:  captureBodySize(meta.Response.Sizes.Body, resBody),
		ReqProto: meta.ProtocolVersion, ResProto: meta.ProtocolVersion,
		Remote: remoteIP(meta.RemoteAddress), Client: remoteIP(meta.ClientAddress),
	}
	if meta.SSL != nil {
		flow.TLS, flow.Cipher = meta.SSL.Protocol, meta.SSL.CipherSuite
	}
	if meta.Tunnel {
		flow.Kind = "connect"
		flow.URL = captureConnectURL(scheme, meta.Host, meta.ActualPort)
	} else {
		flow.URL = captureURL(scheme, meta.Host, meta.ActualPort, pathValue, query)
	}
	if flow.Status == 0 && meta.Status != "" && meta.Status != "COMPLETE" {
		flow.Error = "Charles recorded status " + meta.Status
	}
	return flow
}

func chlzHeader(message charlesChlzMessage) http.Header {
	pairs := make([][2]string, 0, len(message.Header.Headers))
	for _, h := range message.Header.Headers {
		pairs = append(pairs, [2]string{h.Name, h.Value})
	}
	return captureHeader(pairs)
}
