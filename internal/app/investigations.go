package app

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	investigationFileLimit = 16 << 20
	investigationKeepLimit = 48 << 20
	investigationSources   = 8
	investigationFlows     = 2000
	investigationFrames    = 10000
	evidenceFileLimit      = 4 << 20
	evidenceSelectionLimit = 64
)

// EvidenceBundle is a portable historical document, not a request collection.
// It contains raw captured data, including credentials, unless the person
// exporting it edits them out. No automatic redaction is performed.
type EvidenceBundle struct {
	Format  string           `json:"format"`
	Version int              `json:"version"`
	Title   string           `json:"title"`
	Notes   string           `json:"notes"`
	Sources []EvidenceSource `json:"sources"`
}

// EvidenceSource embeds the existing lossless session wire format. It never
// carries spill paths, scripts, rule configuration or an executable request.
type EvidenceSource struct {
	Label   string      `json:"label"`
	Notes   string      `json:"notes,omitempty"`
	Session sessionFile `json:"session"`
}

func validateInvestigationSession(s sessionFile) error {
	if s.Version != 1 {
		return fmt.Errorf("unsupported session version %d", s.Version)
	}
	if len(s.Flows) == 0 || len(s.Flows) > investigationFlows {
		return fmt.Errorf("session must contain 1–%d exchanges", investigationFlows)
	}
	ids := make(map[int]bool, len(s.Flows))
	for _, f := range s.Flows {
		if ids[f.ID] {
			return errors.New("session has duplicate exchange IDs")
		}
		ids[f.ID] = true
	}
	n := 0
	for id, frames := range s.Frames {
		if !ids[id] {
			return errors.New("session has messages without an exchange")
		}
		n += len(frames)
		if n > investigationFrames {
			return fmt.Errorf("session exceeds %d messages", investigationFrames)
		}
	}
	return nil
}

func validateEvidence(b EvidenceBundle) error {
	if b.Format != "vscroxy-evidence" || b.Version != 1 {
		return errors.New("unsupported evidence format or version")
	}
	if strings.TrimSpace(b.Title) == "" || len(b.Title) > 1024 || len(b.Notes) > 64<<10 {
		return errors.New("evidence needs a title (up to 1024 bytes) and notes up to 64 KiB")
	}
	if len(b.Sources) == 0 || len(b.Sources) > investigationSources {
		return fmt.Errorf("evidence must contain 1–%d sources", investigationSources)
	}
	count := 0
	for _, src := range b.Sources {
		if strings.TrimSpace(src.Label) == "" || len(src.Label) > 1024 {
			return errors.New("each source needs a label up to 1024 bytes")
		}
		if len(src.Notes) > 64<<10 {
			return errors.New("source notes exceed 64 KiB")
		}
		if err := validateInvestigationSession(src.Session); err != nil {
			return err
		}
		count += len(src.Session.Flows)
	}
	if count > evidenceSelectionLimit {
		return fmt.Errorf("evidence exceeds %d selected exchanges", evidenceSelectionLimit)
	}
	return nil
}

func decodeEvidence(data []byte) (EvidenceBundle, error) {
	var b EvidenceBundle
	if len(data) > evidenceFileLimit {
		return b, errors.New("evidence exceeds 4 MiB; select fewer exchanges or remove payloads in the review")
	}
	if err := json.Unmarshal(data, &b); err != nil {
		// Do not echo a parser's fragments of potentially secret payloads.
		return b, errors.New("invalid evidence JSON")
	}
	return b, validateEvidence(b)
}

