package cli

import (
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/library"
	"github.com/baba983/vscroxy/internal/postman"
	"github.com/baba983/vscroxy/internal/request"
)

func loadCollection(name, folder string) ([]request.Request, []request.Folder, []string, error) {
	info, err := os.Stat(name)
	if err != nil {
		return nil, nil, nil, err
	}
	var requests []request.Request
	var folders []request.Folder
	var warnings []string
	if info.IsDir() {
		requests, folders, _, err = library.Read(name, func(data []byte, collection string) (request.Request, error) {
			var r request.Request
			err := json.Unmarshal(data, &r)
			r.Collection = collection
			return r, err
		})
	} else {
		var data []byte
		data, err = os.ReadFile(name)
		if err == nil {
			var collection postman.Collection
			collection, err = postman.ReadCollection(data)
			requests, folders, warnings = collection.Requests, collection.Folders, collection.Warnings
		}
	}
	if err != nil {
		return nil, nil, nil, err
	}
	// Identical native inputs keep the same request IDs as the desktop reader.
	seen := map[string]bool{}
	var selected []request.Request
	for _, r := range requests {
		if r.ID == "" {
			r.ID = fmt.Sprintf("legacy-%x", sha256.Sum256([]byte(r.Collection+"\x00"+r.Name)))
		}
		if seen[r.ID] {
			return nil, nil, nil, fmt.Errorf("duplicate request identity")
		}
		seen[r.ID] = true
		if folder == "" || r.Collection == folder || strings.HasPrefix(r.Collection, folder+"/") {
			selected = append(selected, r)
		}
	}
	if len(selected) == 0 {
		return nil, nil, nil, fmt.Errorf("no requests selected; check the collection and folder")
	}
	// Match the desktop Runner: collection paths first, stable saved order
	// within each folder (including Postman imports with folders first).
	slices.SortStableFunc(selected, func(a, b request.Request) int { return strings.Compare(a.Collection, b.Collection) })
	return selected, folders, warnings, nil
}

// readEnvironment accepts either an explicitly selected native env.json or a
// Postman environment/globals file. No implicit reads from the desktop profile.
func readEnvironment(path, selected string) (name string, vars, globals map[string]string, err error) {
	vars, globals = map[string]string{}, map[string]string{}
	if path == "" {
		if selected != "" {
			err = fmt.Errorf("--environment requires --env")
		}
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, nil, err
	}
	if kind := postman.Detect(data); kind == postman.KindEnvironment || kind == postman.KindGlobals {
		env, readErr := postman.ReadEnvironment(data)
		if readErr != nil {
			return "", nil, nil, readErr
		}
		if selected != "" && selected != env.Name {
			return "", nil, nil, fmt.Errorf("environment not found")
		}
		if env.Globals {
			globals = env.Values
		} else {
			name, vars = env.Name, env.Values
		}
		return
	}
	var file struct {
		Active string `json:"active"`
		Envs   []struct {
			Name    string            `json:"name"`
			Vars    map[string]string `json:"vars"`
			Current map[string]string `json:"current"`
		} `json:"environments"`
		Globals map[string]string `json:"globals"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return "", nil, nil, err
	}
	if file.Envs == nil && file.Globals == nil {
		return "", nil, nil, fmt.Errorf("not a native or Postman environment file")
	}
	if file.Globals != nil {
		globals = file.Globals
	}
	if selected == "" {
		selected = file.Active
	}
	for _, env := range file.Envs {
		if env.Name == selected {
			maps.Copy(vars, env.Vars)
			maps.Copy(vars, env.Current)
			return env.Name, vars, globals, nil
		}
	}
	if selected != "" {
		return "", nil, nil, fmt.Errorf("environment not found")
	}
	return "", vars, globals, nil
}
