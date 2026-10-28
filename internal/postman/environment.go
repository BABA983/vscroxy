package postman

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"
	"uuid"
)

// Environment is a Postman environment or globals file.
type Environment struct {
	Name string
	// Globals is true for a globals file.
	Globals bool
	// Values are the enabled variables by name.
	Values map[string]string
	// Warnings say what the import dropped.
	Warnings []string
}

type envFile struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Values     []envValue `json:"values"`
	Scope      string     `json:"_postman_variable_scope"`
	ExportedAt string     `json:"_postman_exported_at"`
	ExportedBy string     `json:"_postman_exported_using"`
}

type envValue struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
}

// ReadEnvironment reads a Postman environment or globals file. Disabled
// values are skipped; values that are not strings keep their JSON text.
func ReadEnvironment(data []byte) (Environment, error) {
	var file struct {
		Name   string         `json:"name"`
		Values jsontext.Value `json:"values"`
		Scope  string         `json:"_postman_variable_scope"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return Environment{}, fmt.Errorf("not a Postman environment: %w", err)
	}
	if file.Values.Kind() != '[' {
		return Environment{}, errors.New("not a Postman environment: it has no values array")
	}
	var values []struct {
		Key     string         `json:"key"`
		Value   jsontext.Value `json:"value"`
		Enabled *bool          `json:"enabled"`
	}
	if err := json.Unmarshal(file.Values, &values); err != nil {
		return Environment{}, fmt.Errorf("read Postman environment values: %w", err)
	}
	env := Environment{Name: file.Name, Globals: file.Scope == "globals", Values: map[string]string{}}
	warn := tally{}
	for _, v := range values {
		if v.Enabled != nil && !*v.Enabled {
			warn.add("disabled variable", "", 1)
			continue
		}
		if v.Key != "" {
			env.Values[v.Key] = text(v.Value)
		}
	}
	env.Warnings = warn.list()
	return env, nil
}

// WriteEnvironment writes values as a Postman environment, or a globals
// file when globals is set, sorted by name.
func WriteEnvironment(name string, values map[string]string, globals bool) ([]byte, error) {
	file := envFile{
		ID: uuid.New().String(), Name: name, Values: []envValue{}, Scope: "environment",
		ExportedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), ExportedBy: "vscroxy",
	}
	if globals {
		file.Scope = "globals"
	}
	for _, key := range slices.Sorted(maps.Keys(values)) {
		file.Values = append(file.Values, envValue{Key: key, Value: values[key], Type: "default", Enabled: true})
	}
	data, err := json.Marshal(file, jsontext.WithIndent("  "))
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
