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

// envFile is what the environments file holds.
type envFile struct {
	Active string        `json:"active"`
	Envs   []environment `json:"environments"`
}

// loadEnvs reads the environments and the one in use; a missing file has none.
func loadEnvs(path string) ([]environment, string, error) {
	if path == "" {
		return nil, "", nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var f envFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, "", err
	}
	return f.Envs, f.Active, nil
}

// saveEnvs keeps the environments and which one is in use.
func saveEnvs(path string, envs []environment, active string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(envFile{Active: active, Envs: envs})
	if err != nil {
		return err
	}
	return writePrivateFile(path, append(data, '\n'))
}
