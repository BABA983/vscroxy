package app

import (
	"encoding/json/v2"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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

// loadEnvs reads the environments file; a missing file has none.
func loadEnvs(path string) (envFile, error) {
	var f envFile
	if path == "" {
		return f, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	err = json.Unmarshal(data, &f)
	return f, err
}

// saveEnvs keeps the environments, which one is in use and the globals.
func saveEnvs(path string, f envFile) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return writePrivateFile(path, append(data, '\n'))
}
