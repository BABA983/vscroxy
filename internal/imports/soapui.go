package imports

import (
	"cmp"
	"encoding/xml"
	"fmt"
	"net/url"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
)

// ReadSoapUI reads the REST services of a SoapUI project. SOAP interfaces are
// reported rather than guessed at.
func ReadSoapUI(data []byte) (Result, error) {
	if len(data) > 16<<20 {
		return Result{}, fmt.Errorf("SoapUI projects are limited to 16 MiB")
	}
	var project soapProject
	if err := xml.Unmarshal(data, &project); err != nil {
		return Result{}, fmt.Errorf("read SoapUI project: %w", err)
	}
	if project.XMLName.Local != "soapui-project" {
		return Result{}, fmt.Errorf("not a SoapUI project")
	}
	name := Segment(cmp.Or(strings.TrimSpace(project.Name), "SoapUI project"))
	out := Collection{Name: name}
	for _, service := range project.REST {
		base := strings.TrimSpace(service.BasePath)
		for _, resource := range service.Resources {
			walkSoapResource(&out, name, base, resource)
		}
	}
	if len(project.Interfaces) > 0 {
		out.Warnings = append(out.Warnings, "SOAP interfaces and operations were not imported")
	}
	if len(out.Requests) == 0 {
		if len(out.Warnings) == 0 {
			out.Warnings = append(out.Warnings, "the project has no REST requests")
		}
		return Result{Collections: []Collection{out}}, nil
	}
	return Result{Collections: []Collection{out}}, nil
}

type soapProject struct {
	XMLName    xml.Name        `xml:"soapui-project"`
	Name       string          `xml:"name,attr"`
	REST       []soapREST      `xml:"restService"`
	Interfaces []soapInterface `xml:"interface"`
}

type soapREST struct {
	Name      string         `xml:"name,attr"`
	BasePath  string         `xml:"basePath"`
	Resources []soapResource `xml:"restResource"`
}

type soapResource struct {
	Name      string         `xml:"name,attr"`
	Path      string         `xml:"path,attr"`
	Resources []soapResource `xml:"restResource"`
	Methods   []soapMethod   `xml:"restMethod"`
}

type soapMethod struct {
	Name    string      `xml:"name,attr"`
	Request soapRequest `xml:"request"`
}

type soapRequest struct {
	ID          string      `xml:"id,attr"`
	Name        string      `xml:"name,attr"`
	MediaType   string      `xml:"mediaType,attr"`
	Content     string      `xml:"requestContent"`
	Parameters  []soapParam `xml:"parameters>parameter"`
	Description string      `xml:"documentation"`
}

type soapParam struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
	Style string `xml:"style,attr"`
	Type  string `xml:"type,attr"`
}

type soapInterface struct {
	Name string `xml:"name,attr"`
}

func walkSoapResource(out *Collection, collection, base string, resource soapResource) {
	path := joinSoapPath(base, resource.Path)
	for _, method := range resource.Methods {
		r := request.Request{ID: method.Request.ID, Name: cmp.Or(strings.TrimSpace(method.Request.Name), strings.ToUpper(method.Name)+" "+path),
			Collection: collection, Method: strings.ToUpper(cmp.Or(strings.TrimSpace(method.Name), "GET")), URL: path}
		for _, p := range method.Request.Parameters {
			switch strings.ToUpper(p.Style) {
			case "QUERY", "QUERYSTRING":
				r.URL += separator(r.URL) + url.QueryEscape(p.Name) + "=" + url.QueryEscape(p.Value)
			case "HEADER":
				r.HeaderList = append(r.HeaderList, request.Field{Name: p.Name, Value: p.Value})
			case "TEMPLATE":
				r.URL = strings.ReplaceAll(r.URL, "{"+p.Name+"}", url.PathEscape(p.Value))
			}
		}
		if body := strings.TrimSpace(method.Request.Content); body != "" {
			SetRawBody(&r, method.Request.MediaType, body)
		}
		FinishHeaders(&r)
		r.Description = strings.TrimSpace(method.Request.Description)
		if !strings.Contains(r.URL, "://") {
			out.Warnings = append(out.Warnings, "a REST request had a relative base path: "+r.URL)
		}
		out.Requests = append(out.Requests, r)
	}
	for _, child := range resource.Resources {
		walkSoapResource(out, collection, path, child)
	}
}

func joinSoapPath(base, path string) string {
	base, path = strings.TrimRight(strings.TrimSpace(base), "/"), strings.TrimSpace(path)
	if path == "" {
		return base
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return base + "/" + strings.TrimLeft(path, "/")
}

func separator(raw string) string {
	if strings.Contains(raw, "?") {
		return "&"
	}
	return "?"
}
