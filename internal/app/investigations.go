package app

// Read-only compatibility with old investigation boards and evidence bundles.
// New evidence is stored and exported as ordinary sessions.

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type investigation struct {
	ID      string              `json:"id"`
	Title   string              `json:"title"`
	Notes   string              `json:"notes,omitempty"`
	Created time.Time           `json:"created"`
	Items   []investigationItem `json:"items,omitempty"`
}

type investigationItem struct {
	ID     string      `json:"id"`
	Note   string      `json:"note,omitempty"`
	Source sessionFlow `json:"source"`
}

type investigationFile struct {
	Version        int              `json:"version"`
	Investigations []*investigation `json:"investigations"`
}

type EvidenceBundle struct {
	Format  string           `json:"format"`
	Version int              `json:"version"`
	Title   string           `json:"title"`
	Notes   string           `json:"notes"`
	Sources []EvidenceSource `json:"sources"`
}

type EvidenceSource struct {
	Label   string      `json:"label"`
	Notes   string      `json:"notes,omitempty"`
	Session sessionFile `json:"session"`
}

// Imported documents are bounded and never restore executable state or paths.
func ReadInvestigationFile(path string) (EvidenceBundle, error) {
	var bundle EvidenceBundle
	f, err := os.Open(path)
	if err != nil {
		return bundle, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return bundle, err
	}
	if !info.Mode().IsRegular() || info.Size() > 48<<20 {
		return bundle, errors.New("choose a regular session file no larger than 48 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, 48<<20+1))
	if err != nil {
		return bundle, err
	}
	if len(data) > 48<<20 {
		return bundle, errors.New("session exceeds 48 MiB")
	}
	var kind struct {
		Format string `json:"format"`
	}
	if json.Unmarshal(data, &kind) != nil {
		return bundle, errors.New("invalid session JSON")
	}
	if kind.Format == "" {
		var file sessionFile
		if json.Unmarshal(data, &file) != nil {
			return bundle, errors.New("invalid session JSON")
		}
		bundle = EvidenceBundle{Sources: []EvidenceSource{{Label: filepath.Base(path), Session: file}}}
	} else if kind.Format != "vscroxy-evidence" || json.Unmarshal(data, &bundle) != nil || bundle.Version != 1 {
		return bundle, errors.New("unsupported legacy evidence format or version")
	}
	if len(bundle.Sources) == 0 {
		return bundle, errors.New("session has no sources")
	}
	for _, source := range bundle.Sources {
		file := source.Session
		if file.Version != 1 {
			return bundle, fmt.Errorf("unsupported session version %d", file.Version)
		}
		ids := map[int]bool{}
		for _, flow := range file.Flows {
			if ids[flow.ID] {
				return bundle, errors.New("session has duplicate exchange IDs")
			}
			ids[flow.ID] = true
		}
		for id := range file.Frames {
			if !ids[id] {
				return bundle, errors.New("session has messages without an exchange")
			}
		}
	}
	return bundle, nil
}

func investigationFlow(f sessionFlow) flow {
	req, res := f.ReqBody, f.ResBody
	if len(f.ReqBytes) > 0 {
		req = string(f.ReqBytes)
	}
	if len(f.ResBytes) > 0 {
		res = string(f.ResBytes)
	}
	return flow{
		id: f.ID, replayOf: f.ReplayOf, actions: f.Actions, pinned: f.Pinned,
		debugKey: f.DebugKey, debugRun: f.DebugRun,
		method: f.Method, scheme: f.Scheme, host: f.Host, path: f.Path, url: f.URL,
		status: f.Status, bytes: f.Bytes, millis: f.Millis,
		reqHeaders: f.ReqHeaders, resHeaders: f.ResHeaders, reqBody: req, resBody: res,
		reqSize: max(f.ReqSize, len(req)), resSize: max(f.ResSize, len(res)),
		reqError: f.ReqError, resError: f.ResError, reqHeader: f.ReqHeader, respHeader: f.RespHeader,
		reqTrailer: f.ReqTrailer, resTrailer: f.ResTrailer, resProto: f.ResProto,
		conn: conn{proto: f.ReqProto, log: f.ConnectionLog, error: f.ConnectionError, errorStage: f.ConnectionStage},
		at:   f.At, kind: f.Kind, color: f.Color, note: f.Note, streamEnd: f.StreamEnd,
	}
}

func investigationMatches(f sessionFlow, notes, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	for _, value := range []string{f.Method, f.URL, strconv.Itoa(f.Status), f.Note, notes, f.ReqError, f.ResError, f.ConnectionError, f.ConnectionStage} {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}
