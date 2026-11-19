package app

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestFuzzyFilterScreenshots uses the repository's opt-in Tester/png workflow.
// Set VSCROXY_FUZZY_SCREENSHOTS to an absolute output directory to capture two
// fuzzy-filtered lists with match highlights in Light and Dark. Both scenes
// reuse the repository's snapshot fixtures, so the rows and the search fields
// are the ones the app renders.
func TestFuzzyFilterScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_FUZZY_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_FUZZY_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	states := snapshotStates(t)
	scene := func(name, file, want string, setup func(*app)) {
		t.Helper()
		var base *snapshot
		for i := range states {
			if states[i].name == name {
				base = &states[i]
				break
			}
		}
		if base == nil {
			t.Fatalf("snapshot scene %q not found", name)
		}
		for _, dark := range []bool{false, true} {
			appearance := "light"
			if dark {
				appearance = "dark"
			}
			t.Run(file+"-"+appearance, func(t *testing.T) {
				wrapped := *base
				wrapped.setup = func(a *app) {
					if base.setup != nil {
						base.setup(a)
					}
					setup(a)
				}
				tt := snapshotTester(t, wrapped, dark, 1280, 800, "right")
				tt.SetScale(2)
				tt.Frame()
				if !tt.HasText(want) {
					t.Fatalf("missing %q: %v", want, tt.Texts())
				}
				var buf bytes.Buffer
				if err := png.Encode(&buf, tt.Image()); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, file+"-"+appearance+".png")
				if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
	// Tree FindFilter: in-place request tree, with labels highlighted.
	scene("capture-sidebar-long", "fuzzy-collection-tree", "List items 3", func(a *app) {
		a.requestQuery = "lsttm3"
	})
	// Quick-pick-style in-place find: the MQTT timeline keeps arrival order.
	scene("requests-mqtt-stream", "fuzzy-mqtt-timeline", "In · sensors/kitchen/temperature", func(a *app) {
		a.mqttState().query = "ktchn"
	})
}
