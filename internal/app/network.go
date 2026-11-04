package app

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

const maxNetworkPresets = 32

// networkWorkbench owns only custom profiles. The running conditions belong
// to a.throttle and are deliberately not restored from this file on launch.
type networkWorkbench struct {
	path    string
	presets []capture.Throttle

	loadedPath string
	open       bool
	selection  string
	editing    string // saved name, independent of the name being edited
	name       string
	draft      networkDraft
	deleting   bool
	problem    string
}

// networkDraft binds inputs to persistent strings, shared with the rule
// editor. A profile's name is edited separately from its numeric conditions.
type networkDraft struct {
	down, up, latency, failure string
}

func newNetworkDraft(t capture.Throttle) networkDraft {
	return networkDraft{
		down: strconv.Itoa(t.DownPerSec), up: strconv.Itoa(t.UpPerSec),
		latency: strconv.FormatInt(int64(t.Latency/time.Millisecond), 10),
		failure: strconv.Itoa(t.FailurePercent),
	}
}

func (d networkDraft) value() (capture.Throttle, error) {
	var t capture.Throttle
	for _, field := range []struct {
		label, text string
		max         int
		value       *int
	}{
		{"Download", d.down, 1 << 30, &t.DownPerSec},
		{"Upload", d.up, 1 << 30, &t.UpPerSec},
		{"Failure", d.failure, 100, &t.FailurePercent},
	} {
		n, err := strconv.Atoi(strings.TrimSpace(field.text))
		if err != nil || n < 0 || n > field.max {
			return t, fmt.Errorf("%s must be a whole number from 0 to %d", field.label, field.max)
		}
		*field.value = n
	}
	ms, err := strconv.ParseInt(strings.TrimSpace(d.latency), 10, 64)
	if err != nil || ms < 0 || ms > 60_000 {
		return t, errors.New("Latency must be a whole number from 0 to 60000 ms")
	}
	t.Latency = time.Duration(ms) * time.Millisecond
	return t, t.Validate()
}

func (d *networkDraft) fields(c *ui.Context) {
	t := c.Theme()
	for _, row := range [][]struct {
		label string
		value *string
	}{
		{{"Download (bytes/s)", &d.down}, {"Upload (bytes/s)", &d.up}},
		{{"Latency (ms)", &d.latency}, {"Failure (%)", &d.failure}},
	} {
		ui.Row(c).Gap(t.Space(3)).Children(func() {
			for _, field := range row {
				ui.Field(c.Key(field.label), field.label, func() {
					uikit.Input(c, field.value).Label(field.label)
				}).Grow(1).MinWidth(0)
			}
		})
	}
}

// Milliseconds keep the on-disk units identical to the editor's units.
type networkPresetFile struct {
	Presets []networkPresetRecord `json:"presets"`
}

type networkPresetRecord struct {
	Name           string `json:"name"`
	DownPerSec     int    `json:"downloadBytesPerSecond"`
	UpPerSec       int    `json:"uploadBytesPerSecond"`
	LatencyMS      int64  `json:"latencyMS"`
	FailurePercent int    `json:"failurePercent"`
}

func validateNetworkPresets(presets []capture.Throttle) error {
	if len(presets) > maxNetworkPresets {
		return fmt.Errorf("Keep at most %d custom network presets", maxNetworkPresets)
	}
	for i, p := range presets {
		if !utf8.ValidString(p.Name) || len(p.Name) == 0 || len(p.Name) > 64 ||
			p.Name != strings.TrimSpace(p.Name) || strings.ContainsFunc(p.Name, unicode.IsControl) {
			return errors.New("Preset names must be trimmed, valid UTF-8, control-free and 1–64 bytes long")
		}
		for _, builtin := range capture.Presets {
			if strings.EqualFold(p.Name, builtin.Name) {
				return fmt.Errorf("%q is a built-in preset name", p.Name)
			}
		}
		for _, other := range presets[:i] {
			if strings.EqualFold(p.Name, other.Name) {
				return fmt.Errorf("A network preset named %q already exists", p.Name)
			}
		}
		if err := p.Validate(); err != nil {
			return fmt.Errorf("Preset %q: %w", p.Name, err)
		}
		if p.Latency%time.Millisecond != 0 {
			return errors.New("Preset latency must be a whole number of milliseconds")
		}
	}
	return nil
}

func (a *app) networkProblem(what string, err error) bool {
	a.network.problem = what + ": " + err.Error()
	a.fail(what, err)
	return false
}

// loadNetworkPresets never applies conditions: reloading a file also leaves
// the session's running conditions alone. An unreadable file stays guarded.
func (a *app) loadNetworkPresets() {
	presets, err := a.readNetworkPresets()
	a.network.loadedPath = a.network.path
	if err != nil {
		a.networkProblem("Could not load network presets; file left untouched", err)
		return
	}
	a.network.presets, a.network.problem = presets, ""
}

