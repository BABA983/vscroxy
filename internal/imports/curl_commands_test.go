package imports

import (
	"strings"
	"testing"
)

func TestParseCurlCommands(t *testing.T) {
	source := `curl 'https://api.example.com/one' -H 'Accept: application/json'
curl 'https://api.example.com/two' \
  -X POST \
  --data 'name=x'
Invoke-WebRequest -Uri 'https://api.example.com/three' -Method Delete`
	got, warnings, err := ParseCurlCommands(source)
	if err != nil {
		t.Fatalf("%v (warnings %q)", err, warnings)
	}
	if len(got) != 3 {
		t.Fatalf("requests = %d: %+v", len(got), got)
	}
	if got[0].URL != "https://api.example.com/one" || got[1].Method != "POST" || got[1].Body != "name=x" || got[2].Method != "DELETE" {
		t.Errorf("requests = %+v", got)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %q", warnings)
	}
}

func TestParseCurlCommandsReportsOneBadCommand(t *testing.T) {
	got, warnings, err := ParseCurlCommands("curl 'https://api.example.com/ok'\ncurl $(unsafe)")
	if err != nil || len(got) != 1 || !strings.Contains(strings.Join(warnings, ";"), "command 2") {
		t.Fatalf("requests=%+v warnings=%q err=%v", got, warnings, err)
	}
}
