//go:build darwin

package appicon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestLookupFindsTheOwningAppsIcon covers the case that showed a letter badge
// for Chrome: the process that owns a connection is a helper bundle inside the
// app, and that helper has no icon of its own.
func TestLookupFindsTheOwningAppsIcon(t *testing.T) {
	app := filepath.Join(t.TempDir(), "Google Chrome.app")
	helper := filepath.Join(app, "Contents", "Frameworks", "Google Chrome Framework.framework",
		"Versions", "155.0", "Helpers", "Google Chrome Helper (Aperitif).app")
	exe := filepath.Join(helper, "Contents", "MacOS", "Google Chrome Helper (Aperitif)")
	write(t, filepath.Join(helper, "Contents", "Info.plist"), `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.google.Chrome.helper</string></dict></plist>`)
	write(t, filepath.Join(app, "Contents", "Info.plist"), `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>CFBundleIconFile</key><string>app.icns</string></dict></plist>`)
	want := pngBytes(t, color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff})
	writeBytes(t, filepath.Join(app, "Contents", "Resources", "app.icns"), icnsFile(want))

	got, ok := Lookup("Google Chrome Helper (Aperitif)", exe)
	if !ok {
		t.Fatalf("Lookup(%q) found no icon, want the enclosing app's", exe)
	}
	if !bytes.Equal(got.Data, want) {
		t.Fatalf("Lookup(%q) returned %d bytes, want the %d-byte PNG", exe, len(got.Data), len(want))
	}
	img, err := png.Decode(bytes.NewReader(got.Data))
	if err != nil {
		t.Fatalf("decode the icon: %v", err)
	}
	if r, g, b, _ := img.At(1, 1).RGBA(); r>>8 != 0x12 || g>>8 != 0x34 || b>>8 != 0x56 {
		t.Errorf("icon pixel = %04x%04x%04x, want 123456", r>>8, g>>8, b>>8)
	}

	if _, ok := Lookup("unknown", filepath.Join(t.TempDir(), "bin", "unknown")); ok {
		t.Error("Lookup of an executable outside any app bundle found an icon, want none")
	}
}

// TestLookupPrefersTheHelperIcon keeps a helper's own icon ahead of the one of
// the app that encloses it.
func TestLookupPrefersTheHelperIcon(t *testing.T) {
	app := filepath.Join(t.TempDir(), "Demo.app")
	helper := filepath.Join(app, "Contents", "Frameworks", "Demo Helper.app")
	exe := filepath.Join(helper, "Contents", "MacOS", "Demo Helper")
	write(t, filepath.Join(helper, "Contents", "Info.plist"), "<plist><dict></dict></plist>")
	write(t, filepath.Join(app, "Contents", "Info.plist"), "<plist><dict></dict></plist>")
	outer := pngBytes(t, color.RGBA{R: 0x01, A: 0xff})
	inner := pngBytes(t, color.RGBA{G: 0x02, A: 0xff})
	writeBytes(t, filepath.Join(app, "Contents", "Resources", "AppIcon.icns"), icnsFile(outer))
	writeBytes(t, filepath.Join(helper, "Contents", "Resources", "AppIcon.icns"), icnsFile(inner))

	got, ok := Lookup("Demo Helper", exe)
	if !ok || !bytes.Equal(got.Data, inner) {
		t.Errorf("Lookup(%q) = %d bytes, %v; want the helper's own icon", exe, len(got.Data), ok)
	}
}

// pngBytes encodes a small valid PNG of one color.
func pngBytes(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := range 2 {
		for x := range 2 {
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return buf.Bytes()
}

// icnsFile wraps a PNG in an .icns container, the form macOS stores its modern
// representations in.
func icnsFile(data []byte) []byte {
	var body bytes.Buffer
	body.WriteString("ic09")
	body.Write(binary.BigEndian.AppendUint32(nil, uint32(8+len(data))))
	body.Write(data)

	var out bytes.Buffer
	out.WriteString("icns")
	out.Write(binary.BigEndian.AppendUint32(nil, uint32(8+body.Len())))
	out.Write(body.Bytes())
	return out.Bytes()
}

func write(t *testing.T, path, content string) {
	t.Helper()
	writeBytes(t, path, []byte(content))
}

func writeBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("make dir: %v", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
