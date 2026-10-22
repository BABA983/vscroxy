package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
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

type protoMessage struct {
	text       string
	size       int
	compressed bool
}

type protoResult struct {
	messages []protoMessage
	typeName string
	problem  string
}

func grpcContentType(ct string) bool { return strings.HasPrefix(ct, "application/grpc") }

// inspectProto reads the complete retained body, not the formatted Body preview.
// A gRPC envelope is parsed only for a gRPC content type, never guessed from a
// leading zero byte. Every frame is accounted for, including empty messages.
func inspectProto(ctx context.Context, f flow, response bool, schema *protoSchema, name string) protoResult {
	var result protoResult
	head, file, size, problem, header := f.reqBody, f.reqFile, f.reqSize, f.reqError, f.reqHeader
	if response {
		head, file, size, problem, header = f.resBody, f.resFile, f.resSize, f.resError, f.respHeader
	}
	if problem != "" {
		result.problem = "Incomplete body: " + problem
		return result
	}
	if file != "" {
		data, err := readProtoFile(file)
		if err != nil {
			result.problem = err.Error()
			return result
		}
		head = string(data)
	}
	if size > len(head) {
		result.problem = fmt.Sprintf("Incomplete body: %d of %d bytes retained", len(head), size)
		return result
	}
	if len(head) > maxProtoBytes {
		result.problem = fmt.Sprintf("Protobuf inspection is limited to %s per body", humanBytes(maxProtoBytes))
		return result
	}
	desc, err := schema.message(name, f.path, response)
	if err != nil {
		result.problem = err.Error()
		return result
	}
	if desc != nil {
		result.typeName = string(desc.FullName())
	}
	ct := f.contentType(response)
	if strings.HasPrefix(ct, "application/grpc-web") {
		result.problem = "gRPC-Web framing is not supported; use native gRPC over inspected HTTP/2"
		return result
	}
	data := []byte(head)
	remaining := maxProtoBytes
	for index := 1; ; index++ {
		if err := ctx.Err(); err != nil {
			result.problem = err.Error()
			return result
		}
		if index > 1000 {
			result.problem = "Inspection is limited to 1000 messages per body"
			return result
		}
		payload, compressed := data, false
		if grpcContentType(ct) {
			if len(data) == 0 {
				return result
			}
			if len(data) < 5 {
				result.problem = fmt.Sprintf("Message %d: incomplete gRPC frame header (%d of 5 bytes)", index, len(data))
				return result
			}
			length := uint64(binary.BigEndian.Uint32(data[1:5]))
			if data[0] > 1 || length > uint64(len(data)-5) {
				result.problem = fmt.Sprintf("Message %d: invalid flag or incomplete gRPC payload (declared %d, available %d)", index, length, len(data)-5)
				return result
			}
			compressed = data[0] == 1
			payload, data = data[5:5+length], data[5+length:]
		}
		limit := min(16<<20, remaining)
		if compressed {
			if !strings.EqualFold(header.Get("Grpc-Encoding"), "gzip") {
				result.problem = fmt.Sprintf("Message %d: unsupported grpc-encoding %q (gzip supported)", index, header.Get("Grpc-Encoding"))
				return result
			}
			reader, err := gzip.NewReader(bytes.NewReader(payload))
			if err == nil {
				payload, err = io.ReadAll(io.LimitReader(reader, int64(limit)+1))
				reader.Close()
			}
			if err != nil {
				result.problem = fmt.Sprintf("Message %d: invalid gzip payload: %v", index, err)
				return result
			}
		}
		if len(payload) > limit {
			result.problem = fmt.Sprintf("Message %d exceeds the 16 MiB message / 32 MiB decoded body limit", index)
			return result
		}
		remaining -= len(payload)
		text, err := protoMessageText(payload, schema, desc)
		if err != nil {
			result.problem = fmt.Sprintf("Message %d: %v", index, err)
			return result
		}
		result.messages = append(result.messages, protoMessage{text: text, size: len(payload), compressed: compressed})
		if !grpcContentType(ct) || len(data) == 0 {
			return result
		}
	}
}

func protoMessageText(data []byte, schema *protoSchema, desc protoreflect.MessageDescriptor) (string, error) {
	if len(data) == 0 {
		return "(empty message)", nil
	}
	if desc != nil {
		message := dynamicpb.NewMessage(desc)
		if err := (proto.UnmarshalOptions{Resolver: schema.types, RecursionLimit: 64}).Unmarshal(data, message); err != nil {
			return "", err
		}
		text, err := (prototext.MarshalOptions{Multiline: true, Indent: "  ", EmitUnknown: true, Resolver: schema.types}).Marshal(message)
		return string(text), err
	}
	fields, err := decodeProto(data)
	if err != nil {
		return "", err
	}
	return protoText(fields), nil
}

// grpcOutcome prefers final trailers, with initial-header fallback for
// trailers-only responses.
func grpcOutcome(f flow) (code, message string) {
	code, message = f.resTrailer.Get("Grpc-Status"), f.resTrailer.Get("Grpc-Message")
	if code == "" {
		code, message = f.respHeader.Get("Grpc-Status"), f.respHeader.Get("Grpc-Message")
	}
	return code, message
}

func (f flow) failed() bool {
	if f.status >= 400 {
		return true
	}
	code, _ := grpcOutcome(f)
	return grpcContentType(f.contentType(true)) && code != "" && code != "0"
}

// grpc-message is percent-encoded, not a query string: '+' remains a plus.
func grpcStatus(f flow) string {
	code, message := grpcOutcome(f)
	if code == "" {
		return "gRPC status missing (HTTP 200 alone does not mean success)"
	}
	names := [...]string{"OK", "Cancelled", "Unknown", "InvalidArgument", "DeadlineExceeded", "NotFound", "AlreadyExists", "PermissionDenied", "ResourceExhausted", "FailedPrecondition", "Aborted", "OutOfRange", "Unimplemented", "Internal", "Unavailable", "DataLoss", "Unauthenticated"}
	name := "Invalid status"
	if n, err := strconv.Atoi(code); err == nil && n >= 0 && n < len(names) {
		name = names[n]
	}
	if decoded, err := url.PathUnescape(message); err == nil {
		message = decoded
	}
	text := "gRPC " + code + " · " + name
	if message != "" {
		text += " · " + message
	}
	return text
}
