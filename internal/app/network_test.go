package app

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

func networkTestApp(t *testing.T) *app {
	t.Helper()
	a := &app{throttle: capture.Presets[0], srv: &capture.Server{}}
	a.network.path = filepath.Join(t.TempDir(), "network.json")
	a.loadNetworkPresets()
	return a
}

func networkFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestNetworkPresetsSaveUpdateDeleteAndRestart(t *testing.T) {
	a := networkTestApp(t)
	p := capture.Throttle{Name: " Mobile ", DownPerSec: 1234, UpPerSec: 5678, Latency: 321 * time.Millisecond, FailurePercent: 37}
	if !a.saveNetworkPreset("", p) {
		t.Fatal(a.message)
	}
	p.Name = "Mobile"
	if a.throttle != p || a.srv.Throttle() != p {
		t.Fatal("saved conditions were not applied to app and server")
	}
	if mode, err := os.Stat(a.network.path); err != nil || mode.Mode().Perm() != 0o600 {
		t.Fatalf("preset file must be private: %v, %v", mode, err)
	}
	all := a.networkPresets()
	if !slices.Equal(all[:len(capture.Presets)], capture.Presets) || all[len(all)-1] != p {
		t.Fatalf("builtins and custom presets: %+v", all)
	}
	all[0].Name, all[len(all)-1].Name = "changed", "changed"
	if a.networkPresets()[0].Name != "Off" || a.network.presets[0] != p {
		t.Fatal("returned menu presets alias saved state")
	}
	// A fresh session loads only profiles, not the previous active choice.
	b := &app{throttle: capture.Presets[0]}
	b.network.path = a.network.path
	b.loadNetworkPresets()
	if !slices.Equal(b.network.presets, []capture.Throttle{p}) || b.throttle != capture.Presets[0] {
		t.Fatalf("restart restored active conditions or lost profiles: %+v", b.network)
	}
	p.Name, p.FailurePercent = "Renamed mobile", 100
	if !a.saveNetworkPreset("Mobile", p) || len(a.network.presets) != 1 || a.srv.Throttle() != p {
		t.Fatal("update/rename failed:", a.message)
	}
	// Reloading profiles during a session must not turn running conditions off.
	a.loadNetworkPresets()
	if a.throttle != p || a.network.presets[0] != p {
		t.Fatal("reload changed running conditions")
	}
	if !a.deleteNetworkPreset(p.Name) || len(a.network.presets) != 0 || a.throttle != capture.Presets[0] || a.srv.Throttle() != capture.Presets[0] {
		t.Fatal("deleting active profile did not persist then reset Off:", a.message)
	}
	b.loadNetworkPresets()
	if len(b.network.presets) != 0 {
		t.Fatal("deleted preset returned on reload")
	}
}

func TestNetworkInvalidPresetsNeverChangeDiskOrActiveState(t *testing.T) {
	a := networkTestApp(t)
	good := capture.Throttle{Name: "Saved", FailurePercent: 12}
	if !a.saveNetworkPreset("", good) {
		t.Fatal(a.message)
	}
	before := networkFileBytes(t, a.network.path)
	for _, p := range []capture.Throttle{
		{Name: ""}, {Name: "   "}, {Name: "off"}, {Name: "3g"}, {Name: "DSL"},
		{Name: "saved"}, {Name: "bad\nname"}, {Name: "\ttrimmed"}, {Name: string([]byte{0xff})},
		{Name: strings.Repeat("é", 33)}, {Name: "bad", DownPerSec: -1}, {Name: "bad", UpPerSec: 1<<30 + 1},
		{Name: "bad", Latency: -time.Millisecond}, {Name: "bad", Latency: 61 * time.Second},
		{Name: "bad", FailurePercent: -1}, {Name: "bad", FailurePercent: 101},
	} {
		t.Run(fmt.Sprintf("%q-%d-%d-%d-%d", p.Name, p.DownPerSec, p.UpPerSec, p.Latency, p.FailurePercent), func(t *testing.T) {
			if a.saveNetworkPreset("", p) || a.throttle != good || a.srv.Throttle() != good || !slices.Equal(a.network.presets, []capture.Throttle{good}) {
				t.Fatal("invalid save changed state")
			}
			if !bytes.Equal(networkFileBytes(t, a.network.path), before) {
				t.Fatal("invalid save touched the file")
			}
		})
	}
	if a.saveNetworkPreset("missing", good) || a.deleteNetworkPreset("missing") {
		t.Fatal("update/delete of a missing profile succeeded")
	}
	if a.setThrottle(capture.Throttle{FailurePercent: 101}) || a.throttle != good || a.srv.Throttle() != good {
		t.Fatal("invalid session conditions changed the app or runtime")
	}
	// All inclusive numeric and name-byte bounds are accepted.
	limit := capture.Throttle{Name: strings.Repeat("é", 32), DownPerSec: 1 << 30, UpPerSec: 1 << 30, Latency: time.Minute, FailurePercent: 100}
	if !a.saveNetworkPreset("", limit) {
		t.Fatal(a.message)
	}
}

