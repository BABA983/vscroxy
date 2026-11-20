package capture

// What a breakpoint shows and what it sends are two different things. The
// editor holds decoded UTF-8 text up to MaxBreakpointBody; the proxy keeps
// the original bytes. This file builds that bounded view and streams the
// original (or a file the user chose) when a decision is applied.

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"unicode/utf8"
)

// breakpointBody is the source of one paused body. wire is a buffered body's
// encoded bytes, file a wire body kept on disk, enc the Content-Encoding of
// either. rawOnly means the encoding could not be decoded, so the bytes are
// shown and saved exactly as received.
type breakpointBody struct {
	wire    []byte
	file    string
	enc     string
	rawOnly bool
	known   bool
}

// breakpointView is how much of a body the editor may see. A body over the
// limit is left out entirely: the dialog offers file actions instead of a
// text field holding bytes it would silently cut short.
type breakpointView struct {
	text        string
	size        int
	readOnly    bool
	undecodable bool
}

// view reads at most one byte past the editor limit to tell "fits" from
// "larger". It counts the rest through a decoder without keeping it.
func (b breakpointBody) view() breakpointView {
	if b.identity() && b.file == "" {
		v := breakpointView{size: len(b.wire)}
		if v.size > MaxBreakpointBody {
			v.readOnly = true
			return v
		}
		v.text = string(b.wire)
		return checkBreakpointText(v)
	}
	src, err := b.reader()
	if err != nil {
		return breakpointView{size: len(b.wire), readOnly: true, undecodable: true}
	}
	defer src.Close()
	buf := make([]byte, MaxBreakpointBody+1)
	n, err := io.ReadAtLeast(src, buf, len(buf))
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return breakpointView{size: len(b.wire), readOnly: true, undecodable: true}
	}
	v := breakpointView{size: n}
	rest, err := io.Copy(io.Discard, src)
	if err != nil {
		return breakpointView{size: len(b.wire), readOnly: true, undecodable: true}
	}
	v.size += int(rest)
	if n <= MaxBreakpointBody {
		v.text = string(buf[:n])
		v = checkBreakpointText(v)
	} else {
		v.readOnly = true
	}
	return v
}

func checkBreakpointText(v breakpointView) breakpointView {
	if !utf8.ValidString(v.text) || strings.IndexByte(v.text, 0) >= 0 {
		v.text, v.readOnly = "", true
	}
	return v
}

func (b breakpointBody) empty() bool { return !b.known }

// identity reports whether the body's bytes are already the decoded body.
func (b breakpointBody) identity() bool { return b.rawOnly || b.enc == "" || b.enc == "identity" }

// reader streams the decoded body. It never materializes more than a
// decoder needs, so saving a large body copies it to disk in constant memory.
func (b breakpointBody) reader() (io.ReadCloser, error) {
	raw, err := b.rawReader()
	if err != nil {
		return nil, err
	}
	if b.identity() {
		return raw, nil
	}
	dec, err := newDecoder(b.enc, raw)
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	return &bodyReadCloser{Reader: dec, close: func() { _ = dec.Close(); _ = raw.Close() }}, nil
}

// rawReader opens the bytes as they went over the wire.
func (b breakpointBody) rawReader() (io.ReadCloser, error) {
	if b.file != "" {
		return os.Open(b.file)
	}
	return io.NopCloser(bytes.NewReader(b.wire)), nil
}

type bodyReadCloser struct {
	io.Reader
	close func()
	once  sync.Once
}

func (r *bodyReadCloser) Close() error { r.once.Do(r.close); return nil }

// SaveBreakpointBody writes a paused body to path as decoded bytes. A body
// that could not be decoded is written exactly as it arrived. The copy is a
// stream, so a body larger than the editor limit never grows the app heap.
func (s *Server) SaveBreakpointBody(bp *Breakpoint, path string) (int64, error) {
	if bp == nil || bp.source.empty() {
		return 0, errors.New("the paused body is no longer available")
	}
	src, err := bp.source.reader()
	if err != nil {
		return 0, err
	}
	defer src.Close()
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(out, src)
	closeErr := out.Close()
	if copyErr != nil {
		return n, copyErr
	}
	return n, closeErr
}
