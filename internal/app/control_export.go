package app

import (
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"os"
	"slices"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/control"
)

const (
	controlHARBodyLimit = 4 << 20
	controlHARSizeLimit = 8 << 20
)

func (a *app) controlHAR(command control.Command) (any, error) {
	p, err := controlParams[struct {
		IDs     []int `json:"ids"`
		Details bool  `json:"details"`
	}](command)
	if err != nil || len(p.IDs) == 0 || len(p.IDs) > 200 {
		return nil, errors.New("ids must select 1–200 unique live HTTP flows")
	}
	wanted := make(map[int]int, len(p.IDs))
	for i, id := range p.IDs {
		if _, exists := wanted[id]; exists || id <= 0 {
			return nil, errors.New("ids must select 1–200 unique live HTTP flows")
		}
		wanted[id] = i
	}
	selected := make([]flow, len(p.IDs))
	found := 0
	for _, f := range a.flows {
		i, ok := wanted[f.id]
		if !ok || !f.live || f.kind != "" {
			continue
		}
		selected[i], found = f, found+1
	}
	if found != len(selected) {
		return nil, errors.New("a selected live HTTP flow is unavailable; list flows again")
	}
	return harExportWork(selected, p.Details)
}

// Snapshot on the UI thread; clipboard and control exports share bounded,
// complete-body serialization in a worker. Redacted jobs retain no payloads.
func harExportWork(flows []flow, details bool) (controlWork, error) {
	if len(flows) == 0 || len(flows) > 200 {
		return nil, errors.New("HAR export requires 1–200 HTTP flows")
	}
	selected := slices.Clone(flows)
	metadata := 0
	for i := range selected {
		f := &selected[i]
		if f.kind != "" {
			return nil, errors.New("HAR export requires HTTP flows without streams or tunnels")
		}
		metadata += len(f.url) + len(f.method) + len(f.reqHeaders) + len(f.resHeaders)
		for _, h := range []http.Header{f.reqHeader, f.respHeader} {
			for name, values := range h {
				metadata += len(name)
				for _, v := range values {
					metadata += len(v)
				}
			}
		}
		if metadata > 1<<20 {
			return nil, errors.New("HAR metadata exceeds 1 MiB; select fewer flows")
		}
		f.reqHeader, f.respHeader = f.reqHeader.Clone(), f.respHeader.Clone()
		if !details {
			// Never open spill files or keep payloads in a redacted export job.
			f.reqBody, f.resBody, f.reqFile, f.resFile = "", "", "", ""
			f.reqError, f.resError = "", ""
		}
	}
	return controlWork(func(ctx context.Context) (any, error) {
		remaining := controlHARBodyLimit
		for i := range selected {
			f := &selected[i]
			if err := ctx.Err(); err != nil {
				return nil, errors.New("HAR export canceled")
			}
			if !details {
				continue
			}
			for _, side := range []struct {
				body    *string
				file    *string
				size    int
				problem string
			}{{&f.reqBody, &f.reqFile, f.reqSize, f.reqError}, {&f.resBody, &f.resFile, f.resSize, f.resError}} {
				body, err := controlHARBody(*side.body, *side.file, side.size, side.problem, remaining)
				if err != nil {
					return nil, err
				}
				*side.body, *side.file = body, ""
				remaining -= len(body)
			}
		}
		doc := harFromFlows(selected)
		for i, f := range selected {
			// HAR strings cannot carry arbitrary bytes. Match the base64
			// convention understood by our importer for either body direction.
			e := &doc.Log.Entries[i]
			if !utf8.ValidString(f.resBody) {
				e.Response.Content.Text = base64.StdEncoding.EncodeToString([]byte(f.resBody))
				e.Response.Content.Encoding = "base64"
			}
			if !utf8.ValidString(f.reqBody) && e.Request.PostData != nil {
				e.Request.PostData.Text = base64.StdEncoding.EncodeToString([]byte(f.reqBody))
				e.Request.PostData.Encoding = "base64"
			}
		}
		if !details {
			redactHAR(&doc, safeExportOptions())
		}
		data, err := json.Marshal(doc)
		if err != nil || len(data) > controlHARSizeLimit {
			return nil, errors.New("HAR exceeds export limits; select fewer flows")
		}
		return jsontext.Value(data), nil
	}), nil
}

// Files are engine-owned retained payloads, not client-supplied paths. Missing,
// partial or oversized bodies fail the whole raw export, never a silent preview.
func controlHARBody(body, path string, size int, problem string, remaining int) (string, error) {
	if problem != "" || size < 0 {
		return "", errors.New("a captured body is incomplete")
	}
	if size > remaining {
		return "", errors.New("HAR bodies exceed 4 MiB; select fewer flows")
	}
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return "", errors.New("a captured body is unavailable")
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > int64(remaining) {
			return "", errors.New("a captured body is invalid or exceeds export limits")
		}
		data, err := io.ReadAll(io.LimitReader(file, int64(remaining)+1))
		if err != nil || int64(len(data)) != info.Size() {
			return "", errors.New("a captured body changed or could not be read")
		}
		body = string(data)
	}
	if len(body) > remaining || size > 0 && len(body) != size {
		return "", errors.New("a captured body is incomplete or exceeds export limits")
	}
	return body, nil
}
