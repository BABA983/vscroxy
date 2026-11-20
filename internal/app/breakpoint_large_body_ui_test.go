package app

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

func largeBreakpointApp(size int64, readOnly bool) *app {
	a := snapshotApp()
	snapshotBreakpoint(a)
	a.bp.Phase = capture.PhaseResponse
	a.bpStatus = "201"
	if readOnly {
		a.bp.BodyReadOnly, a.bp.BodySize, a.bp.Body = true, size, ""
	} else {
		a.bp.BodySize = size
		a.bp.Body = strings.Repeat(`{"name":"Demo project","tags":["api","test"]}`+"\n", int(size/44))
	}
	a.bpBody = a.bp.Body
	a.bpBodySeen, a.bpReplaceFile, a.bpReplaceSize = a.bpBody, "", 0
	return a
}

func TestBreakpointFileActionsAndTextEditPrecedence(t *testing.T) {
	a := largeBreakpointApp(6<<20, true)
	tt := ui.NewTester(a.view, 1400, 900)
	tt.SetLocale("en-US")
	tt.Frame()
	if !tt.HasText(locales.ReplaceBodyFromFile()) || !tt.HasText(locales.SaveBodyToFile()) {
		t.Fatalf("file actions missing: %v", tt.Texts())
	}
	if !tt.HasText("6.0 MiB body") {
		t.Fatalf("over-limit reason missing: %v", tt.Texts())
	}

	a.bpReplaceFile, a.bpReplaceSize, a.bpBodySeen = "/Users/demo/body.json", 42, a.bpBody
	tt.Frame()
	if !tt.HasText("Replacement: body.json (42 bytes)") {
		t.Fatalf("chosen file missing: %v", tt.Texts())
	}
	a.bpBody = "edited after choosing the file"
	tt.Frame()
	if tt.HasText("Replacement: body.json (42 bytes)") || a.bpReplaceFile != "" {
		t.Fatalf("a text edit must win over the chosen file: %v", tt.Texts())
	}

	binary := largeBreakpointApp(900, true)
	binary.bp.BodyReadOnly = true
	bt := ui.NewTester(binary.view, 1400, 900)
	bt.SetLocale("en-US")
	bt.Frame()
	if !bt.HasText(locales.BinaryBodyKept()) || !bt.HasText(locales.ReplaceBodyFromFile()) {
		t.Fatalf("binary file actions missing: %v", bt.Texts())
	}
}

// TestBreakpointLargeBodyScreenshots exports 2x Tester captures of the dialog
// with an editable body and with over-limit file actions, in both themes. Set
// VSCROXY_BREAKPOINT_BODY_SCREENSHOTS to an output directory.
func TestBreakpointLargeBodyScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_BREAKPOINT_BODY_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_BREAKPOINT_BODY_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	scenes := []struct {
		name     string
		size     int64
		readOnly bool
	}{
		{"editable", 3 << 20, false},
		{"over-limit", 6 << 20, true},
	}
	for _, scene := range scenes {
		for _, dark := range []bool{false, true} {
			appearance := "light"
			if dark {
				appearance = "dark"
			}
			t.Run(scene.name+"-"+appearance, func(t *testing.T) {
				a := largeBreakpointApp(scene.size, scene.readOnly)
				if scene.readOnly {
					a.bpReplaceFile, a.bpReplaceSize = "/Users/demo/orders.json", 62
				}
				a.bpBodySeen = a.bpBody
				tt := ui.NewTester(func(c *ui.Context) {
					c.SetTheme(uikit.Theme(dark))
					a.view(c)
				}, 1400, 900)
				tt.SetLocale("en-US")
				tt.SetScale(2)
				tt.Frame()
				title, ok := tt.Find(locales.BreakpointResponsePaused())
				if !ok {
					t.Fatalf("dialog title %q not drawn", locales.BreakpointResponsePaused())
				}
				footer, ok := tt.Find(locales.ApplyContinue())
				if !ok {
					t.Fatalf("dialog footer button %q not drawn", locales.ApplyContinue())
				}
				const scale = 2
				crop := image.Rect(
					int(min(title.X, footer.X-380))*scale, int(title.Y-32)*scale,
					int(footer.X+footer.W+32)*scale, int(footer.Y+footer.H+28)*scale,
				).Intersect(tt.Image().Bounds())
				out := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
				draw.Draw(out, out.Bounds(), tt.Image(), crop.Min, draw.Src)
				var b bytes.Buffer
				if err := png.Encode(&b, out); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, "breakpoint-"+scene.name+"-"+appearance+".png")
				if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
