package request

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const socketIOLimit = 1 << 20
const socketIOMaxAttachments = 32
const socketIOMaxAck = 1<<53 - 1

type socketIOPacket struct {
	kind  byte
	ns    string
	id    *uint64
	data  jsontext.Value
	count int
}

func socketIONamespace(ns string) (string, error) {
	if ns == "" {
		ns = "/"
	}
	if !strings.HasPrefix(ns, "/") || len(ns) > 1024 || strings.ContainsRune(ns, ',') || strings.ContainsFunc(ns, unicode.IsControl) {
		return "", errors.New("Socket.IO namespace must start with / and contain no commas or control characters (1024 bytes maximum)")
	}
	return ns, nil
}

func parseSocketIOPacket(raw []byte) (socketIOPacket, error) {
	p := socketIOPacket{ns: "/"}
	if len(raw) == 0 || len(raw) > socketIOLimit || raw[0] < '0' || raw[0] > '6' {
		return p, errors.New("invalid Socket.IO packet")
	}
	p.kind, raw = raw[0], raw[1:]
	if p.kind == '5' || p.kind == '6' {
		before, after, ok := strings.Cut(string(raw), "-")
		n, err := strconv.Atoi(before)
		if !ok || err != nil || n < 1 || n > socketIOMaxAttachments || strconv.Itoa(n) != before {
			return p, errors.New("invalid Socket.IO attachment count (1–32 required)")
		}
		p.count, raw = n, []byte(after)
	}
	if len(raw) > 0 && raw[0] == '/' {
		before, after, ok := strings.Cut(string(raw), ",")
		if !ok {
			return p, errors.New("invalid Socket.IO namespace")
		}
		var err error
		p.ns, err = socketIONamespace(before)
		if err != nil {
			return p, err
		}
		raw = []byte(after)
	}
	i := 0
	for i < len(raw) && raw[i] >= '0' && raw[i] <= '9' {
		i++
	}
	if i > 0 {
		id, err := strconv.ParseUint(string(raw[:i]), 10, 64)
		if err != nil || id > socketIOMaxAck {
			return p, errors.New("invalid Socket.IO ACK id")
		}
		p.id, raw = new(id), raw[i:]
	}
	p.data = jsontext.Value(raw)
	if len(raw) > 0 && !p.data.IsValid() {
		return p, errors.New("invalid Socket.IO JSON")
	}
	switch p.kind {
	case '0':
		if p.id != nil || len(raw) > 0 && p.data.Kind() != '{' {
			return p, errors.New("invalid Socket.IO CONNECT")
		}
	case '1':
		if p.id != nil || len(raw) != 0 {
			return p, errors.New("invalid Socket.IO DISCONNECT")
		}
	case '2', '5':
		var args []jsontext.Value
		if err := json.Unmarshal(raw, &args); err != nil || len(args) == 0 || args[0].Kind() != '"' && args[0].Kind() != '0' {
			return p, errors.New("Socket.IO event requires a name and argument array")
		}
	case '3', '6':
		if p.id == nil || p.data.Kind() != '[' {
			return p, errors.New("Socket.IO ACK requires an id and argument array")
		}
	case '4':
		if p.id != nil || p.data.Kind() != '{' && p.data.Kind() != '"' {
			return p, errors.New("invalid Socket.IO CONNECT_ERROR")
		}
	}
	return p, nil
}

func (p socketIOPacket) wire() []byte {
	b := []byte{'4', p.kind}
	if p.count > 0 {
		b = strconv.AppendInt(b, int64(p.count), 10)
		b = append(b, '-')
	}
	if p.ns != "/" {
		b = append(b, p.ns...)
		b = append(b, ',')
	}
	if p.id != nil {
		b = strconv.AppendUint(b, *p.id, 10)
	}
	return append(b, p.data...)
}

// Validate placeholders without decoding numbers through float64: JSON event
// arguments retain their original numeric precision. Binary bytes stay separate.
func socketIOPlaceholders(data jsontext.Value, count int) error {
	used := make([]bool, count)
	var visit func(jsontext.Value, int) error
	visit = func(v jsontext.Value, depth int) error {
		if depth > 64 {
			return errors.New("Socket.IO JSON exceeds 64 levels")
		}
		switch v.Kind() {
		case '[':
			var a []jsontext.Value
			if err := json.Unmarshal(v, &a); err != nil {
				return errors.New("invalid Socket.IO argument JSON")
			}
			for _, item := range a {
				if err := visit(item, depth+1); err != nil {
					return err
				}
			}
		case '{':
			var o map[string]jsontext.Value
			if err := json.Unmarshal(v, &o); err != nil {
				return errors.New("invalid Socket.IO argument JSON")
			}
			if string(o["_placeholder"]) == "true" {
				var n int
				if err := json.Unmarshal(o["num"], &n); err != nil || n < 0 || n >= count {
					return errors.New("Socket.IO binary placeholder index is out of range")
				}
				used[n] = true
				return nil
			}
			for _, item := range o {
				if err := visit(item, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(data, 0); err != nil {
		return err
	}
	for i, ok := range used {
		if !ok {
			return fmt.Errorf("Socket.IO attachment %d has no placeholder", i)
		}
	}
	return nil
}
