package inspect

import (
	"bytes"
	"encoding/base64"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"mime"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/vmihailenco/msgpack/v5"
	"github.com/vmihailenco/msgpack/v5/msgpcode"
)

const (
	bodyInputLimit   = 1 << 20
	bodyOutputLimit  = 2 << 20
	bodyPartLimit    = 256
	bodyDepthLimit   = 64
	bodyNodeLimit    = 16384
	bodyPreviewLimit = 4096
)

// BodyFormat returns Form, Multipart, XML, or MessagePack for a supported MIME
// type (including parameters), or an empty string for unsupported/invalid types.
// It only examines the type, never sniffs or parses body bytes.
func BodyFormat(contentType string) string {
	media, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	switch {
	case media == "application/x-www-form-urlencoded":
		return "Form"
	case strings.HasPrefix(media, "multipart/"):
		return "Multipart"
	case media == "application/xml", media == "text/xml", strings.HasSuffix(media, "+xml"):
		return "XML"
	case media == "application/msgpack", media == "application/x-msgpack", media == "application/vnd.msgpack":
		return "MessagePack"
	default:
		return ""
	}
}

// BodyView contains ordered fields/parts for native tables and a copyable
// inspection rendering. It does not retain or modify the source bytes.
type BodyView struct {
	Format string
	Text   string
	Fields []FormField
	Parts  []MultipartPart
}

// InspectBody creates a read-only inspection view. Run it on a worker, not the
// UI thread. It never changes data, uses the filesystem/network, or starts work
// in a goroutine. Errors discard partial output. Limits: 1 MiB input, 2 MiB
// output, 256 multipart parts, depth 64, 16384 nodes/fields, 4 KiB previews.
//
// Forms become ordered JSON entries with raw and decoded key/value spellings.
// Multipart becomes an ordered JSON array with original disposition filenames,
// headers and byte sizes. File/encoded/binary/unsafe/non-UTF-8 fields are never
// previewed. Multipart transfer encodings are not decoded.
//
// XML indents element-only content for display. Mixed content, CDATA and
// xml:space="preserve" subtrees retain their bytes. Prefixes, entities and
// attribute values are not rewritten. This formatted view is not a semantic
// round-trip API: use Raw for significant inter-element whitespace. DTDs and
// directives are rejected; only UTF-8 XML is supported, with no entity resolver.
//
// MessagePack uses JSON numbers without float conversion of integers. Maps use
// {"$map":[{"key":...,"value":...}]} to preserve order, duplicate/nonstring
// keys; binary uses {"$binary":{"base64":"..."}}, extensions (including
// timestamps) use {"$extension":{"type":N,"base64":"..."}}. Nonfinite floats
// use {"$float":"NaN"}, "+Infinity", or "-Infinity". Invalid UTF-8 strings are
// rejected. This is an inspection notation, not a round-trip serialization API.
func InspectBody(contentType string, data []byte) (BodyView, error) {
	var result BodyView
	if len(data) > bodyInputLimit {
		return result, bodyLimit("input")
	}
	media, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return result, errors.New("invalid body content type")
	}
	format := BodyFormat(contentType)
	if format == "" {
		return result, errors.New("unsupported structured body content type")
	}
	if format == "XML" {
		if charset := strings.ToLower(params["charset"]); charset != "" && charset != "utf-8" && charset != "us-ascii" {
			return result, errors.New("XML inspection requires UTF-8")
		}
		text, err := formatBodyXML(data)
		if err != nil {
			return result, err
		}
		return BodyView{Format: format, Text: text}, nil
	}
	var value any
	switch format {
	case "Form":
		if charset := strings.ToLower(params["charset"]); charset != "" && charset != "utf-8" && charset != "us-ascii" {
			return result, errors.New("form inspection requires UTF-8")
		}
		result.Fields, err = formatBodyForm(data)
		value = result.Fields
	case "Multipart":
		boundary := params["boundary"]
		if boundary == "" || len(boundary) > 70 {
			return result, errors.New("multipart requires a boundary of at most 70 bytes")
		}
		result.Parts, err = formatBodyMultipart(media, boundary, data)
		value = result.Parts
	case "MessagePack":
		r := bytes.NewReader(data)
		d := msgpack.NewDecoder(r)
		nodes := 0
		value, err = readBodyMessagePack(d, r, 0, &nodes)
		if err == nil && r.Len() != 0 {
			err = errors.New("MessagePack requires exactly one value")
		}
	}
	if err != nil {
		return BodyView{}, err
	}
	w := new(bodyWriter)
	if err := json.MarshalWrite(w, value, json.Deterministic(true), jsontext.WithIndent("  ")); err != nil {
		return BodyView{}, errors.New("structured body cannot be represented within the output limit")
	}
	result.Format, result.Text = format, w.String()
	return result, nil
}