// ReadInvestigationFile accepts a saved session or evidence JSON. Call off the
// UI thread. Reads are bounded, only regular files are accepted, and all data
// stays in memory: it never restores live state or creates payload spill files.
func ReadInvestigationFile(path string) (EvidenceBundle, error) {
	var empty EvidenceBundle
	info, err := os.Stat(path)
	if err != nil {
		return empty, err
	}
	if !info.Mode().IsRegular() || info.Size() > investigationFileLimit {
		return empty, errors.New("choose a regular JSON file no larger than 16 MiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return empty, err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return empty, err
	}
	if !info.Mode().IsRegular() || info.Size() > investigationFileLimit {
		return empty, errors.New("choose a regular JSON file no larger than 16 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, investigationFileLimit+1))
	if err != nil {
		return empty, err
	}
	if len(data) > investigationFileLimit {
		return empty, errors.New("investigation file exceeds 16 MiB")
	}
	var kind struct {
		Format string `json:"format"`
	}
	if json.Unmarshal(data, &kind) != nil {
		return empty, errors.New("invalid investigation JSON")
	}
	if kind.Format != "" {
		return decodeEvidence(data)
	}
	var session sessionFile
	if json.Unmarshal(data, &session) != nil {
		return empty, errors.New("invalid session JSON")
	}
	if err := validateInvestigationSession(session); err != nil {
		return empty, err
	}
	return EvidenceBundle{Format: "vscroxy-evidence", Version: 1, Title: filepath.Base(path),
		Sources: []EvidenceSource{{Label: filepath.Base(path), Session: session}}}, nil
}

// WriteEvidenceBundle writes exactly the reviewed JSON, after validation, via
// the app's atomic private-file writer. Consent is required even for edited or
// apparently harmless payloads. It does not claim or perform sanitization.
func WriteEvidenceBundle(path string, reviewedJSON []byte, reviewed bool) error {
	if !reviewed {
		return errors.New("review the full evidence and confirm before exporting")
	}
	if _, err := decodeEvidence(reviewedJSON); err != nil {
		return err
	}
	return writePrivateFile(path, reviewedJSON)
}

// Use the session wire fields directly, rather than reserializing an 8 MiB UI
// preview. Keep binary bytes and incomplete/legacy metadata untouched on export.
func investigationFlow(f sessionFlow) flow {
	req, res := f.ReqBody, f.ResBody
	if len(f.ReqBytes) > 0 {
		req = string(f.ReqBytes)
	}
	if len(f.ResBytes) > 0 {
		res = string(f.ResBytes)
	}
	return flow{
		id: f.ID, replayOf: f.ReplayOf, actions: f.Actions,
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

// Search is a case-insensitive literal substring, never a script, regex or
// payload scan. Include bundle notes as well as the original exchange note.
func investigationMatches(f sessionFlow, notes, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	for _, value := range []string{f.URL, strconv.Itoa(f.Status), f.Note, notes, f.ReqError, f.ResError, f.ConnectionError, f.ConnectionStage} {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

type investigationPick struct{ source, row int }

// buildEvidence copies only selected exchanges and their associated messages.
// All nested wire data is immutable in the reader; metadata edits happen to
// the serialized review copy, never to the original opened sessions.
func buildEvidence(sources []EvidenceSource, picks []investigationPick, title, notes string) ([]byte, error) {
	if len(picks) == 0 || len(picks) > evidenceSelectionLimit {
		return nil, fmt.Errorf("select 1–%d exchanges", evidenceSelectionLimit)
	}
	b := EvidenceBundle{Format: "vscroxy-evidence", Version: 1, Title: title, Notes: notes}
	seen := make(map[investigationPick]bool)
	for i, src := range sources {
		subset := EvidenceSource{Label: src.Label, Notes: src.Notes, Session: sessionFile{Version: 1}}
		for _, p := range picks {
			if p.source < 0 || p.source >= len(sources) || p.row < 0 || p.row >= len(sources[p.source].Session.Flows) {
				return nil, errors.New("selection is no longer available")
			}
			if p.source != i || seen[p] {
				continue
			}
			seen[p] = true
			f := src.Session.Flows[p.row]
			subset.Session.Flows = append(subset.Session.Flows, f)
			if frames := src.Session.Frames[f.ID]; len(frames) > 0 {
				if subset.Session.Frames == nil {
					subset.Session.Frames = make(map[int][]sessionFrameData)
				}
				subset.Session.Frames[f.ID] = frames
			}
		}
		if len(subset.Session.Flows) > 0 {
			b.Sources = append(b.Sources, subset)
		}
	}
	if err := validateEvidence(b); err != nil {
		return nil, err
	}
	data, err := json.Marshal(b, jsontext.WithIndent("  "))
	if len(data) > evidenceFileLimit {
		return nil, errors.New("selection exceeds 4 MiB; select fewer exchanges")
	}
	return data, err
}
