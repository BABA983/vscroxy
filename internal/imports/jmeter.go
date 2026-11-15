package imports

import (
	"cmp"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
)

// ReadJMeter reads HTTP samplers from a JMeter .jmx test plan. Thread groups,
// assertions, timers and non-HTTP samplers are reported, not simulated.
func ReadJMeter(data []byte) (Result, error) {
	if len(data) > 16<<20 {
		return Result{}, fmt.Errorf("JMeter test plans are limited to 16 MiB")
	}
	var root jmxNode
	if err := xml.Unmarshal(data, &root); err != nil {
		return Result{}, fmt.Errorf("read JMeter test plan: %w", err)
	}
	if root.XMLName.Local != "jmeterTestPlan" {
		return Result{}, fmt.Errorf("not a JMeter test plan")
	}
	name := Segment(cmp.Or(strings.TrimSpace(root.Name), "JMeter plan"))
	out := Collection{Name: name}
	other := false
	var walk func(jmxNode)
	walk = func(n jmxNode) {
		switch n.XMLName.Local {
		case "HTTPSamplerProxy":
			r, warnings := jmxSampler(name, n)
			out.Requests = append(out.Requests, r)
			out.Warnings = append(out.Warnings, warnings...)
		case "GenericController", "TransactionController", "LoopController", "ThreadGroup", "hashTree",
			"TestPlan", "jmeterTestPlan", "elementProp", "collectionProp", "stringProp", "boolProp", "intProp", "longProp",
			"HeaderManager", "Arguments", "ConfigTestElement", "CookieManager", "ResultCollector", "objProp", "value":
		default:
			other = true
		}
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(root)
	if other {
		out.Warnings = append(out.Warnings, "JMeter elements other than HTTP samplers were not imported")
	}
	if len(out.Requests) == 0 {
		out.Warnings = append(out.Warnings, "the test plan has no HTTP samplers")
	}
	return Result{Collections: []Collection{out}}, nil
}

type jmxNode struct {
	XMLName  xml.Name
	Name     string    `xml:"name,attr"`
	TestName string    `xml:"testname,attr"`
	Text     string    `xml:",chardata"`
	Children []jmxNode `xml:",any"`
}

func jmxSampler(collection string, n jmxNode) (request.Request, []string) {
	props := map[string]string{}
	var collect func(jmxNode)
	collect = func(node jmxNode) {
		switch node.XMLName.Local {
		case "stringProp", "boolProp", "intProp", "longProp":
			if node.Name != "" {
				props[node.Name] = strings.TrimSpace(node.Text)
			}
		}
		for _, child := range node.Children {
			collect(child)
		}
	}
	collect(n)
	domain, port, protocol, path := props["HTTPSampler.domain"], props["HTTPSampler.port"], props["HTTPSampler.protocol"], props["HTTPSampler.path"]
	scheme := cmp.Or(protocol, "http")
	if domain == "" && !strings.Contains(path, "://") {
		return request.Request{Name: cmp.Or(n.TestName, "JMeter request")}, []string{"a JMeter HTTP sampler had no domain or URL"}
	}
	raw := path
	if !strings.Contains(raw, "://") {
		host := domain
		if port != "" {
			host += ":" + port
		}
		raw = scheme + "://" + host + "/" + strings.TrimLeft(path, "/")
	}
	r := request.Request{Name: cmp.Or(strings.TrimSpace(n.TestName), strings.ToUpper(cmp.Or(props["HTTPSampler.method"], "GET"))+" "+path),
		Collection: collection, Method: strings.ToUpper(cmp.Or(props["HTTPSampler.method"], "GET")), URL: raw}
	for _, header := range jmxHeaders(n) {
		r.HeaderList = append(r.HeaderList, header)
	}
	if rawArgs := jmxArguments(n, "Arguments"); len(rawArgs) > 0 {
		if strings.EqualFold(props["HTTPSampler.postBodyRaw"], "true") {
			r.BodyMode, r.Body = "Raw", rawArgs[0].Value
			if !hasHeader(r.HeaderList, "Content-Type") {
				SetHeader(&r, "Content-Type", "text/plain")
			}
		} else {
			SetFormBody(&r, false, rawArgs)
		}
	}
	FinishHeaders(&r)
	if follow, err := strconv.ParseBool(props["HTTPSampler.follow_redirects"]); err == nil {
		r.Settings.FollowRedirects = &follow
	}
	if timeout, err := strconv.Atoi(props["HTTPSampler.response_timeout"]); err == nil && timeout > 0 {
		seconds := (timeout + 999) / 1000
		r.Settings.TimeoutSeconds = &seconds
	}
	var warnings []string
	if props["HTTPSampler.DO_MULTIPART_POST"] == "true" {
		warnings = append(warnings, "a JMeter multipart POST was imported as a URL-encoded body")
	}
	if props["HTTPSampler.auto_redirects"] == "true" {
		warnings = append(warnings, "JMeter auto-redirects were not mapped")
	}
	return r, warnings
}

// jmxArguments reads an Arguments collection's rows.
func jmxArguments(n jmxNode, element string) []request.Field {
	var out []request.Field
	var walk func(jmxNode)
	walk = func(node jmxNode) {
		if !(node.XMLName.Local == "elementProp" && (node.Name == element || node.Name == "HTTPsampler."+element)) {
			for _, child := range node.Children {
				walk(child)
			}
			return
		}
		for _, row := range node.Children {
			if row.XMLName.Local != "collectionProp" {
				continue
			}
			for _, entry := range row.Children {
				if entry.XMLName.Local != "elementProp" {
					continue
				}
				props := map[string]string{}
				var values func(jmxNode)
				values = func(v jmxNode) {
					if v.XMLName.Local == "stringProp" && v.Name != "" {
						props[v.Name] = v.Text
					}
					for _, child := range v.Children {
						values(child)
					}
				}
				values(entry)
				if props["Argument.name"] != "" || props["Argument.value"] != "" {
					out = append(out, request.Field{Name: props["Argument.name"], Value: props["Argument.value"]})
				}
			}
		}
	}
	walk(n)
	return out
}

// jmxHeaders reads a HeaderManager's rows.
func jmxHeaders(n jmxNode) []request.Field {
	var out []request.Field
	var walk func(jmxNode)
	walk = func(node jmxNode) {
		if !(node.XMLName.Local == "elementProp" && (node.Name == "HeaderManager" || node.Name == "HTTPHeaderManager")) {
			for _, child := range node.Children {
				walk(child)
			}
			return
		}
		for _, row := range node.Children {
			if row.XMLName.Local != "collectionProp" {
				continue
			}
			for _, entry := range row.Children {
				props := map[string]string{}
				var values func(jmxNode)
				values = func(v jmxNode) {
					if v.XMLName.Local == "stringProp" && v.Name != "" {
						props[v.Name] = v.Text
					}
					for _, child := range v.Children {
						values(child)
					}
				}
				values(entry)
				if props["Header.name"] != "" {
					out = append(out, request.Field{Name: props["Header.name"], Value: props["Header.value"]})
				}
			}
		}
	}
	walk(n)
	return out
}
