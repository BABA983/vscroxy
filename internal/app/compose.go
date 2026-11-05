package app

import (
	"encoding/json/v2"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/store"
)

// environment is a named set of variables a composed request uses as {{name}},
// kept with the others so a request can move between a dev and a live host.
// Vars are the Initial values, which exports write. Current overrides them on
// this machine only and is never exported.
type environment struct {
	Name string            `json:"name"`
	Vars map[string]string `json:"vars,omitempty"`
	// Current overrides shared values locally and is never exported.
	Current map[string]string `json:"current,omitempty"`
	// Credentials marks Current names that hold credentials from the wizard,
	// recording or token links, so Persist current leaves them local.
	Credentials map[string]bool `json:"credentials,omitempty"`
}

// values are what a send reads: a nonempty Current wins, an empty one falls
// back to Initial.
func (e environment) values() map[string]string {
	return request.Values(e.Vars, e.Current)
}

// envFile is what the environments file holds: the environments, the one in
// use, the global variables every environment sits above, and the Current
// values of collection and folder variables, which stay out of the library.
type envFile struct {
	Active  string            `json:"active"`
	Envs    []environment     `json:"environments"`
	Globals map[string]string `json:"globals,omitempty"`
	// GlobalsCurrent overrides global Initial values on this machine only.
	GlobalsCurrent map[string]string `json:"globalsCurrent,omitempty"`
	// FolderCurrent holds each collection's and folder's local Current
	// values by path and variable name. Initial values live in the library.
	FolderCurrent map[string]map[string]string `json:"folderCurrent,omitempty"`
	// FolderCredentials marks folder Current names that hold credentials, so
	// Persist current leaves them local.
	FolderCredentials map[string]map[string]bool `json:"folderCredentials,omitempty"`
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

// localEnvs is the whole local variables file: every save writes all scopes,
// so one editor never drops another's Current values.
func (a *app) localEnvs() envFile {
	return envFile{Active: a.activeEnv, Envs: a.envs, Globals: a.globals,
		GlobalsCurrent: a.globalsCurrent, FolderCurrent: a.folderCurrent, FolderCredentials: a.folderCredentials}
}

// saveLocalEnvs writes the environments, globals and local collection Current
// values as they are in memory.
func (a *app) saveLocalEnvs() error {
	return saveEnvs(a.file(a.envsPath), a.localEnvs())
}

// globalValues are what a send reads from globals: a nonempty Current wins,
// an empty one falls back to Initial.
func (a *app) globalValues() map[string]string {
	return request.Values(a.globals, a.globalsCurrent)
}