func bodyLimit(kind string) error { return fmt.Errorf("body inspection limit exceeded: %s", kind) }

type bodyWriter struct{ strings.Builder }

func (w *bodyWriter) Write(p []byte) (int, error) {
	if len(p) > bodyOutputLimit-w.Len() {
		return 0, bodyLimit("output")
	}
	return w.Builder.Write(p)
}

type FormField struct {
	RawKey    string `json:"rawKey"`
	RawValue  string `json:"rawValue"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	HasEquals bool   `json:"hasEquals"`
}

func formatBodyForm(data []byte) ([]FormField, error) {
	entries := []FormField{}
	if !utf8.Valid(data) {
		return nil, errors.New("form inspection requires UTF-8")
	}
	if len(data) == 0 {
		return entries, nil
	}
	for field := range strings.SplitSeq(string(data), "&") {
		if len(entries) == bodyNodeLimit {
			return nil, bodyLimit("form fields")
		}
		rawKey, rawValue, equals := strings.Cut(field, "=")
		key, kerr := url.QueryUnescape(rawKey)
		value, verr := url.QueryUnescape(rawValue)
		if kerr != nil || verr != nil || !utf8.ValidString(key) || !utf8.ValidString(value) {
			return nil, errors.New("invalid form escape or UTF-8")
		}
		entries = append(entries, FormField{rawKey, rawValue, key, value, equals})
	}
	return entries, nil
}

type MultipartPart struct {
	Name             string               `json:"name"`
	Filename         string               `json:"filename"`
	File             bool                 `json:"file"`
	ContentType      string               `json:"contentType"`
	Headers          textproto.MIMEHeader `json:"headers"`
	Size             int                  `json:"size"`
	Preview          *string              `json:"preview,omitzero"`
	PreviewTruncated bool                 `json:"previewTruncated"`
}

func formatBodyMultipart(_ string, boundary string, data []byte) ([]MultipartPart, error) {
	// NextRawPart may report EOF for an unfinished final header. Require an
	// actual closing delimiter, not merely an apparently complete prefix.
	closed := false
	closing := []byte("--" + boundary + "--")
	for line := range bytes.Lines(data) {
		if bytes.Equal(bytes.TrimRight(line, "\r\n \t"), closing) {
			closed = true
			break
		}
	}
	if !closed {
		return nil, errors.New("multipart closing boundary is missing")
	}
	r := multipart.NewReader(bytes.NewReader(data), boundary)
	parts := []MultipartPart{}
	headerNodes := 0
	for {
		part, err := r.NextRawPart()
		if errors.Is(err, io.EOF) {
			return parts, nil
		}
		if err != nil {
			return nil, errors.New("malformed or truncated multipart body")
		}
		if len(parts) == bodyPartLimit {
			return nil, bodyLimit("multipart parts")
		}
		for name, values := range part.Header {
			headerNodes += len(values) + 1
			if headerNodes > bodyNodeLimit {
				return nil, bodyLimit("multipart headers")
			}
			if !utf8.ValidString(name) {
				return nil, errors.New("multipart headers require UTF-8")
			}
			for _, v := range values {
				if !utf8.ValidString(v) {
					return nil, errors.New("multipart headers require UTF-8")
				}
			}
		}
		p := MultipartPart{Headers: part.Header, ContentType: part.Header.Get("Content-Type")}
		if disposition := part.Header.Get("Content-Disposition"); disposition != "" {
			_, params, err := mime.ParseMediaType(disposition)
			if err != nil {
				return nil, errors.New("invalid multipart content disposition")
			}
			p.Name = params["name"]
			p.Filename, p.File = params["filename"]
		}
		b, err := io.ReadAll(io.LimitReader(part, bodyInputLimit+1))
		if err != nil {
			return nil, errors.New("malformed or truncated multipart part")
		}
		if len(b) > bodyInputLimit {
			return nil, bodyLimit("multipart part size")
		}
		p.Size = len(b)
		transfer := strings.ToLower(part.Header.Get("Content-Transfer-Encoding"))
		if !p.File && (transfer == "" || transfer == "binary" || transfer == "7bit" || transfer == "8bit") && bodyTextual(p.ContentType) && bodySafeText(b) {
			end := min(len(b), bodyPreviewLimit)
			for end > 0 && !utf8.Valid(b[:end]) {
				end--
			}
			p.Preview = new(string(b[:end]))
			p.PreviewTruncated = end < len(b)
		}
		parts = append(parts, p)
	}
}

func bodyTextual(contentType string) bool {
	if contentType == "" {
		return true // RFC 7578 form fields default to text/plain.
	}
	media, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	charset := strings.ToLower(params["charset"])
	if charset != "" && charset != "utf-8" && charset != "us-ascii" {
		return false
	}
	return strings.HasPrefix(media, "text/") || media == "application/json" || media == "application/xml" || strings.HasSuffix(media, "+json") || strings.HasSuffix(media, "+xml") || media == "application/x-www-form-urlencoded"
}

func bodySafeText(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	for _, r := range string(b) {
		if (unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t') || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

type bodyXMLFrame struct {
	name xml.Name
	ns   map[string]string
	node *bodyXMLNode
}

type bodyXMLNode struct {
	start, openEnd, closeStart, end int64
	verbatim                        bool
	children                        []*bodyXMLNode
}

func formatBodyXML(data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", errors.New("XML inspection requires UTF-8")
	}
	d := xml.NewDecoder(bytes.NewReader(data))
	stack := []bodyXMLFrame{}
	nodes, roots := 0, 0
	var root *bodyXMLNode
	for {
		start := d.InputOffset()
		token, err := d.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", errors.New("malformed XML body")
		}
		nodes++
		if nodes > bodyNodeLimit {
			return "", bodyLimit("XML nodes")
		}
		raw := data[start:d.InputOffset()]
		switch t := token.(type) {
		case xml.StartElement:
			if len(stack) == bodyDepthLimit {
				return "", bodyLimit("XML depth")
			}
			nodes += len(t.Attr)
			if nodes > bodyNodeLimit {
				return "", bodyLimit("XML attributes")
			}
			ns := map[string]string{"xml": "http://www.w3.org/XML/1998/namespace"}
			if len(stack) == 0 {
				roots++
				if roots > 1 {
					return "", errors.New("XML requires exactly one root element")
				}
			} else {
				ns = maps.Clone(stack[len(stack)-1].ns)
			}
			for _, a := range t.Attr {
				if a.Name.Space == "xmlns" || a.Name == (xml.Name{Local: "xmlns"}) {
					prefix := a.Name.Local
					if a.Name.Space == "" {
						prefix = ""
					}
					if prefix == "xmlns" || a.Value == "http://www.w3.org/2000/xmlns/" || (prefix == "xml") != (a.Value == ns["xml"]) || (prefix != "" && a.Value == "") {
						return "", errors.New("invalid XML namespace declaration")
					}
					ns[prefix] = a.Value
				}
			}
			if !bodyXMLName(t.Name, ns) {
				return "", errors.New("invalid or undeclared XML element prefix")
			}
			seen := map[xml.Name]bool{}
			node := &bodyXMLNode{start: start, openEnd: d.InputOffset()}
			for _, a := range t.Attr {
				if a.Name.Space != "xmlns" && !bodyXMLName(a.Name, ns) {
					return "", errors.New("invalid or undeclared XML attribute prefix")
				}
				name := a.Name
				if name.Space != "" && name.Space != "xmlns" {
					name.Space = ns[name.Space]
				}
				if seen[name] {
					return "", errors.New("duplicate XML attribute")
				}
				seen[name] = true
				if a.Name.Space == "xml" && a.Name.Local == "space" && a.Value == "preserve" {
					node.verbatim = true
				}
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1].node
				parent.children = append(parent.children, node)
			} else {
				root = node
			}
			stack = append(stack, bodyXMLFrame{t.Name, ns, node})
			continue
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1].name != t.Name {
				return "", errors.New("mismatched XML element")
			}
			node := stack[len(stack)-1].node
			node.closeStart, node.end = start, d.InputOffset()
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 && (len(bytes.TrimSpace(t)) != 0 || bytes.HasPrefix(raw, []byte("<![CDATA["))) {
				return "", errors.New("XML character data outside root")
			}
			if len(stack) > 0 && (len(bytes.TrimSpace(t)) != 0 || bytes.HasPrefix(raw, []byte("<![CDATA["))) {
				stack[len(stack)-1].node.verbatim = true
			}
		case xml.Directive:
			return "", errors.New("XML DTDs and directives are disabled")
		case xml.ProcInst:
			if strings.EqualFold(t.Target, "xml") && (t.Target != "xml" || start != 0) {
				return "", errors.New("invalid XML declaration placement")
			}
		}
		switch token.(type) {
		case xml.Comment, xml.ProcInst:
			if len(stack) > 0 {
				parent := stack[len(stack)-1].node
				parent.children = append(parent.children, &bodyXMLNode{start: start, end: d.InputOffset(), verbatim: true})
			}
		}
	}
	if roots != 1 || len(stack) != 0 {
		return "", errors.New("incomplete XML document")
	}
	w := new(bodyWriter)
	if _, err := w.Write(data[:root.start]); err != nil {
		return "", err
	}
	if err := writeBodyXMLNode(w, data, root, 0); err != nil {
		return "", err
	}
	if _, err := w.Write(data[root.end:]); err != nil {
		return "", err
	}
	return w.String(), nil
}

func writeBodyXMLNode(w *bodyWriter, data []byte, node *bodyXMLNode, depth int) error {
	if node.verbatim || len(node.children) == 0 {
		_, err := w.Write(data[node.start:node.end])
		return err
	}
	if err := writeBodyXMLStart(w, data[node.start:node.openEnd], depth); err != nil {
		return err
	}
	for _, child := range node.children {
		if _, err := w.Write([]byte("\n" + strings.Repeat("  ", depth+1))); err != nil {
			return err
		}
		if err := writeBodyXMLNode(w, data, child, depth+1); err != nil {
			return err
		}
	}
	if _, err := w.Write([]byte("\n" + strings.Repeat("  ", depth))); err != nil {
		return err
	}
	_, err := w.Write(data[node.closeStart:node.end])
	return err
}

func bodyXMLName(name xml.Name, ns map[string]string) bool {
	return !strings.Contains(name.Local, ":") && !strings.Contains(name.Space, ":") && name.Space != "xmlns" && (name.Space == "" || ns[name.Space] != "")
}

// The XML decoder has already validated the tag. Only replace separators
// outside quotes; never round-trip through xml.Encoder (which rewrites prefixes,
// entities, empty tags, and attribute values).
func writeBodyXMLStart(w *bodyWriter, raw []byte, depth int) error {
	quote := byte(0)
	for i := 0; i < len(raw); {
		c := raw[i]
		if quote == 0 && (c == ' ' || c == '\t' || c == '\n' || c == '\r') {
			end := i + 1
			for end < len(raw) && bytes.ContainsRune([]byte(" \t\n\r"), rune(raw[end])) {
				end++
			}
			// Whitespace beside '=' is part of an attribute, not a separator.
			if end < len(raw) && raw[end] != '=' && raw[end] != '/' && raw[end] != '>' && i > 0 && raw[i-1] != '=' {
				if _, err := w.Write([]byte("\n" + strings.Repeat("  ", depth+1))); err != nil {
					return err
				}
			} else if _, err := w.Write(raw[i:end]); err != nil {
				return err
			}
			i = end
			continue
		}
		if quote == c {
			quote = 0
		} else if quote == 0 && (c == '\'' || c == '"') {
			quote = c
		}
		if _, err := w.Write(raw[i : i+1]); err != nil {
			return err
		}
		i++
	}
	return nil
}

type bodyMapEntry struct {
	Key   any `json:"key"`
	Value any `json:"value"`
}

func readBodyMessagePack(d *msgpack.Decoder, r *bytes.Reader, depth int, nodes *int) (any, error) {
	*nodes++
	if depth > bodyDepthLimit || *nodes > bodyNodeLimit {
		return nil, bodyLimit("MessagePack depth or nodes")
	}
	code, err := d.PeekCode()
	if err != nil {
		return nil, errors.New("truncated MessagePack body")
	}
	switch {
	case msgpcode.IsFixedArray(code) || code == msgpcode.Array16 || code == msgpcode.Array32:
		n, err := d.DecodeArrayLen()
		if err != nil || n < 0 || n > r.Len() {
			return nil, errors.New("malformed or truncated MessagePack array")
		}
		if n > bodyNodeLimit-*nodes {
			return nil, bodyLimit("MessagePack array nodes")
		}
		a := make([]any, n)
		for i := range n {
			a[i], err = readBodyMessagePack(d, r, depth+1, nodes)
			if err != nil {
				return nil, err
			}
		}
		return a, nil
	case msgpcode.IsFixedMap(code) || code == msgpcode.Map16 || code == msgpcode.Map32:
		n, err := d.DecodeMapLen()
		if err != nil || n < 0 || n > r.Len()/2 {
			return nil, errors.New("malformed or truncated MessagePack map")
		}
		if n > (bodyNodeLimit-*nodes)/2 {
			return nil, bodyLimit("MessagePack map nodes")
		}
		entries := make([]bodyMapEntry, n)
		for i := range n {
			entries[i].Key, err = readBodyMessagePack(d, r, depth+1, nodes)
			if err == nil {
				entries[i].Value, err = readBodyMessagePack(d, r, depth+1, nodes)
			}
			if err != nil {
				return nil, err
			}
		}
		return map[string]any{"$map": entries}, nil
	case msgpcode.IsString(code) || msgpcode.IsBin(code) || msgpcode.IsExt(code):
		var n int
		var tag int8
		if msgpcode.IsExt(code) {
			tag, n, err = d.DecodeExtHeader()
		} else {
			n, err = d.DecodeBytesLen()
		}
		if err != nil || n < 0 || n > r.Len() {
			return nil, errors.New("malformed or truncated MessagePack bytes")
		}
		b := make([]byte, n)
		if err := d.ReadFull(b); err != nil {
			return nil, errors.New("truncated MessagePack bytes")
		}
		if msgpcode.IsString(code) {
			if !utf8.Valid(b) {
				return nil, errors.New("MessagePack string is not UTF-8")
			}
			return string(b), nil
		}
		encoded := base64.StdEncoding.EncodeToString(b)
		if msgpcode.IsExt(code) {
			return map[string]any{"$extension": map[string]any{"type": tag, "base64": encoded}}, nil
		}
		return map[string]any{"$binary": map[string]any{"base64": encoded}}, nil
	default:
		// Only scalars reach the library's dynamic decoder: no unbounded
		// recursive decoding, container allocation, or global ext handlers.
		v, err := d.DecodeInterface()
		if err != nil {
			return nil, errors.New("malformed or truncated MessagePack scalar")
		}
		var f float64
		switch n := v.(type) {
		case float32:
			f = float64(n)
		case float64:
			f = n
		}
		if math.IsNaN(f) {
			return map[string]string{"$float": "NaN"}, nil
		}
		if math.IsInf(f, 1) {
			return map[string]string{"$float": "+Infinity"}, nil
		}
		if math.IsInf(f, -1) {
			return map[string]string{"$float": "-Infinity"}, nil
		}
		return v, nil
	}
}
