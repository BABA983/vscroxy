package app

import (
	"context"
	"encoding/binary"
	"net/http"
	"path/filepath"
	"testing"
)

func TestSplitDelimitedMessages(t *testing.T) {
	first, second := []byte{0x08, 0x01}, []byte{0x12, 0x02, 'h', 'i'}
	var body []byte
	for _, msg := range [][]byte{first, second} {
		body = binary.AppendUvarint(body, uint64(len(msg)))
		body = append(body, msg...)
	}
	list, ok := splitDelimited(body)
	if !ok || len(list) != 2 || string(list[0]) != string(first) || string(list[1]) != string(second) {
		t.Fatalf("split: %q %v", list, ok)
	}
	for _, bad := range [][]byte{nil, first, body[:len(body)-1], append(body, 0xff)} {
		if _, ok := splitDelimited(bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestMessageTypeParameter(t *testing.T) {
	for contentType, want := range map[string]string{
		"application/x-protobuf; messageType=shop.Item": "shop.Item",
		"application/protobuf; messageType= shop.Item ": "shop.Item",
		"application/x-protobuf":                        "",
		"not a type":                                    "",
	} {
		if got := messageTypeParam(contentType); got != want {
			t.Fatalf("%q = %q, want %q", contentType, got, want)
		}
	}
}

func TestInspectProtoReadsALengthDelimitedList(t *testing.T) {
	var body []byte
	for _, msg := range [][]byte{{0x08, 0x01}, {0x08, 0x02}} {
		body = binary.AppendUvarint(body, uint64(len(msg)))
		body = append(body, msg...)
	}
	f := flow{resBody: string(body), resSize: len(body), respHeader: http.Header{"Content-Type": {"application/x-protobuf"}}}
	result := inspectProto(context.Background(), f, true, nil, "")
	if result.problem != "" || len(result.messages) != 2 {
		t.Fatalf("result: %+v", result)
	}
	if result.messages[0].text == result.messages[1].text {
		t.Fatalf("messages read the same: %+v", result.messages)
	}
}

func TestProtoTypeRulesSaveMatchAndForget(t *testing.T) {
	a := workspaceApp()
	a.protoTypesPath = filepath.Join(t.TempDir(), "protobuf-types.json")
	if !a.saveProtoTypeRule("https://api.example.com/v1/items", "shop.Item") ||
		!a.saveProtoTypeRule("https://api.example.com/v1", "shop.Envelope") {
		t.Fatal(a.protoTypesProblem)
	}
	if got := a.protoTypeRuleFor("https://api.example.com/v1/items?page=2"); got != "shop.Item" {
		t.Fatalf("longest match = %q", got)
	}
	if got := a.protoTypeRuleFor("https://api.example.com/v2"); got != "" {
		t.Fatalf("unrelated URL matched %q", got)
	}
	if !a.forgetProtoTypeRule("https://api.example.com/v1/items") {
		t.Fatal(a.protoTypesProblem)
	}
	if got := a.protoTypeRuleFor("https://api.example.com/v1/items"); got != "shop.Envelope" {
		t.Fatalf("after forget = %q", got)
	}
	a.protoTypes = nil
	a.loadProtoTypeRules()
	if len(a.protoTypes) != 1 || a.protoTypes[0].Type != "shop.Envelope" {
		t.Fatalf("reloaded %+v", a.protoTypes)
	}
	if err := validateProtoTypeRules([]protoTypeRule{{Match: "", Type: "shop.Item"}}); err == nil {
		t.Fatal("an empty match was accepted")
	}
	if err := validateProtoTypeRules([]protoTypeRule{{Match: "/x", Type: "not a type"}}); err == nil {
		t.Fatal("an invalid type was accepted")
	}
}
