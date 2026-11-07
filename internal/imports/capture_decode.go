package imports

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
)

var errCaptureBodyLimit = errors.New("decoded body exceeds the import body limit")

// decodeCaptureBody undoes the Content-Encoding a source recorded for a body
// it kept compressed. Exports can write either the wire bytes or the decoded
// bytes; when they say the body is still encoded, the app's body model wants
// the decoded form while the header stays as it was on the wire. The output is
// capped by the caller's remaining import budget so a compression bomb cannot
// grow a capture without limit.
func decodeCaptureBody(data []byte, encoding string, limit int64) ([]byte, error) {
	if len(data) == 0 || strings.TrimSpace(encoding) == "" {
		return data, nil
	}
	if limit <= 0 {
		return nil, errCaptureBodyLimit
	}
	encodings := strings.Split(encoding, ",")
	for i := len(encodings) - 1; i >= 0; i-- {
		encoding := strings.ToLower(strings.TrimSpace(encodings[i]))
		var reader io.Reader
		var closer io.Closer
		switch encoding {
		case "", "identity":
			continue
		case "gzip", "x-gzip":
			r, err := gzip.NewReader(bytes.NewReader(data))
			if err != nil {
				return nil, fmt.Errorf("gzip: %w", err)
			}
			reader, closer = r, r
		case "deflate":
			// Some exporters write zlib-wrapped deflate and others raw deflate.
			// Accept both, as browsers do for Content-Encoding: deflate.
			if r, err := zlib.NewReader(bytes.NewReader(data)); err == nil {
				reader, closer = r, r
			} else {
				reader = flate.NewReader(bytes.NewReader(data))
			}
		case "br":
			reader = brotli.NewReader(bytes.NewReader(data))
		default:
			return nil, fmt.Errorf("unsupported Content-Encoding %q", encoding)
		}
		decoded, err := readAllLimited(reader, limit)
		if closer != nil {
			_ = closer.Close()
		}
		if err != nil {
			if errors.Is(err, errCaptureLimit) {
				return nil, fmt.Errorf("%w: %s", errCaptureBodyLimit, encoding)
			}
			return nil, fmt.Errorf("%s: %w", encoding, err)
		}
		data = decoded
	}
	return data, nil
}