func TestNetworkPresetLimitAndInactiveDeletion(t *testing.T) {
	a := networkTestApp(t)
	for i := range maxNetworkPresets {
		if !a.saveNetworkPreset("", capture.Throttle{Name: fmt.Sprintf("Preset %d", i)}) {
			t.Fatal(a.message)
		}
	}
	before := networkFileBytes(t, a.network.path)
	if a.saveNetworkPreset("", capture.Throttle{Name: "One too many"}) || !bytes.Equal(before, networkFileBytes(t, a.network.path)) {
		t.Fatal("33rd profile was saved")
	}
	if !a.saveNetworkPreset("Preset 0", capture.Throttle{Name: "Renamed"}) {
		t.Fatal("update at the limit failed:", a.message)
	}
	a.setThrottle(capture.Presets[1])
	if !a.deleteNetworkPreset("Renamed") || a.throttle != capture.Presets[1] {
		t.Fatal("deleting an inactive preset changed active conditions")
	}
}

func TestNetworkConflictsLeaveFilePresetsAndRuntimeAlone(t *testing.T) {
	a := networkTestApp(t)
	p := capture.Throttle{Name: "Original", FailurePercent: 1}
	if !a.saveNetworkPreset("", p) {
		t.Fatal(a.message)
	}
	b := &app{throttle: capture.Presets[0]}
	b.network.path = a.network.path
	b.loadNetworkPresets()
	if !b.saveNetworkPreset("", capture.Throttle{Name: "External"}) {
		t.Fatal(b.message)
	}
	external := networkFileBytes(t, a.network.path)
	for _, action := range []func() bool{
		func() bool { return a.saveNetworkPreset("Original", capture.Throttle{Name: "Changed"}) },
		func() bool { return a.deleteNetworkPreset("Original") },
	} {
		if action() || a.throttle != p || a.srv.Throttle() != p || !slices.Equal(a.network.presets, []capture.Throttle{p}) {
			t.Fatal("conflicted operation changed saved or running state")
		}
		if !bytes.Equal(external, networkFileBytes(t, a.network.path)) {
			t.Fatal("external changes were overwritten")
		}
	}
	if a.notices[len(a.notices)-1].action != "Reload" {
		t.Fatal("conflict did not offer the conventional Reload action")
	}
	a.loadNetworkPresets()
	if !a.saveNetworkPreset("", capture.Throttle{Name: "After reload"}) || len(a.network.presets) != 3 {
		t.Fatal("reload did not establish a new baseline:", a.message)
	}
}

