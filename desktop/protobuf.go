package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// pbField is one field of a protobuf message as the wire carries it: the
// number and the type are there, the name and the meaning are in a .proto this
// app does not have.
type pbField struct {
	Number int
	Wire   int
	Uint   uint64
	Bytes  []byte
	Nested []pbField
	Text   string
}

// errBadProto is what a body that is not a protobuf message reads as.
var errBadProto = errors.New("not a protobuf message")

// maxProtoDepth bounds how deep a nested message is followed: a proxy decodes
// what it is given, and a wrong guess must not recurse forever.
const maxProtoDepth = 8

// decodeProto reads a protobuf message without its schema.
func decodeProto(data []byte) ([]pbField, error) {
	return decodeProtoDepth(data, 0)
}

func decodeProtoDepth(data []byte, depth int) ([]pbField, error) {
	if depth > maxProtoDepth {
		return nil, errBadProto
	}
	var fields []pbField
	for len(data) > 0 {
		key, n := binary.Uvarint(data)
		if n <= 0 {
			return nil, errBadProto
		}
		data = data[n:]
		number, wire := int(key>>3), int(key&7)
		if number <= 0 {
			return nil, errBadProto
		}
		field := pbField{Number: number, Wire: wire}
		switch wire {
		case 0: // varint
			value, n := binary.Uvarint(data)
			if n <= 0 {
				return nil, errBadProto
			}
			data = data[n:]
			field.Uint = value
		case 1: // 64 bits
			if len(data) < 8 {
				return nil, errBadProto
			}
			field.Uint = binary.LittleEndian.Uint64(data)
			data = data[8:]
		case 5: // 32 bits
			if len(data) < 4 {
				return nil, errBadProto
			}
			field.Uint = uint64(binary.LittleEndian.Uint32(data))
			data = data[4:]
		case 2: // length-delimited
			length, n := binary.Uvarint(data)
			if n <= 0 || length > uint64(len(data)-n) {
				return nil, errBadProto
			}
			data = data[n:]
			field.Bytes = data[:length]
			data = data[length:]
			// A string is the common case and reads best; a message is what
			// the rest is, and is followed when it parses.
			if text, ok := protoString(field.Bytes); ok {
				field.Text = text
			} else if nested, err := decodeProtoDepth(field.Bytes, depth+1); err == nil && len(nested) > 0 {
				field.Nested = nested
			}
		default:
			return nil, errBadProto
		}
		fields = append(fields, field)
	}
	if len(fields) == 0 {
		return nil, errBadProto
	}
	return fields, nil
}

// protoString is the bytes as text when they read as text, which is how a
// protobuf string field shows without its schema.
func protoString(data []byte) (string, bool) {
	if len(data) == 0 || !utf8.Valid(data) {
		return "", false
	}
	for _, r := range string(data) {
		if r == '\n' || r == '\t' || r == '\r' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	return string(data), true
}

// protoText writes a decoded message as a tree, the way a schema-less reader
// can: field numbers, the values, and a nested message in braces.
func protoText(fields []pbField) string {
	var b strings.Builder
	writeProto(&b, fields, 0)
	return strings.TrimSuffix(b.String(), "\n")
}

func writeProto(b *strings.Builder, fields []pbField, depth int) {
	indent := strings.Repeat("  ", depth)
	for _, field := range fields {
		switch {
		case field.Nested != nil:
			fmt.Fprintf(b, "%s%d {\n", indent, field.Number)
			writeProto(b, field.Nested, depth+1)
			fmt.Fprintf(b, "%s}\n", indent)
		case field.Text != "":
			fmt.Fprintf(b, "%s%d: %q\n", indent, field.Number, field.Text)
		case field.Bytes != nil:
			fmt.Fprintf(b, "%s%d: %s (%d bytes)\n", indent, field.Number, hexBytes(field.Bytes), len(field.Bytes))
		case field.Wire == 0:
			fmt.Fprintf(b, "%s%d: %d\n", indent, field.Number, field.Uint)
		case field.Wire == 1:
			fmt.Fprintf(b, "%s%d: %d (%.6g as a double)\n", indent, field.Number, field.Uint, math.Float64frombits(field.Uint))
		case field.Wire == 5:
			fmt.Fprintf(b, "%s%d: %d (%.6g as a float)\n", indent, field.Number, field.Uint, float64(math.Float32frombits(uint32(field.Uint))))
		}
	}
}

// hexBytes writes bytes as hex pairs, the short ones in full.
func hexBytes(data []byte) string {
	const shown = 16
	var b strings.Builder
	for i, by := range data {
		if i == shown {
			fmt.Fprintf(&b, " …")
			break
		}
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%02x", by)
	}
	return b.String()
}

// grpcPayload is the message a gRPC frame carries, without its five-byte
// prefix: a compressed flag and the length that follows.
func grpcPayload(data []byte) ([]byte, bool) {
	if len(data) < 5 || data[0] != 0 {
		return nil, false
	}
	length := int(binary.BigEndian.Uint32(data[1:5]))
	if length <= 0 || length > len(data)-5 {
		return nil, false
	}
	return data[5 : 5+length], true
}

// protoBody is the message a body holds, and whether it reads as one: a gRPC
// frame when the body is one, the body itself otherwise. A frame whose length
// runs past the body is not read as a message at all: its header is not one.
func protoBody(body []byte) ([]pbField, bool) {
	if len(body) >= 5 && body[0] == 0 {
		payload, ok := grpcPayload(body)
		if !ok {
			return nil, false
		}
		body = payload
	}
	fields, err := decodeProto(body)
	if err != nil {
		return nil, false
	}
	return fields, true
}
