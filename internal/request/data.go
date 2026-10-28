package request

import (
	"bytes"
	"encoding/csv"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

// MaxDataRows bounds a Runner data file: each row is one iteration.
const MaxDataRows = 1000

// ReadIterationData reads a Runner data file, as Postman does: a JSON array
// of objects, or CSV whose first row names the columns. Each row is one
// iteration's pm.iterationData; values that are not strings become their
// JSON text. name, the file's name, picks the format by its extension.
func ReadIterationData(name string, data []byte) ([]map[string]string, error) {
	data = bytes.TrimPrefix(data, []byte("\uFEFF"))
	var rows []map[string]string
	var err error
	if strings.EqualFold(filepath.Ext(name), ".json") {
		rows, err = readJSONData(data)
	} else {
		rows, err = readCSVData(data)
	}
	if err != nil {
		return nil, err
	}
	switch {
	case len(rows) == 0:
		return nil, errors.New("the data file has no rows")
	case len(rows) > MaxDataRows:
		return nil, fmt.Errorf("the data file has %d rows; the Runner takes at most %d", len(rows), MaxDataRows)
	}
	return rows, nil
}

func readJSONData(data []byte) ([]map[string]string, error) {
	var list []map[string]jsontext.Value
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("a JSON data file must be an array of objects: %w", err)
	}
	rows := make([]map[string]string, len(list))
	for i, object := range list {
		if object == nil {
			return nil, fmt.Errorf("row %d is not an object", i+1)
		}
		rows[i] = map[string]string{}
		for key, raw := range object {
			if raw.Kind() == '"' {
				var s string
				json.Unmarshal(raw, &s)
				rows[i][key] = s
			} else {
				rows[i][key] = string(raw)
			}
		}
	}
	return rows, nil
}

func readCSVData(data []byte) ([]map[string]string, error) {
	r := csv.NewReader(bytes.NewReader(data))
	header, err := r.Read()
	if errors.Is(err, io.EOF) {
		return nil, errors.New("the data file is empty")
	}
	if err != nil {
		return nil, fmt.Errorf("CSV: %w", err)
	}
	for i, name := range header {
		header[i] = strings.TrimSpace(name)
		if header[i] == "" {
			return nil, fmt.Errorf("CSV column %d has no name in the first row", i+1)
		}
		if slices.Contains(header[:i], header[i]) {
			return nil, fmt.Errorf("CSV column %q appears twice", header[i])
		}
	}
	var rows []map[string]string
	for {
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return nil, fmt.Errorf("CSV: %w", err)
		}
		row := map[string]string{}
		for i, name := range header {
			row[name] = record[i]
		}
		rows = append(rows, row)
	}
}

// DataColumns names the variables data rows set, sorted.
func DataColumns(rows []map[string]string) []string {
	seen := map[string]bool{}
	for _, row := range rows {
		for name := range maps.Keys(row) {
			seen[name] = true
		}
	}
	return slices.Sorted(maps.Keys(seen))
}
