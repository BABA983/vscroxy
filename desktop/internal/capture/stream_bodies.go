package capture

// This file keeps the bodies the engine streams past its buffered path. A body
// past proxy.Options.StreamLargeBodies never reaches Addon.Request or
// Addon.Response: the engine hands the whole body to StreamRequestModifier or
// StreamResponseModifier instead, so without this an exchange with a large body
// would publish no row at all. The readers returned here pass every byte
// through untouched and keep a copy, and the flow is published once, when its
// response ends, when a failure ends it, or when the flow is done.

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/lqqyt2423/go-mitmproxy/proxy"
)

// errStreamAborted is what a body whose transfer stopped before its end is
// marked with, as when a client hangs up mid-download.
var errStreamAborted = errors.New("the transfer stopped before the body ended")

// bodyResult is one streamed body as the flow carries it: the head kept
// inline, the file that holds all of it when it spilled, its decoded size, how
// many wire bytes it took, and why it is not the whole body when it is not.
type bodyResult struct {
	Head string
	File string
	Size int
	Wire int
	Err  string
}

// streamSink keeps a body as it arrives: up to maxBody in memory, and the
// whole body in a file once it grows past that, so a large body never sits in
// memory. A failed write never fails the transfer it is copying; the error is
// kept for the flow instead.
type streamSink struct {
	dir   string
	path  string
	head  []byte
	file  *os.File
	total int // bytes offered
	spill int // bytes in the file
	err   error
}

func (k *streamSink) Write(p []byte) (int, error) {
	n := len(p)
	k.total += n
	if room := maxBody - len(k.head); room > 0 {
		kept := p[:min(n, room)]
		k.head = append(k.head, kept...)
		p = p[len(kept):]
	}
	if len(p) > 0 {
		k.write(p)
	}
	return n, nil
}

// write appends to the spill file, opening it with the head already in it so
// the file always holds the whole body.
func (k *streamSink) write(p []byte) {
	if k.err != nil {
		return
	}
	if k.file == nil {
		if k.dir == "" {
			k.err = errors.New("no spill directory")
			return
		}
		f, err := os.OpenFile(k.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			k.err = err
			return
		}
		k.file = f
		n, err := f.Write(k.head)
		k.spill += n
		if err != nil {
			k.err = err
			return
		}
	}
	n, err := k.file.Write(p)
	k.spill += n
	if err != nil {
		k.err = err
	}
}

func (k *streamSink) close() {
	if k.file == nil {
		return
	}
	err := k.file.Close()
	k.file = nil
	if err != nil && k.err == nil {
		k.err = err
	}
}

// streamBody keeps one body the engine streams. The wire bytes go to a spill
// file once they pass the inline head, and finish turns them into the decoded
// body the flow carries.
type streamBody struct {
	mu     sync.Mutex
	sink   streamSink // the wire bytes: the head inline, then the file
	name   string     // the file names this body uses in the spill directory
	enc    string     // the Content-Encoding of those bytes
	want   int        // the declared length, or -1 when the body was chunked
	eof    bool
	failed error

	finished bool
	result   bodyResult
}

func newStreamBody(dir, name, enc string, want int) *streamBody {
	b := &streamBody{
		sink: streamSink{dir: dir},
		name: name,
		enc:  strings.ToLower(strings.TrimSpace(enc)),
		want: want,
	}
	if dir != "" {
		b.sink.path = filepath.Join(dir, "raw-"+name+".bin")
	}
	return b
}

// write keeps a copy of the bytes that passed. It never fails the transfer.
func (b *streamBody) write(p []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finished {
		return
	}
	_, _ = b.sink.Write(p)
}

// end records how the transfer of the body ended: io.EOF means all of it
// arrived, anything else that is not nil means it was cut off.
func (b *streamBody) end(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case errors.Is(err, io.EOF):
		b.eof = true
	case err != nil && b.failed == nil && !b.eof:
		b.failed = err
	}
}

// release drops a body the flow never carried, as when the proxy stops with an
// exchange still in flight.
func (b *streamBody) release() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.finished = true
	b.sink.close()
	b.dropRaw()
}

// finish turns the bytes kept into the body the flow carries: the head inline
// and the whole decoded body in the spill file when it is larger. It may be
// called more than once, and from another goroutine than the one reading.
func (b *streamBody) finish() bodyResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.finished {
		b.finished = true
		b.result = b.decode()
	}
	return b.result
}

