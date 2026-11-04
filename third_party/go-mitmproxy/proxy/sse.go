package proxy

import (
	"bufio"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// sseReader wraps an io.Reader to parse SSE events and trigger hooks
type sseReader struct {
	flow      *Flow
	proxy     *Proxy
	reader    *bufio.Reader
	buffer    []byte // Current block, including its original line endings.
	scan      int
	lineStart int
	eventEnd  int
	skipLF    bool // A CR ends a line immediately; a following LF belongs to it.
	ended     bool
	mu        sync.Mutex
}

// newSSEReader creates a new SSE reader wrapper
func newSSEReader(f *Flow, r io.Reader) io.Reader {
	return &sseReader{
		flow:   f,
		proxy:  f.ConnContext.proxy,
		reader: bufio.NewReader(r),
		buffer: make([]byte, 0, 1024),
	}
}

// Read implements io.Reader, parsing SSE events as data is read
func (sr *sseReader) Read(p []byte) (n int, err error) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	if sr.ended {
		return 0, io.EOF
	}

	n, err = sr.reader.Read(p)
	if n > 0 {
		sr.buffer = append(sr.buffer, p[:n]...)
		sr.parseEvents()
	}

	if err != nil {
		// Only a blank line dispatches an event, never EOF or a read failure.
		sr.buffer = nil
		sr.triggerEnd()
	}

	return n, err
}

// parseEvents parses SSE events from the buffer
func (sr *sseReader) parseEvents() {
	for sr.scan < len(sr.buffer) {
		i := sr.scan
		b := sr.buffer[i]
		sr.scan++
		if sr.skipLF {
			sr.skipLF = false
			if b == '\n' {
				if i == 0 {
					// The preceding CR dispatched a block; don't retain its LF.
					sr.buffer = sr.buffer[1:]
					sr.scan = 0
				}
				sr.lineStart = sr.scan
				continue
			}
		}
		if b != '\r' && b != '\n' {
			continue
		}
		sr.skipLF = b == '\r'
		if i == sr.lineStart {
			sr.parseAndFlushEvent(string(sr.buffer[:sr.eventEnd]))
			sr.buffer = sr.buffer[sr.scan:]
			sr.scan = 0
			sr.eventEnd = 0
		} else {
			sr.eventEnd = i
		}
		sr.lineStart = sr.scan
	}
}

// parseAndFlushEvent 解析并触发一个事件
func (sr *sseReader) parseAndFlushEvent(eventData string) {
	event, hasData := sr.parseEvent(eventData)
	if !hasData {
		return
	}
	sr.flow.SSE.addEvent(event)
	for _, addon := range sr.proxy.Addons {
		addon.SSEMessage(sr.flow)
	}
}

// parseEvent 解析 SSE 事件文本
func (sr *sseReader) parseEvent(text string) (*SSEEvent, bool) {
	event := &SSEEvent{
		Event: "message",
		Data:  "",
		Raw:   text,
		Time:  time.Now(),
	}

	var data strings.Builder
	hasData := false
	for line := range strings.FieldsFuncSeq(text, func(r rune) bool { return r == '\r' || r == '\n' }) {
		if strings.HasPrefix(line, ":") {
			continue
		}

		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "data":
			hasData = true
			data.WriteString(value)
			data.WriteByte('\n')
		case "event":
			event.Event = value
		case "id":
			event.ID = value
		case "retry":
			if retry, err := strconv.Atoi(value); err == nil {
				event.Retry = retry
			}
		}
	}
	event.Data = strings.TrimSuffix(data.String(), "\n")
	if event.Event == "" {
		event.Event = "message"
	}
	return event, hasData
}

// triggerEnd marks this reader exhausted; the request handler emits SSEEnd.
func (sr *sseReader) triggerEnd() {
	if sr.ended {
		return
	}

	sr.ended = true

	// The request handler owns SSEEnd, including downstream disconnects
	// that prevent this reader from observing EOF.
	log.Debugf("SSE stream ended for %s", sr.flow.Request.URL.String())
}
