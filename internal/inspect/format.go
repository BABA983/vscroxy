package inspect

import (
	"bytes"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"path/filepath"
	"strings"
)

// BeautifyJS re-indents JavaScript without changing its tokens. It is a
// best-effort formatter: strings, template literals and comments are copied
// as written, braces and semicolons outside parentheses start lines, and a
// regular expression containing braces may be read as code. Invalid source is
// formatted as far as it goes, never rejected.
func BeautifyJS(src string) string {
	return beautifyCode(src, false)
}

// BeautifyCSS re-indents CSS, one declaration or selector per line. Strings
// and comments are copied as written.
func BeautifyCSS(src string) string {
	return beautifyCode(src, true)
}

func beautifyCode(src string, css bool) string {
	var out strings.Builder
	indent := 0
	paren := 0
	lineStart := true
	writeIndent := func() {
		if !lineStart {
			return
		}
		out.WriteString(strings.Repeat("  ", indent))
		lineStart = false
	}
	newline := func() {
		trimmed := strings.TrimRight(out.String(), " \t")
		out.Reset()
		out.WriteString(trimmed)
		out.WriteByte('\n')
		lineStart = true
	}
	space := func() {
		if lineStart {
			return
		}
		s := out.String()
		if s == "" || strings.HasSuffix(s, " ") || strings.HasSuffix(s, "\n") {
			return
		}
		out.WriteByte(' ')
	}
	for i := 0; i < len(src); i++ {
		ch := src[i]
		switch {
		case ch == '\'' || ch == '"' || ch == '`':
			writeIndent()
			quote := ch
			out.WriteByte(ch)
			for i++; i < len(src); i++ {
				out.WriteByte(src[i])
				if src[i] == '\\' && i+1 < len(src) {
					i++
					out.WriteByte(src[i])
					continue
				}
				if src[i] == quote {
					break
				}
			}
		case ch == '/' && i+1 < len(src) && src[i+1] == '/':
			writeIndent()
			for ; i < len(src) && src[i] != '\n'; i++ {
				out.WriteByte(src[i])
			}
			newline()
			if i < len(src) && src[i] == '\n' {
				continue
			}
		case ch == '/' && i+1 < len(src) && src[i+1] == '*':
			writeIndent()
			out.WriteString("/*")
			for i += 2; i < len(src); i++ {
				out.WriteByte(src[i])
				if src[i] == '*' && i+1 < len(src) && src[i+1] == '/' {
					i++
					out.WriteByte('/')
					break
				}
			}
			space()
		case ch == '(':
			writeIndent()
			paren++
			out.WriteByte(ch)
		case ch == ')':
			paren = max(0, paren-1)
			writeIndent()
			out.WriteByte(ch)
		case ch == '{':
			writeIndent()
			space()
			out.WriteByte('{')
			indent++
			newline()
		case ch == '}':
			indent = max(0, indent-1)
			if !lineStart {
				newline()
			}
			writeIndent()
			out.WriteByte('}')
			if i+1 < len(src) && (src[i+1] == ';' || src[i+1] == ',') {
				continue
			}
			newline()
		case ch == ';':
			writeIndent()
			out.WriteByte(';')
			if paren == 0 || css {
				newline()
			} else {
				space()
			}
		case ch == ',':
			writeIndent()
			out.WriteByte(',')
			if css && paren == 0 && indent > 0 {
				newline()
			} else {
				space()
			}
		case ch == '\n' || ch == '\r':
			if css && !lineStart {
				newline()
			} else {
				space()
			}
		case ch == ' ' || ch == '\t':
			space()
		default:
			writeIndent()
			out.WriteByte(ch)
		}
	}
	text := strings.TrimSpace(out.String())
	if text == "" {
		return ""
	}
	return text + "\n"
}

// MergeStream turns a Server-Sent Events stream or a newline-delimited stream
// of OpenAI-style chunks into one readable message. It reports false for a
// body it cannot merge.
func MergeStream(contentType, body string) (string, bool) {
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") || strings.Contains(body, "data:") {
		if text, ok := mergeSSE(body); ok {
			return text, true
		}
	}
	return mergeChunks(body)
}

