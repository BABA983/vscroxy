package imports

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"

	"github.com/oasdiff/yaml"
)

// Detect names the export in data, or Unknown. It reads only the top-level
// discriminator; the reader validates the rest.
func Detect(data []byte) Format {
	jsonData := data
	if !jsontext.Value(data).IsValid() {
		var err error
		jsonData, err = yaml.YAMLToJSON(data)
		if err != nil {
			return Unknown
		}
	}
	if jsontext.Value(jsonData).Kind() == '[' {
		var list []jsontext.Value
		if json.Unmarshal(jsonData, &list) != nil || len(list) == 0 {
			return Unknown
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
		return Unknown
	}
	switch {
	case top.ExportType == "export" && string(top.Export) == "4" && top.Resources.Kind() == '[':
		return InsomniaV4
	case strings.HasPrefix(top.Type, "collection.insomnia.rest/5.0"), strings.HasPrefix(top.Type, "spec.insomnia.rest/5.0"), strings.HasPrefix(top.Type, "environment.insomnia.rest/5.0"):
		return InsomniaV5
	case strings.TrimSpace(string(top.Swagger)) == `"2.0"`, strings.TrimSpace(string(top.Swagger)) == `2`, strings.TrimSpace(string(top.Swagger)) == `2.0`:
		return Swagger2
	case top.Info.Kind() == 0 && top.V.Kind() != 0 && top.Name != "":
		switch {
		case top.Folders.Kind() == '[' || top.Requests.Kind() == '[':
			return HoppscotchCollection
		case top.Variables.Kind() == '[':
			return HoppscotchEnvironment
		}
	}
	return Unknown
}
