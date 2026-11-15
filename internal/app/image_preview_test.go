package app

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"net/http"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// hugePNG is a valid PNG header declaring width×height with no pixel data.
// DecodeConfig reads the header alone, which is enough for the size check.
func hugePNG(width, height uint32) []byte {
	var buf bytes.Buffer
	buf.WriteString("\x89PNG\r\n\x1a\n")
	data := make([]byte, 13)
	binary.BigEndian.PutUint32(data[0:], width)
	binary.BigEndian.PutUint32(data[4:], height)
	data[8], data[9] = 8, 6 // 8-bit RGBA
	binary.Write(&buf, binary.BigEndian, uint32(len(data)))
	buf.WriteString("IHDR")
	buf.Write(data)
	binary.Write(&buf, binary.BigEndian, crc32.ChecksumIEEE(append([]byte("IHDR"), data...)))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	buf.WriteString("IEND")
	binary.Write(&buf, binary.BigEndian, crc32.ChecksumIEEE([]byte("IEND")))
	return buf.Bytes()
}

func TestDecodePreviewImageChecksDeclaredSize(t *testing.T) {
	if _, err := decodePreviewImage(hugePNG(60000, 60000)); err == nil {
		t.Fatal("decoded an image declaring 60000×60000")
	} else {
		var tooLarge imageTooLargeError
		if !errors.As(err, &tooLarge) || tooLarge.width != 60000 || tooLarge.height != 60000 {
			t.Fatalf("got %v, want a size error for 60000×60000", err)
		}
	}
	if _, err := decodePreviewImage(hugePNG(0xFFFFFFFF, 0xFFFFFFFF)); err == nil {
		t.Fatal("accepted a size that overflows the pixel count")
	}
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatal(err)
	}
	tiny, err := decodePreviewImage(png)
	if err != nil || tiny == nil {
		t.Fatalf("valid 1×1 PNG: %v", err)
	}
}

func TestImagePreviewRefusesHugeDeclaredCanvas(t *testing.T) {
	a := workspaceApp()
	a.flows[0].resBody = string(hugePNG(60000, 60000))
	a.flows[0].respHeader = http.Header{"Content-Type": {"image/png"}}
	openResponseBody(a)
	tt := ui.NewTester(a.view, 1280, 800)
	if a.imageBmp[0] != nil || !strings.Contains(a.imageErr[0], "60,000×60,000") {
		t.Fatalf("huge image preview: bitmap=%v error=%q", a.imageBmp[0] != nil, a.imageErr[0])
	}
	if !tt.HasText("60,000×60,000") {
		t.Fatal("the size limit hint is not shown")
	}
	if err := tt.Click("Show as text"); err != nil {
		t.Fatal(err)
	}
	if a.bodyMode[0] != 1 {
		t.Fatalf("Show as text chose mode %d", a.bodyMode[0])
	}
}
