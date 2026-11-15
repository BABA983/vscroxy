package imports

import (
	"strings"
	"testing"
)

func TestReadThunderClientCollection(t *testing.T) {
	data := []byte(`{
		"client":"Thunder Client","collectionName":"Shop","version":"1.1",
		"folders":[{"_id":"f1","containerId":"","name":"Orders"},{"_id":"f2","containerId":"f1","name":"Open"}],
		"data":[{"name":"base","value":"https://api.example.com"}],
		"requests":[
			{"_id":"r1","containerId":"f2","name":"List","url":"{{base}}/orders","method":"GET",
			 "headers":[{"name":"Accept","value":"application/json"},{"name":"X-Off","value":"1","isDisabled":true}],
			 "params":[{"name":"page","value":"2"}],
			 "auth":{"type":"bearer","bearer":"{{token}}"},
			 "preScripts":["pm.variables.set('a', 1);"],"postScripts":["pm.test('ok', () => {});"]},
			{"_id":"r2","name":"Create","url":"{{base}}/orders","method":"POST",
			 "body":{"type":"json","raw":"{\"name\":\"{{name}}\"}"},
			 "auth":{"type":"basic","username":"ann","password":"pw"},
			 "tests":[{"type":"res-code","custom":"","action":"equal","value":"201"}]}
		]}`)
	if got := Detect(data); got != ThunderClient {
		t.Fatalf("Detect = %v, want ThunderClient", got)
	}
	result, err := ReadThunder(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Collections) != 1 {
		t.Fatalf("collections = %+v", result.Collections)
	}
	c := result.Collections[0]
	if c.Name != "Shop" || len(c.Requests) != 2 {
		t.Fatalf("collection = %+v", c)
	}
	var list, create int = -1, -1
	for i, r := range c.Requests {
		switch r.ID {
		case "r1":
			list = i
		case "r2":
			create = i
		}
	}
	r := c.Requests[list]
	if r.Collection != "Shop/Orders/Open" || r.URL != "{{base}}/orders?page=2" || r.Auth.Type != "Bearer" || r.Auth.Token != "{{token}}" {
		t.Errorf("list = %+v", r)
	}
	if !strings.Contains(r.Headers, "Accept: application/json") || strings.Contains(r.Headers, "X-Off") || r.Pre == "" || r.Post == "" {
		t.Errorf("list headers/scripts = %q / %q / %q", r.Headers, r.Pre, r.Post)
	}
	r = c.Requests[create]
	if r.Method != "POST" || r.BodyMode != "Raw" || r.Body != `{"name":"{{name}}"}` || r.Auth.Type != "Basic" || r.Checks.Status != "201" {
		t.Errorf("create = %+v", r)
	}
	if !strings.Contains(r.Headers, "Content-Type: application/json") {
		t.Errorf("create headers = %q", r.Headers)
	}
	if len(c.Folders) < 2 || c.Folders[0].Path == "" {
		t.Errorf("folders = %+v", c.Folders)
	}
}