// streamChunk is the part of an OpenAI-style chunk this merge reads.
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
			Tool    []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		Text    string `json:"text"`
		Message *struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c streamChunk) content() (string, bool) {
	var b strings.Builder
	for _, choice := range c.Choices {
		b.WriteString(choice.Delta.Content)
		b.WriteString(choice.Text)
		if choice.Message != nil {
			b.WriteString(choice.Message.Content)
		}
		for _, tool := range choice.Delta.Tool {
			if tool.Function.Name != "" {
				b.WriteString("\n[tool " + tool.Function.Name + "] ")
			}
			b.WriteString(tool.Function.Arguments)
		}
	}
	return b.String(), b.Len() > 0
}

func (c streamChunk) finish() string {
	for _, choice := range c.Choices {
		if choice.FinishReason != "" {
			return choice.FinishReason
		}
	}
	return ""
}

// mergeSSE reads events and joins the OpenAI content they carry. Without
// OpenAI chunks it returns a readable event transcript instead.
func mergeSSE(body string) (string, bool) {
	var events []struct{ name, data string }
	var data strings.Builder
	name := ""
	flush := func() {
		if data.Len() == 0 && name == "" {
			return
		}
		events = append(events, struct{ name, data string }{name, data.String()})
		data.Reset()
		name = ""
	}
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimSuffix(line, "\r")
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
	}
	flush()
	if len(events) < 2 {
		return "", false
	}
	var merged strings.Builder
	openAI, finish := false, ""
	var transcript strings.Builder
	for _, event := range events {
		var chunk streamChunk
		valid := json.Unmarshal([]byte(event.data), &chunk) == nil
		if valid {
			if text, ok := chunk.content(); ok {
				openAI = true
				merged.WriteString(text)
			}
			if reason := chunk.finish(); reason != "" {
				finish = reason
			}
			if chunk.Error != nil && chunk.Error.Message != "" {
				merged.WriteString("\n[error] " + chunk.Error.Message)
			}
		}
		if event.name != "" {
			transcript.WriteString("event: " + event.name + "\n")
		}
		transcript.WriteString(event.data)
		transcript.WriteString("\n\n")
	}
	if openAI {
		if finish != "" {
			merged.WriteString("\n[finish: " + finish + "]")
		}
		return strings.TrimSpace(merged.String()), true
	}
	return strings.TrimSpace(transcript.String()), true
}

// mergeChunks joins newline-delimited OpenAI-style chunks.
func mergeChunks(body string) (string, bool) {
	var merged strings.Builder
	chunks, finish := 0, ""
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "data: [DONE]" {
			continue
		}
		line = strings.TrimPrefix(line, "data: ")
		var chunk streamChunk
		if json.Unmarshal([]byte(line), &chunk) != nil {
			return "", false
		}
		text, ok := chunk.content()
		if !ok {
			continue
		}
		chunks++
		merged.WriteString(text)
		if reason := chunk.finish(); reason != "" {
			finish = reason
		}
	}
	if chunks < 2 {
		return "", false
	}
	if finish != "" {
		merged.WriteString("\n[finish: " + finish + "]")
	}
	return strings.TrimSpace(merged.String()), true
}

// MultipartPartBytes returns the bytes of one multipart part and a suggested
// file name: the part's filename, or part-N.bin. It re-reads the bounded body
// and never returns a truncated part.
func MultipartPartBytes(contentType string, data []byte, index int) ([]byte, string, error) {
	if len(data) > bodyInputLimit {
		return nil, "", bodyLimit("input")
	}
	media, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.HasPrefix(media, "multipart/") {
		return nil, "", errors.New("not a multipart body")
	}
	boundary := params["boundary"]
	if boundary == "" || len(boundary) > 70 {
		return nil, "", errors.New("multipart requires a boundary of at most 70 bytes")
	}
	reader := multipart.NewReader(bytes.NewReader(data), boundary)
	for i := 0; ; i++ {
		part, err := reader.NextPart()
		if err == io.EOF {
			return nil, "", errors.New("part not found")
		}
		if err != nil {
			return nil, "", err
		}
		if i != index {
			_, _ = io.Copy(io.Discard, part)
			continue
		}
		body, err := io.ReadAll(io.LimitReader(part, bodyInputLimit+1))
		if err != nil {
			return nil, "", err
		}
		if len(body) > bodyInputLimit {
			return nil, "", bodyLimit("part")
		}
		name := filepath.Base(part.FileName())
		if name == "" || name == "." || name == "/" {
			name = fmt.Sprintf("part-%d.bin", index+1)
		}
		return body, name, nil
	}
}
