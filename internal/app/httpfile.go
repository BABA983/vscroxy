package app

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/httpfile"
	"github.com/baba983/vscroxy/internal/imports"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
)

// importHTTPFile reads a JetBrains / VS Code .http file as one collection.
func (a *app) importHTTPFile(data []byte, path string) {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	doc, err := httpfile.Parse(name, data)
	if err != nil {
		a.fail(locales.ImportHttpFile(), err)
		return
	}
	collection := imports.Collection{Name: doc.Name, Requests: doc.Requests, Warnings: doc.Warnings}
	if len(doc.Variables) > 0 {
		collection.Folders = []request.Folder{{Path: doc.Name, Variables: doc.Variables}}
	}
	a.importThirdParty(imports.Result{Collections: []imports.Collection{collection}}, locales.ImportHttpFile())
}

// exportHTTPCollection writes one collection as a .http file.
func (a *app) exportHTTPCollection(name string) {
	var list []request.Request
	for _, r := range a.requests {
		if collectionRoot(r.Collection) == name {
			list = append(list, r.Request)
		}
	}
	var vars []request.Variable
	var warnings []string
	if i := slices.IndexFunc(a.folders, func(f request.Folder) bool { return f.Path == name }); i >= 0 {
		vars = a.folders[i].Variables
		if auth := a.folders[i].Auth; auth.Type != "" && auth.Type != request.AuthInherit && auth.Type != "None" {
			warnings = append(warnings, "collection auth is not applied to the .http file")
		}
	}
	for _, folder := range a.folders {
		if strings.HasPrefix(folder.Path, name+"/") {
			warnings = append(warnings, "folders are flattened into one .http file")
			if auth := folder.Auth; auth.Type != "" && auth.Type != request.AuthInherit && auth.Type != "None" {
				warnings = append(warnings, "folder auth is not applied to the .http file")
			}
			break
		}
	}
	data, writeWarnings, err := httpfile.Write(name, list, vars)
	if err != nil {
		a.fail(locales.ExportHttp(), err)
		return
	}
	if len(bytes.TrimSpace(data)) == 0 {
		a.warn(locales.HttpExportNoRequests())
		return
	}
	a.reviewHTTPExport(locales.ExportHttp(), strings.ReplaceAll(name, "∕", "-")+".http", data, append(warnings, writeWarnings...))
}

// exportHTTPDocument writes the open request as a .http file, with its
// collection's variables when it has one.
func (a *app) exportHTTPDocument(d *requestDocument) {
	if d == nil {
		return
	}
	r := d.value()
	var vars []request.Variable
	var warnings []string
	if i := slices.IndexFunc(a.folders, func(f request.Folder) bool { return f.Path == collectionRoot(r.Collection) }); i >= 0 {
		vars = a.folders[i].Variables
	}
	if r.Auth.Type == request.AuthInherit {
		if resolved, _ := a.inheritance(r.Collection).InheritAuth(r.Auth); resolved.Type != "" && resolved.Type != "None" {
			warnings = append(warnings, "inherited auth is not applied to the .http file")
		}
	}
	data, writeWarnings, err := httpfile.Write(r.Name, []request.Request{r.Request}, vars)
	if err != nil {
		a.fail(locales.ExportHttp(), err)
		return
	}
	if len(bytes.TrimSpace(data)) == 0 {
		a.warn(locales.HttpExportNoRequests())
		return
	}
	warnings = append(warnings, writeWarnings...)
	file := strings.TrimSpace(strings.ReplaceAll(r.Name, "∕", "-"))
	if file == "" {
		file = "request"
	}
	a.reviewHTTPExport(locales.ExportHttp(), file+".http", data, warnings)
}

// reviewHTTPExport reviews a .http copy before saving. Its redaction options
// apply to the file's variables, URLs, headers and bodies.
func (a *app) reviewHTTPExport(title, file string, data []byte, warnings []string) {
	a.exportUI = exportReview{open: true, title: title, file: file, data: bytes.Clone(data),
		warnings: slices.Clone(warnings), options: safeExportOptions(), text: redactHTTPFile}
}

// redactHTTPFile applies the export options to a .http file by reading it back
// and writing a redacted copy. Comments and ordering inside a request may
// change; the values are what this protects.
func redactHTTPFile(data []byte, options exportOptions) ([]byte, error) {
	if options == (exportOptions{}) {
		return bytes.Clone(data), nil
	}
	doc, err := httpfile.Parse("export", data)
	if err != nil {
		return nil, err
	}
	if options.Variables {
		for i := range doc.Variables {
			doc.Variables[i].Initial = exportReplacement
		}
	}
	for i := range doc.Requests {
		r := &doc.Requests[i]
		if options.Query || options.Auth {
			r.URL = redactExportURL(r.URL, options)
		}
		if options.Headers || options.Auth {
			for j := range r.HeaderList {
				name := r.HeaderList[j].Name
				if options.Headers && !exportPublicHeader(name) || options.Auth && exportAuthHeader(name) {
					r.HeaderList[j].Value = exportReplacement
				}
			}
			r.Headers = imports.HeaderBlock(r.HeaderList)
		}
		if options.Bodies {
			r.Body = ""
		}
	}
	out, _, err := httpfile.Write(doc.Name, doc.Requests, doc.Variables)
	return out, err
}

func exportAuthHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "cookie":
		return true
	}
	return false
}
