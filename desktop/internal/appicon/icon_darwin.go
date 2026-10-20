//go:build darwin

package appicon

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
)

// lookup reads the icon of the app bundle the executable belongs to: the
// .icns its Info.plist names, whose PNG representations macOS 10.7 and later
// store inside it.
func lookup(name, path string) (Icon, bool) {
	bundle := appBundle(path)
	if bundle == "" {
		return Icon{}, false
	}
	icon := plistIcon(filepath.Join(bundle, "Contents", "Info.plist"))
	if icon == "" {
		icon = "AppIcon"
	}
	for _, candidate := range []string{icon, icon + ".icns", "AppIcon"} {
		file := filepath.Join(bundle, "Contents", "Resources", candidate)
		if !strings.HasSuffix(file, ".icns") {
			file += ".icns"
		}
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		if png, ok := icnsPNG(data); ok {
			return Icon{Data: png}, true
		}
	}
	return Icon{}, false
}

// appBundle walks up from the executable to the .app directory that holds it.
func appBundle(path string) string {
	path = filepath.Clean(path)
	for dir := path; dir != "/" && dir != "."; dir = filepath.Dir(dir) {
		if strings.HasSuffix(dir, ".app") {
			return dir
		}
	}
	return ""
}

// plistIcon reads CFBundleIconFile from an Info.plist.
func plistIcon(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	const key = "<key>CFBundleIconFile</key>"
	at := bytes.Index(data, []byte(key))
	if at < 0 {
		return ""
	}
	rest := data[at+len(key):]
	start := bytes.Index(rest, []byte("<string>"))
	if start < 0 {
		return ""
	}
	rest = rest[start+len("<string>"):]
	end := bytes.Index(rest, []byte("</string>"))
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(string(rest[:end]))
}

// icnsPNG returns the largest PNG an .icns holds, the form its modern
// representations take.
func icnsPNG(data []byte) ([]byte, bool) {
	if len(data) < 8 || string(data[:4]) != "icns" {
		return nil, false
	}
	var best []byte
	for off := 8; off+8 <= len(data); {
		size := int(binary.BigEndian.Uint32(data[off+4 : off+8]))
		if size < 8 || off+size > len(data) {
			break
		}
		body := data[off+8 : off+size]
		if len(body) > 8 && bytes.HasPrefix(body, []byte("\x89PNG\r\n\x1a\n")) && len(body) > len(best) {
			best = body
		}
		off += size
	}
	return best, best != nil
}