func (a *app) readNetworkPresets() ([]capture.Throttle, error) {
	n := &a.network
	if n.path == "" {
		return nil, errors.New("Network presets location is unavailable")
	}
	f := a.file(n.path)
	data, err := f.Read()
	if err != nil {
		return nil, err
	}
	var presets []capture.Throttle
	if data != nil {
		var file networkPresetFile
		err = json.Unmarshal(data, &file, json.RejectUnknownMembers(true))
		if err == nil && file.Presets == nil {
			err = errors.New("Expected a network presets file with a presets array")
		}
		if err == nil {
			for _, p := range file.Presets {
				// Bound milliseconds before converting to Duration to avoid overflow.
				if p.LatencyMS < 0 || p.LatencyMS > 60_000 {
					err = errors.New("Preset latency must be from 0 to 60000 ms")
					break
				}
				presets = append(presets, capture.Throttle{
					Name: p.Name, DownPerSec: p.DownPerSec, UpPerSec: p.UpPerSec,
					Latency: time.Duration(p.LatencyMS) * time.Millisecond, FailurePercent: p.FailurePercent,
				})
			}
		}
		if err == nil {
			err = validateNetworkPresets(presets)
		}
		if err != nil {
			f.Fail(err)
			return nil, err
		}
	}
	return presets, nil
}

// networkPresetBaseline also supports an editor opened before startup loaded
// its file, without publishing a new in-memory list until its save succeeds.
func (a *app) networkPresetBaseline() ([]capture.Throttle, error) {
	if a.network.path != "" && a.network.loadedPath != a.network.path {
		return a.readNetworkPresets()
	}
	return slices.Clone(a.network.presets), nil
}

// writeNetworkPresets validates the complete replacement before the guarded
// write. Callers publish it and apply conditions only after this succeeds.
func (a *app) writeNetworkPresets(presets []capture.Throttle) bool {
	if err := validateNetworkPresets(presets); err != nil {
		return a.networkProblem("Could not save network presets", err)
	}
	n := &a.network
	if n.path == "" {
		return a.networkProblem("Could not save network presets", errors.New("Network presets location is unavailable"))
	}
	f := a.file(n.path)
	if err := f.Problem(); err != nil {
		return a.networkProblem("Network presets are read-only until reloaded cleanly", err)
	}
	// Atomic replacement must not bypass an explicitly read-only file's mode.
	info, err := os.Stat(n.path)
	if err == nil && info.Mode().Perm()&0o222 == 0 {
		return a.networkProblem("Could not save network presets", fs.ErrPermission)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return a.networkProblem("Could not save network presets", err)
	}
	file := networkPresetFile{Presets: make([]networkPresetRecord, 0, len(presets))}
	for _, p := range presets {
		file.Presets = append(file.Presets, networkPresetRecord{
			Name: p.Name, DownPerSec: p.DownPerSec, UpPerSec: p.UpPerSec,
			LatencyMS: int64(p.Latency / time.Millisecond), FailurePercent: p.FailurePercent,
		})
	}
	data, err := json.Marshal(file)
	if err == nil {
		err = f.Write(append(data, '\n'))
	}
	if err != nil {
		return a.networkProblem("Could not save network presets", err)
	}
	n.problem, n.loadedPath = "", n.path
	return true
}

func (a *app) networkPresets() []capture.Throttle {
	return append(slices.Clone(capture.Presets), a.network.presets...)
}

// setThrottle changes session state only, and never presents an invalid or
// rejected server setting as applied in the app.
func (a *app) setThrottle(t capture.Throttle) bool {
	if err := t.Validate(); err != nil {
		return a.networkProblem("Could not apply network conditions", err)
	}
	if a.srv != nil {
		if err := a.srv.SetThrottle(t); err != nil {
			return a.networkProblem("Could not apply network conditions", err)
		}
	}
	a.throttle = t
	a.network.problem = ""
	a.inform("Network conditions: " + t.Name)
	return true
}

func (a *app) saveNetworkPreset(original string, p capture.Throttle) bool {
	// Reject controls before trimming so newlines cannot silently become names.
	if !utf8.ValidString(p.Name) || strings.ContainsFunc(p.Name, unicode.IsControl) {
		return a.networkProblem("Could not save network preset", errors.New("Preset names must be valid UTF-8 and control-free"))
	}
	p.Name = strings.TrimSpace(p.Name)
	list, err := a.networkPresetBaseline()
	if err != nil {
		return a.networkProblem("Could not save network preset; file left untouched", err)
	}
	if original == "" {
		list = append(list, p)
	} else {
		index := slices.IndexFunc(list, func(t capture.Throttle) bool { return t.Name == original })
		if index < 0 {
			return a.networkProblem("Could not save network preset", errors.New("The preset no longer exists; select it again"))
		}
		list[index] = p
	}
	if !a.writeNetworkPresets(list) {
		return false
	}
	a.network.presets = list
	a.network.selectPreset(p.Name)
	return a.setThrottle(p)
}

