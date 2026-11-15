package imports

import (
	"strings"
	"testing"
)

func TestReadSoapUIREST(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<con:soapui-project xmlns:con="http://eviware.com/soapui/config" name="Shop">
  <con:restService name="Shop API">
    <con:basePath>https://api.example.com/v1</con:basePath>
    <con:restResource name="Items" path="/items">
      <con:restMethod name="GET">
        <con:request id="list" name="List items" mediaType="application/json">
          <con:parameters>
            <con:parameter name="page" value="2" style="QUERY"/>
            <con:parameter name="X-Client" value="desktop" style="HEADER"/>
          </con:parameters>
          <con:requestContent/>
        </con:request>
      </con:restMethod>
      <con:restResource name="Item" path="/{id}">
        <con:restMethod name="POST">
          <con:request id="create" name="Create" mediaType="application/json">
            <con:parameters><con:parameter name="id" value="42" style="TEMPLATE"/></con:parameters>
            <con:requestContent>{"name":"x"}</con:requestContent>
          </con:request>
        </con:restMethod>
      </con:restResource>
    </con:restResource>
  </con:restService>
</con:soapui-project>`)
	if got := Detect(data); got != SoapUI {
		t.Fatalf("Detect = %v, want SoapUI", got)
	}
	result, err := ReadSoapUI(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Collections) != 1 || len(result.Collections[0].Requests) != 2 {
		t.Fatalf("result = %+v", result)
	}
	r := result.Collections[0].Requests[0]
	if r.ID != "list" || r.URL != "https://api.example.com/v1/items?page=2" || !strings.Contains(r.Headers, "X-Client: desktop") {
		t.Errorf("list = %+v", r)
	}
	r = result.Collections[0].Requests[1]
	if r.ID != "create" || r.URL != "https://api.example.com/v1/items/42" || r.Body != `{"name":"x"}` || r.BodyMode != "Raw" {
		t.Errorf("create = %+v", r)
	}
}