// decode is finish's work, with the body's lock held.
func (b *streamBody) decode() bodyResult {
	b.sink.close()
	res := bodyResult{Wire: b.sink.total}
	if err := b.problem(); err != nil {
		// What is here is not the body: say so, and keep only the head of a
		// body that needs no decoding, never a file that reads as complete.
		res.Size = b.sink.total
		res.Err = err.Error()
		if b.identity() {
			res.Head = string(b.sink.head)
		}
		b.dropRaw()
		return res
	}
	if b.identity() {
		// The wire bytes are the body, so the spill file is already it.
		res.Size = b.sink.total
		res.Head = string(b.sink.head)
		if b.sink.spill > 0 {
			file := filepath.Join(b.sink.dir, b.name+".bin")
			if err := os.Rename(b.sink.path, file); err != nil {
				res.Err = "the body could not be kept: " + err.Error()
			} else {
				res.File = file
			}
		}
		return res
	}
	src, done, err := b.open()
	if err != nil {
		res.Size = b.sink.total
		res.Err = "the body could not be read back: " + err.Error()
		return res
	}
	defer done()
	dec, err := newDecoder(b.enc, src)
	if err != nil {
		// An encoding the capture cannot decode leaves the wire bytes as no
		// body at all, rather than as a body the search would trust.
		res.Size = b.sink.total
		res.Err = err.Error()
		b.dropRaw()
		return res
	}
	defer dec.Close()
	out := &streamSink{dir: b.sink.dir}
	if b.sink.dir != "" {
		out.path = filepath.Join(b.sink.dir, b.name+".bin")
	}
	_, copyErr := io.Copy(out, dec)
	out.close()
	res.Size = out.total
	res.Head = string(out.head)
	switch {
	case copyErr != nil:
		res.Err = fmt.Sprintf("the %s body could not be decoded: %v", b.enc, copyErr)
	case out.err != nil:
		res.Err = "the body could not be kept: " + out.err.Error()
	}
	switch {
	case res.Err != "":
		// A partial payload is not the body: do not hand out a file that
		// reads as if it were all of it.
		if out.path != "" {
			_ = os.Remove(out.path)
		}
	case out.spill > 0:
		res.File = out.path
	}
	b.dropRaw()
	return res
}

// problem says why the bytes kept are not the whole body, or nil when they are.
func (b *streamBody) problem() error {
	switch {
	case b.sink.err != nil:
		return fmt.Errorf("the body could not be kept: %w", b.sink.err)
	case b.failed != nil:
		return fmt.Errorf("the body was cut off: %w", b.failed)
	case b.want >= 0 && b.sink.total < b.want:
		return fmt.Errorf("the body was cut off: %d of %d bytes", b.sink.total, b.want)
	case b.eof:
		return nil
	case b.sink.total > 0:
		return errors.New("the body was cut off before it ended")
	}
	return nil
}

// identity reports whether the wire bytes are the body itself.
func (b *streamBody) identity() bool { return b.enc == "" || b.enc == "identity" }

// open reads the wire bytes back: from the head when they fit in memory, from
// the spill file when they did not.
func (b *streamBody) open() (io.Reader, func(), error) {
	if b.sink.spill == 0 {
		return bytes.NewReader(b.sink.head), func() {}, nil
	}
	f, err := os.Open(b.sink.path)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { _ = f.Close() }, nil
}

func (b *streamBody) dropRaw() {
	if b.sink.dir != "" && b.sink.spill > 0 {
		_ = os.Remove(b.sink.path)
	}
}

// decoder is a streaming decoder for one content-encoding, the encodings the
// engine's buffered path decodes a captured body with.
type decoder struct {
	io.Reader
	close func()
}

func (d *decoder) Close() error {
	d.close()
	return nil
}

// newDecoder opens the decoder for a content-encoding. It fails for an encoding
// the capture cannot decode, so the flow says so instead of showing compressed
// bytes as the body.
func newDecoder(enc string, src io.Reader) (*decoder, error) {
	switch enc {
	case "", "identity":
		return &decoder{Reader: src, close: func() {}}, nil
	case "gzip":
		r, err := gzip.NewReader(src)
		if err != nil {
			return nil, err
		}
		return &decoder{Reader: r, close: func() { _ = r.Close() }}, nil
	case "deflate":
		r := flate.NewReader(src)
		return &decoder{Reader: r, close: func() { _ = r.Close() }}, nil
	case "br":
		return &decoder{Reader: brotli.NewReader(src), close: func() {}}, nil
	case "zstd":
		r, err := zstd.NewReader(src)
		if err != nil {
			return nil, err
		}
		return &decoder{Reader: r, close: r.Close}, nil
	}
	return nil, fmt.Errorf("unsupported content-encoding %q", enc)
}

// streamBodies holds the bodies of one flow the engine streamed, and whether
// the flow has been published: publishing takes the bodies once, and the entry
// lives until the flow is done, so a body that arrives late cannot publish a
// second row.
type streamBodies struct {
	mu        sync.Mutex
	req, res  *streamBody
	published bool
}

// add keeps a body for its side. It reports false when the flow is already
// published, since nothing more belongs to it then.
func (b *streamBodies) add(side string, body *streamBody) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.published {
		return false
	}
	if side == "req" {
		b.req = body
	} else {
		b.res = body
	}
	return true
}

