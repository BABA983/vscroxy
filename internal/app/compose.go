package app

import (
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
)

// environment is a named set of variables a composed request uses as {{name}},
// kept with the others so a request can move between a dev and a live host.
type environment struct {
	Name string            `json:"name"`
	Vars map[string]string `json:"vars,omitempty"`
	// Current overrides shared values locally and is never exported.
	Current map[string]string `json:"current,omitempty"`
}

func (e environment) values() map[string]string {
	values := maps.Clone(e.Vars)
	if values == nil {
		values = map[string]string{}
	}
	maps.Copy(values, e.Current)
	return values
}

// envFile is what the environments file holds: the environments, the one in
// use, and the global variables every environment sits above.
type envFile struct {
	Active  string            `json:"active"`
	Envs    []environment     `json:"environments"`
	Globals map[string]string `json:"globals,omitempty"`
}

// loadEnvs reads the environments file; a missing file has none. One that
// does not parse stays read-only.
func loadEnvs(file *store.File) (envFile, error) {
	var f envFile
	if file.Path == "" {
		return f, nil
	}
	data, err := file.Read()
	if err != nil || data == nil {
		return f, err
	}
	if err = json.Unmarshal(data, &f); err != nil {
		file.Fail(err)
	}
	return f, err
}

// saveEnvs keeps the environments, which one is in use and the globals.
func saveEnvs(file *store.File, f envFile) error {
	if file.Path == "" {
		return nil
	}
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return file.Write(append(data, '\n'))
}

func (a *app) currentVariablesEditor(c *ui.Context, t *ui.Theme) {
	uikit.Body(c, "Current values · local only")
	uikit.Hint(c, "Override shared values for sends. Not included in environment exports; stored locally in plaintext. Use Vault for encryption.")
	remove := -1
	ui.Column(c).Gap(t.Space(1)).Disabled(a.envsProblem != "").Children(func() {
		for i := range a.varsCurrent {
			r := &a.varsCurrent[i]
			ui.Row(c.Key(i)).Gap(t.Space(1)).Children(func() {
				uikit.Input(c, &r.Name).Label(fmt.Sprintf("Current variable name %d", i+1)).Basis(0).Grow(1)
				value := uikit.Input(c, &r.Value).Label(fmt.Sprintf("Current variable value %d", i+1)).Basis(0).Grow(2)
				if !r.Revealed {
					value.Password()
				}
				uikit.IconButton(c, fmt.Sprintf("Reveal current variable %d", i+1), "Reveal or hide current value", eyeIcon).OnClick(func() { r.Revealed = !r.Revealed })
				if uikit.IconButton(c, fmt.Sprintf("Remove current variable %d", i+1), "Remove current value", closeIcon).Clicked() {
					remove = i
				}
			})
		}
		uikit.SecondaryButton(c, "Add current value").OnClick(func() { a.varsCurrent = append(a.varsCurrent, request.Field{}) })
	})
	if remove >= 0 {
		a.varsCurrent = slices.Delete(a.varsCurrent, remove, remove+1)
	}
}