func (a *app) deleteNetworkPreset(name string) bool {
	list, err := a.networkPresetBaseline()
	if err != nil {
		return a.networkProblem("Could not delete network preset; file left untouched", err)
	}
	index := slices.IndexFunc(list, func(t capture.Throttle) bool { return t.Name == name })
	if index < 0 {
		return a.networkProblem("Could not delete network preset", errors.New("The preset no longer exists; select it again"))
	}
	list = slices.Delete(list, index, index+1)
	if !a.writeNetworkPresets(list) {
		return false
	}
	a.network.presets = list
	a.network.selectPreset("")
	if a.throttle.Name == name {
		return a.setThrottle(capture.Presets[0])
	}
	a.done("Deleted network preset " + name)
	return true
}

func (n *networkWorkbench) selectPreset(name string) {
	n.selection, n.editing, n.name = "", "", ""
	n.draft = newNetworkDraft(capture.Throttle{})
	n.deleting, n.problem = false, ""
	for _, p := range n.presets {
		if p.Name == name {
			n.selection, n.editing, n.name = name, name, name
			n.draft = newNetworkDraft(p)
			return
		}
	}
}

func (a *app) openNetwork() {
	n := &a.network
	n.selectPreset(a.throttle.Name)
	n.open = true
}

func (a *app) networkModal(c *ui.Context, t *ui.Theme) {
	n := &a.network
	if !n.open {
		return
	}
	uikit.Modal(c.Key("network-presets"), &n.open, func() {
		ui.Column(c).Width(560).Gap(t.Space(2)).Children(func() {
			uikit.DialogTitle(c, "Custom network presets")
			uikit.Meta(c, "HTTP, inspected HTTPS and SSE only; not WebSocket or raw CONNECT. Conditions apply to new exchanges.")
			uikit.Meta(c, "Rates limit each body by direction (0 = unlimited). Latency waits once before forwarding. Failure randomly disconnects requests, not TCP packets.")
			ui.Row(c).Gap(t.Space(2)).Children(func() {
				uikit.SecondaryButton(c, "New preset").OnClick(func() { n.selectPreset("") })
				names := make([]string, 0, len(n.presets))
				for _, p := range n.presets {
					names = append(names, p.Name)
				}
				uikit.Select(c, &n.selection, names).Label("Custom preset").Placeholder("Choose a custom preset").Grow(1).
					Disabled(len(names) == 0).OnChange(func() { n.selectPreset(n.selection) })
			})
			ui.Field(c, "Preset name", func() {
				uikit.Input(c, &n.name).Label("Preset name").Placeholder("e.g. Unreliable mobile")
			})
			n.draft.fields(c)
			uikit.Meta(c, "Whole numbers: rates 0–1073741824 bytes/s, latency 0–60000 ms, failure 0–100%. Only custom presets are saved; active conditions restart Off.")
			if n.problem != "" {
				ui.Text(c, n.problem).FontSize(uikit.TextMeta).TextColor(t.Danger)
			}
			if n.deleting {
				uikit.Meta(c, "Delete “"+n.editing+"”? This cannot be undone. If active, global conditions will turn Off.")
				ui.Row(c).Gap(t.Space(2)).Justify(ui.End).Children(func() {
					uikit.SecondaryButton(c, "Cancel delete").OnClick(func() { n.deleting = false })
					uikit.SecondaryButton(c, "Confirm delete").OnClick(func() { a.deleteNetworkPreset(n.editing) })
				})
			} else {
				ui.Row(c).Gap(t.Space(2)).Children(func() {
					uikit.SecondaryButton(c, "Copy active conditions").OnClick(func() { n.draft = newNetworkDraft(a.throttle) })
					uikit.SecondaryButton(c, "Delete").Disabled(n.editing == "").OnClick(func() { n.deleting = true })
					ui.Spacer(c)
					uikit.SecondaryButton(c, "Close").OnClick(func() { n.open = false })
					uikit.PrimaryButton(c, "Save & apply").OnClick(func() {
						p, err := n.draft.value()
						if err != nil {
							a.networkProblem("Could not save network preset", err)
							return
						}
						p.Name = n.name
						a.saveNetworkPreset(n.editing, p)
					})
				})
			}
		})
	})
}
