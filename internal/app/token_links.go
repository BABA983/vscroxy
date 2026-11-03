package app

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	tokenSelectionLimit = 200
	tokenMetadataLimit  = 64 << 10
	tokenResponseLimit  = 256 << 10
	tokenReadLimit      = 4 << 20
	tokenJSONDepth      = 32
	tokenJSONNodes      = 16384
)

type tokenLink struct {
	Source    int      `json:"source"`
	Path      []string `json:"path"`
	Consumers []int    `json:"consumers"`
	token     string
}

type tokenAnalysis struct {
	Links   []tokenLink `json:"links"`
	Skipped int         `json:"skipped"`
}

// recognizeTokenLinks is read-only inference, not proof of a dependency. The
// caller supplies immutable header-cloned snapshots and runs this on a worker:
// retained spill files are read here, never the display previews. Skipped counts
// selected responses that could not be fully inspected, not unmatched fields.
// Cancellation returns no partial analysis and only the standard context error.
func recognizeTokenLinks(ctx context.Context, flows []flow) (tokenAnalysis, error) {
	if err := ctx.Err(); err != nil {
		return tokenAnalysis{}, err
	}
	if len(flows) == 0 || len(flows) > tokenSelectionLimit {
		return tokenAnalysis{}, errors.New("select 1–200 unique positive HTTP flow IDs")
	}
	seen := make(map[int]bool, len(flows))
	for _, f := range flows {
		if f.id <= 0 || seen[f.id] || f.kind != "" || strings.EqualFold(f.method, "CONNECT") {
			return tokenAnalysis{}, errors.New("select 1–200 unique positive HTTP flow IDs")
		}
		seen[f.id] = true
		if !tokenMetadataOK(f) {
			return tokenAnalysis{}, errors.New("selected flow metadata exceeds 64 KiB")
		}
	}
	// Sorting a private slice also makes the read budget independent of UI order.
	ordered := slices.Clone(flows)
	slices.SortFunc(ordered, func(a, b flow) int { return cmp.Compare(a.id, b.id) })
	type source struct {
		link   tokenLink
		origin string
		at     time.Time
	}
	var sources []source
	byToken := make(map[string][]int)
	result := tokenAnalysis{Links: []tokenLink{}}
	remaining := tokenReadLimit
	for _, f := range ordered {
		if err := ctx.Err(); err != nil {
			return tokenAnalysis{}, err
		}
		origin := tokenOrigin(f)
		ct, count := tokenHeader(f.respHeader, f.resHeaders, "Content-Type")
		media, _, err := mime.ParseMediaType(ct)
		isJSON := media == "application/json" || strings.HasSuffix(media, "+json")
		if f.at.IsZero() || origin == "" || f.status < 200 || f.status >= 300 || count != 1 || err != nil || !isJSON {
			result.Skipped++
			continue
		}
		body, ok := tokenResponseBody(ctx, f, &remaining)
		if !ok {
			result.Skipped++
			continue
		}
		links, ok := tokenResponseFields(ctx, body, f.id)
		if !ok {
			result.Skipped++
			continue
		}
		for _, link := range links {
			byToken[link.token] = append(byToken[link.token], len(sources))
			sources = append(sources, source{link, origin, f.at})
		}
	}
	for _, f := range ordered {
		if err := ctx.Err(); err != nil {
			return tokenAnalysis{}, err
		}
		// Millis is truncated from elapsed time. Require an extra millisecond
		// beyond the estimated start, including when the recorded duration is 0.
		if f.at.IsZero() || f.millis < 0 || int64(f.millis) > int64((1<<63-1)/time.Millisecond)-1 {
			continue
		}
		start := f.at.Add(-time.Duration(f.millis)*time.Millisecond - time.Millisecond)
		value, count := tokenHeader(f.reqHeader, f.reqHeaders, "Authorization")
		scheme, value, found := strings.Cut(value, " ")
		value = strings.TrimLeft(value, " ") // RFC 6750 permits 1*SP, not tabs.
		if count != 1 || !found || !strings.EqualFold(scheme, "Bearer") || !validInferredToken(value) {
			continue
		}
		origin, match := tokenOrigin(f), -1
		for _, i := range byToken[value] {
			if err := ctx.Err(); err != nil {
				return tokenAnalysis{}, err
			}
			s := &sources[i]
			if s.origin != origin || s.at.After(start) || !s.at.Before(f.at) || s.link.Source == f.id {
				continue
			}
			if match != -1 {
				match = -1 // Two eligible fields or sources: do not guess.
				break
			}
			match = i
		}
		if match >= 0 {
			sources[match].link.Consumers = append(sources[match].link.Consumers, f.id)
		}
	}
	for _, s := range sources {
		if len(s.link.Consumers) > 0 {
			result.Links = append(result.Links, s.link)
		}
	}
	slices.SortFunc(result.Links, func(a, b tokenLink) int {
		if n := cmp.Compare(a.Source, b.Source); n != 0 {
			return n
		}
		return slices.Compare(a.Path, b.Path)
	})
	if err := ctx.Err(); err != nil {
		return tokenAnalysis{}, err
	}
	return result, nil
}

