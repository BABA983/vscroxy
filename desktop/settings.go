package main

import (
	"encoding/json/v2"
	"errors"
	"io/fs"
	"maps"
	"os"
	"slices"

	"github.com/egoist/mygo/ui"
)

// settings is what the app keeps between launches besides the window's
// bounds, which MyGo saves itself for a window with a StateKey.
type settings struct {
	Dark         bool         `json:"dark,omitempty"`
	SidebarWidth float32      `json:"sidebarWidth,omitempty"`
	TablePercent float32      `json:"tablePercent,omitempty"`
	Columns      columnLayout `json:"columns,omitempty"`
	// HeaderColumn is the request or response header the table shows as a
	// column of its own, empty for none.
	HeaderColumn string `json:"headerColumn,omitempty"`
}

// columnLayout is a table's columns as the user arranged them, saved with
// names of our own so the file does not follow MyGo's Go names.
type columnLayout struct {
	Order  []string           `json:"order,omitempty"`
	Widths map[string]float32 `json:"widths,omitempty"`
}

// loadSettings reads the saved settings, the defaults when there is no file.
func loadSettings(path string) (settings, error) {
	if path == "" {
		return settings{}, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return settings{}, nil
	}
	if err != nil {
		return settings{}, err
	}
	var s settings
	if err := json.Unmarshal(data, &s); err != nil {
		return settings{}, err
	}
	return s, nil
}

// writeSettings keeps the settings for the next launch.
func writeSettings(path string, s settings) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// layout turns the saved columns into the table's.
func (c columnLayout) layout() ui.TableLayout {
	return ui.TableLayout{Order: slices.Clone(c.Order), Widths: maps.Clone(c.Widths)}
}

// savedLayout turns the table's columns into the file's.
func savedLayout(l ui.TableLayout) columnLayout {
	return columnLayout{Order: slices.Clone(l.Order), Widths: maps.Clone(l.Widths)}
}

// sameSettings reports whether two settings would write the same file.
func sameSettings(x, y settings) bool {
	return x.Dark == y.Dark &&
		x.SidebarWidth == y.SidebarWidth &&
		x.TablePercent == y.TablePercent &&
		slices.Equal(x.Columns.Order, y.Columns.Order) &&
		maps.Equal(x.Columns.Widths, y.Columns.Widths)
}
