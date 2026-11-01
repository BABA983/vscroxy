package app

import (
	"encoding/json/v2"
	"github.com/baba983/vscroxy/internal/store"
)

// environment is a named set of variables a composed request uses as {{name}},
// kept with the others so a request can move between a dev and a live host.
type environment struct {
	Name string            `json:"name"`
	Vars map[string]string `json:"vars,omitempty"`
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