// take claims the right to publish the flow, for the one caller that gets true.
func (b *streamBodies) take() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.published {
		return false
	}
	b.published = true
	return true
}

func (b *streamBodies) sides() (req, res *streamBody) {
	if b == nil {
		return nil, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.req, b.res
}

func (b *streamBodies) release() {
	if b == nil {
		return
	}
	req, res := b.sides()
	if req != nil {
		req.release()
	}
	if res != nil {
		res.release()
	}
}

// captureStream starts keeping a body the engine streams and returns the reader
// the transfer should use: every byte passes through untouched, and a copy
// stays with the flow.
func (s *Server) captureStream(f *proxy.Flow, side string, in io.Reader, enc string, want int) io.Reader {
	if in == nil {
		return nil
	}
	body := newStreamBody(s.bodyDir, side+"-"+f.Id.String(), enc, want)
	s.mu.Lock()
	st := s.streams[f.Id]
	watch := st == nil
	if watch {
		st = &streamBodies{}
		s.streams[f.Id] = st
	}
	s.mu.Unlock()
	if !st.add(side, body) {
		// The flow is already published, so it keeps nothing more.
		return in
	}
	if watch {
		go s.awaitStream(f)
	}
	return &streamReader{src: in, body: body, flow: f, side: side, server: s}
}

// contentEncoding is the encoding a header block declares, or empty.
func contentEncoding(h http.Header) string {
	return strings.ToLower(strings.TrimSpace(h.Get("Content-Encoding")))
}

// requestLength is the length a request declared, or -1 when it was chunked.
// The server keeps the length out of the header map, so it is read from the
// request itself.
func requestLength(r *proxy.Request) int {
	if raw := r.Raw(); raw != nil {
		return int(raw.ContentLength)
	}
	return declaredLength(r.Header)
}

// declaredLength is the length a header block declares, or -1 when the body
// was chunked.
func declaredLength(h http.Header) int {
	if n, err := strconv.Atoi(h.Get("Content-Length")); err == nil {
		return n
	}
	return -1
}

// streamReader passes a streamed body through and keeps a copy for the flow.
type streamReader struct {
	src    io.Reader
	body   *streamBody
	flow   *proxy.Flow
	side   string
	server *Server
}

func (r *streamReader) Read(p []byte) (int, error) {
	// The source is read without the body's lock: a request body can block on
	// a slow client while the transport closes it from another goroutine.
	n, err := r.src.Read(p)
	if n > 0 {
		r.body.write(p[:n])
	}
	if err != nil {
		r.body.end(err)
		r.server.streamEnded(r.flow, r.side)
	}
	return n, err
}

// Close seals a body whose transfer stopped, as the transport does when it
// gives up on a request body.
func (r *streamReader) Close() error {
	r.body.end(errStreamAborted)
	r.server.streamEnded(r.flow, r.side)
	if c, ok := r.src.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// streamEnded finishes a streamed body. The end of a response completes the
// flow; the end of a request only finishes that side, since its response is
// still to come.
func (s *Server) streamEnded(f *proxy.Flow, side string) {
	// The body has ended, so the transport has finished the trailer block of
	// this side: keep it before the flow is published, which the end of a
	// response does right here.
	s.keepTrailers(f, side, sideTrailer(f, side))
	if side == "res" {
		s.publishStreamed(f)
	}
}

// sideTrailer is the trailer block one side of a flow ended with, as the
// engine's flow holds it.
func sideTrailer(f *proxy.Flow, side string) http.Header {
	if side == "req" {
		return f.Request.Trailers()
	}
	return f.Response.Trailers()
}

// publishStreamed publishes the flow of a streamed exchange, once: the end of
// its response, a failure, or its Done publishes it, and the others find it
// published already.
func (s *Server) publishStreamed(f *proxy.Flow) {
	if st := s.takeStream(f); st != nil {
		s.publishFlow(f, "http", nil, 0, st)
	}
}

// takeStream hands a flow's streamed bodies to the one caller that publishes
// them, and nil to every other.
func (s *Server) takeStream(f *proxy.Flow) *streamBodies {
	s.mu.Lock()
	st := s.streams[f.Id]
	s.mu.Unlock()
	if st == nil || !st.take() {
		return nil
	}
	return st
}

// awaitStream publishes a flow whose body is still being kept when the exchange
// ends, so a client that hangs up mid-body still leaves one row.
func (s *Server) awaitStream(f *proxy.Flow) {
	select {
	case <-f.Done():
		s.publishStreamed(f)
	case <-s.closed:
		// The proxy is stopping: drop what is left rather than publish into
		// a stream nobody reads.
	}
	s.mu.Lock()
	st := s.streams[f.Id]
	delete(s.streams, f.Id)
	s.mu.Unlock()
	if st != nil && !st.taken() {
		st.release()
	}
}

// taken reports whether a flow's bodies were already published.
func (b *streamBodies) taken() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.published
}