func tokenMetadataOK(f flow) bool {
	remaining := tokenMetadataLimit
	for _, value := range []string{f.url, f.scheme, f.host, f.path, f.method, f.reqHeaders, f.resHeaders} {
		if len(value) > remaining {
			return false
		}
		remaining -= len(value)
	}
	for _, h := range []http.Header{f.reqHeader, f.respHeader, f.reqTrailer, f.resTrailer} {
		for name, values := range h {
			// Charge framing too, so empty header entries cannot evade the bound.
			if len(name)+2 > remaining {
				return false
			}
			remaining -= len(name) + 2
			for _, value := range values {
				if len(value)+2 > remaining {
					return false
				}
				remaining -= len(value) + 2
			}
		}
	}
	return true
}

// Count across case spellings as Header.Get/Values only use canonical keys.
func tokenHeader(h http.Header, formatted, name string) (string, int) {
	var value string
	count := 0
	for _, pair := range headerPairs(formatted, h) {
		if strings.EqualFold(pair.Name, name) {
			value, count = pair.Value, count+1
			if len(h) == 0 {
				value = strings.TrimSuffix(value, "\r")
			}
		}
	}
	return value, count
}

func tokenOrigin(f flow) string {
	raw := f.url
	if raw == "" && f.scheme != "" && f.host != "" {
		raw = f.scheme + "://" + f.host
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return ""
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n <= 0 || n > 65535 {
			return ""
		}
		u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(n))
	}
	return diagnosticOrigin(u)
}

// Charge every byte actually read, even from a malformed or changing file.
// Check before opening too: a directory or FIFO must not become a blocking read.
func tokenResponseBody(ctx context.Context, f flow, remaining *int) (string, bool) {
	if f.resError != "" || f.resSize < 0 || f.resSize > tokenResponseLimit || f.resSize > *remaining {
		return "", false
	}
	if f.resFile == "" {
		if len(f.resBody) > tokenResponseLimit || len(f.resBody) > *remaining || f.resSize > 0 && len(f.resBody) != f.resSize {
			return "", false
		}
		*remaining -= len(f.resBody)
		return f.resBody, true
	}
	info, err := os.Stat(f.resFile)
	if err != nil || !info.Mode().IsRegular() || info.Size() > tokenResponseLimit || info.Size() > int64(*remaining) {
		return "", false
	}
	file, err := os.Open(f.resFile)
	if err != nil {
		return "", false
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() != info.Size() {
		return "", false
	}
	limit := min(tokenResponseLimit+1, *remaining)
	data := make([]byte, 0, min(int(info.Size()), limit))
	var buf [32 << 10]byte
	for len(data) < limit {
		if ctx.Err() != nil {
			return "", false
		}
		n, readErr := file.Read(buf[:min(len(buf), limit-len(data))])
		*remaining -= n
		data = append(data, buf[:n]...)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil || n == 0 {
			return "", false
		}
	}
	after, err := file.Stat()
	if err != nil || after.Size() != info.Size() || int64(len(data)) != info.Size() || len(data) > tokenResponseLimit || f.resSize > 0 && len(data) != f.resSize {
		return "", false
	}
	return string(data), true
}

func validInferredToken(value string) bool {
	if len(value) < 16 || len(value) > 8192 {
		return false
	}
	padding := false
	for i := range len(value) {
		c := value[i]
		if c == '=' {
			padding = true
			continue
		}
		if padding || !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~+/", rune(c))) {
			return false
		}
	}
	return value[0] != '=' // RFC 6750 requires a nonempty unpadded prefix.
}

// Stream tokens rather than constructing an unbounded generic JSON tree. The
// decoder also rejects duplicate object names and malformed UTF-8. Any failure
// discards all candidates from this response, including fields already visited.
func tokenResponseFields(ctx context.Context, body string, id int) ([]tokenLink, bool) {
	dec := jsontext.NewDecoder(strings.NewReader(body))
	var links []tokenLink
	nodes := 0
	var walk func([]string, bool) bool
	walk = func(path []string, candidate bool) bool {
		nodes++
		if ctx.Err() != nil || len(path) > tokenJSONDepth || nodes > tokenJSONNodes {
			return false
		}
		t, err := dec.ReadToken()
		if err != nil {
			return false
		}
		switch t.Kind() {
		case '{', '[':
			end := jsontext.Kind('}')
			if t.Kind() == '[' {
				end = ']'
			}
			for i := 0; dec.PeekKind() != end; i++ {
				key, eligible := strconv.Itoa(i), false
				if t.Kind() == '{' {
					name, err := dec.ReadToken()
					if err != nil || name.Kind() != '"' {
						return false
					}
					key = name.String()
					switch key {
					case "token", "access_token", "accessToken", "id_token", "idToken":
						eligible = true
					}
				}
				if !walk(append(path, key), eligible) {
					return false
				}
			}
			if _, err := dec.ReadToken(); err != nil {
				return false
			}
		case '"':
			if candidate && validInferredToken(t.String()) {
				links = append(links, tokenLink{Source: id, Path: slices.Clone(path), token: t.String()})
			}
		}
		return true
	}
	if !walk(nil, false) {
		return nil, false
	}
	_, err := dec.ReadToken()
	return links, errors.Is(err, io.EOF)
}
