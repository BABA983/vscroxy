package main

import (
	"encoding/binary"
	"strings"
	"testing"
)

// pbVarint writes a varint field.
func pbVarint(number int, value uint64) []byte {
	out := binary.AppendUvarint(nil, uint64(number)<<3)
	return binary.AppendUvarint(out, value)
}

// pbBytes writes a length-delimited field.
func pbBytes(number int, data []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(number)<<3|2)
	out = binary.AppendUvarint(out, uint64(len(data)))
	return append(out, data...)
}

// pbMessage is what a gRPC frame carries: a flag and the length, then the
// message.
func pbMessage(data []byte) []byte {
	out := []byte{0}
	out = binary.BigEndian.AppendUint32(out, uint32(len(data)))
	return append(out, data...)
}

func TestDecodeProtoWithoutASchema(t *testing.T) {
	// {1: 7, 2: "vscroxy", 3: {1: "id", 2: 42}, 4: "x"}
	nested := append(pbBytes(1, []byte("id")), pbVarint(2, 42)...)
	body := pbVarint(1, 7)
	body = append(body, pbBytes(2, []byte("vscroxy"))...)
	body = append(body, pbBytes(3, nested)...)
	body = append(body, pbBytes(4, []byte("x"))...)

	fields, ok := protoBody(body)
	if !ok {
		t.Fatal("the message did not read as protobuf")
	}
	text := protoText(fields)
	for _, want := range []string{
		"1: 7",
		`2: "vscroxy"`,
		"3 {",
		`  1: "id"`,
		"  2: 42",
		`4: "x"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("decoded text misses %q:\n%s", want, text)
		}
	}
	// A string stays a string; a message stays a message.
	if fields[1].Nested != nil || fields[1].Text != "vscroxy" {
		t.Errorf("field 2 = %+v, want the string", fields[1])
	}
	if len(fields[2].Nested) != 2 {
		t.Errorf("field 3 = %+v, want the nested message", fields[2])
	}
}

func TestProtoBodyReadsAGrpcFrame(t *testing.T) {
	message := append(pbVarint(1, 1), pbBytes(2, []byte("owner"))...)
	fields, ok := protoBody(pbMessage(message))
	if !ok {
		t.Fatal("a gRPC frame did not read as protobuf")
	}
	if len(fields) != 2 || fields[1].Text != "owner" {
		t.Fatalf("fields = %+v", fields)
	}
	// A frame whose length runs past the body is not a frame: its header must
	// not be read as a message either.
	if _, ok := protoBody(append([]byte{0, 0, 0, 0, 200}, message...)); ok {
		t.Error("a frame with a wrong length was accepted")
	}
}

func TestProtoBodyRejectsWhatItCannotRead(t *testing.T) {
	for _, body := range [][]byte{
		{},
		{0x08},                   // a key with no value
		[]byte("GET / HTTP/1.1"), // text
		{0x0a, 0x05, 0x01, 0x02}, // a length past the body
	} {
		if _, ok := protoBody(body); ok {
			t.Errorf("%q read as protobuf, want it rejected", body)
		}
	}
}
