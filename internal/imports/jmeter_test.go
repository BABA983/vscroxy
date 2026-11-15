package imports

import (
	"strings"
	"testing"
)

func TestReadJMeterHTTPSampler(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.5">
  <hashTree>
    <TestPlan guiclass="TestPlanGui" testclass="TestPlan" testname="Shop">
      <hashTree>
        <HTTPSamplerProxy guiclass="HttpTestSampleGui" testclass="HTTPSamplerProxy" testname="Create item">
          <elementProp name="HTTPsampler.Arguments" elementType="Arguments">
            <collectionProp name="Arguments.arguments">
              <elementProp name="" elementType="HTTPArgument">
                <boolProp name="HTTPArgument.always_encode">false</boolProp>
                <stringProp name="Argument.value">{"name":"x"}</stringProp>
              </elementProp>
            </collectionProp>
          </elementProp>
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.port">443</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/items?page=2</stringProp>
          <stringProp name="HTTPSampler.method">POST</stringProp>
          <boolProp name="HTTPSampler.postBodyRaw">true</boolProp>
          <boolProp name="HTTPSampler.follow_redirects">false</boolProp>
          <elementProp name="HeaderManager" elementType="HeaderManager">
            <collectionProp name="HeaderManager.headers">
              <elementProp name="" elementType="Header">
                <stringProp name="Header.name">Content-Type</stringProp>
                <stringProp name="Header.value">application/json</stringProp>
              </elementProp>
            </collectionProp>
          </elementProp>
        </HTTPSamplerProxy>
      </hashTree>
    </TestPlan>
  </hashTree>
</jmeterTestPlan>`)
	if got := Detect(data); got != JMeter {
		t.Fatalf("Detect = %v, want JMeter", got)
	}
	result, err := ReadJMeter(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Collections) != 1 || len(result.Collections[0].Requests) != 1 {
		t.Fatalf("result = %+v", result)
	}
	r := result.Collections[0].Requests[0]
	if r.Name != "Create item" || r.Method != "POST" || r.URL != "https://api.example.com:443/items?page=2" {
		t.Errorf("request = %+v", r)
	}
	if r.Body != `{"name":"x"}` || r.BodyMode != "Raw" || !strings.Contains(r.Headers, "Content-Type: application/json") {
		t.Errorf("body/headers = %q / %q", r.Body, r.Headers)
	}
	if r.Settings.FollowRedirects == nil || *r.Settings.FollowRedirects {
		t.Errorf("follow redirects = %v", r.Settings.FollowRedirects)
	}
}
