package imports

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"strings"

	"github.com/baba983/vscroxy/internal/yamlguard"
)

// Detect names the export in data, or Unknown. It reads only the top-level
// discriminator; the reader validates the rest. A YAML document whose aliases
// expand beyond the guard is reported as Unknown here; callers that show an
// error use DetectWithError.
func Detect(data []byte) Format {
	format, _ := DetectWithError(data)
	return format
}

// DetectWithError is Detect plus the expansion error that would otherwise be
// lost. Malformed YAML stays Unknown without an error, as before; only a
// guard limit is surfaced, because that is the failure a user needs to see.
func DetectWithError(data []byte) (Format, error) {
	jsonData := data
	if !jsontext.Value(data).IsValid() {
		var err error
		jsonData, err = yamlguard.ToJSON(data, yamlguard.Options{})
		if err != nil {
			if errors.Is(err, yamlguard.ErrExpansion) {
				return Unknown, err
			}
			return Unknown, nil
		}
	}
	if jsontext.Value(jsonData).Kind() == '[' {
		var list []jsontext.Value
		if json.Unmarshal(jsonData, &list) != nil || len(list) == 0 {
			return Unknown, nil
		}
		jsonData = list[0]
	}
	var top struct {
		Type        string         `json:"type"`
		Swagger     jsontext.Value `json:"swagger"`
		ExportType  string         `json:"_type"`
		Export      jsontext.Value `json:"__export_format"`
		Resources   jsontext.Value `json:"resources"`
		V           jsontext.Value `json:"v"`
		Name        string         `json:"name"`
		Folders     jsontext.Value `json:"folders"`
		Requests    jsontext.Value `json:"requests"`
		Variables   jsontext.Value `json:"variables"`
		Info        jsontext.Value `json:"info"`
		Collections jsontext.Value `json:"collection"`
	}
	if json.Unmarshal(jsonData, &top) != nil {
		return Unknown, nil
	}
	switch {
	case top.ExportType == "export" && string(top.Export) == "4" && top.Resources.Kind() == '[':
		return InsomniaV4, nil
	case strings.HasPrefix(top.Type, "collection.insomnia.rest/5.0"), strings.HasPrefix(top.Type, "spec.insomnia.rest/5.0"), strings.HasPrefix(top.Type, "environment.insomnia.rest/5.0"):
		return InsomniaV5, nil
	case strings.TrimSpace(string(top.Swagger)) == `"2.0"`, strings.TrimSpace(string(top.Swagger)) == `2`, strings.TrimSpace(string(top.Swagger)) == `2.0`:
		return Swagger2, nil
	case top.Info.Kind() == 0 && top.V.Kind() != 0 && top.Name != "":
		switch {
		case top.Folders.Kind() == '[' || top.Requests.Kind() == '[':
			return HoppscotchCollection, nil
		case top.Variables.Kind() == '[':
			return HoppscotchEnvironment, nil
		}
	}
	return Unknown, nil
}
