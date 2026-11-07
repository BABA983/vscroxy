package request

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/certs"
	"golang.org/x/text/encoding/unicode"
)

// SSEEvent is a dispatched event, not a heartbeat or an incomplete EOF block.
type SSEEvent struct {
	Type, ID, Data string
}

// SSEUpdate reports connection transitions and events serially on the caller's
// goroutine. Header belongs to this update; callbacks must return promptly.
type SSEUpdate struct {
	State, URL, LastEventID string
	Header                  http.Header
	Retry                   time.Duration
	Event                   *SSEEvent
}

// ReceiveSSE receives GET event streams through the capture proxy until EOF,
// failure, HTTP 204 or cancellation. Reconnect is explicit opt-in. Requests and
// headers are snapshotted; redirect handling uses net/http's credential policy.
// No request scripts run. A complete event/line is limited to 1 MiB.
func ReceiveSSE(ctx context.Context, proxy string, req *Prepared, ca []byte, certificates certs.Settings, jar http.CookieJar, reconnect bool, emit func(SSEUpdate)) error {
	u, err := url.Parse(req.URL)
	if err != nil || u.Hostname() == "" || u.Scheme != "http" && u.Scheme != "https" || u.User != nil {
		return errors.New("SSE requires an absolute HTTP(S) URL without embedded credentials")
	}
	if req.Method != "" && req.Method != http.MethodGet || req.Body != "" {
		return errors.New("SSE uses GET with no request body")
	}
	proxyURL, err := url.Parse("http://" + proxy)
	if err != nil || proxy == "" || proxyURL.Hostname() == "" {
		return errors.New("SSE requires a running capture proxy")
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = http.ProxyURL(proxyURL)
	base.DisableKeepAlives = true
	base.ResponseHeaderTimeout = 15 * time.Second
	client := &http.Client{Transport: certificates.Transport(base, ca, false), Jar: jar}
	header := req.Header.Clone()
	if header == nil {
		header = http.Header{}
	}
	header.Set("Accept", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Del("Content-Length")
	decoder := sseDecoder{retry: 3 * time.Second, lastID: header.Get("Last-Event-ID")}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return err
		}
		r.Header = header.Clone()
		r.Host = r.Header.Get("Host")
		r.Header.Del("Host")
		r.Header.Del("Last-Event-ID")
		if decoder.lastID != "" {
			r.Header.Set("Last-Event-ID", decoder.lastID)
		}
		resp, err := client.Do(r)
		if ctx.Err() != nil {
			if resp != nil {
				resp.Body.Close()
			}
			return ctx.Err()
		}
		if err == nil {
			if resp.StatusCode == http.StatusNoContent {
				resp.Body.Close()
				emit(SSEUpdate{State: "Stopped", URL: resp.Request.URL.String(), Header: resp.Header.Clone(), LastEventID: decoder.lastID})
				return nil
			}
			media, _, parseErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
			if resp.StatusCode != http.StatusOK || parseErr != nil || media != "text/event-stream" {
				resp.Body.Close()
				return fmt.Errorf("SSE requires HTTP 200 and text/event-stream; got %s (%s)", resp.Status, resp.Header.Get("Content-Type"))
			}
			emit(SSEUpdate{State: "Connected", URL: resp.Request.URL.String(), Header: resp.Header.Clone(), LastEventID: decoder.lastID})
			err = decoder.read(ctx, resp.Body, func(e SSEEvent) { emit(SSEUpdate{Event: &e}) })
			resp.Body.Close()
			if errors.Is(err, errSSELimit) {
				return err
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !reconnect {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		emit(SSEUpdate{State: "Reconnecting", LastEventID: decoder.lastID, Retry: decoder.retry})
		timer := time.NewTimer(decoder.retry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

const maxSSEEvent = 1 << 20

var errSSELimit = errors.New("SSE line or event exceeds 1 MiB")

type sseDecoder struct {
	lastID string
	retry  time.Duration
}

// read follows WHATWG event-stream framing, including lone CR, empty data,
// one optional space, persistent IDs and discarding un-dispatched EOF data.
func (d *sseDecoder) read(ctx context.Context, r io.Reader, emit func(SSEEvent)) error {
	reader := bufio.NewReader(r)
	var line []byte
	var data strings.Builder
	event, pendingID := "", d.lastID
	first, afterCR := true, false
	process := func() error {
		text := string(line)
		if first {
			text = strings.TrimPrefix(text, "\xef\xbb\xbf")
			first = false
		}
		if !utf8.ValidString(text) {
			// Replace maximal ill-formed subparts, not each byte of a valid
			// prefix of a truncated multibyte sequence (WHATWG UTF-8 decode).
			text, _ = unicode.UTF8.NewDecoder().String(text)
		}
		if len(text) > maxSSEEvent {
			return errSSELimit
		}
		if text == "" {
			d.lastID = pendingID
			if data.Len() > 0 {
				emit(SSEEvent{Type: cmp.Or(event, "message"), ID: d.lastID, Data: strings.TrimSuffix(data.String(), "\n")})
			}
			data.Reset()
			event = ""
			return nil
		}
		field, value, _ := strings.Cut(text, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "data":
			if data.Len()+len(value)+1 > maxSSEEvent+1 {
				return errSSELimit
			}
			data.WriteString(value)
			data.WriteByte('\n')
		case "event":
			event = value
		case "id":
			if !strings.ContainsRune(value, 0) {
				pendingID = value
			}
		case "retry":
			if value != "" && !strings.ContainsFunc(value, func(r rune) bool { return r < '0' || r > '9' }) {
				if ms, err := strconv.ParseUint(value, 10, 64); err == nil {
					// Avoid overflow and zero-delay retry loops on a busy endpoint.
					d.retry = time.Duration(max(100, min(ms, 300_000))) * time.Millisecond
				}
			}
		}
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		b, err := reader.ReadByte()
		if err != nil {
			return err
		} // EOF never dispatches an incomplete event.
		if afterCR {
			afterCR = false
			if b == '\n' {
				continue
			}
		}
		if b == '\r' || b == '\n' {
			if err := process(); err != nil {
				return err
			}
			line = line[:0]
			afterCR = b == '\r'
		} else {
			line = append(line, b)
			if len(line) > maxSSEEvent {
				return errSSELimit
			}
		}
	}
}