func TestNetworkMalformedAndReadOnlyFilesAreNeverReplaced(t *testing.T) {
	for _, data := range []string{
		"", "{", "null", `[]`, `{"version":1,"presets":[]}`, `{"presets":null}`,
		`{"presets":[],"active":"3G"}`,
		`{"presets":[{"name":"Bad","latencyMS":9223372036854775807}]}`,
		`{"presets":[{"name":"Bad","failurePercent":101}]}`,
		`{"presets":[{"name":"Off"}]}`,
		`{"presets":[{"name":"Same"},{"name":"same"}]}`,
	} {
		t.Run(data, func(t *testing.T) {
			a := networkTestApp(t)
			if err := os.WriteFile(a.network.path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			a.loadNetworkPresets()
			if a.file(a.network.path).Problem() == nil || a.saveNetworkPreset("", capture.Throttle{Name: "Do not overwrite"}) {
				t.Fatal("malformed profile file was not guarded")
			}
			if string(networkFileBytes(t, a.network.path)) != data || len(a.network.presets) != 0 || a.throttle != capture.Presets[0] {
				t.Fatal("malformed file or session state changed")
			}
		})
	}
	a := networkTestApp(t)
	if !a.saveNetworkPreset("", capture.Throttle{Name: "Private"}) {
		t.Fatal(a.message)
	}
	before := networkFileBytes(t, a.network.path)
	if err := os.Chmod(a.network.path, 0o400); err != nil {
		t.Fatal(err)
	}
	if a.deleteNetworkPreset("Private") || !bytes.Equal(before, networkFileBytes(t, a.network.path)) || a.throttle.Name != "Private" {
		t.Fatal("read-only file was replaced")
	}
	a.network.path = ""
	if a.saveNetworkPreset("", capture.Throttle{Name: "Missing path"}) || a.throttle.Name != "Private" {
		t.Fatal("missing location saved or applied conditions")
	}
	a.network.path = t.TempDir()
	a.loadNetworkPresets()
	if a.saveNetworkPreset("", capture.Throttle{Name: "Unreadable"}) {
		t.Fatal("unreadable file was replaced")
	}
}

func TestNetworkSaveWithoutStartupLoadPreservesExistingProfiles(t *testing.T) {
	a := networkTestApp(t)
	if !a.saveNetworkPreset("", capture.Throttle{Name: "Existing"}) {
		t.Fatal(a.message)
	}
	b := &app{}
	b.network.path = a.network.path
	before := networkFileBytes(t, a.network.path)
	if b.saveNetworkPreset("", capture.Throttle{Name: "Invalid", FailurePercent: 101}) || len(b.network.presets) != 0 || b.throttle != (capture.Throttle{}) || !bytes.Equal(before, networkFileBytes(t, a.network.path)) {
		t.Fatal("a failed save before startup load published presets or changed disk/runtime")
	}
	if !b.saveNetworkPreset("", capture.Throttle{Name: "New"}) || len(b.network.presets) != 2 {
		t.Fatal("save before startup load lost existing profiles:", b.message)
	}
	// An app that never loaded must still guard an invalid existing file.
	if err := os.WriteFile(a.network.path, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &app{}
	c.network.path = a.network.path
	if c.saveNetworkPreset("", capture.Throttle{Name: "New"}) || string(networkFileBytes(t, a.network.path)) != "broken" {
		t.Fatal("save before load overwrote a damaged file")
	}
}

func TestNetworkDraftValidation(t *testing.T) {
	want := capture.Throttle{DownPerSec: 123, UpPerSec: 456, Latency: 789 * time.Millisecond, FailurePercent: 12}
	if got, err := newNetworkDraft(want).value(); err != nil || got != want {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
	for _, d := range []networkDraft{
		{down: "1.5", up: "0", latency: "0", failure: "0"},
		{down: "0", up: "-1", latency: "0", failure: "0"},
		{down: "0", up: "0", latency: "9223372036854775807", failure: "0"},
		{down: "0", up: "0", latency: "60001", failure: "0"},
		{down: "0", up: "0", latency: "0", failure: "0.5"},
		{down: "0", up: "0", latency: "0", failure: "101"},
		{},
	} {
		if _, err := d.value(); err == nil {
			t.Fatalf("accepted invalid draft: %+v", d)
		}
	}
}

func TestNetworkModalEditsAndConfirmsDeletionAt960(t *testing.T) {
	a := networkTestApp(t)
	a.openNetwork()
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(uikit.Theme(false))
		a.networkModal(c, c.Theme())
	}, 960, 600)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	edit := func(label, value string) {
		t.Helper()
		click(label)
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Type(value)
		tt.Frame()
	}
	edit("Preset name", "Mobile test")
	edit("Download (bytes/s)", "12000")
	edit("Upload (bytes/s)", "3400")
	edit("Latency (ms)", "250")
	edit("Failure (%)", "25")
	for range 3 {
		tt.Frame()
	}
	click("Save & apply")
	want := capture.Throttle{Name: "Mobile test", DownPerSec: 12000, UpPerSec: 3400, Latency: 250 * time.Millisecond, FailurePercent: 25}
	if a.throttle != want || a.srv.Throttle() != want {
		t.Fatalf("edited fields were not retained/applied: %+v; %s", a.throttle, a.message)
	}
	// Check persistence through the app's load contract, not implementation order.
	b := &app{}
	b.network.path = a.network.path
	b.loadNetworkPresets()
	if !slices.Equal(b.network.presets, []capture.Throttle{want}) {
		t.Fatal("UI save did not persist all edited fields")
	}
	edit("Failure (%)", "101")
	click("Save & apply")
	if a.throttle != want || !strings.Contains(a.network.problem, "Failure") {
		t.Fatal("invalid UI value was applied without feedback")
	}
	click("Copy active conditions")
	click("Save & apply")
	if a.throttle != want {
		t.Fatal("copy active did not replace numeric conditions")
	}
	click("New preset")
	edit("Preset name", "Second")
	click("Copy active conditions")
	click("Save & apply")
	if len(a.network.presets) != 2 || a.throttle.Name != "Second" {
		t.Fatal("New preset replaced the existing preset")
	}
	click("Custom preset")
	click("Mobile test")
	click("Save & apply")
	if a.throttle != want {
		t.Fatal("selecting a custom preset did not load its fields")
	}
	click("Delete")
	if len(a.network.presets) != 2 || !tt.HasText("Confirm delete") {
		t.Fatal("Delete did not ask for explicit confirmation")
	}
	click("Cancel delete")
	click("Delete")
	click("Confirm delete")
	if len(a.network.presets) != 1 || a.throttle != capture.Presets[0] {
		t.Fatal("confirmed deletion did not reset conditions")
	}
	click("Close")
	for range 3 {
		tt.Frame()
	}
	a.openNetwork()
	tt.Frame()
	edit("Preset name", "Reopened")
	click("Save & apply")
	if a.throttle.Name != "Reopened" {
		t.Fatal("reopened dialog lost persistent bindings")
	}
	// The JSON has only custom presets, no format version or active-session field.
	var file map[string]any
	if err := json.Unmarshal(networkFileBytes(t, a.network.path), &file); err != nil || len(file) != 1 || file["presets"] == nil {
		t.Fatalf("unexpected preset format: %+v, %v", file, err)
	}
}

func TestNetworkPresetsIntegrateWithToolsAndActiveChip(t *testing.T) {
	a := workspaceApp()
	a.network.path = filepath.Join(t.TempDir(), "network.json")
	a.loadNetworkPresets()
	if !a.saveNetworkPreset("", capture.Throttle{Name: "Mobile", Latency: time.Second}) {
		t.Fatal(a.message)
	}
	a.setThrottle(capture.Presets[0])
	tt := ui.NewTester(a.view, 960, 600)
	if err := tt.Click("Tools"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Custom presets…"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Custom network presets") {
		t.Fatal("Tools did not open the custom preset editor")
	}
	if err := tt.Click("Close"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Tools"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(tt, "Mobile"); err != nil {
		t.Fatal(err)
	}
	if a.throttle.Name != "Mobile" || !tt.HasText("Active tool: Mobile") {
		t.Fatal("saved preset was not applied from the shared menu")
	}
	if err := tt.Click("Disable Mobile"); err != nil {
		t.Fatal(err)
	}
	if a.throttle != capture.Presets[0] || tt.HasText("Active tool: Mobile") {
		t.Fatal("active chip did not turn conditions Off")
	}
}

// TestNetworkSnapshots is opt-in, like the app's other native snapshots.
// It renders this modal in isolation without touching user settings or traffic.
func TestNetworkSnapshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_NETWORK_SNAPSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_NETWORK_SNAPSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		a := networkTestApp(t)
		if !a.saveNetworkPreset("", capture.Throttle{Name: "Unreliable mobile", DownPerSec: 12000, UpPerSec: 3400, Latency: 250 * time.Millisecond, FailurePercent: 25}) {
			t.Fatal(a.message)
		}
		a.openNetwork()
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(uikit.Theme(dark))
			ui.Column(c).Fill().Background(c.Theme().Background)
			a.networkModal(c, c.Theme())
		}, 960, 600)
		tt.SetScale(2)
		for _, state := range []string{"preset", "invalid", "delete"} {
			switch state {
			case "invalid":
				if err := tt.Click("Failure (%)"); err != nil {
					t.Fatal(err)
				}
				tt.Key(ui.Cmd, ui.KeyA)
				tt.Type("101")
				if err := tt.Click("Save & apply"); err != nil {
					t.Fatal(err)
				}
			case "delete":
				if err := tt.Click("Copy active conditions"); err != nil {
					t.Fatal(err)
				}
				if err := tt.Click("Save & apply"); err != nil {
					t.Fatal(err)
				}
				if err := tt.Click("Delete"); err != nil {
					t.Fatal(err)
				}
			}
			name := fmt.Sprintf("network-%s-dark-%t-960x600.png", state, dark)
			f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, tt.Image())
			closeErr := f.Close()
			if err != nil || closeErr != nil {
				t.Fatalf("save snapshot: %v, %v", err, closeErr)
			}
		}
	}
}
